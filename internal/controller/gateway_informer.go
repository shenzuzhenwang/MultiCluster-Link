package controller

import (
	"context"
	"fmt"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"strings"

	ovn "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GatewayInformer struct {
	ClusterId string
	Client    client.Client
	Config    *rest.Config
}

//+kubebuilder:rbac:groups=kubeovn.io,resources=vpc-nat-gateways,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.io,resources=vpcs,verbs=get;list;watch;create;update;patch;delete

func NewInformer(clusterId string, client client.Client, config *rest.Config) *GatewayInformer {
	return &GatewayInformer{ClusterId: clusterId, Client: client, Config: config}
}

func (r *GatewayInformer) Start(ctx context.Context) error {
	clientSet, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		log.Log.Error(err, "Error create client")
		return err
	}
	var vpcNatTunnelList kubeovnv1.VpcNatTunnelList
	labelSelector := labels.Set{
		"ovn.kubernetes.io/vpc-nat-gw": "true",
	}
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector.AsSelector().String()
				return clientSet.AppsV1().StatefulSets("kube-system").List(ctx, options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector.AsSelector().String()
				return clientSet.AppsV1().StatefulSets("kube-system").Watch(ctx, options)
			},
		},
		&appsv1.StatefulSet{},
		0,
		cache.Indexers{},
	)
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			statefulSet := obj.(*appsv1.StatefulSet)
			gatewayName := strings.TrimPrefix(statefulSet.Name, "vpc-nat-gw-")
			if statefulSet.Status.AvailableReplicas == 1 {
				natGw := &ovn.VpcNatGateway{}
				err = r.Client.Get(ctx, client.ObjectKey{
					Name: gatewayName,
				}, natGw)
				if err != nil {
					log.Log.Error(err, "Error Get Vpc-Nat-Gateway")
					return
				}
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				pod, err := getNatGwPod(gatewayName, r.Client)
				if err != nil {
					log.Log.Error(err, "Error get gw pod")
					return
				}
				err = r.Client.Get(ctx, client.ObjectKey{
					Name:      natGw.Spec.Vpc + "." + r.ClusterId,
					Namespace: "kube-system",
				}, gatewayExIp)
				if err != nil {
					if errors.IsNotFound(err) {
						vpcName := natGw.Spec.Vpc
						vpc := &ovn.Vpc{}
						err = r.Client.Get(ctx, client.ObjectKey{
							Name: vpcName,
						}, vpc)
						if err != nil {
							log.Log.Error(err, "Error Get Vpc")
							return
						}
						for _, router := range vpc.Spec.StaticRoutes {
							if router.NextHopIP != natGw.Spec.LanIP {
								return
							}
						}
						submarinerCluster := &Submariner.Cluster{}
						err := r.Client.Get(ctx, client.ObjectKey{
							Namespace: "submariner-operator",
							Name:      r.ClusterId,
						}, submarinerCluster)
						if err != nil {
							log.Log.Error(err, "Error get submarinerCluster")
							return
						}
						GwExternIP, err := getGwExternIP(pod)
						if err != nil {
							log.Log.Error(err, "Error get GwExternIP")
							return
						}
						gatewayExIp.Name = natGw.Spec.Vpc + "." + r.ClusterId
						gatewayExIp.Namespace = pod.Namespace
						gatewayExIp.Spec.ExternalIP = GwExternIP
						gatewayExIp.Spec.GlobalNetCIDR = submarinerCluster.Spec.GlobalCIDR[0]
						label := make(map[string]string)
						label["localVpc"] = natGw.Spec.Vpc
						label["localGateway"] = natGw.Name
						label["localCluster"] = r.ClusterId
						gatewayExIp.Labels = label
						err = r.Client.Create(ctx, gatewayExIp)
						if err != nil {
							log.Log.Error(err, "Error create gatewayExIp")
							return
						}
						log.Log.Info("GatewayExIp create success: " + gatewayExIp.Name)
					}
				} else {
				}
			}
		},
		UpdateFunc: func(old, new interface{}) {
			oldStatefulSet := old.(*appsv1.StatefulSet)
			newStatefulSet := new.(*appsv1.StatefulSet)

			if oldStatefulSet.Status.AvailableReplicas == 1 && newStatefulSet.Status.AvailableReplicas == 0 {
				gatewayName := strings.TrimPrefix(newStatefulSet.Name, "vpc-nat-gw-")
				labelSet := map[string]string{
					"localGateway": gatewayName,
					"localCluster": r.ClusterId,
				}
				options := client.ListOptions{
					Namespace:     "kube-system",
					LabelSelector: labels.SelectorFromSet(labelSet),
				}
				gatewayExIpList := &kubeovnv1.GatewayExIpList{}
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				if err = r.Client.List(ctx, gatewayExIpList, &options); err != nil {
					return
				}
				for _, ExIp := range gatewayExIpList.Items {
					gatewayExIp = &ExIp
					if gatewayExIp.Labels["localGateway"] == gatewayName {
						break
					}
				}
				if gatewayExIp.Labels["localGateway"] != gatewayName {
					return
				}
				vpc := &ovn.Vpc{}
				if err = r.Client.Get(ctx, client.ObjectKey{Name: gatewayExIp.Labels["localVpc"]}, vpc); err != nil {
					return
				}
				labelsSet := map[string]string{
					"localVpc": vpc.Name,
				}
				option := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelsSet),
				}
				err = r.Client.List(ctx, &vpcNatTunnelList, &option)
				if err != nil {
					log.Log.Error(err, "Error get vpcNatTunnel list")
					return
				}
				GwList := &appsv1.StatefulSetList{}
				err = r.Client.List(ctx, GwList, client.InNamespace("kube-system"), client.MatchingLabels{"ovn.kubernetes.io/vpc-nat-gw": "true"})
				if err != nil {
					log.Log.Error(err, "Error get StatefulSetList")
					return
				}
				GwStatefulSet := &appsv1.StatefulSet{}
				// find Vpc-Gateway which status is Active
				for _, statefulSet := range GwList.Items {
					if statefulSet.Status.AvailableReplicas == 1 && statefulSet.Name != newStatefulSet.Name &&
						statefulSet.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] == newStatefulSet.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] {
						GwStatefulSet = &statefulSet
						break
					}
				}
				if GwStatefulSet.Name == "" {
					return
				}
				for _, route := range vpc.Spec.StaticRoutes {
					route.NextHopIP = GwStatefulSet.Spec.Template.Annotations["ovn.kubernetes.io/ip_address"]
				}
				podNext, err := getNatGwPod(strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-"), r.Client)
				if err != nil {
					log.Log.Error(err, "Error get GwPod")
					return
				}
				GwExternIP, err := getGwExternIP(podNext)
				if err != nil {
					log.Log.Error(err, "Error get GwExternIP")
					return
				}
				err = r.Client.Update(ctx, vpc)
				if err != nil {
					log.Log.Error(err, "Error update Vpc")
					return
				}
				log.Log.Info("vpc route updated: " + vpc.Name)
				gatewayExIp.Spec.ExternalIP = GwExternIP
				gatewayExIp.Labels["localGateway"] = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")
				err = r.Client.Update(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error update gatewayExIp")
					return
				}
				log.Log.Info("GatewayExIp updated: " + gatewayExIp.Name)
				for _, vpcTunnel := range vpcNatTunnelList.Items {
					vpcTunnel.Spec.InternalIP = GwExternIP
					vpcTunnel.Spec.LocalGw = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")
					if err = r.Client.Update(ctx, &vpcTunnel); err != nil {
						log.Log.Error(err, "Error update vpcTunnel")
						return
					}
				}
			}
			if oldStatefulSet.Status.AvailableReplicas == 0 && newStatefulSet.Status.AvailableReplicas == 1 {
				gatewayName := strings.TrimPrefix(newStatefulSet.Name, "vpc-nat-gw-")
				labelSet := map[string]string{
					"localGateway": gatewayName,
					"localCluster": r.ClusterId,
				}
				options := client.ListOptions{
					Namespace:     "kube-system",
					LabelSelector: labels.SelectorFromSet(labelSet),
				}
				gatewayExIpList := &kubeovnv1.GatewayExIpList{}
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				if err = r.Client.List(ctx, gatewayExIpList, &options); err != nil {
					return
				}
				for _, ExIp := range gatewayExIpList.Items {
					gatewayExIp = &ExIp
					if gatewayExIp.Labels["localGateway"] == gatewayName {
						break
					}
				}
				if gatewayExIp.Labels["localGateway"] != gatewayName {
					return
				}
				podNext, err := getNatGwPod(gatewayName, r.Client)
				if err != nil {
					log.Log.Error(err, "Error get GwPod")
					return
				}
				GwExternIP, err := getGwExternIP(podNext)
				if err != nil {
					log.Log.Error(err, "Error get GwExternIP")
					return
				}
				gatewayExIp.Spec.ExternalIP = GwExternIP
				err = r.Client.Update(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error update gatewayExIp")
					return
				}
				log.Log.Info("GatewayExIp updated: " + gatewayExIp.Name)
				labelsSet := map[string]string{
					"localVpc": gatewayExIp.Labels["localVpc"],
				}
				option := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelsSet),
				}
				err = r.Client.List(ctx, &vpcNatTunnelList, &option)
				if err != nil {
					log.Log.Error(err, "Error get vpcNatTunnel list")
					return
				}
				for _, vpcTunnel := range vpcNatTunnelList.Items {
					vpcTunnel.Spec.InternalIP = GwExternIP
					if err = r.Client.Update(ctx, &vpcTunnel); err != nil {
						log.Log.Error(err, "Error update vpcTunnel")
						return
					}
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			statefulSet := obj.(*appsv1.StatefulSet)
			gatewayName := strings.TrimPrefix(statefulSet.Name, "vpc-nat-gw-")
			labelSet := map[string]string{
				"localGateway": gatewayName,
				"localCluster": r.ClusterId,
			}
			options := client.ListOptions{
				Namespace:     "kube-system",
				LabelSelector: labels.SelectorFromSet(labelSet),
			}
			gatewayExIpList := &kubeovnv1.GatewayExIpList{}
			gatewayExIp := &kubeovnv1.GatewayExIp{}
			if err = r.Client.List(ctx, gatewayExIpList, &options); err != nil {
				return
			}
			for _, ExIp := range gatewayExIpList.Items {
				gatewayExIp = &ExIp
				if gatewayExIp.Labels["localGateway"] == gatewayName {
					break
				}
			}
			if gatewayExIp.Labels["localGateway"] != gatewayName {
				return
			}
			vpcName := gatewayExIp.Labels["localVpc"]
			vpc := &ovn.Vpc{}
			err = r.Client.Get(ctx, client.ObjectKey{
				Name: vpcName,
			}, vpc)
			if err != nil {
				log.Log.Error(err, "Error Get Vpc")
				return
			}
			GwList := &appsv1.StatefulSetList{}
			err = r.Client.List(ctx, GwList, client.InNamespace("kube-system"), client.MatchingLabels{"ovn.kubernetes.io/vpc-nat-gw": "true"})
			if err != nil {
				log.Log.Error(err, "Error get StatefulSetList")
				return
			}
			GwStatefulSet := &appsv1.StatefulSet{}
			for _, st := range GwList.Items {
				if st.Status.AvailableReplicas == 1 &&
					st.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] == statefulSet.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] {
					GwStatefulSet = &st
					break
				}
			}
			if GwStatefulSet.Name == "" {
				err = r.Client.Delete(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error delete gatewayExIp")
					return
				}
				return
			}
			for _, route := range vpc.Spec.StaticRoutes {
				route.NextHopIP = GwStatefulSet.Spec.Template.Annotations["ovn.kubernetes.io/ip_address"]
			}
			podNext, err := getNatGwPod(strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-"), r.Client)
			if err != nil {
				log.Log.Error(err, "Error get GwPod")
				return
			}
			GwExternIP, err := getGwExternIP(podNext)
			if err != nil {
				log.Log.Error(err, "Error get GwExternIP")
				return
			}
			err = r.Client.Update(ctx, vpc)
			if err != nil {
				log.Log.Error(err, "Error update Vpc")
				return
			}
			gatewayExIp.Spec.ExternalIP = GwExternIP
			gatewayExIp.Labels["localGateway"] = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")
			err = r.Client.Update(ctx, gatewayExIp)
			if err != nil {
				log.Log.Error(err, "Error update gatewayExIp")
				return
			}
			labelsSet := map[string]string{
				"localVpc": vpcName,
			}
			option := client.ListOptions{
				LabelSelector: labels.SelectorFromSet(labelsSet),
			}
			err = r.Client.List(ctx, &vpcNatTunnelList, &option)
			if err != nil {
				log.Log.Error(err, "Error get vpcNatTunnel list")
				return
			}
			for _, vpcTunnel := range vpcNatTunnelList.Items {
				vpcTunnel.Spec.InternalIP = GwExternIP
				vpcTunnel.Spec.LocalGw = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")
				if err = r.Client.Update(ctx, &vpcTunnel); err != nil {
					log.Log.Error(err, "Error update vpcTunnel")
					return
				}
			}
		},
	})
	if err != nil {
		return err
	}
	stopCh := make(chan struct{})
	defer close(stopCh)
	go informer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		return fmt.Errorf("error syncing cache")
	}
	select {
	case <-stopCh:
		log.Log.Info("received termination signal, exiting")
	}
	return nil
}
