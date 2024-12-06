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
	"strings"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kubeovnv1 "kubeovn-multivpc/api/v1"

	ovn "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// VpcDnsForwardReconciler reconciles a VpcDnsForward object
type VpcDnsForwardReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *rest.Config
}

//+kubebuilder:rbac:groups=example.io,resources=vpcdnsforwards,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=example.io,resources=vpcdnsforwards/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=example.io,resources=vpcdnsforwards/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.io,resources=subnets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.io,resources=vpc-dnses,verbs=get;list;watch;create;update;patch;delete

func (r *VpcDnsForwardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	vpcDns := &kubeovnv1.VpcDnsForward{}
	err := r.Get(ctx, req.NamespacedName, vpcDns)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !vpcDns.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, vpcDns)
	}
	return r.handleCreateOrUpdate(ctx, vpcDns)
}

func (r *VpcDnsForwardReconciler) handleCreateOrUpdate(ctx context.Context, vpcDns *kubeovnv1.VpcDnsForward) (ctrl.Result, error) {
	if !containsString(vpcDns.ObjectMeta.Finalizers, "dns.finalizer.example.io") {
		controllerutil.AddFinalizer(vpcDns, "dns.finalizer.example.io")
		err := r.Update(ctx, vpcDns)
		if err != nil {
			log.Log.Error(err, "Error Update VpcDnsForward")
			return ctrl.Result{}, err
		}
	}
	err := r.createDnsConnection(ctx, vpcDns)
	if err != nil {
		log.Log.Error(err, "Error createDnsConnection to VpcDnsForward")
		return ctrl.Result{}, err
	}
	log.Log.Info("create VpcDnsForward success: " + vpcDns.ObjectMeta.Name)
	return ctrl.Result{}, nil
}

func (r *VpcDnsForwardReconciler) handleDelete(ctx context.Context, vpcDns *kubeovnv1.VpcDnsForward) (ctrl.Result, error) {
	if containsString(vpcDns.ObjectMeta.Finalizers, "dns.finalizer.example.io") {
		err := r.deleteDnsConnection(ctx, vpcDns)
		if err != nil {
			log.Log.Error(err, "Error deleteDnsConnection to VpcDnsForward")
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(vpcDns, "dns.finalizer.example.io")
		err = r.Update(ctx, vpcDns)
		if err != nil {
			log.Log.Error(err, "Error Update VpcDnsForward")
			return ctrl.Result{}, err
		}
		log.Log.Info("delete VpcDnsForward success: " + vpcDns.ObjectMeta.Name)
	}
	return ctrl.Result{}, nil
}

// check Corefile of vpc-dns if updated
func (r *VpcDnsForwardReconciler) checkDnsCorefile(ctx context.Context) (bool, error) {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Name:      "vpc-dns-corefile",
		Namespace: "kube-system",
	}, cm)
	if err != nil {
		log.Log.Error(err, "Error Get ConfigMap vpc-dns-corefile")
		return false, err
	}
	// find whether "clusterset.local" in corefile
	if strings.Contains(cm.Data["Corefile"], "clusterset.local") {
		return true, nil
	} else {
		return false, nil
	}
}

func (r *VpcDnsForwardReconciler) updateDnsCorefile(ctx context.Context) error {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKey{
		Name:      "vpc-dns-corefile",
		Namespace: "kube-system",
	}, cm)
	if err != nil {
		log.Log.Error(err, "Error Get ConfigMap vpc-dns-corefile")
		return err
	}
	// find k8s coredns cluster ip
	coreDnsSvc := &corev1.Service{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Name:      "kube-dns",
		Namespace: "kube-system",
	}, coreDnsSvc)
	if err != nil {
		log.Log.Error(err, "Error Get Service kube-dns")
		return err
	}
	// add dns forward to k8s coredns cluster ip
	add := `clusterset.local:53 {
    forward . ` + coreDnsSvc.Spec.ClusterIP + `
  }
  .:53 {`
	cm.Data["Corefile"] = strings.Replace(cm.Data["Corefile"], ".:53 {", add, 1)
	err = r.Client.Update(ctx, cm)
	if err != nil {
		log.Log.Error(err, "Error Update ConfigMap vpc-dns-corefile")
		return err
	}
	return nil
}

func (r *VpcDnsForwardReconciler) genRouteToCoreDNS(ctx context.Context) (string, error) {
	// find k8s CoreDNS svc
	coreDnsSvc := &corev1.Service{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Name:      "kube-dns",
		Namespace: "kube-system",
	}, coreDnsSvc)
	if err != nil {
		log.Log.Error(err, "Error Get Service kube-dns")
		return "", err
	}
	// find default subnet
	subnet := &ovn.Subnet{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Name: "ovn-default",
	}, subnet)
	if err != nil {
		log.Log.Error(err, "Error Get Subnet ovn-default")
		return "", err
	}
	// add route from custom vpc to default vpc via vpc-dns net1
	route := `ip -4 route add ` + coreDnsSvc.Spec.ClusterIP + ` via ` + subnet.Spec.Gateway + ` dev net1;`
	return route, nil
}

func (r *VpcDnsForwardReconciler) getVpcDnsDeployment(ctx context.Context, vpcDns *kubeovnv1.VpcDnsForward) (*appsv1.Deployment, error) {
	// find Vpc-Dns list
	ovnDnsList := &ovn.VpcDnsList{}
	err := r.Client.List(ctx, ovnDnsList, &client.ListOptions{})
	if err != nil {
		log.Log.Error(err, "Error Get VpcDnsList")
		return nil, err
	}
	// find Vpc-Dns which status is Active
	ovnDns := &ovn.VpcDns{}
	for _, it := range ovnDnsList.Items {
		if it.Spec.Vpc == vpcDns.Spec.Vpc && it.Status.Active {
			*ovnDns = it
			break
		}
	}
	// find deployment of this vpc-dns
	vpcDnsDeployment := &appsv1.Deployment{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Name:      "vpc-dns-" + ovnDns.Name,
		Namespace: "kube-system",
	}, vpcDnsDeployment)
	if err != nil {
		log.Log.Error(err, "Error Get vpcDnsDeployment")
		return nil, err
	}
	return vpcDnsDeployment, nil
}

// add vpc-dns deployment route
func (r *VpcDnsForwardReconciler) createDnsConnection(ctx context.Context, vpcDns *kubeovnv1.VpcDnsForward) error {
	state, err := r.checkDnsCorefile(ctx)
	if err != nil {
		log.Log.Error(err, "Error checkDnsCorefile")
		return err
	}
	// if vpc-dns Corefile didn't update, then update it
	if !state {
		err = r.updateDnsCorefile(ctx)
		if err != nil {
			log.Log.Error(err, "Error updateDnsCorefile")
			return err
		}
	}

	route, err := r.genRouteToCoreDNS(ctx)
	if err != nil {
		log.Log.Error(err, "Error genRouteToCoreDNS")
		return err
	}
	vpcDnsDeployment, err := r.getVpcDnsDeployment(ctx, vpcDns)
	if err != nil {
		log.Log.Error(err, "Error getVpcDnsDeployment")
		return err
	}

	// add route from custom vpc to default vpc via vpc-dns net1 in vpc-dns deployment
	initContainers := &vpcDnsDeployment.Spec.Template.Spec.InitContainers[0]
	for j := 0; j < len(initContainers.Command); j++ {
		if strings.Contains(initContainers.Command[j], "ip -4 route add") {
			if !strings.Contains(initContainers.Command[j], route) {
				initContainers.Command[j] = initContainers.Command[j] + route
			}
		}
	}
	err = r.Client.Update(ctx, vpcDnsDeployment)
	if err != nil {
		log.Log.Error(err, "Error Update vpcDnsDeployment")
		return err
	}
	return nil
}

// delete vpc-dns deployment route
func (r *VpcDnsForwardReconciler) deleteDnsConnection(ctx context.Context, vpcDns *kubeovnv1.VpcDnsForward) error {
	route, err := r.genRouteToCoreDNS(ctx)
	if err != nil {
		log.Log.Error(err, "Error genRouteToCoreDNS")
		return err
	}
	vpcDnsDeployment, err := r.getVpcDnsDeployment(ctx, vpcDns)
	if err != nil {
		log.Log.Error(err, "Error getVpcDnsDeployment")
		return err
	}
	initContainers := &vpcDnsDeployment.Spec.Template.Spec.InitContainers[0]
	for j := 0; j < len(initContainers.Command); j++ {
		initContainers.Command[j] = strings.Replace(initContainers.Command[j], route, "", -1)
	}
	err = r.Client.Update(ctx, vpcDnsDeployment)
	if err != nil {
		log.Log.Error(err, "Error Update vpcDnsDeployment")
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VpcDnsForwardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeovnv1.VpcDnsForward{}).
		Complete(r)
}
