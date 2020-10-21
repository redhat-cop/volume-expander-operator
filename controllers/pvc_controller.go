/*
Copyright 2020 Red Hat Community of Practice.

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

package controllers

import (
	"context"
	"crypto/tls"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"errors"

	"github.com/go-logr/logr"
	promapi "github.com/prometheus/client_golang/api"
	"github.com/prometheus/common/model"
	promv1 "github.com/redhat-cop/volume-expander-operator/controllers/prometheusclient"
	corev1 "k8s.io/api/core/v1"
	errs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const autoExtendAnnotation = "volume-expander-operator.redhat-cop.io/autoexpand"
const pollingFrequencyAnnotation = "volume-expander-operator.redhat-cop.io/polling-frequency"
const extendByPercentAnnotation = "volume-expander-operator.redhat-cop.io/expand-by-percent"
const extendUpToAnnotation = "volume-expander-operator.redhat-cop.io/expand-up-to"
const defaultPollingFrequency = time.Second * 30
const defaultExtendByPercent = 25
const extendThresholdPercentAnnotation = "volume-expander-operator.redhat-cop.io/expand-threshold-percent"
const defaultExtendThresholdPercent = 80

const defaultPrometheusAddress = "https://prometheus-k8s.openshift-monitoring.svc:9092"
const tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"

const tokenEnvironmentVariable = "TOKEN"
const prometheusEnvironmentVariable = "PROMETHEUS_URL"

var defaultExtendUpTo = resource.NewQuantity(math.MaxInt64, resource.DecimalSI)

// PVCReconciler reconciles a PVC object
type PVCReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;delete
func (r *PVCReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	logger := r.Log.WithValues("pvc", req.NamespacedName)

	// your logic here
	//1. poll metrics
	//2. if need extension, increase pvc value and exit
	//3. if desired and actual value is incongruent, kill attached pods

	// Fetch the GlobalRouteDiscovery instance
	instance := &corev1.PersistentVolumeClaim{}
	err := r.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errs.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	reconcileContext := &reconcileContext{
		instance: instance,
		logger:   logger,
	}

	ok, used, capacity, err := r.pollMetrics(reconcileContext)
	if err != nil {
		logger.Error(err, "unable to poll metrics for", "pvc", instance)
		return r.manageError(err, reconcileContext)
	}
	if !ok {
		//metrics are still not available
		return r.manageSuccess(reconcileContext)
	}

	logger.Info("", "used", used, "capacity", capacity)
	logger.V(1).Info("", "expand threshold", reconcileContext.getExtendThresholdPercent(), "ratio", float64(used)/float64(capacity)*100)

	if float64(used)/float64(capacity)*100 > float64(reconcileContext.getExtendThresholdPercent()) {
		//we need expand the pvc
		logger.V(1).Info("we need to expland the volume")
		var newValue *resource.Quantity
		logger.V(1).Info("", "expand upto", reconcileContext.getExtendUpTo())
		logger.V(1).Info("", "expand by percet", reconcileContext.getExtendByPercent())
		logger.V(1).Info("new calculated", "value", capacity*int64(100+reconcileContext.getExtendByPercent())/int64(100))
		if reconcileContext.getExtendUpTo().CmpInt64(capacity*int64(100+reconcileContext.getExtendByPercent())/int64(100)) == 1 {
			//we expand to the new percent
			newValueint64 := capacity * int64(100+reconcileContext.getExtendByPercent()) / int64(100)
			newValue = resource.NewQuantity(newValueint64, resource.BinarySI)
		} else {
			//we expand up to the maximum
			newValue = reconcileContext.getExtendUpTo()
		}
		logger.V(1).Info("final", "value", newValue)
		if newValue.Cmp(reconcileContext.instance.Spec.Resources.Requests[corev1.ResourceStorage]) == 1 {
			reconcileContext.instance.Spec.Resources.Requests[corev1.ResourceStorage] = *newValue
			err := r.Update(context.TODO(), reconcileContext.instance, &client.UpdateOptions{})
			if err != nil {
				logger.Error(err, "unable to update", "pvc", reconcileContext.instance)
				return r.manageError(err, reconcileContext)
			}
			return r.manageSuccess(reconcileContext)
		}

	}

	if !reconcileContext.instance.Spec.Resources.Requests[corev1.ResourceStorage].Equal(reconcileContext.instance.Status.Capacity[corev1.ResourceStorage]) {
		//we need to kill the attached pods
		logger.V(1).Info("we need to kill the attached pods")
		podList := &corev1.PodList{}
		err := r.List(context.TODO(), podList, &client.ListOptions{
			Namespace: reconcileContext.instance.Namespace,
		})
		if err != nil {
			logger.Error(err, "unable to list pods", "in namespace", reconcileContext.instance.Namespace)
			r.manageError(err, reconcileContext)
		}
		toBeKilledPods := filterPods(reconcileContext, podList.Items)
		logger.V(1).Info("to be killed pods", "len", len(toBeKilledPods))
		for _, pod := range toBeKilledPods {
			err := r.Delete(context.TODO(), &pod, &client.DeleteOptions{})
			if err != nil {
				logger.Error(err, "unable to delete", "pod", pod.Name)
				r.manageError(err, reconcileContext)
			}
		}
	}

	return r.manageSuccess(reconcileContext)
}

func filterPods(reconcileContext *reconcileContext, pods []corev1.Pod) []corev1.Pod {
	result := []corev1.Pod{}
	for _, pod := range pods {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim.ClaimName == reconcileContext.instance.Name {
				if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
					result = append(result, pod)
					break
				}
			}
		}
	}
	return result
}

type reconcileContext struct {
	instance *corev1.PersistentVolumeClaim
	logger   logr.Logger
}

func (r *reconcileContext) getPollingFrequency() time.Duration {
	if pollingFrquencyString, ok := r.instance.Annotations[pollingFrequencyAnnotation]; ok {
		pollingFrequency, err := time.ParseDuration(pollingFrquencyString)
		if err != nil {
			r.logger.Error(err, "unable to parse", "duration", pollingFrquencyString)
			return defaultPollingFrequency
		}
		return pollingFrequency
	}
	return defaultPollingFrequency
}

func (r *reconcileContext) getExtendByPercent() int {
	if extendByPercentString, ok := r.instance.Annotations[extendByPercentAnnotation]; ok {
		extendByPercent, err := strconv.Atoi(extendByPercentString)
		if err != nil {
			r.logger.Error(err, "unable to parse", "integer", extendByPercentString)
			return defaultExtendByPercent
		}
		if extendByPercent < 1 {
			r.logger.Error(errors.New("extension cannot be negative"), "invalid value", "percent", extendByPercent)
			return defaultExtendByPercent
		}
		return extendByPercent
	}
	return defaultExtendByPercent
}

func (r *reconcileContext) getExtendThresholdPercent() int {
	if extendThresholdPercentString, ok := r.instance.Annotations[extendThresholdPercentAnnotation]; ok {
		extendThresholdPercent, err := strconv.Atoi(extendThresholdPercentString)
		if err != nil {
			r.logger.Error(err, "unable to parse", "integer", extendThresholdPercentString)
			return defaultExtendThresholdPercent
		}
		if extendThresholdPercent < 1 || extendThresholdPercent > 99 {
			r.logger.Error(errors.New("threshold must be between 1 and 99"), "invalid value", "percent", extendThresholdPercent)
			return defaultExtendThresholdPercent
		}
		return extendThresholdPercent
	}
	return defaultExtendThresholdPercent
}

func (r *reconcileContext) getExtendUpTo() *resource.Quantity {
	if extendUpToString, ok := r.instance.Annotations[extendUpToAnnotation]; ok {
		extendUpTo, err := resource.ParseQuantity(extendUpToString)
		if err != nil {
			r.logger.Error(err, "unable to parse", "quantity", extendUpToString)
			return defaultExtendUpTo
		}
		return &extendUpTo
	}
	return defaultExtendUpTo
}

func (r *PVCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	isAnnotatedPVC := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			value, _ := e.MetaNew.GetAnnotations()[autoExtendAnnotation]
			return value == "true"
		},
		CreateFunc: func(e event.CreateEvent) bool {
			value, _ := e.Meta.GetAnnotations()[autoExtendAnnotation]
			return value == "true"
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		WithEventFilter(isAnnotatedPVC).
		Complete(r)
}

func (r *PVCReconciler) getOauthToken(reconcileContext *reconcileContext) string {
	if token, ok := os.LookupEnv(tokenEnvironmentVariable); ok {
		return token
	}
	content, err := ioutil.ReadFile(tokenFile)
	if err != nil {
		reconcileContext.logger.Error(err, "unable to read", "file", tokenFile)
		return ""
	}
	// Convert []byte to string and print to screen
	return string(content)
}

func (r *PVCReconciler) pollMetrics(reconcileContext *reconcileContext) (available bool, used int64, capacity int64, err error) {
	address, found := os.LookupEnv(prometheusEnvironmentVariable)
	if !found {
		address = defaultPrometheusAddress
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	promClient, err := promapi.NewClient(promapi.Config{
		Address:      address,
		RoundTripper: transport,
	})
	if err != nil {
		reconcileContext.logger.Error(err, "unable to create prometheus", "client", address)
		return false, 0, 0, err
	}
	papi := promv1.NewAPI(promClient, map[string]string{
		"Authorization": "Bearer " + r.getOauthToken(reconcileContext),
	})

	resultUsed, warnings, err := papi.Query(context.TODO(), "kubelet_volume_stats_used_bytes{namespace=\""+reconcileContext.instance.Namespace+"\",persistentvolumeclaim=\""+reconcileContext.instance.Name+"\"}", time.Now())
	if err != nil {
		reconcileContext.logger.Error(err, "unable to query for used bytes", "query", "kubelet_volume_stats_used_bytes{namespace=\""+reconcileContext.instance.Namespace+"\",persistentvolumeclaim=\""+reconcileContext.instance.Name+"\"}")
		return false, 0, 0, err
	}
	if len(warnings) > 0 {
		for _, warning := range []string(warnings) {
			reconcileContext.logger.Info(warning)
		}
	}
	reconcileContext.logger.V(1).Info("result for used bytes", "string", resultUsed.String(), "type", resultUsed.Type())
	switch resultUsed.Type() {
	case model.ValVector:
		{
			value := resultUsed.(model.Vector)
			if len(value) == 0 {
				//metrics are still not available
				return false, 0, 0, nil
			}
			if len(value) != 1 {
				err := errors.New("unexpected value")
				reconcileContext.logger.Error(err, "unexpected number of results", "lenght", len(value))
				return false, 0, 0, err
			}
			sample := ([]*model.Sample(value))[0]
			reconcileContext.logger.V(1).Info("used bytes", "string value", float64(sample.Value))
			used = int64(float64(sample.Value))
		}
	default:
		{
			err := errors.New("unexpected type")
			reconcileContext.logger.Error(err, "unexpected", "type", resultUsed.Type())
			return false, 0, 0, err
		}
	}
	resultCapacity, warnings, err := papi.Query(context.TODO(), "kubelet_volume_stats_capacity_bytes{namespace=\""+reconcileContext.instance.Namespace+"\",persistentvolumeclaim=\""+reconcileContext.instance.Name+"\"}", time.Now())
	if err != nil {
		reconcileContext.logger.Error(err, "unable to query for capacity bytes", "query", "kubelet_volume_stats_capacity_bytes{namespace=\""+reconcileContext.instance.Namespace+"\",persistentvolumeclaim=\""+reconcileContext.instance.Name+"\"}")
		return false, 0, 0, err
	}
	if len(warnings) > 0 {
		for _, warning := range []string(warnings) {
			reconcileContext.logger.Info(warning)
		}
	}
	reconcileContext.logger.Info("result for capacity bytes", "string", resultCapacity.String(), "type", resultCapacity.Type())
	switch resultCapacity.Type() {
	case model.ValVector:
		{
			value := resultCapacity.(model.Vector)
			if len(value) == 0 {
				//metrics are still not available
				return false, 0, 0, nil
			}
			if len(value) != 1 {
				err := errors.New("unexpected value")
				reconcileContext.logger.Error(err, "unexpected number of results", "lenght", len(value))
				return false, 0, 0, err
			}
			sample := ([]*model.Sample(value))[0]
			reconcileContext.logger.V(1).Info("used bytes", "string value", float64(sample.Value))
			capacity = int64(float64(sample.Value))
		}
	default:
		{
			err := errors.New("unexpected type")
			reconcileContext.logger.Error(err, "unexpected", "type", resultUsed.Type())
			return false, 0, 0, err
		}
	}
	return true, used, capacity, nil
}

func (r *PVCReconciler) manageError(err error, reconcileContext *reconcileContext) (ctrl.Result, error) {
	r.Recorder.Event(reconcileContext.instance, "Warning", "UnableToExtend", err.Error())
	return reconcile.Result{}, err
}

func (r *PVCReconciler) manageSuccess(reconcileContext *reconcileContext) (ctrl.Result, error) {
	return reconcile.Result{
		RequeueAfter: reconcileContext.getPollingFrequency(),
	}, nil
}
