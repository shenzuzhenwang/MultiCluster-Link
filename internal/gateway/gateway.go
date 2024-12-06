package gateway

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	apiv1 "example.io/pkg/apis/v1"
	gwController "example.io/pkg/gwController"
	"example.io/pkg/util"
)

var (
	natGatewayEnabled       = "unknown"
	NatGatewayConfigVersion = ""
	natGwCreationTime       = ""
)

const (
	natGwInitialSetup      = "init"
	natGwAddFloatingIP     = "floating-ip-add"
	natGwRemoveFloatingIP  = "floating-ip-del"
	natGwAddSubnetRoute    = "subnet-route-add"
	natGwRemoveSubnetRoute = "subnet-route-del"
	natGwAddExtSubnetRoute = "ext-subnet-route-add"
	natGwAddDnat           = "dnat-add"
	natGwRemoveDnat        = "dnat-del"
	natGwAddSnat           = "snat-add"
	natGwRemoveSnat        = "snat-del"

	retrieveIptablesVersion = "get-iptables-version"
)

func (c *gwController.Controller) syncNatGatewayConfig() {
	configMap, err := c.configMapsLister.ConfigMaps(c.config.PodNamespace).Get(util.NatGatewayConfig)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("failed to get nat-gateway-config, %v", err)
		return
	}

	if k8serrors.IsNotFound(err) || configMap.Data["enable-nat-gw"] == "false" {
		if natGatewayEnabled == "false" {
			return
		}
		klog.Info("start cleaning up nat gateway")
		if err := c.cleanUpNatGateway(); err != nil {
			klog.Errorf("failed to clean up nat gateway, %v", err)
			return
		}
		natGatewayEnabled = "false"
		NatGatewayConfigVersion = ""
		klog.Info("finished cleaning up nat gateway")
		return
	}
	if natGatewayEnabled == "true" && NatGatewayConfigVersion == configMap.ResourceVersion {
		return
	}
	gateways, err := c.natGatewayLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list nat gateways, %v", err)
		return
	}
	if err = c.syncNatGatewayImage(); err != nil {
		klog.Errorf("failed to sync nat gateway config, err: %v", err)
		return
	}
	natGatewayEnabled = "true"
	NatGatewayConfigVersion = configMap.ResourceVersion
	for _, gw := range gateways {
		c.addOrUpdateNatGatewayQueue.Add(gw.Name)
	}
	klog.Info("finished setting up nat-gateway")
}

func (c *gwController.Controller) handleDeleteNatGw(key string) error {
	c.natGwKeyMutex.LockKey(key)
	defer func() { _ = c.natGwKeyMutex.UnlockKey(key) }()
	name := util.GenerateNatGwStsName(key)
	klog.Infof("deleting nat gw %s", name)
	if err := c.config.KubeClient.AppsV1().StatefulSets(c.config.PodNamespace).Delete(context.Background(),
		name, metav1.DeleteOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func (c *gwController.Controller) enqueueNatGwCreation(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(3).Infof("enqueue create nat gw %s", key)
	c.addOrUpdateNatGatewayQueue.Add(key)
}

func (c *gwController.Controller) enqueueNatGwUpdate(_, newObj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(newObj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(3).Infof("enqueue update nat gw %s", key)
	c.addOrUpdateNatGatewayQueue.Add(key)
}

func (c *gwController.Controller) enqueueNatGwDeletion(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(3).Infof("enqueue delete nat gw %s", key)
	c.deleteNatGatewayQueue.Add(key)
}

func (c *gwController.Controller) runNatGwAddOrUpdateWorker() {
	for c.processNextQueueItem("addOrUpdateNatGateway", c.addOrUpdateNatGatewayQueue, c.handleAddOrUpdateNatGw) {
	}
}

func (c *gwController.Controller) runNatGwDeletionWorker() {
	for c.processNextQueueItem("deleteNatGateway", c.deleteNatGatewayQueue, c.handleDeleteNatGw) {
	}
}

func (c *gwController.Controller) processNextQueueItem(name string, queue workqueue.RateLimitingInterface, handler func(key string) error) bool {
	obj, shutdown := queue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer queue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			queue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := handler(key); err != nil {
			return fmt.Errorf("error processing '%s': %s, requeuing", key, err.Error())
		}
		queue.Forget(obj)
		return nil
	}(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("process: %s. err: %v", name, err))
		queue.AddRateLimited(obj)
		return true
	}
	return true
}

func (c *gwController.Controller) handleAddOrUpdateNatGw(key string) error {
	c.natGwKeyMutex.LockKey(key)
	defer func() { _ = c.natGwKeyMutex.UnlockKey(key) }()
	klog.Infof("handling add/update for nat gateway %s", key)

	if natGatewayEnabled != "true" {
		return fmt.Errorf("nat gw not enabled")
	}
	gw, err := c.natGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		klog.Error(err)
		return err
	}
	if _, err := c.vpcLister.Get(gw.Spec.Vpc); err != nil {
		err = fmt.Errorf("failed to get vpc '%s', err: %v", gw.Spec.Vpc, err)
		klog.Error(err)
		return err
	}
	if _, err := c.subnetLister.Get(gw.Spec.Subnet); err != nil {
		err = fmt.Errorf("failed to get subnet '%s', err: %v", gw.Spec.Subnet, err)
		klog.Error(err)
		return err
	}

	// Check and create statefulset if needed
	needsCreation := false
	needsUpdate := false
	existingSts, err := c.config.KubeClient.AppsV1().StatefulSets(c.config.PodNamespace).
		Get(context.Background(), util.GenerateNatGwStsName(gw.Name), metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			needsCreation = true
		} else {
			klog.Error(err)
			return err
		}
	}
	newSts := c.generateNatGwStatefulSet(gw, existingSts.DeepCopy())
	if !needsCreation && isNatGwModified(gw) {
		needsUpdate = true
	}

	switch {
	case needsCreation:
		if _, err := c.config.KubeClient.AppsV1().StatefulSets(c.config.PodNamespace).
			Create(context.Background(), newSts, metav1.CreateOptions{}); err != nil {
			err := fmt.Errorf("failed to create statefulset '%s', err: %v", newSts.Name, err)
			klog.Error(err)
			return err
		}
		if err = c.updateNatGwStatus(key); err != nil {
			klog.Errorf("failed to update nat gw status for %s, %v", key, err)
			return err
		}
	case needsUpdate:
		if _, err := c.config.KubeClient.AppsV1().StatefulSets(c.config.PodNamespace).
			Update(context.Background(), newSts, metav1.UpdateOptions{}); err != nil {
			err := fmt.Errorf("failed to update statefulset '%s', err: %v", newSts.Name, err)
			klog.Error(err)
			return err
		}
		if err = c.updateNatGwStatus(key); err != nil {
			klog.Errorf("failed to update nat gw status for %s, %v", key, err)
			return err
		}
	default:
	}
	return nil
}

func (c *gwController.Controller) cleanUpNatGateway() error {
	gateways, err := c.natGatewayLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list nat gateways, %v", err)
		return err
	}
	for _, gw := range gateways {
		c.deleteNatGatewayQueue.Add(gw.Name)
	}
	return nil
}

func isNatGwModified(gw *apiv1.VpcNatGateway) bool {
	if !reflect.DeepEqual(gw.Spec.ExternalSubnets, gw.Status.ExternalSubnets) {
		gw.Status.ExternalSubnets = gw.Spec.ExternalSubnets
		return true
	}
	if !reflect.DeepEqual(gw.Spec.Selector, gw.Status.Selector) {
		gw.Status.Selector = gw.Spec.Selector
		return true
	}
	if !reflect.DeepEqual(gw.Spec.Tolerations, gw.Status.Tolerations) {
		gw.Status.Tolerations = gw.Spec.Tolerations
		return true
	}
	if !reflect.DeepEqual(gw.Spec.Affinity, gw.Status.Affinity) {
		gw.Status.Affinity = gw.Spec.Affinity
		return true
	}
	return false
}

var natGwImage = ""

func (c *gwController.Controller) syncNatGatewayImage() error {
	cm, err := c.configMapsLister.ConfigMaps(c.config.PodNamespace).Get(util.VpcNatConfig)
	if err != nil {
		err = fmt.Errorf("failed to get ovn-vpc-nat-config, %v", err)
		klog.Error(err)
		return err
	}
	image, exist := cm.Data["image"]
	if !exist {
		err = fmt.Errorf("%s should have image field", util.VpcNatConfig)
		klog.Error(err)
		return err
	}
	natGwImage = image
	return nil
}

func (c *gwController.Controller) generateNatGwStatefulSet(gw *apiv1.VpcNatGateway, oldSts *v1.StatefulSet) *v1.StatefulSet {
	replicas := int32(1)
	name := util.GenerateNatGwStsName(gw.Name)
	privEscalation := true
	privileged := true
	labels := map[string]string{
		"app":                name,
		util.NatGatewayLabel: "true",
	}
	newPodAnnotations := map[string]string{}
	if oldSts != nil && len(oldSts.Annotations) != 0 {
		newPodAnnotations = oldSts.Annotations
	}
	externalNetwork := util.GetNatGwExternalNetwork(gw.Spec.ExternalSubnets)
	podAnnotations := map[string]string{
		util.NatGatewayAnnotation:        gw.Name,
		util.AttachmentNetworkAnnotation: fmt.Sprintf("%s/%s", c.config.PodNamespace, externalNetwork),
		util.LogicalSwitchAnnotation:     gw.Spec.Subnet,
		util.IPAddressAnnotation:         gw.Spec.LanIP,
	}
	for key, value := range podAnnotations {
		newPodAnnotations[key] = value
	}

	selectors := make(map[string]string)
	for _, v := range gw.Spec.Selector {
		parts := strings.Split(strings.TrimSpace(v), ":")
		if len(parts) != 2 {
			continue
		}
		selectors[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	klog.V(3).Infof("Preparing nat gateway pod, node selector: %v", selectors)
	v4SubnetGw, _, _ := c.GetGatewayBySubnet(gw.Spec.Subnet)
	newSts := &v1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: v1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: newPodAnnotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "nat-gw",
							Image:           natGwImage,
							Command:         []string{"bash"},
							Args:            []string{"-c", "while true; do sleep 10000; done"},
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								Privileged:               &privileged,
								AllowPrivilegeEscalation: &privEscalation,
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:            "nat-gw-init",
							Image:           natGwImage,
							Command:         []string{"bash"},
							Args:            []string{"-c", fmt.Sprintf("bash /kube-ovn/nat-gateway.sh init %s,%s", c.config.ServiceClusterIPRange, v4SubnetGw)},
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								Privileged:               &privileged,
								AllowPrivilegeEscalation: &privEscalation,
							},
						},
					},
					NodeSelector: selectors,
					Tolerations:  gw.Spec.Tolerations,
					Affinity:     &gw.Spec.Affinity,
				},
			},
			UpdateStrategy: v1.StatefulSetUpdateStrategy{
				Type: v1.RollingUpdateStatefulSetStrategyType,
			},
		},
	}
	return newSts
}

// 	// Check if the VPC NAT Gateway already exists
// 	gw, err := c.vpcNatGatewayLister.Get(key)
// 	if err != nil {
// 		if k8serrors.IsNotFound(err) {
// 			klog.Warningf("VPC NAT Gateway %s not found, skipping add/update", key)
// 			return nil
// 		}
// 		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
// 	}

// 	// Check if the gateway is changed
// 	if isVpcNatGwChanged(gw) {
// 		klog.Infof("VPC NAT Gateway %s configuration changed", key)
// 		// Update the StatefulSet for the changed configuration
// 		if err := c.updateVpcNatGwStatefulSet(gw); err != nil {
// 			return fmt.Errorf("failed to update StatefulSet for VPC NAT Gateway %s: %v", key, err)
// 		}
// 	}

// 	// Create or update the gateway's services, IPs, and other resources
// 	if err := c.createOrUpdateVpcNatGwResources(gw); err != nil {
// 		return fmt.Errorf("failed to create/update resources for VPC NAT Gateway %s: %v", key, err)
// 	}

// 	klog.Infof("Successfully handled add/update for VPC NAT Gateway %s", key)
// 	return nil
// }

func (c *gwController.Controller) updateVpcNatGwStatefulSet(gw *apiv1.VpcNatGateway) error {
	name := util.GenNatGwStsName(gw.Name)
	statefulSet, err := c.config.KubeClient.AppsV1().StatefulSets(c.config.PodNamespace).Get(context.Background(),
		name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get StatefulSet for VPC NAT Gateway %s: %v", name, err)
	}

	// Update StatefulSet with the new configuration
	statefulSet.Spec.Template.Spec.Containers[0].Args = []string{
		"vpc-nat-gw",
		"--config", gw.Spec.ConfigFile,
	}

	_, err = c.config.KubeClient.AppsV1().StatefulSets(c.config.PodNamespace).Update(context.Background(),
		statefulSet, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update StatefulSet %s for VPC NAT Gateway %s: %v", name, gw.Name, err)
	}

	klog.Infof("Successfully updated StatefulSet for VPC NAT Gateway %s", name)
	return nil
}

func (c *gwController.Controller) createOrUpdateVpcNatGwResources(gw *apiv1.VpcNatGateway) error {
	// Ensure the necessary services and IPs are created or updated for the VPC NAT Gateway
	if err := c.ensureVpcNatGwServices(gw); err != nil {
		return fmt.Errorf("failed to ensure services for VPC NAT Gateway %s: %v", gw.Name, err)
	}
	if err := c.ensureVpcNatGwIPs(gw); err != nil {
		return fmt.Errorf("failed to ensure IPs for VPC NAT Gateway %s: %v", gw.Name, err)
	}

	klog.Infof("Successfully created/updated resources for VPC NAT Gateway %s", gw.Name)
	return nil
}

func (c *gwController.Controller) ensureVpcNatGwServices(gw *apiv1.VpcNatGateway) error {
	// Ensure the service for the VPC NAT Gateway is created or updated
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.GenNatGwServiceName(gw.Name),
			Namespace: c.config.PodNamespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: gw.Spec.Selector,
			Ports: []corev1.ServicePort{
				{
					Port: 80,
				},
			},
		},
	}

	_, err := c.config.KubeClient.CoreV1().Services(c.config.PodNamespace).Create(context.Background(), service, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create service for VPC NAT Gateway %s: %v", gw.Name, err)
	}

	klog.Infof("Successfully created/updated service for VPC NAT Gateway %s", gw.Name)
	return nil
}

func (c *gwController.Controller) ensureVpcNatGwIPs(gw *apiv1.VpcNatGateway) error {
	// Ensure the floating IPs and other IP resources are created or updated
	if gw.Spec.FloatingIP != "" {
		if err := c.createOrUpdateVpcNatGwFloatingIP(gw); err != nil {
			return fmt.Errorf("failed to create/update floating IP for VPC NAT Gateway %s: %v", gw.Name, err)
		}
	}

	klog.Infof("Successfully created/updated IP resources for VPC NAT Gateway %s", gw.Name)
	return nil
}

func (c *gwController.Controller) createOrUpdateVpcNatGwFloatingIP(gw *apiv1.VpcNatGateway) error {
	// Create or update floating IP for the VPC NAT Gateway
	fip := &apiv1.FloatingIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      util.GenNatGwFipName(gw.Name),
			Namespace: c.config.PodNamespace,
		},
		Spec: apiv1.FloatingIPSpec{
			IP: gw.Spec.FloatingIP,
		},
	}

	_, err := c.config.KubeClient.CoreV1().ConfigMaps(c.config.PodNamespace).Create(context.Background(), fip, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create floating IP for VPC NAT Gateway %s: %v", gw.Name, err)
	}

	klog.Infof("Successfully created/updated floating IP for VPC NAT Gateway %s", gw.Name)
	return nil
}
func (c *gwController.Controller) handleUpdateVpcFloatingIP(key string) error {
	// Handle the update for VPC Floating IP
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc floating IP %s", key)

	// Fetch the VPC NAT Gateway associated with the floating IP
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Check if the gateway is in the correct state for floating IP update
	if !gw.Spec.EnableFloatingIP {
		klog.Infof("VPC NAT Gateway %s does not have floating IP enabled, skipping update", key)
		return nil
	}

	// Update the floating IP resource
	if err := c.createOrUpdateVpcNatGwFloatingIP(gw); err != nil {
		return fmt.Errorf("failed to update floating IP for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC floating IP %s", key)
	return nil
}

func (c *gwController.Controller) handleUpdateVpcEip(key string) error {
	// Handle the update for VPC EIP (Elastic IP)
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc EIP %s", key)

	// Fetch the VPC NAT Gateway associated with the EIP
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the EIP resource
	if err := c.createOrUpdateVpcNatGwEIP(gw); err != nil {
		return fmt.Errorf("failed to update EIP for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC EIP %s", key)
	return nil
}

func (c *gwController.Controller) handleUpdateVpcDnat(key string) error {
	// Handle the update for VPC DNAT (Destination NAT)
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc DNAT %s", key)

	// Fetch the VPC NAT Gateway associated with DNAT
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the DNAT configuration
	if err := c.updateVpcNatGwDnat(gw); err != nil {
		return fmt.Errorf("failed to update DNAT for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC DNAT %s", key)
	return nil
}

func (c *gwController.Controller) handleUpdateVpcSnat(key string) error {
	// Handle the update for VPC SNAT (Source NAT)
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc SNAT %s", key)

	// Fetch the VPC NAT Gateway associated with SNAT
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the SNAT configuration
	if err := c.updateVpcNatGwSnat(gw); err != nil {
		return fmt.Errorf("failed to update SNAT for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC SNAT %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwDnat(gw *apiv1.VpcNatGateway) error {
	// Update the DNAT configuration for the VPC NAT Gateway
	klog.Infof("Updating DNAT for VPC NAT Gateway %s", gw.Name)
	// Add your DNAT update logic here
	return nil
}

func (c *gwController.Controller) updateVpcNatGwSnat(gw *apiv1.VpcNatGateway) error {
	// Update the SNAT configuration for the VPC NAT Gateway
	klog.Infof("Updating SNAT for VPC NAT Gateway %s", gw.Name)
	// Add your SNAT update logic here
	return nil
}

func (c *gwController.Controller) createOrUpdateVpcNatGwEIP(gw *apiv1.VpcNatGateway) error {
	// Create or update EIP for the VPC NAT Gateway
	klog.Infof("Creating or updating EIP for VPC NAT Gateway %s", gw.Name)
	// Add your EIP creation/update logic here
	return nil
}

func (c *gwController.Controller) handleUpdateVpcSubnetRoute(key string) error {
	// Handle the update for VPC Subnet Route
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc subnet route %s", key)

	// Fetch the VPC NAT Gateway associated with subnet route
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the VPC NAT Gateway subnet route
	if err := c.updateVpcNatGwSubnetRoute(gw); err != nil {
		return fmt.Errorf("failed to update subnet route for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC subnet route %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwSubnetRoute(gw *apiv1.VpcNatGateway) error {
	// Update the subnet route for the VPC NAT Gateway
	klog.Infof("Updating subnet route for VPC NAT Gateway %s", gw.Name)
	// Add your subnet route update logic here
	return nil
}
func (c *gwController.Controller) handleUpdateVpcNATRule(key string) error {
	// Handle the update for VPC NAT rule (DNAT, SNAT)
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc NAT rule %s", key)

	// Fetch the VPC NAT Gateway associated with NAT rule
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the NAT rule configuration
	if err := c.updateVpcNatGwNATRule(gw); err != nil {
		return fmt.Errorf("failed to update NAT rule for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC NAT rule %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwNATRule(gw *apiv1.VpcNatGateway) error {
	// Update the NAT rule configuration for the VPC NAT Gateway
	klog.Infof("Updating NAT rule for VPC NAT Gateway %s", gw.Name)
	// Implement your logic for updating the NAT rule (e.g., add/update DNAT/SNAT rules)
	return nil
}

func (c *gwController.Controller) handleUpdateVpcSubnet(key string) error {
	// Handle the update for VPC subnet
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc subnet %s", key)

	// Fetch the VPC subnet associated with the VPC NAT Gateway
	subnet, err := c.vpcSubnetLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC Subnet %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC Subnet %s: %v", key, err)
	}

	// Update the subnet configuration
	if err := c.updateVpcNatGwSubnet(subnet); err != nil {
		return fmt.Errorf("failed to update subnet for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC subnet %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwSubnet(subnet *apiv1.VpcSubnet) error {
	// Update the subnet configuration for the VPC NAT Gateway
	klog.Infof("Updating subnet for VPC NAT Gateway %s", subnet.Name)
	// Implement logic for updating subnet-specific configurations here
	return nil
}

func (c *gwController.Controller) handleUpdateVpcNatRuleMapping(key string) error {
	// Handle the update for VPC NAT rule mapping (DNAT/SNAT rules mapping)
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc NAT rule mapping %s", key)

	// Fetch the VPC NAT Gateway mapping associated with NAT rule
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the NAT rule mapping configuration
	if err := c.updateVpcNatGwNatRuleMapping(gw); err != nil {
		return fmt.Errorf("failed to update NAT rule mapping for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC NAT rule mapping %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwNatRuleMapping(gw *apiv1.VpcNatGateway) error {
	// Update the NAT rule mapping configuration for the VPC NAT Gateway
	klog.Infof("Updating NAT rule mapping for VPC NAT Gateway %s", gw.Name)
	// Implement your logic to update rule mappings (DNAT/SNAT rule mappings)
	return nil
}

func (c *gwController.Controller) handleUpdateVpcNatGateway(key string) error {
	// Handle the update for VPC NAT Gateway
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc NAT gateway %s", key)

	// Fetch the VPC NAT Gateway
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Update the VPC NAT Gateway configuration
	if err := c.updateVpcNatGw(gw); err != nil {
		return fmt.Errorf("failed to update VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC NAT Gateway %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGw(gw *apiv1.VpcNatGateway) error {
	// Update the VPC NAT Gateway configuration
	klog.Infof("Updating VPC NAT Gateway %s", gw.Name)
	// Implement logic to update the gateway configuration (e.g., floating IP, EIP, SNAT, DNAT)
	return nil
}

func (c *gwController.Controller) handleDeleteVpcNatGateway(key string) error {
	// Handle the delete for VPC NAT Gateway
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle delete vpc NAT gateway %s", key)

	// Fetch the VPC NAT Gateway
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping delete", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Delete the VPC NAT Gateway
	if err := c.deleteVpcNatGw(gw); err != nil {
		return fmt.Errorf("failed to delete VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled delete for VPC NAT Gateway %s", key)
	return nil
}

func (c *gwController.Controller) deleteVpcNatGw(gw *apiv1.VpcNatGateway) error {
	// Delete the VPC NAT Gateway configuration
	klog.Infof("Deleting VPC NAT Gateway %s", gw.Name)
	// Implement the logic for deleting VPC NAT Gateway and associated resources
	return nil
}
func (c *gwController.Controller) handleCreateVpcNatGateway(key string) error {
	// Handle the creation of a VPC NAT Gateway
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle create vpc NAT gateway %s", key)

	// Fetch the VPC NAT Gateway object from the key
	gw, err := c.vpcNatGatewayLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway %s not found, skipping creation", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway %s: %v", key, err)
	}

	// Proceed with creating the NAT Gateway
	if err := c.createVpcNatGw(gw); err != nil {
		return fmt.Errorf("failed to create VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully created VPC NAT Gateway %s", key)
	return nil
}

func (c *gwController.Controller) createVpcNatGw(gw *apiv1.VpcNatGateway) error {
	// Create the VPC NAT Gateway
	klog.Infof("Creating VPC NAT Gateway %s", gw.Name)
	// Implement logic for creating the NAT Gateway (e.g., configuring resources, associating rules)
	return nil
}

func (c *gwController.Controller) handleUpdateVpcNatGwAttachment(key string) error {
	// Handle the update for VPC NAT Gateway attachment
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc NAT gateway attachment %s", key)

	// Fetch the VPC NAT Gateway attachment object
	attachment, err := c.vpcNatGwAttachmentLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway attachment %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway attachment %s: %v", key, err)
	}

	// Update the NAT Gateway attachment configuration
	if err := c.updateVpcNatGwAttachment(attachment); err != nil {
		return fmt.Errorf("failed to update attachment for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC NAT Gateway attachment %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwAttachment(attachment *apiv1.VpcNatGwAttachment) error {
	// Update the VPC NAT Gateway attachment configuration
	klog.Infof("Updating VPC NAT Gateway attachment %s", attachment.Name)
	// Implement logic for updating attachment settings
	return nil
}

func (c *gwController.Controller) handleDeleteVpcNatGwAttachment(key string) error {
	// Handle the deletion of VPC NAT Gateway attachment
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle delete vpc NAT gateway attachment %s", key)

	// Fetch the VPC NAT Gateway attachment object
	attachment, err := c.vpcNatGwAttachmentLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway attachment %s not found, skipping delete", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway attachment %s: %v", key, err)
	}

	// Delete the VPC NAT Gateway attachment
	if err := c.deleteVpcNatGwAttachment(attachment); err != nil {
		return fmt.Errorf("failed to delete VPC NAT Gateway attachment %s: %v", key, err)
	}

	klog.Infof("Successfully handled delete for VPC NAT Gateway attachment %s", key)
	return nil
}

func (c *gwController.Controller) deleteVpcNatGwAttachment(attachment *apiv1.VpcNatGwAttachment) error {
	// Delete the VPC NAT Gateway attachment
	klog.Infof("Deleting VPC NAT Gateway attachment %s", attachment.Name)
	// Implement logic for deleting the attachment and detaching from resources
	return nil
}

func (c *gwController.Controller) handleUpdateVpcNATRulePolicy(key string) error {
	// Handle the update for VPC NAT rule policy (e.g., access control policies)
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle update vpc NAT rule policy %s", key)

	// Fetch the VPC NAT Gateway policy
	policy, err := c.vpcNatGwPolicyLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway policy %s not found, skipping update", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway policy %s: %v", key, err)
	}

	// Update the NAT rule policy configuration
	if err := c.updateVpcNatGwRulePolicy(policy); err != nil {
		return fmt.Errorf("failed to update NAT rule policy for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled update for VPC NAT rule policy %s", key)
	return nil
}

func (c *gwController.Controller) updateVpcNatGwRulePolicy(policy *apiv1.VpcNatGwPolicy) error {
	// Update the VPC NAT Gateway rule policy configuration
	klog.Infof("Updating NAT rule policy for VPC NAT Gateway %s", policy.Name)
	// Implement logic for updating the NAT rule policy
	return nil
}

func (c *gwController.Controller) handleDeleteVpcNATRulePolicy(key string) error {
	// Handle the deletion of VPC NAT rule policy
	c.vpcNatGwKeyMutex.LockKey(key)
	defer func() { _ = c.vpcNatGwKeyMutex.UnlockKey(key) }()

	klog.Infof("handle delete vpc NAT rule policy %s", key)

	// Fetch the VPC NAT Gateway policy
	policy, err := c.vpcNatGwPolicyLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			klog.Warningf("VPC NAT Gateway policy %s not found, skipping delete", key)
			return nil
		}
		return fmt.Errorf("failed to get VPC NAT Gateway policy %s: %v", key, err)
	}

	// Delete the VPC NAT Gateway policy
	if err := c.deleteVpcNatGwRulePolicy(policy); err != nil {
		return fmt.Errorf("failed to delete NAT rule policy for VPC NAT Gateway %s: %v", key, err)
	}

	klog.Infof("Successfully handled delete for VPC NAT rule policy %s", key)
	return nil
}

func (c *gwController.Controller) deleteVpcNatGwRulePolicy(policy *apiv1.VpcNatGwPolicy) error {
	// Delete the VPC NAT Gateway rule policy configuration
	klog.Infof("Deleting NAT rule policy for VPC NAT Gateway %s", policy.Name)
	// Implement logic for deleting NAT rule policies
	return nil
}
