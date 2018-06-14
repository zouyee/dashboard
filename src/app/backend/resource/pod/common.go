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

package pod

import (
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/dataselect"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/metric"
	api "k8s.io/client-go/pkg/api/v1"
)

// Gets restart count of given pod (total number of its containers restarts).
func getRestartCount(pod api.Pod) int32 {
	var restartCount int32 = 0
	for _, containerStatus := range pod.Status.ContainerStatuses {
		restartCount += containerStatus.RestartCount
	}
	return restartCount
}

// getPodStatus returns a PodStatus object containing a summary of the pod's status.
func getPodStatus(pod api.Pod, warnings []common.Event) PodStatus {
	var states []api.ContainerState
	for _, containerStatus := range pod.Status.ContainerStatuses {
		states = append(states, containerStatus.State)
	}

	return PodStatus{
		Status:          getPodStatusStatus(pod, warnings),
		PodPhase:        pod.Status.Phase,
		HostIP:          pod.Status.HostIP,
		ContainerStates: states,
	}
}

// getPodStatus returns one of three pod statuses (pending, success, failed)
func getPodStatusStatus(pod api.Pod, warnings []common.Event) string {
	// For terminated pods that failed
	if pod.Status.Phase == api.PodFailed {
		return "failed"
	}

	// For successfully terminated pods
	if pod.Status.Phase == api.PodSucceeded {
		return "success"
	}

	ready := false
	initialized := false
	for _, c := range pod.Status.Conditions {
		if c.Type == api.PodReady {
			ready = c.Status == api.ConditionTrue
		}
		if c.Type == api.PodInitialized {
			initialized = c.Status == api.ConditionTrue
		}
	}

	if initialized && ready {
		return "success"
	}

	// If the pod would otherwise be pending but has warning then label it as
	// failed and show and error to the user.
	if len(warnings) > 0 {
		return "failed"
	}

	// Unknown?
	return "pending"
}

// GetPodPhaseStatus
func GetPodPhaseStatus(pod api.Pod, warnings []common.Event) api.PodPhase {
	// For terminated pods that failed
	if pod.Status.Phase == api.PodFailed {
		return api.PodFailed
	}

	// For successfully terminated pods
	if pod.Status.Phase == api.PodSucceeded {
		return api.PodSucceeded
	}

	ready := false
	initialized := false
	for _, c := range pod.Status.Conditions {
		if c.Type == api.PodReady {
			ready = c.Status == api.ConditionTrue
		}
		if c.Type == api.PodInitialized {
			initialized = c.Status == api.ConditionTrue
		}
	}

	if initialized && ready {
		return api.PodRunning
	}

	// If the pod would otherwise be pending but has warning then label it as
	// failed and show and error to the user.
	if len(warnings) > 0 {
		return api.PodFailed
	}

	// Unknown?
	return api.PodPending
}

// ToPod transforms Kubernetes pod object into object returned by API.
func ToPod(pod *api.Pod, metrics *common.MetricsByPod, warnings []common.Event) Pod {
	podDetail := Pod{
		ObjectMeta:   common.NewObjectMeta(pod.ObjectMeta),
		TypeMeta:     common.NewTypeMeta(common.ResourceKindPod),
		Spec:         pod.Spec,
		PodStatus:    getPodStatus(*pod, warnings),
		RestartCount: getRestartCount(*pod),
	}
	//log.Printf("~~~~~~~~~~~~~~~~~pod is %#v, metrics is %v", podDetail, metrics)
	if metrics != nil && metrics.MetricsMap[pod.Namespace] != nil {
		metric := metrics.MetricsMap[pod.Namespace][pod.Name]
		podDetail.Metrics = &metric
	}

	return podDetail
}

// GetContainerImages returns container image strings from the given pod spec.
func GetContainerImages(podTemplate *api.PodSpec) []string {
	var containerImages []string
	for _, container := range podTemplate.Containers {
		containerImages = append(containerImages, container.Image)
	}
	return containerImages
}

// The code below allows to perform complex data section on []api.Pod

type PodCell api.Pod

func (self PodCell) GetProperty(name dataselect.PropertyName) dataselect.ComparableValue {
	switch name {
	case dataselect.NameProperty:
		return dataselect.StdComparableString(self.ObjectMeta.Name)
	case dataselect.CreationTimestampProperty:
		return dataselect.StdComparableTime(self.ObjectMeta.CreationTimestamp.Time)
	case dataselect.NamespaceProperty:
		return dataselect.StdComparableString(self.ObjectMeta.Namespace)
	case dataselect.StatusProperty:
		return dataselect.StdComparableString(self.Status.Phase)
	default:
		// if name is not supported then just return a constant dummy value, sort will have no effect.
		return nil
	}
}

func (self PodCell) GetResourceSelector() *metric.ResourceSelector {
	return &metric.ResourceSelector{
		Namespace:    self.ObjectMeta.Namespace,
		ResourceType: common.ResourceKindPod,
		ResourceName: self.ObjectMeta.Name,
	}
}

func toCells(std []api.Pod) []dataselect.DataCell {
	cells := make([]dataselect.DataCell, len(std))
	for i := range std {
		cells[i] = PodCell(std[i])
	}
	return cells
}

func fromCells(cells []dataselect.DataCell) []api.Pod {
	std := make([]api.Pod, len(cells))
	for i := range std {
		std[i] = api.Pod(cells[i].(PodCell))
	}
	return std
}

func getPodConditions(pod api.Pod) []common.Condition {
	var conditions []common.Condition
	for _, condition := range pod.Status.Conditions {
		conditions = append(conditions, common.Condition{
			Type:               string(condition.Type),
			Status:             condition.Status,
			LastProbeTime:      condition.LastProbeTime,
			LastTransitionTime: condition.LastTransitionTime,
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return conditions
}
