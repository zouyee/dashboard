package client

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	api "k8s.io/client-go/pkg/api"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// GroupName is the group name use in this package
const GroupName = "custom.metrics.k8s.io"

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: runtime.APIVersionInternal}

// Kind takes an unqualified kind and returns back a Group qualified GroupKind
func Kind(kind string) schema.GroupKind {
	return SchemeGroupVersion.WithKind(kind).GroupKind()
}

// Resource takes an unqualified resource and returns back a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&MetricValue{},
		&MetricValueList{},
	)
	return nil
}

// a list of values for a given metric for some set of objects
type MetricValueList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// the value of the metric across the described objects
	Items []MetricValue `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// a metric value for some object
type MetricValue struct {
	metav1.TypeMeta `json:",inline"`

	// a reference to the described object
	DescribedObject ObjectReference `json:"describedObject"`

	// the name of the metric
	MetricName string `json:"metricName"`

	// indicates the time at which the metrics were produced
	Timestamp metav1.Time `json:"timestamp"`

	// indicates the window ([Timestamp-Window, Timestamp]) from
	// which these metrics were calculated, when returning rate
	// metrics calculated from cumulative metrics (or zero for
	// non-calculated instantaneous metrics).
	WindowSeconds *int64 `json:"window,omitempty"`

	// the value of the metric for this
	Value resource.Quantity `json:"value"`
}

// allObjects is a wildcard used to select metrics
// for all objects matching the given label selector
const AllObjects = "*"

// NOTE: ObjectReference is copied from k8s.io/kubernetes/pkg/api/types.go. We
// cannot depend on k8s.io/kubernetes/pkg/api because that creates cyclic
// dependency between k8s.io/metrics and k8s.io/kubernetes. We cannot depend on
// k8s.io/client-go/pkg/api because the package is going to be deprecated soon.
// There is no need to keep it an exact copy. Each repo can define its own
// internal objects.

// ObjectReference contains enough information to let you inspect or modify the referred object.
type ObjectReference struct {
	Kind            string
	Namespace       string
	Name            string
	UID             types.UID
	APIVersion      string
	ResourceVersion string
	FieldPath       string
}

// CustomMetricsClient is a client for fetching metrics
// describing both root-scoped and namespaced resources.
type CustomMetricsClient interface {
	RootScopedMetricsGetter
	NamespacedMetricsGetter
}

// RootScopedMetricsGetter provides access to an interface for fetching
// metrics describing root-scoped objects.  Note that metrics describing
// a namespace are simply considered a special case of root-scoped metrics.
type RootScopedMetricsGetter interface {
	RootScopedMetrics() MetricsInterface
}

// NamespacedMetricsGetter provides access to an interface for fetching
// metrics describing resources in a particular namespace.
type NamespacedMetricsGetter interface {
	NamespacedMetrics(namespace string) MetricsInterface
}

// MetricsInterface provides access to metrics describing Kubernetes objects.
type MetricsInterface interface {
	// GetForObject fetchs the given metric describing the given object.
	GetForObject(groupKind schema.GroupKind, name string, metricName string) (*MetricValue, error)

	// GetForObjects fetches the given metric describing all objects of the given
	// type matching the given label selector (or simply all objects of the given type
	// if the selector is nil).
	GetForObjects(groupKind schema.GroupKind, selector labels.Selector, metricName string) (*MetricValueList, error)
}

type customMetricsClient struct {
	client rest.Interface
	mapper meta.RESTMapper
}

func new(client rest.Interface) CustomMetricsClient {
	return &customMetricsClient{
		client: client,
	}
}

func NewForConfig(c *rest.Config) (CustomMetricsClient, error) {
	configShallowCopy := *c
	if configShallowCopy.RateLimiter == nil && configShallowCopy.QPS > 0 {
		configShallowCopy.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(configShallowCopy.QPS, configShallowCopy.Burst)
	}
	configShallowCopy.APIPath = "/apis"
	if configShallowCopy.UserAgent == "" {
		configShallowCopy.UserAgent = rest.DefaultKubernetesUserAgent()
	}
	configShallowCopy.GroupVersion = &SchemeGroupVersion
	configShallowCopy.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: api.Codecs}

	client, err := rest.RESTClientFor(&configShallowCopy)
	if err != nil {
		return nil, err
	}

	return new(client), nil
}

func NewForConfigOrDie(c *rest.Config) CustomMetricsClient {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// NewForMapper constucts the client with a RESTMapper, which allows more
// accurate translation from GroupVersionKind to GroupVersionResource.
func NewForMapper(client rest.Interface, mapper meta.RESTMapper) CustomMetricsClient {
	return &customMetricsClient{
		client: client,
		mapper: mapper,
	}
}

func (c *customMetricsClient) RootScopedMetrics() MetricsInterface {
	return &rootScopedMetrics{c}
}

func (c *customMetricsClient) NamespacedMetrics(namespace string) MetricsInterface {
	return &namespacedMetrics{
		client:    c,
		namespace: namespace,
	}
}

func (c *customMetricsClient) qualResourceForKind(groupKind schema.GroupKind) (string, error) {
	if c.mapper == nil {
		// the version doesn't matter
		gvk := groupKind.WithVersion("")
		gvr, _ := meta.UnsafeGuessKindToResource(gvk)
		gr := gvr.GroupResource()
		return gr.String(), nil
	}

	// use the mapper if it's available
	mapping, err := c.mapper.RESTMapping(groupKind)
	if err != nil {
		return "", fmt.Errorf("unable to map kind %s to resource: %v", groupKind.String(), err)
	}

	groupResource := schema.GroupResource{
		Group:    mapping.GroupVersionKind.Group,
		Resource: mapping.Resource,
	}
	return groupResource.String(), nil
}

type rootScopedMetrics struct {
	client *customMetricsClient
}

func (m *rootScopedMetrics) getForNamespace(namespace string, metricName string) (*MetricValue, error) {
	res := &MetricValueList{}
	err := m.client.client.Get().
		Resource("metrics").
		Namespace(namespace).
		Name(metricName).
		Do().
		Into(res)

	if err != nil {
		return nil, err
	}

	if len(res.Items) != 1 {
		return nil, fmt.Errorf("the custom metrics API server returned %v results when we asked for exactly one", len(res.Items))
	}

	return &res.Items[0], nil
}

func (m *rootScopedMetrics) GetForObject(groupKind schema.GroupKind, name string, metricName string) (*MetricValue, error) {
	// handle namespace separately
	if groupKind.Kind == "Namespace" && groupKind.Group == "" {
		return m.getForNamespace(name, metricName)
	}

	resourceName, err := m.client.qualResourceForKind(groupKind)
	if err != nil {
		return nil, err
	}

	res := &MetricValueList{}
	err = m.client.client.Get().
		Resource(resourceName).
		Name(name).
		SubResource(metricName).
		Do().
		Into(res)

	if err != nil {
		return nil, err
	}

	if len(res.Items) != 1 {
		return nil, fmt.Errorf("the custom metrics API server returned %v results when we asked for exactly one", len(res.Items))
	}

	return &res.Items[0], nil
}

func (m *rootScopedMetrics) GetForObjects(groupKind schema.GroupKind, selector labels.Selector, metricName string) (*MetricValueList, error) {
	// we can't wildcard-fetch for namespaces
	if groupKind.Kind == "Namespace" && groupKind.Group == "" {
		return nil, fmt.Errorf("cannot fetch metrics for multiple namespaces at once")
	}

	resourceName, err := m.client.qualResourceForKind(groupKind)
	if err != nil {
		return nil, err
	}

	res := &MetricValueList{}
	err = m.client.client.Get().
		Resource(resourceName).
		Name(AllObjects).
		SubResource(metricName).
		VersionedParams(&metav1.ListOptions{
			LabelSelector: selector.String(),
		}, metav1.ParameterCodec).
		Do().
		Into(res)

	if err != nil {
		return nil, err
	}

	return res, nil
}

type namespacedMetrics struct {
	client    *customMetricsClient
	namespace string
}

func (m *namespacedMetrics) GetForObject(groupKind schema.GroupKind, name string, metricName string) (*MetricValue, error) {
	resourceName, err := m.client.qualResourceForKind(groupKind)
	if err != nil {
		return nil, err
	}

	res := &MetricValueList{}
	err = m.client.client.Get().
		Resource(resourceName).
		Namespace(m.namespace).
		Name(name).
		SubResource(metricName).
		Do().
		Into(res)

	if err != nil {
		return nil, err
	}

	if len(res.Items) != 1 {
		return nil, fmt.Errorf("the custom metrics API server returned %v results when we asked for exactly one", len(res.Items))
	}

	return &res.Items[0], nil
}

func (m *namespacedMetrics) GetForObjects(groupKind schema.GroupKind, selector labels.Selector, metricName string) (*MetricValueList, error) {
	resourceName, err := m.client.qualResourceForKind(groupKind)
	if err != nil {
		return nil, err
	}

	res := &MetricValueList{}
	err = m.client.client.Get().
		Resource(resourceName).
		Namespace(m.namespace).
		Name(AllObjects).
		SubResource(metricName).
		VersionedParams(&metav1.ListOptions{
			LabelSelector: selector.String(),
		}, metav1.ParameterCodec).
		Do().
		Into(res)

	if err != nil {
		return nil, err
	}

	return res, nil
}
