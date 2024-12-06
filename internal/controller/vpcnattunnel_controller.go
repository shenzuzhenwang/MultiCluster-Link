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
	"bytes"
	"context"
	"errors"
	"fmt"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"kubeovn-multivpc/internal/tunnel/factory"
	"strings"
	"time"

	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// VpcNatTunnelReconciler reconciles a VpcNatTunnel object
type VpcNatTunnelReconciler struct {
	client.Client
	ClusterId    string
	Scheme       *runtime.Scheme
	Config       *rest.Config
	tunnelOpFact *factory.TunnelOperationFactory
}

//+kubebuilder:rbac:groups=example.io,resources=vpcnattunnels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=example.io,resources=vpcnattunnels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=example.io,resources=vpcnattunnels/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/exec,verbs=get;create
//+kubebuilder:rbac:groups=submariner.io,resources=gateways,verbs=get;list;watch;
//+kubebuilder:rbac:groups=submariner.io,resources=clusterglobalegressips,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete

func (r *VpcNatTunnelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	vpcTunnel := &kubeovnv1.VpcNatTunnel{}
	err := r.Get(ctx, req.NamespacedName, vpcTunnel)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !vpcTunnel.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, vpcTunnel)
	}
	return r.handleCreateOrUpdate(ctx, vpcTunnel)
}

func (r *VpcNatTunnelReconciler) execCommandInPod(podName, namespace, containerName, command string) error {
	clientSet, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		log.Log.Error(err, "Error NewForConfig in execCommandInPod")
		return err
	}
	cmd := []string{
		"sh",
		"-c",
		command,
	}
	const tty = false
	req := clientSet.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).SubResource("exec").Param("container", containerName)
	req.VersionedParams(
		&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     tty,
		},
		scheme.ParameterCodec,
	)

	var stdout, stderr bytes.Buffer
	exec, err := remotecommand.NewSPDYExecutor(r.Config, "POST", req.URL())
	if err != nil {
		log.Log.Error(err, "Error NewSPDYExecutor in execCommandInPod")
		return err
	}
	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		log.Log.Error(err, "Error StreamWithContext in execCommandInPod")
		return err
	}
	if strings.TrimSpace(stderr.String()) != "" {
		return fmt.Errorf(strings.TrimSpace(stderr.String()))
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VpcNatTunnelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Config = mgr.GetConfig()
	r.tunnelOpFact = factory.NewTunnelOpFactory()
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeovnv1.VpcNatTunnel{}).
		Complete(r)
}

func GenNatGwStsName(name string) string {
	return fmt.Sprintf("vpc-nat-gw-%s", name)
}

func getNatGwPod(name string, c client.Client) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	matchLabels := map[string]string{"app": GenNatGwStsName(name), "ovn.kubernetes.io/vpc-nat-gw": "true"}
	listOpts := []client.ListOption{
		client.InNamespace("kube-system"),
		client.MatchingLabels(matchLabels),
	}
	err := c.List(context.TODO(), podList, listOpts...)
	if err != nil {
		return nil, err
	}
	pods := podList.Items
	switch {
	case len(pods) == 0:
		return nil, k8serrors.NewNotFound(corev1.Resource("pod"), name)
	case len(pods) != 1:
		time.Sleep(5 * time.Second)
		return nil, fmt.Errorf("too many pod")
	case pods[0].Status.Phase != "Running":
		time.Sleep(5 * time.Second)
		return nil, fmt.Errorf("pod is not active now")
	}

	return &pods[0], nil
}

func (r *VpcNatTunnelReconciler) getGlobalnetCIDR() (string, error) {
	submarinerCluster := &Submariner.Cluster{}
	err := r.Client.Get(context.Background(), client.ObjectKey{
		Namespace: "submariner-operator",
		Name:      r.ClusterId,
	}, submarinerCluster)
	if err != nil {
		log.Log.Error(err, "Error get submarinerCluster")
		return "", err
	}
	return submarinerCluster.Spec.GlobalCIDR[0], nil
}

func (r *VpcNatTunnelReconciler) getGlobalEgressIP() ([]string, error) {
	submGlobalEgressIP := &Submariner.ClusterGlobalEgressIP{}
	err := r.Get(context.TODO(), client.ObjectKey{Name: "cluster-egress.submariner.io"}, submGlobalEgressIP)
	if err != nil {
		return nil, err
	}
	return submGlobalEgressIP.Status.AllocatedIPs, nil
}

func getGwExternIP(pod *corev1.Pod) (string, error) {
	if ExternIP, ok := pod.Annotations["ovn-vpc-external-network.kube-system.kubernetes.io/ip_address"]; ok {
		return ExternIP, nil
	} else {
		return "", fmt.Errorf("no ovn-vpc-external-network ip")
	}
}

func (r *VpcNatTunnelReconciler) getOvnGwIP(pod *corev1.Pod) (string, error) {
	if gw, ok := pod.Annotations["ovn.kubernetes.io/gateway"]; ok {
		return gw, nil
	} else {
		return "", fmt.Errorf("no ovn gateway")
	}
}

func genCreateTunnelCmd(tunnelOpFact *factory.TunnelOperationFactory, tunnel *kubeovnv1.VpcNatTunnel) string {
	return tunnelOpFact.CreateTunnelOperation(tunnel).CreateCmd()
}

func genGlobalnetRoute(vpcTunnel *kubeovnv1.VpcNatTunnel) string {
	InFlowRoute := fmt.Sprintf("ip route add %s via %s dev eth0", vpcTunnel.Status.GlobalnetCIDR, vpcTunnel.Status.OvnGwIP)
	OutFlowRoute := fmt.Sprintf("ip route add %s dev %s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Name)

	SNAT := fmt.Sprintf("iptables -t nat -A POSTROUTING -d %s -j SNAT --to-source %s-%s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Status.GlobalEgressIP[0], vpcTunnel.Status.GlobalEgressIP[len(vpcTunnel.Status.GlobalEgressIP)-1])
	return InFlowRoute + ";" + OutFlowRoute + ";" + SNAT
}

func genDelGlobalnetRoute(vpcTunnel *kubeovnv1.VpcNatTunnel) string {
	InFlowRoute := fmt.Sprintf("ip route del %s via %s dev eth0", vpcTunnel.Status.GlobalnetCIDR, vpcTunnel.Status.OvnGwIP)
	OutFlowRoute := fmt.Sprintf("ip route del %s dev %s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Name)

	SNAT := fmt.Sprintf("iptables -t nat -D POSTROUTING -d %s -j SNAT --to-source %s-%s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Status.GlobalEgressIP[0], vpcTunnel.Status.GlobalEgressIP[len(vpcTunnel.Status.GlobalEgressIP)-1])
	return InFlowRoute + ";" + OutFlowRoute + ";" + SNAT
}

func genDeleteTunnelCmd(tunnelOpFact *factory.TunnelOperationFactory, tunnel *kubeovnv1.VpcNatTunnel) string {
	return tunnelOpFact.CreateTunnelOperation(tunnel).DeleteCmd()
}

func (r *VpcNatTunnelReconciler) handleCreateOrUpdate(ctx context.Context, vpcTunnel *kubeovnv1.VpcNatTunnel) (ctrl.Result, error) {
	if !containsString(vpcTunnel.ObjectMeta.Finalizers, "tunnel.finalizer.example.io") {
		controllerutil.AddFinalizer(vpcTunnel, "tunnel.finalizer.example.io")
		err := r.Update(ctx, vpcTunnel)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	remoteGatewayExIp := &kubeovnv1.GatewayExIp{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Name:      vpcTunnel.Spec.RemoteVpc + "." + vpcTunnel.Spec.RemoteCluster,
		Namespace: "kube-system",
	}, remoteGatewayExIp)
	if err != nil {
		log.Log.Error(err, "Error get GatewayExIp")
		return ctrl.Result{}, err
	}
	// first build tunnel
	if !vpcTunnel.Status.Initialized {
		localGatewayExIp := &kubeovnv1.GatewayExIp{}
		err := r.Client.Get(ctx, client.ObjectKey{
			Name:      vpcTunnel.Spec.LocalVpc + "." + r.ClusterId,
			Namespace: "kube-system",
		}, localGatewayExIp)
		if err != nil {
			log.Log.Error(err, "Error get local GatewayExIp")
			return ctrl.Result{}, err
		}

		vpcTunnel.Status.LocalGw = localGatewayExIp.GetObjectMeta().GetLabels()["localGateway"]
		vpcTunnel.Status.RemoteIP = remoteGatewayExIp.Spec.ExternalIP
		vpcTunnel.Status.RemoteGlobalnetCIDR = remoteGatewayExIp.Spec.GlobalNetCIDR
		vpcTunnel.Status.Initialized = true
		vpcTunnel.Status.InterfaceAddr = vpcTunnel.Spec.InterfaceAddr
		vpcTunnel.Status.Type = vpcTunnel.Spec.Type

		gwPod, err := getNatGwPod(vpcTunnel.Status.LocalGw, r.Client)
		if err != nil {
			log.Log.Error(err, "Error get GwPod")
			return ctrl.Result{}, err
		}
		vpcTunnel.Status.GlobalnetCIDR, err = r.getGlobalnetCIDR()
		if err != nil {
			log.Log.Error(err, "Error get local GlobalNetCIDR")
			return ctrl.Result{}, err
		}
		vpcTunnel.Status.OvnGwIP, err = r.getOvnGwIP(gwPod)
		if err != nil {
			log.Log.Error(err, "Error get local PodGwIP")
			return ctrl.Result{}, err
		}
		vpcTunnel.Status.GlobalEgressIP, err = r.getGlobalEgressIP()
		if err != nil {
			log.Log.Error(err, "Error get local GlobalEgressIP")
			return ctrl.Result{}, err
		}
		vpcTunnel.Status.InternalIP, err = getGwExternIP(gwPod)
		if err != nil {
			log.Log.Error(err, "Error get local GwExternIP")
			return ctrl.Result{}, err
		}

		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genCreateTunnelCmd(r.tunnelOpFact, vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genCreateTunnelCmd in Initialized")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genGlobalNetRoute in Initialized")
			return ctrl.Result{}, err
		}
		err = r.Status().Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel status")
			return ctrl.Result{}, err
		}
		// add label to find tunnel
		labels := vpcTunnel.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		vpcTunnel.Spec.InternalIP = vpcTunnel.Status.InternalIP
		vpcTunnel.Spec.LocalGw = localGatewayExIp.GetObjectMeta().GetLabels()["localGateway"]
		vpcTunnel.Spec.RemoteIP = remoteGatewayExIp.Spec.ExternalIP
		labels["localVpc"] = vpcTunnel.Spec.LocalVpc
		labels["remoteCluster"] = vpcTunnel.Spec.RemoteCluster
		labels["remoteVpc"] = vpcTunnel.Spec.RemoteVpc
		vpcTunnel.Labels = labels
		err = r.Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel")
			return ctrl.Result{}, err
		}
		log.Log.Info("create VpcNatTunnel success: " + vpcTunnel.Name)
		// if ClusterId or GatewayName update, then gatewayExIp.Spec.ExternalIP or gatewayExIp.Spec.GlobalNetCIDR will update too
	} else if vpcTunnel.Status.RemoteIP != vpcTunnel.Spec.RemoteIP || vpcTunnel.Status.InternalIP != vpcTunnel.Spec.InternalIP ||
		vpcTunnel.Status.InterfaceAddr != vpcTunnel.Spec.InterfaceAddr || vpcTunnel.Status.LocalGw != vpcTunnel.Spec.LocalGw ||
		vpcTunnel.Status.RemoteGlobalnetCIDR != remoteGatewayExIp.Spec.GlobalNetCIDR || vpcTunnel.Status.Type != vpcTunnel.Spec.Type {
		// can't update type
		if vpcTunnel.Status.Type != vpcTunnel.Spec.Type {
			log.Log.Error(errors.New("tunnel type should not change"), fmt.Sprintf("tunnel :%#v\n", vpcTunnel))
			vpcTunnel.Spec.Type = vpcTunnel.Status.Type
			err := r.Update(ctx, vpcTunnel)
			if err != nil {
				log.Log.Error(err, "Error Update vpcTunnel")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if vpcTunnel.Spec.LocalGw == "" && vpcTunnel.Spec.RemoteIP == "" && vpcTunnel.Spec.InternalIP == "" {
			return ctrl.Result{}, nil
		}
		gwPod := &corev1.Pod{}
		// if gateway remain alive, we should delete route and tunnel, then create again
		if vpcTunnel.Status.LocalGw == vpcTunnel.Spec.LocalGw && vpcTunnel.Spec.InternalIP == vpcTunnel.Status.InternalIP {
			gwPod, err = getNatGwPod(vpcTunnel.Status.LocalGw, r.Client)
			if err != nil {
				log.Log.Error(err, "Error get GwPod")
				return ctrl.Result{}, err
			}
			err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genDelGlobalnetRoute(vpcTunnel))
			if err != nil {
				log.Log.Error(err, "Error exec genDelGlobalnetRoute in update")
				return ctrl.Result{}, err
			}
			err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genDeleteTunnelCmd(r.tunnelOpFact, vpcTunnel))
			if err != nil {
				log.Log.Error(err, "Error exec genDeleteTunnelCmd in update")
				return ctrl.Result{}, err
			}
		} else {
			vpcTunnel.Status.LocalGw = vpcTunnel.Spec.LocalGw
			gwPod, err = getNatGwPod(vpcTunnel.Status.LocalGw, r.Client)
			if err != nil {
				log.Log.Error(err, "Error get GwPod")
				return ctrl.Result{}, err
			}
		}

		log.Log.Info("VpcNatTunnel Spec: " + vpcTunnel.Spec.LocalGw + "," + vpcTunnel.Spec.InternalIP + "," + vpcTunnel.Spec.RemoteIP)
		log.Log.Info("VpcNatTunnel Status: " + vpcTunnel.Status.LocalGw + "," + vpcTunnel.Status.InternalIP + "," + vpcTunnel.Status.RemoteIP)

		vpcTunnel.Status.RemoteIP = vpcTunnel.Spec.RemoteIP
		vpcTunnel.Status.RemoteGlobalnetCIDR = remoteGatewayExIp.Spec.GlobalNetCIDR
		vpcTunnel.Status.InternalIP = vpcTunnel.Spec.InternalIP
		vpcTunnel.Status.InterfaceAddr = vpcTunnel.Spec.InterfaceAddr

		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genCreateTunnelCmd(r.tunnelOpFact, vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genCreateTunnelCmd in update")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genGlobalNetRoute in update")
			return ctrl.Result{}, err
		}
		err = r.Status().Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel")
			return ctrl.Result{}, err
		}
		// if RemoteCluster, RemoteVpc or LocalVpc update, label need to update
		if vpcTunnel.Labels["remoteCluster"] != vpcTunnel.Spec.RemoteCluster || vpcTunnel.Labels["remoteVpc"] != vpcTunnel.Spec.RemoteVpc || vpcTunnel.Labels["localVpc"] != vpcTunnel.Spec.LocalVpc {
			vpcTunnel.Labels["remoteCluster"] = vpcTunnel.Spec.RemoteCluster
			vpcTunnel.Labels["remoteVpc"] = vpcTunnel.Spec.RemoteVpc
			vpcTunnel.Labels["localVpc"] = vpcTunnel.Spec.LocalVpc
			if err = r.Update(ctx, vpcTunnel); err != nil {
				log.Log.Error(err, "Error Update vpcTunnel")
				return ctrl.Result{}, err
			}
		}
		log.Log.Info("update VpcNatTunnel success: " + vpcTunnel.Name)
	}
	return ctrl.Result{}, nil
}

func (r *VpcNatTunnelReconciler) handleDelete(ctx context.Context, vpcTunnel *kubeovnv1.VpcNatTunnel) (ctrl.Result, error) {
	if containsString(vpcTunnel.ObjectMeta.Finalizers, "tunnel.finalizer.example.io") {

		// delete route and tunnel
		gwPod, err := getNatGwPod(vpcTunnel.Status.LocalGw, r.Client)
		if err != nil {
			log.Log.Error(err, "Error get GwPod")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genDelGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genDelGlobalNetRoute in handleDelete")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genDeleteTunnelCmd(r.tunnelOpFact, vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genDeleteTunnelCmd in handleDelete")
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(vpcTunnel, "tunnel.finalizer.example.io")
		err = r.Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel")
			return ctrl.Result{}, err
		}
		log.Log.Info("delete VpcNatTunnel success: " + vpcTunnel.Name)
	}
	return ctrl.Result{}, nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
