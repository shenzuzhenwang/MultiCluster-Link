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

package main

import (
	"crypto/tls"
	"flag"
	"os"

	"github.com/kelseyhightower/envconfig"
	ovn "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"kubeovn-multivpc/internal/controller"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kubeovnv1 "kubeovn-multivpc/api/v1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(ovn.AddToScheme(scheme))

	utilruntime.Must(Submariner.AddToScheme(scheme))

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(kubeovnv1.AddToScheme(scheme))

	utilruntime.Must(submarinerv1alpha1.AddToScheme(scheme))

	utilruntime.Must(Submariner.AddToScheme(clientgoscheme.Scheme))

	utilruntime.Must(submarinerv1alpha1.AddToScheme(clientgoscheme.Scheme))

	utilruntime.Must(kubeovnv1.AddToScheme(clientgoscheme.Scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "a18cab20.example.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// ************* submariner broker syner gwexip crd
	cfg := mgr.GetConfig()

	restMapper, err := util.BuildRestMapper(cfg)
	if err != nil {
		setupLog.Error(err, "Error building rest mapper")
		os.Exit(1)
	}

	localClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "Error creating dynamic client")
		os.Exit(1)
	}
	syncerConfig := broker.SyncerConfig{
		LocalRestConfig: cfg,
		LocalClient:     localClient,
		RestMapper:      restMapper,
		Scheme:          clientgoscheme.Scheme,
	}
	err = controller.InitEnvVars(syncerConfig)
	if err != nil {
		setupLog.Error(err, "error init environment vars")
		os.Exit(1)
	}
	agentSpec := controller.AgentSpecification{
		Verbosity: log.DEBUG,
	}

	if err := envconfig.Process("submariner", &agentSpec); err != nil {
		setupLog.Error(err, "Error processing env config for agent spec")
		os.Exit(1)
	}

	if err = mgr.Add(controller.NewGwExIpSyner(mgr.GetClient(), &agentSpec, syncerConfig)); err != nil {
		setupLog.Error(err, "unable to set up gatewayexip agent")
		os.Exit(1)
	}
	/************/

	// VpcNatTunnel Reconciler
	vpcNatTunnelReconciler := &controller.VpcNatTunnelReconciler{
		ClusterId: agentSpec.ClusterID,
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
	}
	if err = (vpcNatTunnelReconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VpcNatTunnel")
		os.Exit(1)
	}

	// VpcDnsForward Reconciler
	if err = (&controller.VpcDnsForwardReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VpcDnsForward")
		os.Exit(1)
	}

	// Gateway StatefulSet Informer
	if err := mgr.Add(controller.NewInformer(agentSpec.ClusterID, mgr.GetClient(), mgr.GetConfig())); err != nil {
		setupLog.Error(err, "unable to set up gateway informer")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
