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

package deployment

import (
	"log"

	heapster "gerrit.cmss.com/BC-PaaS/backend/src/app/backend/client"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/dataselect"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/horizontalpodautoscaler"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/pod"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/replicaset"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
	client "k8s.io/client-go/kubernetes"
	extensions "k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

// RollingUpdateStrategy is behavior of a rolling update. See RollingUpdateDeployment K8s object.
type RollingUpdateStrategy struct {
	MaxSurge       *intstr.IntOrString `json:"maxSurge"`
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable"`
}

type StatusInfo struct {
	// Total number of desired replicas on the deployment
	Replicas int32 `json:"replicas"`

	// Number of non-terminated pods that have the desired template spec
	Updated int32 `json:"updated"`

	// Number of available pods (ready for at least minReadySeconds)
	// targeted by this deployment
	Available int32 `json:"available"`

	// Total number of unavailable pods targeted by this deployment.
	Unavailable int32 `json:"unavailable"`
}

// DeploymentDetail is a presentation layer view of Kubernetes Deployment resource.
type DeploymentDetail struct {
	ObjectMeta common.ObjectMeta `json:"objectMeta"`
	TypeMeta   common.TypeMeta   `json:"typeMeta"`

	// Detailed information about Pods belonging to this Deployment.
	PodList pod.PodList `json:"podList"`

	// Label selector of the service.
	Selector map[string]string `json:"selector"`

	// Status information on the deployment
	StatusInfo `json:"statusInfo"`

	// The deployment strategy to use to replace existing pods with new ones.
	// Valid options: Recreate, RollingUpdate
	Strategy extensions.DeploymentStrategyType `json:"strategy"`

	// Min ready seconds
	MinReadySeconds int32 `json:"minReadySeconds"`

	// Rolling update strategy containing maxSurge and maxUnavailable
	RollingUpdateStrategy *RollingUpdateStrategy `json:"rollingUpdateStrategy,omitempty"`

	// RepliaSetList containing old replica sets from the deployment
	OldReplicaSetList replicaset.ReplicaSetList `json:"oldReplicaSetList"`

	// New replica set used by this deployment
	NewReplicaSet replicaset.ReplicaSet `json:"newReplicaSet"`

	// Optional field that specifies the number of old Replica Sets to retain to allow rollback.
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit"`

	// List of events related to this Deployment
	EventList common.EventList `json:"eventList"`

	// List of Horizontal Pod AutoScalers targeting this Deployment
	HorizontalPodAutoscalerList horizontalpodautoscaler.HorizontalPodAutoscalerList `json:"horizontalPodAutoscalerList"`
}

// GetDeploymentDetail returns model object of deployment and error, if any.
func GetDeploymentDetail(client client.Interface, heapsterClient heapster.HeapsterClient, namespace string,
	deploymentName string) (*DeploymentDetail, error) {

	log.Printf("Getting details of %s deployment in %s namespace", deploymentName, namespace)

	deployment, err := client.ExtensionsV1beta1().Deployments(namespace).Get(deploymentName, metaV1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// TODO fix not to use kubernetes utils
	selector, err := metaV1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metaV1.ListOptions{LabelSelector: selector.String()}

	channels := &common.ResourceChannels{
		ReplicaSetList: common.GetReplicaSetListChannelWithOptions(client,
			common.NewSameNamespaceQuery(namespace), options, 1),
		PodList: common.GetPodListChannelWithOptions(client,
			common.NewSameNamespaceQuery(namespace), options, 1),
	}

	rawRs := <-channels.ReplicaSetList.List
	if err := <-channels.ReplicaSetList.Error; err != nil {
		return nil, err
	}
	rawPods := <-channels.PodList.List
	if err := <-channels.PodList.Error; err != nil {
		return nil, err
	}

	// Pods
	podList, err := GetDeploymentPods(client, heapsterClient, dataselect.DefaultDataSelectWithMetrics, namespace, deploymentName)
	if err != nil {
		return nil, err
	}
	// Events
	eventList, err := GetDeploymentEvents(client, dataselect.DefaultDataSelect, namespace, deploymentName)
	if err != nil {
		return nil, err
	}
	// Horizontal Pod Autoscalers
	hpas, err := horizontalpodautoscaler.GetHorizontalPodAutoscalerListForResource(client, namespace, "Deployment", deploymentName)
	if err != nil {
		return nil, err
	}

	// Old Replica Sets
	oldReplicaSetList, err := GetDeploymentOldReplicaSets(client, dataselect.DefaultDataSelect, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	// New Replica Set
	rawRepSets := make([]*extensions.ReplicaSet, 0)
	for i := range rawRs.Items {
		rawRepSets = append(rawRepSets, &rawRs.Items[i])
	}

	newRs, err := FindNewReplicaSet(deployment, rawRepSets)
	if err != nil {
		return nil, err
	}
	log.Printf(" newRs is %#v, the value is %#v", newRs, newRs != nil)
	var newReplicaSet replicaset.ReplicaSet
	if newRs != nil {
		newRsPodInfo := common.GetPodInfo(newRs.Status.Replicas, *newRs.Spec.Replicas, rawPods.Items)
		log.Printf("pod warning is %#v, pod warning lenth is %#v", newRsPodInfo.Warnings, len(newRsPodInfo.Warnings))
		newReplicaSet = replicaset.ToReplicaSet(newRs, &newRsPodInfo)
	}

	// Extra Info
	var rollingUpdateStrategy *RollingUpdateStrategy
	if deployment.Spec.Strategy.RollingUpdate != nil {
		rollingUpdateStrategy = &RollingUpdateStrategy{
			MaxSurge:       deployment.Spec.Strategy.RollingUpdate.MaxSurge,
			MaxUnavailable: deployment.Spec.Strategy.RollingUpdate.MaxUnavailable,
		}
	}

	return &DeploymentDetail{
		ObjectMeta:                  common.NewObjectMeta(deployment.ObjectMeta),
		TypeMeta:                    common.NewTypeMeta(common.ResourceKindDeployment),
		PodList:                     *podList,
		Selector:                    deployment.Spec.Selector.MatchLabels,
		StatusInfo:                  GetStatusInfo(&deployment.Status),
		Strategy:                    deployment.Spec.Strategy.Type,
		MinReadySeconds:             deployment.Spec.MinReadySeconds,
		RollingUpdateStrategy:       rollingUpdateStrategy,
		OldReplicaSetList:           *oldReplicaSetList,
		NewReplicaSet:               newReplicaSet,
		RevisionHistoryLimit:        deployment.Spec.RevisionHistoryLimit,
		EventList:                   *eventList,
		HorizontalPodAutoscalerList: *hpas,
	}, nil

}

func GetStatusInfo(deploymentStatus *extensions.DeploymentStatus) StatusInfo {
	return StatusInfo{
		Replicas:    deploymentStatus.Replicas,
		Updated:     deploymentStatus.UpdatedReplicas,
		Available:   deploymentStatus.AvailableReplicas,
		Unavailable: deploymentStatus.UnavailableReplicas,
	}
}
