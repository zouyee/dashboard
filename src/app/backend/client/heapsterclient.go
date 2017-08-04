// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"log"
	"sync"

	"github.com/hashicorp/golang-lru/simplelru"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Cache is a thread-safe fixed size LRU cache.
type Cache struct {
	lru  *simplelru.LRU
	lock sync.RWMutex
}

// New creates an LRU of the given size
func New(size int) (*Cache, error) {
	return NewWithEvict(size, nil)
}

// NewWithEvict constructs a fixed size cache with the given eviction
// callback.
func NewWithEvict(size int, onEvicted func(key interface{}, value interface{})) (*Cache, error) {
	lru, err := simplelru.NewLRU(size, simplelru.EvictCallback(onEvicted))
	if err != nil {
		return nil, err
	}
	c := &Cache{
		lru: lru,
	}
	return c, nil
}

// Purge is used to completely clear the cache
func (c *Cache) Purge() {
	c.lock.Lock()
	c.lru.Purge()
	c.lock.Unlock()
}

// Add adds a value to the cache.  Returns true if an eviction occurred.
func (c *Cache) Add(key, value interface{}) bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.lru.Add(key, value)
}

// Get looks up a key's value from the cache.
func (c *Cache) Get(key interface{}) (interface{}, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.lru.Get(key)
}

// Check if a key is in the cache, without updating the recent-ness
// or deleting it for being stale.
func (c *Cache) Contains(key interface{}) bool {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.lru.Contains(key)
}

// Returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *Cache) Peek(key interface{}) (interface{}, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.lru.Peek(key)
}

// ContainsOrAdd checks if a key is in the cache  without updating the
// recent-ness or deleting it for being stale,  and if not, adds the value.
// Returns whether found and whether an eviction occurred.
func (c *Cache) ContainsOrAdd(key, value interface{}) (ok, evict bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.lru.Contains(key) {
		return true, false
	} else {
		evict := c.lru.Add(key, value)
		return false, evict
	}
}

// Remove removes the provided key from the cache.
func (c *Cache) Remove(key interface{}) {
	c.lock.Lock()
	c.lru.Remove(key)
	c.lock.Unlock()
}

// RemoveOldest removes the oldest item from the cache.
func (c *Cache) RemoveOldest() {
	c.lock.Lock()
	c.lru.RemoveOldest()
	c.lock.Unlock()
}

// Keys returns a slice of the keys in the cache, from oldest to newest.
func (c *Cache) Keys() []interface{} {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.lru.Keys()
}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.lru.Len()
}

// HeapsterClient  is a client used to make requests to a Heapster instance.
type HeapsterClient interface {
	// Creates a new GET HTTP request to heapster, specified by the path param, to the V1 API
	// endpoint. The path param is without the API prefix, e.g.,
	// /model/namespaces/default/pod-list/foo/metrics/memory-usage
	Get(path string) RequestInterface
	Metrics() bool
	SetMetrics(metric bool) HeapsterClient
}

// PrometheusClient is a client to used to make requests to a Prometheus instance
type PrometheusClient interface {
	Get(path string) RequestInterface
}

// RequestInterface is an interface that allows to make operations on pure request object.
// Separation is done to allow testing.
type RequestInterface interface {
	DoRaw() ([]byte, error)
}

// InClusterHeapsterClient is an in-cluster implementation of a Heapster client. Talks with Heapster
// through service proxy.
type InClusterHeapsterClient struct {
	client rest.Interface
	Cache  *Cache
	Metric bool
}

// InClusterPrometheusClient is an in-cluster implementation of a Prometheus client. Talks with Prometheus
// through service proxy.
type InClusterPrometheusClient struct {
	client rest.Interface
}

// Get creates request to given path.
func (c InClusterHeapsterClient) Get(path string) RequestInterface {
	return c.client.Get().Prefix("proxy").
		Namespace("kube-system").
		Resource("services").
		Name("heapster").
		Suffix("/api/v1" + path)
}

func (c InClusterHeapsterClient) Metrics() bool {
	return c.Metric
}

func (c InClusterHeapsterClient) SetMetrics(metric bool) HeapsterClient {
	c.Metric = metric
	return c
}

// Get create request to given path
func (c InClusterPrometheusClient) Get(path string) RequestInterface {
	return c.client.Get().Prefix("proxy").
		Namespace("kube-system").
		Resource("services").
		Name("prometheus:9090").
		Suffix("/api/v1" + path)
}

// RemoteHeapsterClient is an implementation of a remote Heapster client. Talks with Heapster
// through raw RESTClient.
type RemoteHeapsterClient struct {
	client rest.Interface
	Cache  *Cache
	Metric bool
}

// Get creates request to given path.
func (c RemoteHeapsterClient) Get(path string) RequestInterface {
	return c.client.Get().Suffix(path)
}

func (c RemoteHeapsterClient) Metrics() bool {
	return c.Metric
}

func (c RemoteHeapsterClient) SetMetrics(metric bool) HeapsterClient {
	c.Metric = metric
	return c
}

// RemotePrometheusClient struct
type RemotePrometheusClient struct {
	client rest.Interface
}

// Get creates request to given path.
func (c RemotePrometheusClient) Get(path string) RequestInterface {
	return c.client.Get().Suffix(path)
}

// CreateHeapsterRESTClient creates new Heapster REST client. When heapsterHost param is empty
// string the function assumes that it is running inside a Kubernetes cluster and connects via
// service proxy. heapsterHost param is in the format of protocol://address:port,
// e.g., http://localhost:8002.
func CreateHeapsterRESTClient(heapsterHost string, apiclient *kubernetes.Clientset) (
	HeapsterClient, error) {

	if heapsterHost == "" {
		log.Print("Creating in-cluster Heapster client")

		heapster := InClusterHeapsterClient{client: apiclient.Core().RESTClient()}

		return heapster, nil
	}

	cfg := &rest.Config{Host: heapsterHost, QPS: defaultQPS, Burst: defaultBurst}
	restClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	log.Printf("Creating remote Heapster client for %s", heapsterHost)
	rhc := RemoteHeapsterClient{client: restClient.Core().RESTClient()}
	rhc.Cache, _ = New(50)
	return rhc, nil
}

// CreatePrometheusRESTClient return prometheus client
func CreatePrometheusRESTClient(prometheusHost string, apiclient *kubernetes.Clientset) (
	PrometheusClient, error) {

	if prometheusHost == "" {
		log.Print("Creating in-cluster Prometheus client")
		return InClusterPrometheusClient{client: apiclient.Core().RESTClient()}, nil
	}

	cfg := &rest.Config{Host: prometheusHost, QPS: defaultQPS, Burst: defaultBurst}
	restClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	log.Printf("Creating remote Prometheus client for %s", prometheusHost)
	return RemotePrometheusClient{client: restClient.Core().RESTClient()}, nil
}
