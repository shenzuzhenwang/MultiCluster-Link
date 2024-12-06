/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pkgError "github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

//+kubebuilder:rbac:groups=example.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=example.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=example.io,resources=gatewayexips/finalizers,verbs=update
//+kubebuilder:rbac:groups=submariner.io,resources=servicediscoveries,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=submariner.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete

type AgentSpecification struct {
	ClusterID        string
	Namespace        string
	Verbosity        int
	GlobalnetEnabled bool `split_words:"true"`
	Uninstall        bool
	HaltOnCertError  bool `split_words:"true"`
	Debug            bool
}

var BrokerResyncPeriod = time.Minute * 2

type Controller struct {
	ClusterID string
	Client    client.Client
	Syncer    *broker.Syncer
	Scheme    *runtime.Scheme
}

// InitEnvVars from ServiceDiscovery crd, set the environment vars
func InitEnvVars(syncerConf broker.SyncerConfig) error {
	cr := &submarinerv1alpha1.ServiceDiscovery{}
	obj, err := syncerConf.LocalClient.Resource(schema.GroupVersionResource{
		Group:    "submariner.io",
		Version:  "v1alpha1",
		Resource: "servicediscoveries",
	}).Namespace("submariner-operator").Get(context.TODO(), "service-discovery", metav1.GetOptions{})
	if err != nil {
		return err
	}
	utilruntime.Must(syncerConf.Scheme.Convert(obj, cr, nil))
	os.Setenv("SUBMARINER_NAMESPACE", cr.Spec.Namespace)
	os.Setenv("SUBMARINER_CLUSTERID", cr.Spec.ClusterID)
	os.Setenv("SUBMARINER_DEBUG", strconv.FormatBool(cr.Spec.Debug))
	os.Setenv("SUBMARINER_GLOBALNET_ENABLED", strconv.FormatBool(cr.Spec.GlobalnetEnabled))
	os.Setenv("SUBMARINER_HALT_ON_CERT_ERROR", strconv.FormatBool(cr.Spec.HaltOnCertificateError))
	os.Setenv(broker.EnvironmentVariable("ApiServer"), cr.Spec.BrokerK8sApiServer)
	os.Setenv(broker.EnvironmentVariable("ApiServerToken"), cr.Spec.BrokerK8sApiServerToken)
	os.Setenv(broker.EnvironmentVariable("RemoteNamespace"), cr.Spec.BrokerK8sRemoteNamespace)
	os.Setenv(broker.EnvironmentVariable("CA"), cr.Spec.BrokerK8sCA)
	os.Setenv(broker.EnvironmentVariable("Insecure"), strconv.FormatBool(cr.Spec.BrokerK8sInsecure))
	os.Setenv(broker.EnvironmentVariable("Secret"), cr.Spec.BrokerK8sSecret)
	return nil
}

func NewGwExIpSyner(client client.Client, spec *AgentSpecification, syncerConfig broker.SyncerConfig) *Controller {
	c := &Controller{
		ClusterID: spec.ClusterID,
		Scheme:    syncerConfig.Scheme,
		Client:    client,
	}
	var err error
	// set GatewayExIp crd syncer config
	syncerConfig.LocalNamespace = metav1.NamespaceAll
	syncerConfig.LocalClusterID = spec.ClusterID
	syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace:       metav1.NamespaceAll,
			LocalResourceType:          &kubeovnv1.GatewayExIp{},
			TransformLocalToBroker:     c.onLocalGatewayExIp,
			OnSuccessfulSyncToBroker:   c.onLocalGatewayExIpSynced,
			BrokerResourceType:         &kubeovnv1.GatewayExIp{},
			TransformBrokerToLocal:     c.onRemoteGatewayExIp,
			OnSuccessfulSyncFromBroker: c.onRemoteGatewayExIpSynced,
			BrokerResyncPeriod:         BrokerResyncPeriod,
		},
	}
	// create broker Syncer, set Two-way sync for ResourceConfig
	c.Syncer, err = broker.NewSyncer(syncerConfig)
	if err != nil {
		log.Log.Error(err, "error creating GatewayExIp syncer")
		return nil
	}
	return c
}

// Start syncer to sync
func (c *Controller) Start(ctx context.Context) error {
	stopCh := ctx.Done()
	if err := c.Syncer.Start(stopCh); err != nil {
		return pkgError.Wrap(err, "error starting syncer")
	}
	log.Log.Info("Agent controller started")
	return nil
}

// operate before local to Broker
func (c *Controller) onLocalGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	if gatewayExIp.Namespace == "k8s-broker" {
		return nil, false
	}
	return gatewayExIp, false
}

// operate after local to Broker
func (c *Controller) onLocalGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	return false
}

// operate before Broker to local
func (c *Controller) onRemoteGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	gatewayExIp.Namespace = gatewayExIp.GetObjectMeta().GetLabels()["submariner-io/originatingNamespace"]
	return gatewayExIp, false
}

// operate after Broker to local
func (c *Controller) onRemoteGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	// find vpcNatTunnels which use this GatewayExIp
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	splitStrings := strings.SplitN(gatewayExIp.Name, ".", 2)
	remoteVpc := splitStrings[0]
	remoteCluster := splitStrings[1]

	labelsSet := map[string]string{
		"remoteCluster": remoteCluster,
		"remoteVpc":     remoteVpc,
	}
	option := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelsSet),
	}
	vpcNatTunnelList := &kubeovnv1.VpcNatTunnelList{}
	err := c.Client.List(context.Background(), vpcNatTunnelList, &option)
	if err != nil {
		return false
	}
	// update vpcNatTunnels
	for _, vpcNatTunnel := range vpcNatTunnelList.Items {
		if vpcNatTunnel.Spec.RemoteIP == gatewayExIp.Spec.ExternalIP {
			continue
		}
		vpcNatTunnel.Spec.RemoteIP = gatewayExIp.Spec.ExternalIP
		if err = c.Client.Update(context.Background(), &vpcNatTunnel); err != nil {
			log.Log.Error(err, "Error update vpcTunnel")
			return false
		}
	}
	return false
}
