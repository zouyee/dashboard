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

package common

import api "k8s.io/client-go/pkg/api/v1"

// PodInfo represents aggregate information about controller's pods.
type PodInfo struct {
	// Number of pods that are created.
	Current int32 `json:"current"`

	// Number of pods that are desired.
	Desired int32 `json:"desired"`

	// Number of pods that are currently running.
	Running int32 `json:"running"`

	// Number of pods that are currently waiting.
	Pending int32 `json:"pending"`

	// Number of pods that are failed.
	Failed int32 `json:"failed"`

	// Number of pods that are succeeded.
	Succeeded int32 `json:"succeeded"`

	// Unique warning messages related to pods in this resource.
	Warnings []Event `json:"warnings"`
}

// GetPodInfo returns aggregate information about a group of pods.
func GetPodEventInfo(current int32, desired int32, pods []api.Pod, eve []Event) PodInfo {
	result := PodInfo{
		Current:  current,
		Desired:  desired,
		Warnings: make([]Event, 0),
	}

	for _, pod := range pods {
		pod.Status.Phase = getPodPhaseStatus(pod, eve)
		switch pod.Status.Phase {
		case api.PodRunning:
			result.Running++
		case api.PodPending:
			result.Pending++
		case api.PodFailed:
			result.Failed++
		case api.PodSucceeded:
			result.Succeeded++
		}
	}
	result.Warnings = eve
	return result
}

func GetPodInfo(current int32, desired int32, pods []api.Pod) PodInfo {
	result := PodInfo{
		Current:  current,
		Desired:  desired,
		Warnings: make([]Event, 0),
	}

	for _, pod := range pods {
		switch pod.Status.Phase {
		case api.PodRunning:
			result.Running++
		case api.PodPending:
			result.Pending++
		case api.PodFailed:
			result.Failed++
		case api.PodSucceeded:
			result.Succeeded++
		}
	}
	return result
}

// GetPodPhaseStatus
func getPodPhaseStatus(pod api.Pod, warnings []Event) api.PodPhase {
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
