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

package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dchest/captcha"
	restful "github.com/emicklei/go-restful"
	"github.com/kubernetes/dashboard/src/app/backend/client"
	"github.com/kubernetes/dashboard/src/app/backend/resource/cluster"
	"github.com/kubernetes/dashboard/src/app/backend/resource/common"
	"github.com/kubernetes/dashboard/src/app/backend/resource/config"
	"github.com/kubernetes/dashboard/src/app/backend/resource/configmap"
	"github.com/kubernetes/dashboard/src/app/backend/resource/container"
	"github.com/kubernetes/dashboard/src/app/backend/resource/daemonset"
	"github.com/kubernetes/dashboard/src/app/backend/resource/dataselect"
	"github.com/kubernetes/dashboard/src/app/backend/resource/deployment"
	"github.com/kubernetes/dashboard/src/app/backend/resource/discovery"
	"github.com/kubernetes/dashboard/src/app/backend/resource/event"
	"github.com/kubernetes/dashboard/src/app/backend/resource/horizontalpodautoscaler"
	"github.com/kubernetes/dashboard/src/app/backend/resource/ingress"
	"github.com/kubernetes/dashboard/src/app/backend/resource/job"
	"github.com/kubernetes/dashboard/src/app/backend/resource/logs"
	"github.com/kubernetes/dashboard/src/app/backend/resource/metric"
	"github.com/kubernetes/dashboard/src/app/backend/resource/namespace"
	"github.com/kubernetes/dashboard/src/app/backend/resource/node"
	"github.com/kubernetes/dashboard/src/app/backend/resource/persistentvolume"
	"github.com/kubernetes/dashboard/src/app/backend/resource/persistentvolumeclaim"
	"github.com/kubernetes/dashboard/src/app/backend/resource/pod"
	"github.com/kubernetes/dashboard/src/app/backend/resource/rbacrolebindings"
	"github.com/kubernetes/dashboard/src/app/backend/resource/rbacroles"
	"github.com/kubernetes/dashboard/src/app/backend/resource/replicaset"
	"github.com/kubernetes/dashboard/src/app/backend/resource/replicationcontroller"
	"github.com/kubernetes/dashboard/src/app/backend/resource/report"
	"github.com/kubernetes/dashboard/src/app/backend/resource/secret"
	resourceService "github.com/kubernetes/dashboard/src/app/backend/resource/service"
	"github.com/kubernetes/dashboard/src/app/backend/resource/statefulset"
	"github.com/kubernetes/dashboard/src/app/backend/resource/storageclass"
	"github.com/kubernetes/dashboard/src/app/backend/resource/thirdpartyresource"
	"github.com/kubernetes/dashboard/src/app/backend/resource/workload"
	"github.com/kubernetes/dashboard/src/app/backend/validation"
	"golang.org/x/net/xsrftoken"
	errorsK8s "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	clientK8s "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	heapster "k8s.io/heapster/metrics/api/v1/types"
)

const (
	// RequestLogString is a template for request log message.
	RequestLogString = "[%s] Incoming %s %s %s request from %s: %s"

	// ResponseLogString is a template for response log message.
	ResponseLogString = "[%s] Outcoming response to %s with %d status code"
)

// APIHandler is a representation of API handler. Structure contains client, Heapster client and
// client configuration.
type APIHandler struct {
	client           *clientK8s.Clientset
	heapsterClient   client.HeapsterClient
	config           *restclient.Config
	prometheusClient client.PrometheusClient
	mysqlClient      *sql.DB
	verber           common.ResourceVerber
	csrfKey          string
}

// CsrfToken ...
type CsrfToken struct {
	Token string `json:"token"`
}

func wsMetrics(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
	startTime := time.Now()
	verb := req.Request.Method
	resource := mapUrlToResource(req.SelectedRoutePath())
	client := utilnet.GetHTTPClient(req.Request)
	chain.ProcessFilter(req, resp)
	code := resp.StatusCode()
	contentType := resp.Header().Get("Content-Type")
	if resource != nil {
		Monitor(verb, *resource, client, contentType, code, startTime)
	}
}

// Post requests should set correct X-CSRF-TOKEN header, all other requests
// should either not edit anything or be already safe to CSRF attacks (PUT
// and DELETE)
func shouldDoCsrfValidation(req *restful.Request) bool {
	if req.Request.Method != "POST" {
		return false
	}
	// Validation handlers are idempotent functions, and not actual data
	// modification operations
	if strings.HasPrefix(req.SelectedRoutePath(), "/api/v1/appdeployment/validate/") {
		return false
	}
	return false
}

func xsrfValidation(csrfKey string) func(*restful.Request, *restful.Response, *restful.FilterChain) {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		resource := mapUrlToResource(req.SelectedRoutePath())
		if resource == nil || (shouldDoCsrfValidation(req) &&
			!xsrftoken.Valid(req.HeaderParameter("X-CSRF-TOKEN"),
				csrfKey,
				"none",
				*resource)) {

			err := errors.New("CSRF validation failed")
			log.Print(err)
			resp.AddHeader("Content-Type", "text/plain")
			resp.WriteErrorString(http.StatusUnauthorized, err.Error()+"\n")
		} else {
			chain.ProcessFilter(req, resp)
		}
	}
}

// mapUrlToResource extracts the resource from the URL path /api/v1/<resource>. Ignores potential
// subresources.
func mapUrlToResource(url string) *string {
	parts := strings.Split(url, "/")
	if len(parts) < 3 {
		return nil
	}
	return &parts[3]
}

// logRequestAndReponse is a web-service filter function used for request and response logging.
func logRequestAndReponse(request *restful.Request, response *restful.Response, chain *restful.FilterChain) {
	log.Printf(formatRequestLog(request))
	chain.ProcessFilter(request, response)
	log.Printf(formatResponseLog(response, request))
}

// formatRequestLog formats request log string.
func formatRequestLog(request *restful.Request) string {
	uri := ""
	if request.Request.URL != nil {
		uri = request.Request.URL.RequestURI()
	}

	content := "{}"
	entity := make(map[string]interface{})
	request.ReadEntity(&entity)
	if len(entity) > 0 {
		bytes, err := json.MarshalIndent(entity, "", "  ")
		if err == nil {
			content = string(bytes)
		}
	}

	return fmt.Sprintf(RequestLogString, time.Now().Format(time.RFC3339), request.Request.Proto,
		request.Request.Method, uri, request.Request.RemoteAddr, content)
}

// formatResponseLog formats response log string.
func formatResponseLog(response *restful.Response, request *restful.Request) string {
	return fmt.Sprintf(ResponseLogString, time.Now().Format(time.RFC3339),
		request.Request.RemoteAddr, response.StatusCode())
}

// CreateHTTPAPIHandler creates a new HTTP handler that handles all requests to the API of the backend.
func CreateHTTPAPIHandler(client *clientK8s.Clientset, heapsterClient client.HeapsterClient,
	prometheusClient client.PrometheusClient, mysql *sql.DB, clientConfig *restclient.Config) (http.Handler, error) {

	verber := common.NewResourceVerber(client.CoreV1().RESTClient(),
		client.ExtensionsV1beta1().RESTClient(), client.AppsV1beta1().RESTClient(),
		client.BatchV1().RESTClient(), client.AutoscalingV1().RESTClient(), client.StorageV1beta1().RESTClient())

	var csrfKey string
	inClusterConfig, err := restclient.InClusterConfig()
	if err == nil {
		// We run in a cluster, so we should use a signing key that is the same for potential replications
		log.Printf("Using service account token for csrf signing")
		csrfKey = inClusterConfig.BearerToken
	} else {
		// Most likely running for a dev, so no replica issues, just generate a random key
		log.Printf("Using random key for csrf signing")
		bytes := make([]byte, 256)
		_, err := rand.Read(bytes)
		if err != nil {
			return nil, err
		}
		csrfKey = string(bytes)
	}

	apiHandler := APIHandler{client, heapsterClient, clientConfig, prometheusClient, mysql, verber, csrfKey}
	wsContainer := restful.NewContainer()
	wsContainer.EnableContentEncoding(true)

	apiV1Ws := new(restful.WebService)
	apiV1Ws.Filter(logRequestAndReponse)

	RegisterMetrics()
	apiV1Ws.Filter(wsMetrics)
	apiV1Ws.Filter(xsrfValidation(csrfKey))
	apiV1Ws.Path("/api/v1").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	wsContainer.Add(apiV1Ws)

	apiV1Ws.Route(
		apiV1Ws.GET("csrftoken/{action}").
			To(apiHandler.handleGetCsrfToken).
			Writes(CsrfToken{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/login/process").
			To(apiHandler.handleCaptchaValidity).
			Reads(validation.CaptchaValidtySpec{}).
			Writes(validation.CaptchaValidty{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/login/captcha").
			To(apiHandler.handleGetCaptcha).
			Writes(validation.CaptchaValidtySpec{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/appdeployment").
			To(apiHandler.handleDeploy).
			Reads(deployment.AppDeploymentSpec{}).
			Writes(deployment.AppDeploymentSpec{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/appdeployment/validate/name").
			To(apiHandler.handleNameValidity).
			Reads(validation.AppNameValiditySpec{}).
			Writes(validation.AppNameValidity{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/appdeployment/validate/imagereference").
			To(apiHandler.handleImageReferenceValidity).
			Reads(validation.ImageReferenceValiditySpec{}).
			Writes(validation.ImageReferenceValidity{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/appdeployment/validate/protocol").
			To(apiHandler.handleProtocolValidity).
			Reads(validation.ProtocolValiditySpec{}).
			Writes(validation.ProtocolValidity{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/appdeployment/protocols").
			To(apiHandler.handleGetAvailableProcotols).
			Writes(deployment.Protocols{}))

	apiV1Ws.Route(
		apiV1Ws.POST("/appdeploymentfromfile").
			To(apiHandler.handleDeployFromFile).
			Reads(deployment.AppDeploymentFromFileSpec{}).
			Writes(deployment.AppDeploymentFromFileResponse{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/replicationcontroller").
			To(apiHandler.handleGetReplicationControllerList).
			Writes(replicationcontroller.ReplicationControllerList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicationcontroller/{namespace}").
			To(apiHandler.handleGetReplicationControllerList).
			Writes(replicationcontroller.ReplicationControllerList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicationcontroller/{namespace}/{replicationController}").
			To(apiHandler.handleGetReplicationControllerDetail).
			Writes(replicationcontroller.ReplicationControllerDetail{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/replicationcontroller/{namespace}/{replicationController}/update/pod").
			To(apiHandler.handleUpdateReplicasCount).
			Reads(replicationcontroller.ReplicationControllerSpec{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicationcontroller/{namespace}/{replicationController}/pod").
			To(apiHandler.handleGetReplicationControllerPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicationcontroller/{namespace}/{replicationController}/event").
			To(apiHandler.handleGetReplicationControllerEvents).
			Writes(common.EventList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicationcontroller/{namespace}/{replicationController}/service").
			To(apiHandler.handleGetReplicationControllerServices).
			Writes(resourceService.ServiceList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/workload").
			To(apiHandler.handleGetWorkloads).
			Writes(workload.Workloads{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/workload/{namespace}").
			To(apiHandler.handleGetWorkloads).
			Writes(workload.Workloads{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/cluster").
			To(apiHandler.handleGetCluster).
			Writes(cluster.Cluster{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/discovery").
			To(apiHandler.handleGetDiscovery).
			Writes(discovery.Discovery{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/discovery/{namespace}").
			To(apiHandler.handleGetDiscovery).
			Writes(discovery.Discovery{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/config").
			To(apiHandler.handleGetConfig).
			Writes(config.Config{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/config/{namespace}").
			To(apiHandler.handleGetConfig).
			Writes(config.Config{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/replicaset").
			To(apiHandler.handleGetReplicaSets).
			Writes(replicaset.ReplicaSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicaset/{namespace}").
			To(apiHandler.handleGetReplicaSets).
			Writes(replicaset.ReplicaSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicaset/{namespace}/{replicaSet}").
			To(apiHandler.handleGetReplicaSetDetail).
			Writes(replicaset.ReplicaSetDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicaset/{namespace}/{replicaSet}/pod").
			To(apiHandler.handleGetReplicaSetPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/replicaset/{namespace}/{replicaSet}/event").
			To(apiHandler.handleGetReplicaSetEvents).
			Writes(common.EventList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/pod").
			To(apiHandler.handleGetPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/pod/{namespace}").
			To(apiHandler.handleGetPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/pod/{namespace}/{pod}").
			To(apiHandler.handleGetPodDetail).
			Writes(pod.PodDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/pod/{namespace}/{pod}/container").
			To(apiHandler.handleGetPodContainers).
			Writes(pod.PodDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/pod/{namespace}/{pod}/log").
			To(apiHandler.handleLogs).
			Writes(logs.Logs{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/pod/{namespace}/{pod}/log/{container}").
			To(apiHandler.handleLogs).
			Writes(logs.Logs{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/pod/{namespace}/{pod}/event").
			To(apiHandler.handleGetPodEvents).
			Writes(common.EventList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/deployment").
			To(apiHandler.handleGetDeployments).
			Writes(deployment.DeploymentList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/deployment/{namespace}").
			To(apiHandler.handleGetDeployments).
			Writes(deployment.DeploymentList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/deployment/{namespace}/{deployment}").
			To(apiHandler.handleGetDeploymentDetail).
			Writes(deployment.DeploymentDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/deployment/{namespace}/{deployment}/event").
			To(apiHandler.handleGetDeploymentEvents).
			Writes(common.EventList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/deployment/{namespace}/{deployment}/oldreplicaset").
			To(apiHandler.handleGetDeploymentOldReplicaSets).
			Writes(replicaset.ReplicaSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/daemonset").
			To(apiHandler.handleGetDaemonSetList).
			Writes(daemonset.DaemonSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/daemonset/{namespace}").
			To(apiHandler.handleGetDaemonSetList).
			Writes(daemonset.DaemonSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/daemonset/{namespace}/{daemonSet}").
			To(apiHandler.handleGetDaemonSetDetail).
			Writes(daemonset.DaemonSetDetail{}))
	apiV1Ws.Route(
		apiV1Ws.DELETE("/daemonset/{namespace}/{daemonSet}").
			To(apiHandler.handleDeleteDaemonSet))
	apiV1Ws.Route(
		apiV1Ws.GET("/daemonset/{namespace}/{daemonSet}/pod").
			To(apiHandler.handleGetDaemonSetPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/daemonset/{namespace}/{daemonSet}/service").
			To(apiHandler.handleGetDaemonSetServices).
			Writes(resourceService.ServiceList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/daemonset/{namespace}/{daemonSet}/event").
			To(apiHandler.handleGetDaemonSetEvents).
			Writes(common.EventList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/horizontalpodautoscaler").
			To(apiHandler.handleGetHorizontalPodAutoscalerList).
			Writes(horizontalpodautoscaler.HorizontalPodAutoscalerList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/horizontalpodautoscaler/{namespace}").
			To(apiHandler.handleGetHorizontalPodAutoscalerList).
			Writes(horizontalpodautoscaler.HorizontalPodAutoscalerList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/horizontalpodautoscaler/{namespace}/{horizontalpodautoscaler}").
			To(apiHandler.handleGetHorizontalPodAutoscalerDetail).
			Writes(horizontalpodautoscaler.HorizontalPodAutoscalerDetail{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/job").
			To(apiHandler.handleGetJobList).
			Writes(job.JobList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/job/{namespace}").
			To(apiHandler.handleGetJobList).
			Writes(job.JobList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/job/{namespace}/{job}").
			To(apiHandler.handleGetJobDetail).
			Writes(job.JobDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/job/{namespace}/{job}/pod").
			To(apiHandler.handleGetJobPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/job/{namespace}/{job}/event").
			To(apiHandler.handleGetJobEvents).
			Writes(common.EventList{}))

	apiV1Ws.Route(
		apiV1Ws.POST("/namespace").
			To(apiHandler.handleCreateNamespace).
			Reads(namespace.NamespaceSpec{}).
			Writes(namespace.NamespaceSpec{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/namespace").
			To(apiHandler.handleGetNamespaces).
			Writes(namespace.NamespaceList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/namespace/{name}").
			To(apiHandler.handleGetNamespaceDetail).
			Writes(namespace.NamespaceDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/namespace/{name}/event").
			To(apiHandler.handleGetNamespaceEvents).
			Writes(common.EventList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/secret").
			To(apiHandler.handleGetSecretList).
			Writes(secret.SecretList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/secret/{namespace}").
			To(apiHandler.handleGetSecretList).
			Writes(secret.SecretList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/secret/{namespace}/{name}").
			To(apiHandler.handleGetSecretDetail).
			Writes(secret.SecretDetail{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/secret").
			To(apiHandler.handleCreateImagePullSecret).
			Reads(secret.ImagePullSecretSpec{}).
			Writes(secret.Secret{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/configmap").
			To(apiHandler.handleGetConfigMapList).
			Writes(configmap.ConfigMapList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/configmap/{namespace}").
			To(apiHandler.handleGetConfigMapList).
			Writes(configmap.ConfigMapList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/configmap/{namespace}/{configmap}").
			To(apiHandler.handleGetConfigMapDetail).
			Writes(configmap.ConfigMapDetail{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/service").
			To(apiHandler.handleGetServiceList).
			Writes(resourceService.ServiceList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/service/{namespace}").
			To(apiHandler.handleGetServiceList).
			Writes(resourceService.ServiceList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/service/{namespace}/{service}").
			To(apiHandler.handleGetServiceDetail).
			Writes(resourceService.ServiceDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/service/{namespace}/{service}/pod").
			To(apiHandler.handleGetServicePods).
			Writes(pod.PodList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/ingress").
			To(apiHandler.handleGetIngressList).
			Writes(ingress.IngressList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/ingress/{namespace}").
			To(apiHandler.handleGetIngressList).
			Writes(ingress.IngressList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/ingress/{namespace}/{name}").
			To(apiHandler.handleGetIngressDetail).
			Writes(ingress.IngressDetail{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/statefulset").
			To(apiHandler.handleGetStatefulSetList).
			Writes(statefulset.StatefulSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/statefulset/{namespace}").
			To(apiHandler.handleGetStatefulSetList).
			Writes(statefulset.StatefulSetList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/statefulset/{namespace}/{statefulset}").
			To(apiHandler.handleGetStatefulSetDetail).
			Writes(statefulset.StatefulSetDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/statefulset/{namespace}/{statefulset}/pod").
			To(apiHandler.handleGetStatefulSetPods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/statefulset/{namespace}/{statefulset}/event").
			To(apiHandler.handleGetStatefulSetEvents).
			Writes(common.EventList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/node").
			To(apiHandler.handleGetNodeList).
			Writes(node.NodeList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/node/{name}").
			To(apiHandler.handleGetNodeDetail).
			Writes(node.NodeDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/node/{name}/event").
			To(apiHandler.handleGetNodeEvents).
			Writes(common.EventList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/node/{name}/pod").
			To(apiHandler.handleGetNodePods).
			Writes(pod.PodList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/node/{name}/{kind}/{query}").
			To(apiHandler.handleGetPrometheusMetrics).
			Writes(metric.QueryData{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/_raw/{kind}/namespace/{namespace}").
			To(apiHandler.handleGetResourceList))
	apiV1Ws.Route(
		apiV1Ws.POST("/_raw/{kind}/namespace/{namespace}").
			To(apiHandler.handlePostResource))
	apiV1Ws.Route(
		apiV1Ws.DELETE("/_raw/{kind}/namespace/{namespace}/name/{name}").
			To(apiHandler.handleDeleteResource))
	apiV1Ws.Route(
		apiV1Ws.GET("/_raw/{kind}/namespace/{namespace}/name/{name}").
			To(apiHandler.handleGetResource))
	apiV1Ws.Route(
		apiV1Ws.PUT("/_raw/{kind}/namespace/{namespace}/name/{name}").
			To(apiHandler.handlePutResource))

	apiV1Ws.Route(
		apiV1Ws.GET("/_raw/{kind}").
			To(apiHandler.handleGetResourceList))
	apiV1Ws.Route(
		apiV1Ws.POST("/_raw/{kind}").
			To(apiHandler.handlePostResource))
	apiV1Ws.Route(
		apiV1Ws.DELETE("/_raw/{kind}/name/{name}").
			To(apiHandler.handleDeleteResource))
	apiV1Ws.Route(
		apiV1Ws.GET("/_raw/{kind}/name/{name}/finialize").
			To(apiHandler.handleFinializeResource))
	apiV1Ws.Route(
		apiV1Ws.GET("/_raw/{kind}/name/{name}").
			To(apiHandler.handleGetResource))
	apiV1Ws.Route(
		apiV1Ws.PUT("/_raw/{kind}/name/{name}").
			To(apiHandler.handlePutResource))
	apiV1Ws.Route(
		apiV1Ws.GET("/rbacrole").
			To(apiHandler.handleGetRbacRoleList).
			Writes(rbacroles.RbacRoleList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/rbacrolebinding").
			To(apiHandler.handleGetRbacRoleBindingList).
			Writes(rbacrolebindings.RbacRoleBindingList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/persistentvolume").
			To(apiHandler.handleGetPersistentVolumeList).
			Writes(persistentvolume.PersistentVolumeList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/persistentvolume/{persistentvolume}").
			To(apiHandler.handleGetPersistentVolumeDetail).
			Writes(persistentvolume.PersistentVolumeDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/persistentvolume/namespace/{namespace}/name/{persistentvolume}").
			To(apiHandler.handleGetPersistentVolumeDetail).
			Writes(persistentvolume.PersistentVolumeDetail{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/persistentvolumeclaim/").
			To(apiHandler.handleGetPersistentVolumeClaimList).
			Writes(persistentvolumeclaim.PersistentVolumeClaimList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/persistentvolumeclaim/{namespace}").
			To(apiHandler.handleGetPersistentVolumeClaimList).
			Writes(persistentvolumeclaim.PersistentVolumeClaimList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/persistentvolumeclaim/{namespace}/{name}").
			To(apiHandler.handleGetPersistentVolumeClaimDetail).
			Writes(persistentvolumeclaim.PersistentVolumeClaimDetail{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/thirdpartyresource").
			To(apiHandler.handleGetThirdPartyResource).
			Writes(thirdpartyresource.ThirdPartyResourceList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/thirdpartyresource/{thirdpartyresource}").
			To(apiHandler.handleGetThirdPartyResourceDetail).
			Writes(thirdpartyresource.ThirdPartyResourceDetail{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/thirdpartyresource/{thirdpartyresource}/object").
			To(apiHandler.handleGetThirdPartyResourceObjects).
			Writes(thirdpartyresource.ThirdPartyResourceObjectList{}))

	apiV1Ws.Route(
		apiV1Ws.GET("/storageclass").
			To(apiHandler.handleGetStorageClassList).
			Writes(storageclass.StorageClassList{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/storageclass/{storageclass}").
			To(apiHandler.handleGetStorageClass).
			Writes(storageclass.StorageClass{}))
	apiV1Ws.Route(
		apiV1Ws.GET("/metrics/{kind}/{type}").
			To(apiHandler.handleGetMetric).
			Writes(heapster.MetricResult{}))

	// report
	apiV1Ws.Route(
		apiV1Ws.GET("/report/namespace/{namespace}/username/{username}/name/{name}").
			To(apiHandler.handleGetForm))

	apiV1Ws.Route(
		apiV1Ws.GET("/report/namespace/{namespace}/username/{username}").
			To(apiHandler.handleGetFormList).
			Writes([]string{}))
	apiV1Ws.Route(
		apiV1Ws.POST("/report/namespace/{namespace}/username/{username}").
			To(apiHandler.handlePOSTForm))

	apiV1Ws.Route(
		apiV1Ws.DELETE("/report/namespace/{namespace}/username/{username}/name/{name}").
			To(apiHandler.handleDeleteForm))
	apiV1Ws.Route(
		apiV1Ws.PUT("/report/namespace/{namespace}/username/{username}/name/{name}").
			To(apiHandler.handlePUTForm))

	return wsContainer, nil
}

func (apiHandler *APIHandler) handleGetForm(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	username := request.PathParameter("username")
	name := request.PathParameter("name")
	rf := &report.Form{
		Meta: &report.Meta{
			Name:      name,
			NameSpace: namespace,
			User:      username,
		}}
	client.GetForm(apiHandler.mysqlClient, rf)
	if len(rf.Kind) == 0 {
		rf = &report.Form{}
	}
	response.WriteHeaderAndEntity(http.StatusOK, rf)

}

func (apiHandler *APIHandler) handleGetFormList(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	username := request.PathParameter("username")
	rf := &report.Form{
		Meta: &report.Meta{
			NameSpace: namespace,
			User:      username}}

	list := client.ListForm(apiHandler.mysqlClient, rf)
	response.WriteHeaderAndEntity(http.StatusOK, list)

}

func (apiHandler *APIHandler) handlePOSTForm(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	username := request.PathParameter("username")
	name := request.PathParameter("name")
	rf := &report.Form{Meta: &report.Meta{
		Name:      name,
		NameSpace: namespace,
		User:      username,
	}}
	if err := request.ReadEntity(rf); err != nil {
		handleInternalError(response, err)
		return
	}
	client.CreateForm(apiHandler.mysqlClient, rf)
	response.WriteHeader(http.StatusCreated)

}

func (apiHandler *APIHandler) handleDeleteForm(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	username := request.PathParameter("username")
	name := request.PathParameter("name")
	rf := report.Form{
		Meta: &report.Meta{
			Name:      name,
			NameSpace: namespace,
			User:      username}}
	client.DeleteForm(apiHandler.mysqlClient, rf)
	response.WriteHeader(http.StatusOK)
}

func (apiHandler *APIHandler) handlePUTForm(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	username := request.PathParameter("username")
	name := request.PathParameter("name")
	rf := &report.Form{Meta: &report.Meta{
		Name:      name,
		NameSpace: namespace,
		User:      username,
	}}
	if err := request.ReadEntity(rf); err != nil {
		handleInternalError(response, err)
		return
	}
	client.UpdateForm(apiHandler.mysqlClient, rf)
	response.WriteHeader(http.StatusCreated)
}

func (apiHandler *APIHandler) handleGetMetric(request *restful.Request, response *restful.Response) {
	// TODO: Handle case in which RBAC feature is not enabled in API server. Currently returns 404 resource not found
	kind := request.PathParameter("kind")
	ty := request.PathParameter("type")
	path := "/model/metrics/" + kind + "/" + ty
	rawResult := heapster.MetricResult{}
	err := heapsterUnmarshalType(apiHandler.heapsterClient, path, &rawResult)
	log.Print(rawResult.Metrics)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, &rawResult)
}

func heapsterUnmarshalType(client client.HeapsterClient, path string, v interface{}) error {
	rawData, err := client.Get(path).DoRaw()
	log.Print(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(rawData, v)
}

func (apiHandler *APIHandler) handleGetRbacRoleList(request *restful.Request, response *restful.Response) {
	// TODO: Handle case in which RBAC feature is not enabled in API server. Currently returns 404 resource not found
	dataSelect := parseDataSelectPathParameter(request)
	result, err := rbacroles.GetRbacRoleList(apiHandler.client, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetRbacRoleBindingList(request *restful.Request, response *restful.Response) {
	// TODO: Handle case in which RBAC feature is not enabled in API server. Currently returns 404 resource not found
	dataSelect := parseDataSelectPathParameter(request)
	result, err := rbacrolebindings.GetRbacRoleBindingList(apiHandler.client, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetCsrfToken(request *restful.Request,
	response *restful.Response) {
	action := request.PathParameter("action")
	token := xsrftoken.Generate(apiHandler.csrfKey, "none", action)
	log.Printf("action is %#v, token is %#v", action, token)

	response.WriteHeaderAndEntity(http.StatusOK, CsrfToken{Token: token})
}

// Handles get pet set list API call.
func (apiHandler *APIHandler) handleGetStatefulSetList(request *restful.Request,
	response *restful.Response) {
	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics
	result, err := statefulset.GetStatefulSetList(apiHandler.client, namespace, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get pet set detail API call.
func (apiHandler *APIHandler) handleGetStatefulSetDetail(request *restful.Request,
	response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("statefulset")

	result, err := statefulset.GetStatefulSetDetail(apiHandler.client, apiHandler.heapsterClient,
		namespace, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get pet set pods API call.
func (apiHandler *APIHandler) handleGetStatefulSetPods(request *restful.Request,
	response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("statefulset")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := statefulset.GetStatefulSetPods(apiHandler.client, apiHandler.heapsterClient,
		dataSelect, name, namespace)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get pet set events API call.
func (apiHandler *APIHandler) handleGetStatefulSetEvents(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("statefulset")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := statefulset.GetStatefulSetEvents(apiHandler.client, dataSelect, namespace,
		name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get service list API call.
func (apiHandler *APIHandler) handleGetServiceList(request *restful.Request, response *restful.Response) {
	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	result, err := resourceService.GetServiceList(apiHandler.client, namespace, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get service detail API call.
func (apiHandler *APIHandler) handleGetServiceDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	service := request.PathParameter("service")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := resourceService.GetServiceDetail(apiHandler.client, apiHandler.heapsterClient,
		namespace, service, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetIngressDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("name")
	result, err := ingress.GetIngressDetail(apiHandler.client, namespace, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetResourceList(
	request *restful.Request, response *restful.Response) {
	kind := request.PathParameter("kind")
	namespace, ok := request.PathParameters()["namespace"]

	if kind == "resourcequota" {
		result, err := apiHandler.client.CoreV1().ResourceQuotas(namespace).List(metaV1.ListOptions{
			LabelSelector: labels.Everything().String(),
			FieldSelector: fields.Everything().String(),
		})
		if err != nil {
			handleInternalError(response, err)
			return
		}

		response.WriteHeaderAndEntity(http.StatusOK, result)
	} else {
		result, err := apiHandler.verber.GetList(kind, ok, namespace)
		if err != nil {
			handleInternalError(response, err)
			return
		}

		response.WriteHeaderAndEntity(http.StatusOK, result)
	}
}

func (apiHandler *APIHandler) handleGetIngressList(request *restful.Request, response *restful.Response) {
	dataSelect := parseDataSelectPathParameter(request)
	namespace := parseNamespacePathParameter(request)
	result, err := ingress.GetIngressList(apiHandler.client, namespace, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get service pods API call.
func (apiHandler *APIHandler) handleGetServicePods(request *restful.Request,
	response *restful.Response) {

	namespace := request.PathParameter("namespace")
	service := request.PathParameter("service")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := resourceService.GetServicePods(apiHandler.client, apiHandler.heapsterClient,
		namespace, service, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get node list API call.
func (apiHandler *APIHandler) handleGetNodeList(request *restful.Request, response *restful.Response) {
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics

	result, err := node.GetNodeList(apiHandler.client, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetCluster(request *restful.Request, response *restful.Response) {
	result, err := cluster.GetCluster(apiHandler.client)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get node detail API call.
func (apiHandler *APIHandler) handleGetNodeDetail(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("name")

	result, err := node.GetNodeDetail(apiHandler.client, apiHandler.heapsterClient, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get node events API call.
func (apiHandler *APIHandler) handleGetNodeEvents(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("name")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := event.GetNodeEvents(apiHandler.client, dataSelect, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get node pods API call.
func (apiHandler *APIHandler) handleGetNodePods(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("name")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := node.GetNodePods(apiHandler.client, apiHandler.heapsterClient, dataSelect, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// handle get prometheus metrics
func (apiHandler *APIHandler) handleGetPrometheusMetrics(request *restful.Request, response *restful.Response) {

	query := request.PathParameter("query")

	result, err := apiHandler.prometheusClient.Get(query).DoRaw()
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles deploy API call.
func (apiHandler *APIHandler) handleDeploy(request *restful.Request, response *restful.Response) {
	appDeploymentSpec := new(deployment.AppDeploymentSpec)
	if err := request.ReadEntity(appDeploymentSpec); err != nil {
		handleInternalError(response, err)
		return
	}
	if err := deployment.DeployApp(appDeploymentSpec, apiHandler.client); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusCreated, appDeploymentSpec)
}

// Handles deploy from file API call.
func (apiHandler *APIHandler) handleDeployFromFile(request *restful.Request, response *restful.Response) {
	deploymentSpec := new(deployment.AppDeploymentFromFileSpec)
	if err := request.ReadEntity(deploymentSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	isDeployed, err := deployment.DeployAppFromFile(
		deploymentSpec, deployment.CreateObjectFromInfoFn)
	if !isDeployed {
		handleInternalError(response, err)
		return
	}

	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}

	response.WriteHeaderAndEntity(http.StatusCreated, deployment.AppDeploymentFromFileResponse{
		Name:    deploymentSpec.Name,
		Content: deploymentSpec.Content,
		Error:   errorMessage,
	})
}

// Handles app name validation API call.
func (apiHandler *APIHandler) handleNameValidity(request *restful.Request, response *restful.Response) {
	spec := new(validation.AppNameValiditySpec)
	if err := request.ReadEntity(spec); err != nil {
		handleInternalError(response, err)
		return
	}

	validity, err := validation.ValidateAppName(spec, apiHandler.client)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, validity)
}

// Handles image reference validation API call.
func (APIHandler *APIHandler) handleImageReferenceValidity(request *restful.Request, response *restful.Response) {
	spec := new(validation.ImageReferenceValiditySpec)
	if err := request.ReadEntity(spec); err != nil {
		handleInternalError(response, err)
		return
	}

	validity, err := validation.ValidateImageReference(spec)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, validity)
}

// Handles protocol validation API call.
func (apiHandler *APIHandler) handleProtocolValidity(request *restful.Request, response *restful.Response) {
	spec := new(validation.ProtocolValiditySpec)
	if err := request.ReadEntity(spec); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, validation.ValidateProtocol(spec))
}

//  protocol validation API call.
func (apiHandler *APIHandler) handleCaptchaValidity(request *restful.Request, response *restful.Response) {
	spec := new(validation.CaptchaValidtySpec)
	if err := request.ReadEntity(spec); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, validation.ValidateCaptcha(spec))
}

// Handles get available protocols API call.
func (apiHandler *APIHandler) handleGetAvailableProcotols(request *restful.Request, response *restful.Response) {
	response.WriteHeaderAndEntity(http.StatusOK, deployment.GetAvailableProtocols())
}

// handleGetCaptcha get random captcha
func (apiHandler *APIHandler) handleGetCaptcha(request *restful.Request, response *restful.Response) {

	response.WriteHeaderAndEntity(http.StatusOK, validation.CaptchaValidtySpec{CaptchaID: captcha.New()})
}

// Handles get Replication Controller list API call.
func (apiHandler *APIHandler) handleGetReplicationControllerList(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics
	result, err := replicationcontroller.GetReplicationControllerList(apiHandler.client, namespace, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Workloads list API call.
func (apiHandler *APIHandler) handleGetWorkloads(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	result, err := workload.GetWorkloads(apiHandler.client, apiHandler.heapsterClient,
		namespace, dataselect.StandardMetrics)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetDiscovery(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	result, err := discovery.GetDiscovery(apiHandler.client, namespace)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetConfig(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	result, err := config.GetConfig(apiHandler.client, apiHandler.heapsterClient, namespace,
		dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Replica Sets list API call.
func (apiHandler *APIHandler) handleGetReplicaSets(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics
	result, err := replicaset.GetReplicaSetList(apiHandler.client, namespace, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Replica Sets Detail API call.
func (apiHandler *APIHandler) handleGetReplicaSetDetail(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	replicaSet := request.PathParameter("replicaSet")

	result, err := replicaset.GetReplicaSetDetail(apiHandler.client, apiHandler.heapsterClient,
		namespace, replicaSet)

	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Replica Sets pods API call.
func (apiHandler *APIHandler) handleGetReplicaSetPods(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	replicaSet := request.PathParameter("replicaSet")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := replicaset.GetReplicaSetPods(apiHandler.client, apiHandler.heapsterClient,
		dataSelect, replicaSet, namespace)

	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Replica Set services API call.
func (apiHandler *APIHandler) handleGetReplicaSetServices(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	replicaSet := request.PathParameter("replicaSet")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := replicaset.GetReplicaSetServices(apiHandler.client, dataSelect, namespace,
		replicaSet)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get replica set events API call.
func (apiHandler *APIHandler) handleGetReplicaSetEvents(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("replicaSet")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := replicaset.GetReplicaSetEvents(apiHandler.client, dataSelect, namespace,
		name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)

}

// Handles get pod set events API call.
func (apiHandler *APIHandler) handleGetPodEvents(request *restful.Request, response *restful.Response) {
	log.Println("Getting events related to a pod in namespace")

	namespace := request.PathParameter("namespace")
	podName := request.PathParameter("pod")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := pod.GetEventsForPod(apiHandler.client, dataSelect, namespace,
		podName)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Deployment list API call.
func (apiHandler *APIHandler) handleGetDeployments(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics
	result, err := deployment.GetDeploymentList(apiHandler.client, namespace, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Deployment detail API call.
func (apiHandler *APIHandler) handleGetDeploymentDetail(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	name := request.PathParameter("deployment")

	result, err := deployment.GetDeploymentDetail(apiHandler.client, apiHandler.heapsterClient, namespace, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get deployment events API call.
func (apiHandler *APIHandler) handleGetDeploymentEvents(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("deployment")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := deployment.GetDeploymentEvents(apiHandler.client, dataSelect, namespace,
		name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get deployment old replica sets API call.
func (apiHandler *APIHandler) handleGetDeploymentOldReplicaSets(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("deployment")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := deployment.GetDeploymentOldReplicaSets(apiHandler.client, dataSelect, namespace,
		name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Pod list API call.
func (apiHandler *APIHandler) handleGetPods(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics // download standard metrics - cpu, and memory - by default
	result, err := pod.GetPodList(apiHandler.client, apiHandler.heapsterClient, namespace, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Pod detail API call.
func (apiHandler *APIHandler) handleGetPodDetail(request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	podName := request.PathParameter("pod")
	result, err := pod.GetPodDetail(apiHandler.client, apiHandler.heapsterClient, namespace, podName)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Replication Controller detail API call.
func (apiHandler *APIHandler) handleGetReplicationControllerDetail(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	replicationController := request.PathParameter("replicationController")

	result, err := replicationcontroller.GetReplicationControllerDetail(apiHandler.client,
		apiHandler.heapsterClient, namespace, replicationController)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles update of Replication Controller pods update API call.
func (apiHandler *APIHandler) handleUpdateReplicasCount(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	replicationControllerName := request.PathParameter("replicationController")
	replicationControllerSpec := new(replicationcontroller.ReplicationControllerSpec)

	if err := request.ReadEntity(replicationControllerSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	if err := replicationcontroller.UpdateReplicasCount(apiHandler.client, namespace, replicationControllerName,
		replicationControllerSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeader(http.StatusAccepted)
}

func (apiHandler *APIHandler) handleFinializeResource(
	request *restful.Request, response *restful.Response) {
	kind := request.PathParameter("kind")
	name := request.PathParameter("name")
	log.Printf("kind is %s, name is %s", kind, name)
	if err := apiHandler.verber.Finialize(kind, name); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeader(http.StatusOK)
}

func (apiHandler *APIHandler) handleGetResource(
	request *restful.Request, response *restful.Response) {
	kind := request.PathParameter("kind")
	namespace, ok := request.PathParameters()["namespace"]
	name := request.PathParameter("name")

	if kind == "resourcequota" {
		result, err := apiHandler.client.CoreV1().ResourceQuotas(namespace).Get(name, metaV1.GetOptions{})
		if err != nil {
			handleInternalError(response, err)
			return
		}

		response.WriteHeaderAndEntity(http.StatusOK, result)
	} else {
		result, err := apiHandler.verber.Get(kind, ok, namespace, name)
		if err != nil {
			handleInternalError(response, err)
			return
		}

		response.WriteHeaderAndEntity(http.StatusOK, result)
	}
}

func (apiHandler *APIHandler) handlePostResource(
	request *restful.Request, response *restful.Response) {
	kind := request.PathParameter("kind")
	namespace, ok := request.PathParameters()["namespace"]
	putSpec := &runtime.Unknown{}
	if err := request.ReadEntity(putSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	if err := apiHandler.verber.Post(kind, ok, namespace, putSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeader(http.StatusCreated)
}

func (apiHandler *APIHandler) handlePutResource(
	request *restful.Request, response *restful.Response) {
	kind := request.PathParameter("kind")
	namespace, ok := request.PathParameters()["namespace"]
	name := request.PathParameter("name")
	putSpec := &runtime.Unknown{}
	if err := request.ReadEntity(putSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	if err := apiHandler.verber.Put(kind, ok, namespace, name, putSpec); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeader(http.StatusCreated)
}

func (apiHandler *APIHandler) handleDeleteResource(
	request *restful.Request, response *restful.Response) {
	kind := request.PathParameter("kind")
	namespace, ok := request.PathParameters()["namespace"]
	name := request.PathParameter("name")

	if err := apiHandler.verber.Delete(kind, ok, namespace, name); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeader(http.StatusOK)
}

// Handles get Replication Controller Pods API call.
func (apiHandler *APIHandler) handleGetReplicationControllerPods(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	replicationController := request.PathParameter("replicationController")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := replicationcontroller.GetReplicationControllerPods(apiHandler.client, apiHandler.heapsterClient,
		dataSelect, replicationController, namespace)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles namespace creation API call.
func (apiHandler *APIHandler) handleCreateNamespace(request *restful.Request,
	response *restful.Response) {
	namespaceSpec := new(namespace.NamespaceSpec)
	if err := request.ReadEntity(namespaceSpec); err != nil {
		handleInternalError(response, err)
		return
	}
	if err := namespace.CreateNamespace(namespaceSpec, apiHandler.client); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusCreated, namespaceSpec)
}

// Handles get namespace list API call.
func (apiHandler *APIHandler) handleGetNamespaces(
	request *restful.Request, response *restful.Response) {

	dataSelect := parseDataSelectPathParameter(request)
	result, err := namespace.GetNamespaceList(apiHandler.client, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get namespace detail API call.
func (apiHandler *APIHandler) handleGetNamespaceDetail(request *restful.Request,
	response *restful.Response) {
	name := request.PathParameter("name")
	result, err := namespace.GetNamespaceDetail(apiHandler.client, apiHandler.heapsterClient, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get namespace events API call.
func (apiHandler *APIHandler) handleGetNamespaceEvents(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("name")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := event.GetNamespaceEvents(apiHandler.client, dataSelect, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles image pull secret creation API call.
func (apiHandler *APIHandler) handleCreateImagePullSecret(request *restful.Request, response *restful.Response) {
	secretSpec := new(secret.ImagePullSecretSpec)
	if err := request.ReadEntity(secretSpec); err != nil {
		handleInternalError(response, err)
		return
	}
	secret, err := secret.CreateSecret(apiHandler.client, secretSpec)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusCreated, secret)
}

func (apiHandler *APIHandler) handleGetSecretDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("name")
	result, err := secret.GetSecretDetail(apiHandler.client, namespace, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get secrets list API call.
func (apiHandler *APIHandler) handleGetSecretList(request *restful.Request, response *restful.Response) {
	dataSelect := parseDataSelectPathParameter(request)
	namespace := parseNamespacePathParameter(request)
	result, err := secret.GetSecretList(apiHandler.client, namespace, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetConfigMapList(request *restful.Request, response *restful.Response) {
	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	result, err := configmap.GetConfigMapList(apiHandler.client, namespace, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetConfigMapDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("configmap")
	result, err := configmap.GetConfigMapDetail(apiHandler.client, namespace, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetPersistentVolumeList(request *restful.Request, response *restful.Response) {
	dataSelect := parseDataSelectPathParameter(request)
	result, err := persistentvolume.GetPersistentVolumeList(apiHandler.client, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetThirdPartyResource(request *restful.Request,
	response *restful.Response) {
	dataSelect := parseDataSelectPathParameter(request)
	result, err := thirdpartyresource.GetThirdPartyResourceList(apiHandler.client, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetThirdPartyResourceDetail(request *restful.Request,
	response *restful.Response) {
	name := request.PathParameter("thirdpartyresource")
	result, err := thirdpartyresource.GetThirdPartyResourceDetail(apiHandler.client, apiHandler.config, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetThirdPartyResourceObjects(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("thirdpartyresource")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := thirdpartyresource.GetThirdPartyResourceObjects(apiHandler.client, apiHandler.config, dataSelect, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetPersistentVolumeDetail(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("persistentvolume")
	result, err := persistentvolume.GetPersistentVolumeDetail(apiHandler.client, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetPersistentVolumeClaimList(request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	result, err := persistentvolumeclaim.GetPersistentVolumeClaimList(apiHandler.client, namespace, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetPersistentVolumeClaimDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("name")
	result, err := persistentvolumeclaim.GetPersistentVolumeClaimDetail(apiHandler.client, namespace, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles log API call.
func (apiHandler *APIHandler) handleLogs(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	podID := request.PathParameter("pod")
	containerID := request.PathParameter("container")

	refTimestamp := request.QueryParameter("referenceTimestamp")
	if refTimestamp == "" {
		refTimestamp = logs.NewestTimestamp
	}

	refLineNum, err := strconv.Atoi(request.QueryParameter("referenceLineNum"))
	if err != nil {
		refLineNum = 0
	}

	relativeFrom, err1 := strconv.Atoi(request.QueryParameter("relativeFrom"))
	relativeTo, err2 := strconv.Atoi(request.QueryParameter("relativeTo"))

	var logSelector *logs.LogViewSelector
	if err1 != nil || err2 != nil {
		logSelector = logs.DefaultLogViewSelector
	} else {

		logSelector = &logs.LogViewSelector{
			ReferenceLogLineId: logs.LogLineId{
				LogTimestamp: logs.LogTimestamp(refTimestamp),
				LineNum:      refLineNum,
			},
			RelativeFrom: relativeFrom,
			RelativeTo:   relativeTo,
		}
	}

	result, err := container.GetPodLogs(apiHandler.client, namespace, podID, containerID, logSelector)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetPodContainers(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	podID := request.PathParameter("pod")

	result, err := container.GetPodContainers(apiHandler.client, namespace, podID)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get replication controller events API call.
func (apiHandler *APIHandler) handleGetReplicationControllerEvents(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	replicationController := request.PathParameter("replicationController")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := replicationcontroller.GetReplicationControllerEvents(apiHandler.client, dataSelect, namespace,
		replicationController)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get replication controller services API call.
func (apiHandler *APIHandler) handleGetReplicationControllerServices(request *restful.Request,
	response *restful.Response) {
	namespace := request.PathParameter("namespace")
	replicationController := request.PathParameter("replicationController")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := replicationcontroller.GetReplicationControllerServices(apiHandler.client, dataSelect,
		namespace, replicationController)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handler that writes the given error to the response and sets appropriate HTTP status headers.
func handleInternalError(response *restful.Response, err error) {
	log.Print(err)

	statusCode := http.StatusInternalServerError
	statusError, ok := err.(*errorsK8s.StatusError)
	if ok && statusError.Status().Code > 0 {
		statusCode = int(statusError.Status().Code)
	}

	response.AddHeader("Content-Type", "text/plain")
	response.WriteErrorString(statusCode, err.Error()+"\n")
}

// Handles get Daemon Set list API call.
func (apiHandler *APIHandler) handleGetDaemonSetList(
	request *restful.Request, response *restful.Response) {

	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics
	result, err := daemonset.GetDaemonSetList(apiHandler.client, namespace, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Daemon Set detail API call.
func (apiHandler *APIHandler) handleGetDaemonSetDetail(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	daemonSet := request.PathParameter("daemonSet")

	result, err := daemonset.GetDaemonSetDetail(apiHandler.client, apiHandler.heapsterClient,
		namespace, daemonSet)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Daemon Set pods API call.
func (apiHandler *APIHandler) handleGetDaemonSetPods(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	daemonSet := request.PathParameter("daemonSet")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := daemonset.GetDaemonSetPods(apiHandler.client, apiHandler.heapsterClient,
		dataSelect, daemonSet, namespace)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Daemon Set services API call.
func (apiHandler *APIHandler) handleGetDaemonSetServices(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	daemonSet := request.PathParameter("daemonSet")
	dataSelect := parseDataSelectPathParameter(request)
	result, err := daemonset.GetDaemonSetServices(apiHandler.client, dataSelect, namespace,
		daemonSet)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get daemon set events API call.
func (apiHandler *APIHandler) handleGetDaemonSetEvents(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("daemonSet")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := daemonset.GetDaemonSetEvents(apiHandler.client, dataSelect, namespace,
		name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles delete Daemon Set API call.
func (apiHandler *APIHandler) handleDeleteDaemonSet(
	request *restful.Request, response *restful.Response) {

	namespace := request.PathParameter("namespace")
	daemonSet := request.PathParameter("daemonSet")
	deleteServices, err := strconv.ParseBool(request.QueryParameter("deleteServices"))
	if err != nil {
		handleInternalError(response, err)
		return
	}

	if err := daemonset.DeleteDaemonSet(apiHandler.client, namespace,
		daemonSet, deleteServices); err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeader(http.StatusOK)
}

// Handles get HorizontalPodAutoscalers list API call.
func (apiHandler *APIHandler) handleGetHorizontalPodAutoscalerList(request *restful.Request,
	response *restful.Response) {
	namespace := parseNamespacePathParameter(request)

	result, err := horizontalpodautoscaler.GetHorizontalPodAutoscalerList(apiHandler.client, namespace)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetHorizontalPodAutoscalerDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	horizontalpodautoscalerParam := request.PathParameter("horizontalpodautoscaler")

	result, err := horizontalpodautoscaler.GetHorizontalPodAutoscalerDetail(apiHandler.client, namespace, horizontalpodautoscalerParam)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Jobs list API call.
func (apiHandler *APIHandler) handleGetJobList(request *restful.Request,
	response *restful.Response) {
	namespace := parseNamespacePathParameter(request)
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics

	result, err := job.GetJobList(apiHandler.client, namespace, dataSelect, &apiHandler.heapsterClient)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

func (apiHandler *APIHandler) handleGetJobDetail(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	jobParam := request.PathParameter("job")
	dataSelect := parseDataSelectPathParameter(request)
	dataSelect.MetricQuery = dataselect.StandardMetrics

	result, err := job.GetJobDetail(apiHandler.client, apiHandler.heapsterClient, namespace, jobParam)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get Job pods API call.
func (apiHandler *APIHandler) handleGetJobPods(request *restful.Request,
	response *restful.Response) {

	namespace := request.PathParameter("namespace")
	jobParam := request.PathParameter("job")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := job.GetJobPods(apiHandler.client, apiHandler.heapsterClient, dataSelect,
		namespace, jobParam)
	if err != nil {
		handleInternalError(response, err)
		return
	}

	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get job events API call.
func (apiHandler *APIHandler) handleGetJobEvents(request *restful.Request, response *restful.Response) {
	namespace := request.PathParameter("namespace")
	name := request.PathParameter("job")
	dataSelect := parseDataSelectPathParameter(request)

	result, err := job.GetJobEvents(apiHandler.client, dataSelect, namespace,
		name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get storage class list API call.
func (apiHandler *APIHandler) handleGetStorageClassList(request *restful.Request, response *restful.Response) {
	dataSelect := parseDataSelectPathParameter(request)

	result, err := storageclass.GetStorageClassList(apiHandler.client, dataSelect)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// Handles get storage class API call.
func (apiHandler *APIHandler) handleGetStorageClass(request *restful.Request, response *restful.Response) {
	name := request.PathParameter("storageclass")

	result, err := storageclass.GetStorageClass(apiHandler.client, name)
	if err != nil {
		handleInternalError(response, err)
		return
	}
	response.WriteHeaderAndEntity(http.StatusOK, result)
}

// parseNamespacePathParameter parses namespace selector for list pages in path parameter.
// The namespace selector is a comma separated list of namespaces that are trimmed.
// No namespaces means "view all user namespaces", i.e., everything except kube-system.
func parseNamespacePathParameter(request *restful.Request) *common.NamespaceQuery {
	namespace := request.PathParameter("namespace")
	namespaces := strings.Split(namespace, ",")
	var nonEmptyNamespaces []string
	for _, n := range namespaces {
		n = strings.Trim(n, " ")
		if len(n) > 0 {
			nonEmptyNamespaces = append(nonEmptyNamespaces, n)
		}
	}
	return common.NewNamespaceQuery(nonEmptyNamespaces)
}

func parsePaginationPathParameter(request *restful.Request) *dataselect.PaginationQuery {
	/*
		itemsPerPage, err := strconv.ParseInt(request.QueryParameter("itemsPerPage"), 10, 0)
		if err != nil {
			return dataselect.NoPagination
		}

		page, err := strconv.ParseInt(request.QueryParameter("page"), 10, 0)
		if err != nil {
			return dataselect.NoPagination
		}

		// Frontend pages start from 1 and backend starts from 0
		return dataselect.NewPaginationQuery(int(itemsPerPage), int(page-1))
	*/
	return dataselect.NoPagination
}

func parseFilterPathParameter(request *restful.Request) *dataselect.FilterQuery {
	return dataselect.NewFilterQuery(strings.Split(request.QueryParameter("filterby"), ","))
}

// Parses query parameters of the request and returns a SortQuery object
func parseSortPathParameter(request *restful.Request) *dataselect.SortQuery {
	return dataselect.NewSortQuery(strings.Split(request.QueryParameter("sortby"), ","))
}

// Parses query parameters of the request and returns a MetricQuery object
func parseMetricPathParameter(request *restful.Request) *dataselect.MetricQuery {
	metricNamesParam := request.QueryParameter("metricNames")
	var metricNames []string
	if metricNamesParam != "" {
		metricNames = strings.Split(metricNamesParam, ",")
	} else {
		metricNames = nil
	}
	aggregationsParam := request.QueryParameter("aggregations")
	var rawAggregations []string
	if aggregationsParam != "" {
		rawAggregations = strings.Split(aggregationsParam, ",")
	} else {
		rawAggregations = nil
	}
	aggregationNames := metric.AggregationNames{}
	for _, e := range rawAggregations {
		aggregationNames = append(aggregationNames, metric.AggregationName(e))
	}
	return dataselect.NewMetricQuery(metricNames, aggregationNames)

}

// Parses query parameters of the request and returns a DataSelectQuery object
func parseDataSelectPathParameter(request *restful.Request) *dataselect.DataSelectQuery {
	paginationQuery := parsePaginationPathParameter(request)
	sortQuery := parseSortPathParameter(request)
	filterQuery := parseFilterPathParameter(request)
	metricQuery := parseMetricPathParameter(request)
	return dataselect.NewDataSelectQuery(paginationQuery, sortQuery, filterQuery, metricQuery)
}
