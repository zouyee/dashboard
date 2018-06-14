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

package cronjob

import (
	"encoding/json"
	"fmt"
	"strings"

	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/client"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/dataselect"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/job"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/pod"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sClient "k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/pkg/api/v1"
	batchv2alpha1 "k8s.io/client-go/pkg/apis/batch/v2alpha1"
)

// CronJobDetail is a presentation layer view of Kubernetes Job resource. This means
// it is Job plus additional augmented data we can get from other sources
// (like services that target the same pods).
type CronJobDetail struct {
	ObjectMeta common.ObjectMeta `json:"objectMeta"`
	TypeMeta   common.TypeMeta   `json:"typeMeta"`

	// Aggregate information about pods belonging to this Job.
	PodInfo common.PodInfo `json:"podInfo"`

	// Detailed information about Pods belonging to this Job.
	PodList pod.PodList `json:"podList"`

	// Container images of the Job.
	ContainerImages []string `json:"containerImages"`

	// List of events related to this Job.
	EventList common.EventList `json:"eventList"`

	Spec   batchv2alpha1.CronJobSpec   `json:"spec"`
	Status batchv2alpha1.CronJobStatus `json:"status"`
}

// GetCronJobDetail gets cronjob details.
func GetCronJobDetail(client k8sClient.Interface, heapsterClient *client.HeapsterClient,
	namespace, name string) (*CronJobDetail, error) {

	cronjob, err := client.BatchV2alpha1().CronJobs(namespace).Get(name, metaV1.GetOptions{})

	if err != nil {
		return nil, err
	}
	namespaces := strings.Split(namespace, ",")
	var nonEmptyNamespaces []string
	for _, n := range namespaces {
		n = strings.Trim(n, " ")
		if len(n) > 0 {
			nonEmptyNamespaces = append(nonEmptyNamespaces, n)
		}
	}
	nsQuery := common.NewNamespaceQuery(nonEmptyNamespaces)

	channels := &common.ResourceChannels{
		JobList:   common.GetJobListChannel(client, nsQuery, 1),
		PodList:   common.GetPodListChannel(client, nsQuery, 1),
		EventList: common.GetEventListChannel(client, nsQuery, 1),
	}
	fmt.Print(1)
	jobs := <-channels.JobList.List
	err = <-channels.JobList.Error
	if err != nil {
		return nil, err
	}
	fmt.Print(2)
	pods := <-channels.PodList.List
	if err := <-channels.PodList.Error; err != nil {
		return nil, err
	}
	fmt.Print(3)

	events := <-channels.EventList.List
	if err := <-channels.EventList.Error; err != nil {
		return nil, err
	}
	fmt.Print(4)
	joblists := job.CreateJobList(jobs.Items, pods.Items, events.Items, dataselect.DefaultDataSelect, heapsterClient)

	matchingJob := FilterJobByAnnotationc(*cronjob, joblists.Jobs)

	cron := toCronJobDetail(cronjob, matchingJob)

	cron.PodList.ListMeta.TotalItems = len(cron.PodList.Pods)
	return cron, nil

}

func toCronJobDetail(cronjob *batchv2alpha1.CronJob, jobs []job.Job) *CronJobDetail {

	cron := &CronJobDetail{
		ObjectMeta:      common.NewObjectMeta(cronjob.ObjectMeta),
		TypeMeta:        common.NewTypeMeta(common.ResourceKindJob),
		Spec:            cronjob.Spec,
		ContainerImages: common.GetContainerImages(&cronjob.Spec.JobTemplate.Spec.Template.Spec),
		Status:          cronjob.Status,
	}

	cron.Spec = cronjob.Spec
	cron.Status = cronjob.Status
	cron.PodList = pod.PodList{
		Pods: make([]pod.Pod, 0),
	}

	cron.PodInfo.Warnings = make([]common.Event, 0)
	for _, job := range jobs {
		// will fix plus action
		cron.PodInfo.Current = job.Pods.Succeeded + cron.PodInfo.Current
		cron.PodInfo.Desired = job.Pods.Desired + cron.PodInfo.Desired
		cron.PodInfo.Running = job.Pods.Running + cron.PodInfo.Running
		cron.PodInfo.Pending = job.Pods.Pending + cron.PodInfo.Pending
		cron.PodInfo.Failed = job.Pods.Failed + cron.PodInfo.Failed
		if job.Pods.Warnings == nil {
			break
		}
		cron.PodInfo.Warnings = append(cron.PodInfo.Warnings, job.Pods.Warnings...)
		cron.PodList.Pods = append(cron.PodList.Pods, job.PodList.Pods...)
	}
	if cron.PodInfo.Desired < int32(len(cron.PodList.Pods)) {
		cron.PodInfo.Desired = int32(len(cron.PodList.Pods))
	}
	cron.PodList.ListMeta.TotalItems = len(cron.PodList.Pods)

	return cron
}

func extractCreatedByc(annotation map[string]string) *api.ObjectReference {

	value, ok := annotation[api.CreatedByAnnotation]
	if ok {
		var r api.SerializedReference
		err := json.Unmarshal([]byte(value), &r)
		if err == nil {
			return &r.Reference
		}
	}
	return nil
}

func FilterJobByAnnotationc(cronJob batchv2alpha1.CronJob, jobs []job.Job) []job.Job {
	var matchingJobs []job.Job

	for _, job := range jobs {

		if extractCreatedByc(job.ObjectMeta.Annotations) == nil {
			continue
		}
		if extractCreatedByc(job.ObjectMeta.Annotations).Name == cronJob.Name &&
			cronJob.ObjectMeta.Namespace == job.ObjectMeta.Namespace {
			matchingJobs = append(matchingJobs, job)

		}
	}

	return matchingJobs
}
