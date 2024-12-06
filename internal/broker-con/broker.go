package BrokerController

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"admiral/pkg/constants"
	"admiral/pkg/federate"
	"admiral/pkg/log"
	"admiral/pkg/syncer"
	"admiral/pkg/syncer/broker"
	"admiral/pkg/util"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	validations "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/BrokerController-runtime/pkg/log"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

type converter struct {
	scheme *runtime.Scheme
}

const (
	nonExistentService = "ServiceUnavailable"
	unsupportedService = "UnsupportedServiceType"
)

type Configuration struct {
	ImportCounterName string
	ExportCounterName string
}

type BrokerController struct {
	clusterID                   string
	globalnetEnabled            bool
	namespace                   string
	serviceExportClient         *ServiceExportClient
	serviceExportSyncer         syncer.Interface
	serviceSyncer               syncer.Interface
	serviceImportController     *ServiceImportController
	localServiceImportFederator federate.Federator
}

type ServiceExportClient struct {
	dynamic.NamespaceableResourceInterface
	converter
	localSyncer syncer.Interface
}
type ServiceImportAggregator struct {
	clusterID       string
	converter       converter
	brokerClient    dynamic.Interface
	brokerNamespace string
}
type ServiceImportController struct {
	localClient             dynamic.Interface
	restMapper              meta.RESTMapper
	serviceImportAggregator *ServiceImportAggregator
	serviceExportClient     *ServiceExportClient
	localSyncer             syncer.Interface
	remoteSyncer            syncer.Interface
	endpointControllers     sync.Map
	clusterID               string
	localNamespace          string
	converter               converter
}

type AgentConfig struct {
	ClusterID        string
	Namespace        string
	Verbosity        int
	GlobalnetEnabled bool `split_words:"true"`
	Uninstall        bool
	HaltOnCertError  bool `split_words:"true"`
	Debug            bool
}

var loggerInstance = log.Logger{Logger: logf.Log.WithName("agent")}

//nolint:gocritic // (hugeParam) We modify syncerConfig, avoid passing by pointer
func Initialize(config *AgentConfig, syncConfig broker.SyncerConfig, metricNames Configuration) (*BrokerController, error) {
	if validationErrors := validations.IsDNS1123Label(config.ClusterID); len(validationErrors) > 0 {
		return nil, errors.Errorf("%s is not a valid ClusterID %v", config.ClusterID, validationErrors)
	}

	BrokerController := &BrokerController{
		clusterID:        config.ClusterID,
		namespace:        config.Namespace,
		globalnetEnabled: config.GlobalnetEnabled,
	}

	_, resourceGV, err := util.ToUnstructuredResource(&mcsv1a1.ServiceExport{}, syncConfig.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "resource conversion failed")
	}

	BrokerController.localServiceImportFederator = federate.NewCreateOrUpdateFederator(syncConfig.LocalClient, syncConfig.RestMapper,
		config.Namespace, "")

	BrokerController.serviceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ServiceExport -> ServiceImport",
		SourceClient:    syncConfig.LocalClient,
		SourceNamespace: metav1.NamespaceAll,
		RestMapper:      syncConfig.RestMapper,
		Federator:       BrokerController.localServiceImportFederator,
		ResourceType:    &mcsv1a1.ServiceExport{},
		Transform:       BrokerController.transformServiceExportToServiceImport,
		ResourcesEquivalent: func(oldObj, newObj *unstructured.Unstructured) bool {
			return !BrokerController.shouldProcessServiceExportUpdate(oldObj, newObj)
		},
		Scheme: syncConfig.Scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "service export syncer creation failed")
	}

	BrokerController.serviceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Service deletion",
		SourceClient:    syncConfig.LocalClient,
		SourceNamespace: metav1.NamespaceAll,
		RestMapper:      syncConfig.RestMapper,
		Federator:       BrokerController.localServiceImportFederator,
		ResourceType:    &corev1.Service{},
		Transform:       BrokerController.transformServiceToRemoteServiceImport,
		Scheme:          syncConfig.Scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "service syncer creation failed")
	}

	BrokerController.serviceExportClient = &ServiceExportClient{
		NamespaceableResourceInterface: syncConfig.LocalClient.Resource(*resourceGV),
		converter:                      converter{scheme: syncConfig.Scheme},
		localSyncer:                    BrokerController.serviceExportSyncer,
	}

	return BrokerController, nil
}

func (c *BrokerController) Start(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	// Initialize informers and caches
	loggerInstance.Info("Initializing Agent BrokerController")

	if err := c.serviceExportSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "service export syncer startup failed")
	}

	if err := c.serviceSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "service syncer startup failed")
	}

	loggerInstance.Info("Agent BrokerController initialization complete")

	return nil
}

func (c *BrokerController) transformServiceExportToServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	svcExport := obj.(*mcsv1a1.ServiceExport)

	ctx := context.Background()

	loggerInstance.V(log.DEBUG).Infof("Processing ServiceExport %s/%s %sd", svcExport.Namespace, svcExport.Name, op)

	if op == syncer.Delete {
		return c.newServiceImport(svcExport.Name, svcExport.Namespace), false
	}

	obj, found, err := c.serviceSyncer.GetResource(svcExport.Name, svcExport.Namespace)
	if err != nil {
		// Log and requeue on error
		c.serviceExportClient.updateStatusConditions(ctx, svcExport.Name, svcExport.Namespace,
			newServiceExportCondition(mcsv1a1.ServiceExportValid, corev1.ConditionUnknown, "ServiceRetrievalFailed",
				fmt.Sprintf("Error retrieving the Service: %v", err)))
		loggerInstance.Errorf(err, "Failed to retrieve Service %s/%s", svcExport.Namespace, svcExport.Name)

		return nil, true
	}

	if !found {
		loggerInstance.V(log.DEBUG).Infof("Service for export (%s/%s) not found", svcExport.Namespace, svcExport.Name)
		c.serviceExportClient.updateStatusConditions(ctx, svcExport.Name, svcExport.Namespace,
			newServiceExportCondition(mcsv1a1.ServiceExportValid, corev1.ConditionFalse, nonExistentService,
				"Service to be exported doesn't exist"))

		return nil, false
	}

	svc := obj.(*corev1.Service)

	serviceType, valid := getServiceImportType(svc)

	if !valid {
		c.serviceExportClient.updateStatusConditions(ctx, svcExport.Name, svcExport.Namespace,
			newServiceExportCondition(mcsv1a1.ServiceExportValid, corev1.ConditionFalse, unsupportedService,
				fmt.Sprintf("Service of type %v not supported", svc.Spec.Type)))
		loggerInstance.Errorf(nil, "Unsupported Service type %q for Service (%s/%s)", svc.Spec.Type, svcExport.Namespace, svcExport.Name)

		err = c.localServiceImportFederator.Delete(ctx, c.newServiceImport(svcExport.Name, svcExport.Namespace))
		if err == nil || apierrors.IsNotFound(err) {
			return nil, false
		}

		loggerInstance.Errorf(nil, "Failed to delete ServiceImport for Service (%s/%s)", svcExport.Namespace, svcExport.Name)

		return nil, true
	}

	serviceImport := c.newServiceImport(svcExport.Name, svcExport.Namespace)
	serviceImport.Annotations[constants.PublishNotReadyAddresses] = strconv.FormatBool(svc.Spec.PublishNotReadyAddresses)

	serviceImport.Spec = mcsv1a1.ServiceImportSpec{
		Ports:                 []mcsv1a1.ServicePort{},
		Type:                  serviceType,
		SessionAffinityConfig: new(corev1.SessionAffinityConfig),
	}

	serviceImport.Status = mcsv1a1.ServiceImportStatus{}
	if svc.Spec.ClusterIP != "" {
		serviceImport.Status.ClusterIPs = append(serviceImport.Status.ClusterIPs, svc.Spec.ClusterIP)
	}

	if serviceType == mcsv1a1.ClusterIP || serviceType == mcsv1a1.ExternalName {
		return serviceImport, true
	}

	// This ensures the ServiceImport is created in the correct place if
	// there are Endpoints of the correct type.
	// For this, only Addresses of the current types are processed.
	return serviceImport, false
}

func getServiceImportType(service *corev1.Service) (mcsv1a1.ServiceImportType, bool) {
	if service.Spec.Type != "" && service.Spec.Type != corev1.ServiceTypeClusterIP {
		return "", false
	}

	if service.Spec.ClusterIP == corev1.ClusterIPNone {
		return mcsv1a1.Headless, true
	}

	return mcsv1a1.ClusterSetIP, true
}

var logger = log.Logger{Logger: logf.Log.WithName("agent")}

func (c *ServiceExportClient) updateStatusConditions(ctx context.Context, name, namespace string,
	conditions ...mcsv1a1.ServiceExportCondition) {
	c.tryUpdateStatusConditions(ctx, name, namespace, true, conditions...)
}

func (c *ServiceExportClient) tryUpdateStatusConditions(ctx context.Context, name, namespace string, canReplace bool,
	conditions ...mcsv1a1.ServiceExportCondition) {
	findStatusCondition := func(conditions []mcsv1a1.ServiceExportCondition, condType mcsv1a1.ServiceExportConditionType,
	) *mcsv1a1.ServiceExportCondition {
		cond := FindServiceExportStatusCondition(conditions, condType)

		// TODO - this handles migration of the Synced type to Ready which can be removed once we no longer support a version
		// prior to the introduction of Ready.
		if cond == nil && condType == constants.ServiceExportReady {
			cond = FindServiceExportStatusCondition(conditions, "Synced")
		}

		return cond
	}

	c.doUpdate(ctx, name, namespace, func(toUpdate *mcsv1a1.ServiceExport) bool {
		updated := false

		for i := range conditions {
			condition := &conditions[i]

			prevCond := findStatusCondition(toUpdate.Status.Conditions, condition.Type)
			if prevCond == nil {
				logger.V(log.DEBUG).Infof("Add status condition for ServiceExport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
					namespace, name, condition.Type, condition.Status, *condition.Reason, *condition.Message)

				toUpdate.Status.Conditions = append(toUpdate.Status.Conditions, *condition)
				updated = true
			} else if canReplace {
				logger.V(log.DEBUG).Infof("Update status condition for ServiceExport (%s/%s): Type: %q, Status: %q, Reason: %q, Message: %q",
					namespace, name, condition.Type, condition.Status, *condition.Reason, *condition.Message)

				*prevCond = *condition
				updated = true
			}
		}

		return updated
	})
}

func FindServiceExportStatusCondition(conditions []mcsv1a1.ServiceExportCondition,
	condType mcsv1a1.ServiceExportConditionType,
) *mcsv1a1.ServiceExportCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}

	return nil
}
func (c *ServiceExportClient) doUpdate(ctx context.Context, name, namespace string, update func(toUpdate *mcsv1a1.ServiceExport) bool) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := c.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			logger.V(log.TRACE).Infof("ServiceExport (%s/%s) not found - unable to update status", namespace, name)
			return nil
		} else if err != nil {
			return errors.Wrap(err, "error retrieving ServiceExport")
		}

		return errors.Wrap(err, "error from UpdateStatus")
	})
	if err != nil {
		logger.Errorf(err, "Error updating status for ServiceExport (%s/%s)", namespace, name)
	}
}

func (c *BrokerController) newServiceImport(name, namespace string) *mcsv1a1.ServiceImport {
	return &mcsv1a1.ServiceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func (a *BrokerController) shouldProcessServiceExportUpdate(oldObj, newObj *unstructured.Unstructured) bool {
	oldValidCond := FindServiceExportStatusCondition(a.toServiceExport(oldObj).Status.Conditions, mcsv1a1.ServiceExportValid)
	newValidCond := FindServiceExportStatusCondition(a.toServiceExport(newObj).Status.Conditions, mcsv1a1.ServiceExportValid)

	if newValidCond != nil && !reflect.DeepEqual(oldValidCond, newValidCond) && newValidCond.Status == corev1.ConditionFalse {
		return true
	}

	return false
}
func (a *BrokerController) toServiceExport(obj runtime.Object) *mcsv1a1.ServiceExport {
	return a.serviceImportController.converter.toServiceExport(obj)
}
func (c converter) toServiceExport(obj runtime.Object) *mcsv1a1.ServiceExport {
	to := &mcsv1a1.ServiceExport{}
	utilruntime.Must(c.scheme.Convert(obj, to, nil))

	return to
}

func newServiceExportCondition(condType mcsv1a1.ServiceExportConditionType, status corev1.ConditionStatus, reason, message string) mcsv1a1.ServiceExportCondition {
	return mcsv1a1.ServiceExportCondition{
		Type:               condType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

func (c *BrokerController) transformServiceToRemoteServiceImport(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	service := obj.(*corev1.Service)

	ctx := context.Background()

	loggerInstance.V(log.DEBUG).Infof("Processing Service %s/%s %sd", service.Namespace, service.Name, op)

	if op == syncer.Delete {
		return c.newServiceImport(service.Name, service.Namespace), false
	}

	// Handle service import creation and update logic here
	serviceImport := c.newServiceImport(service.Name, service.Namespace)
	serviceImport.Annotations[constants.PublishNotReadyAddresses] = strconv.FormatBool(service.Spec.PublishNotReadyAddresses)

	serviceImport.Spec = mcsv1a1.ServiceImportSpec{
		Ports: []mcsv1a1.ServicePort{},
	}

	// Logic to handle specific service types
	if service.Spec.Type == corev1.ServiceTypeClusterIP {
		serviceImport.Status.ClusterIPs = append(serviceImport.Status.ClusterIPs, service.Spec.ClusterIP)
	}

	return serviceImport, true
}

func (c *BrokerController) logServiceImportCreation(serviceImport *mcsv1a1.ServiceImport) {
	// Log the creation of a new service import
	loggerInstance.Infof("Created ServiceImport %s/%s", serviceImport.Namespace, serviceImport.Name)
}

func (c *BrokerController) serviceSynchronizerInitialization(stopCh <-chan struct{}) error {
	// Initialize synchronizers for services
	if err := c.serviceSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "service syncer startup failed")
	}

	loggerInstance.Info("Service synchronizer initialized")
	return nil
}
