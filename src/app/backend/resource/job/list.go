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

package job

import (
	"fmt"
	"log"

	heapster "github.com/kubernetes/dashboard/src/app/backend/client"
	"github.com/kubernetes/dashboard/src/app/backend/resource/common"
	"github.com/kubernetes/dashboard/src/app/backend/resource/dataselect"
	"github.com/kubernetes/dashboard/src/app/backend/resource/event"
	"github.com/kubernetes/dashboard/src/app/backend/resource/metric"
	"github.com/kubernetes/dashboard/src/app/backend/resource/pod"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	client "k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/pkg/api/v1"
	batch "k8s.io/client-go/pkg/apis/batch/v1"
)

// JobList contains a list of Jobs in the cluster.
type JobList struct {
	ListMeta common.ListMeta `json:"listMeta"`

	// Unordered list of Jobs.
	Jobs              []Job           `json:"jobs"`
	CumulativeMetrics []metric.Metric `json:"cumulativeMetrics"`
}

// Job is a presentation layer view of Kubernetes Job resource. This means it is Job plus additional
// augmented data we can get from other sources
type Job struct {
	ObjectMeta common.ObjectMeta `json:"objectMeta"`
	TypeMeta   common.TypeMeta   `json:"typeMeta"`

	// Aggregate information about pods belonging to this Job.
	Pods common.PodInfo `json:"pods"`

	// Detailed information about Pods belonging to this Deployment.
	PodList pod.PodList `json:"podList"`

	// Container images of the Job.
	ContainerImages []string `json:"containerImages"`
}

// GetJobList returns a list of all Jobs in the cluster.
func GetJobList(client client.Interface, nsQuery *common.NamespaceQuery,
	dsQuery *dataselect.DataSelectQuery, heapsterClient *heapster.HeapsterClient) (*JobList, error) {
	log.Print("Getting list of all jobs in the cluster")

	channels := &common.ResourceChannels{
		JobList:   common.GetJobListChannel(client, nsQuery, 1),
		PodList:   common.GetPodListChannel(client, nsQuery, 1),
		EventList: common.GetEventListChannel(client, nsQuery, 1),
	}

	return GetJobListFromChannels(channels, dsQuery, heapsterClient)
}

// GetJobList returns a list of all Jobs in the cluster reading required resource list once from the channels.
func GetJobListFromChannels(channels *common.ResourceChannels, dsQuery *dataselect.DataSelectQuery, heapsterClient *heapster.HeapsterClient) (
	*JobList, error) {

	jobs := <-channels.JobList.List
	if err := <-channels.JobList.Error; err != nil {
		statusErr, ok := err.(*k8serrors.StatusError)
		if ok && statusErr.ErrStatus.Reason == "NotFound" {
			// NotFound - this means that the server does not support Job objects, which
			// is fine.
			emptyList := &JobList{
				Jobs: make([]Job, 0),
			}
			return emptyList, nil
		}
		return nil, err
	}

	pods := <-channels.PodList.List
	if err := <-channels.PodList.Error; err != nil {
		return nil, err
	}

	events := <-channels.EventList.List
	if err := <-channels.EventList.Error; err != nil {
		return nil, err
	}

	return CreateJobList(jobs.Items, pods.Items, events.Items, dsQuery, heapsterClient), nil
}

// CreateJobList returns a list of all Job model objects in the cluster, based on all
// Kubernetes Job API objects.
func CreateJobList(jobs []batch.Job, pods []api.Pod, events []api.Event,
	dsQuery *dataselect.DataSelectQuery, heapsterClient *heapster.HeapsterClient) *JobList {

	jobList := &JobList{
		Jobs:     make([]Job, 0),
		ListMeta: common.ListMeta{TotalItems: len(jobs)},
	}

	cachedResources := &dataselect.CachedResources{
		Pods: pods,
	}
	jobCells, metricPromises := dataselect.GenericDataSelectWithMetrics(ToCells(jobs), dsQuery, cachedResources, heapsterClient)
	jobs = FromCells(jobCells)

	for _, job := range jobs {
		var completions int32
		matchingPods := common.FilterNamespacedPodsBySelector(pods, job.ObjectMeta.Namespace, job.Spec.Selector.MatchLabels)
		if job.Spec.Completions != nil {
			completions = *job.Spec.Completions
		}
		podInfo := common.GetPodEventInfo(job.Status.Active, completions, matchingPods, event.GetPodsEventWarnings(events, matchingPods))
		podList, err := getJobPods(job, *heapsterClient, dataselect.DefaultDataSelectWithMetrics, pods)
		if err != nil {
			fmt.Printf("getdeploymentpods err is %#v", err)
		}

		jobList.Jobs = append(jobList.Jobs, ToJob(&job, &podInfo, podList))
	}

	cumulativeMetrics, err := metricPromises.GetMetrics()
	jobList.CumulativeMetrics = cumulativeMetrics
	if err != nil {
		jobList.CumulativeMetrics = make([]metric.Metric, 0)
	}

	return jobList
}

func ToJob(job *batch.Job, podInfo *common.PodInfo, podlist *pod.PodList) Job {
	return Job{
		ObjectMeta:      common.NewObjectMeta(job.ObjectMeta),
		TypeMeta:        common.NewTypeMeta(common.ResourceKindJob),
		ContainerImages: common.GetContainerImages(&job.Spec.Template.Spec),
		Pods:            *podInfo,
		PodList:         *podlist,
	}
}

// getDeploymentPods returns list of pods targeting deployment.
func getJobPods(jb batch.Job, heapsterClient heapster.HeapsterClient,
	dsQuery *dataselect.DataSelectQuery, podlist []api.Pod) (*pod.PodList, error) {
	fmt.Println("monitor getDeploymentPods pods before")
	pods := common.FilterNamespacedPodsBySelector(podlist, jb.ObjectMeta.Namespace,
		jb.Spec.Selector.MatchLabels)
	fmt.Println("monitor getDeploymentPods pods after")
	podList := pod.CreatePodList(pods, []api.Event{}, dsQuery, heapsterClient)
	return &podList, nil
}
