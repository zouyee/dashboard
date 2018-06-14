// Copyright 2017 The Kubernetes Dashboard Authors.
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
	"log"

	heapster "gerrit.cmss.com/BC-PaaS/backend/src/app/backend/client"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/dataselect"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/job"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/metric"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/pod"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	client "k8s.io/client-go/kubernetes"
	api "k8s.io/client-go/pkg/api/v1"
	batch2 "k8s.io/client-go/pkg/apis/batch/v2alpha1"
)

// CronJobList contains a list of CronJobs in the cluster.
type CronJobList struct {
	ListMeta          common.ListMeta `json:"listMeta"`
	CumulativeMetrics []metric.Metric `json:"cumulativeMetrics"`

	// Unordered list of CronJobs.
	CronJobs []CronJob `json:"cronJobs"`

	// List of non-critical errors, that occurred during resource retrieval.
	Errors []error `json:"errors"`
}

// CronJob is a presentation layer view of Kubernetes CronJob resource. This means it is CronJob plus additional
// augmented data we can get from other sources
type CronJob struct {
	ObjectMeta common.ObjectMeta  `json:"objectMeta"`
	TypeMeta   common.TypeMeta    `json:"typeMeta"`
	Spec       batch2.CronJobSpec `json:"sepc"`
	// Aggregate information about pods belonging to this CronJob.
	Pods common.PodInfo `json:"pods"`

	// Detailed information about Pods belonging to this Deployment.
	PodList pod.PodList          `json:"podList"`
	Status  batch2.CronJobStatus `json:"status"`
}

// GetCronJobList returns a list of all CronJobs in the cluster.
func GetCronJobList(client client.Interface, nsQuery *common.NamespaceQuery,
	dsQuery *dataselect.DataSelectQuery, heapsterClient *heapster.HeapsterClient) (*CronJobList, error) {
	log.Print("Getting list of all cronJobs in the cluster")

	channels := &common.ResourceChannels{
		CronJobList: common.GetCronJobListChannel(client, nsQuery, 1),
		JobList:     common.GetJobListChannel(client, nsQuery, 1),
		PodList:     common.GetPodListChannel(client, nsQuery, 1),
		EventList:   common.GetEventListChannel(client, nsQuery, 1),
	}

	return GetCronJobListFromChannels(channels, dsQuery, heapsterClient)
}

// GetCronJobListFromChannels returns a list of all CronJobs in the cluster reading required resource
// list once from the channels.
func GetCronJobListFromChannels(channels *common.ResourceChannels, dsQuery *dataselect.DataSelectQuery,
	heapsterClient *heapster.HeapsterClient) (*CronJobList, error) {

	cronJobs := <-channels.CronJobList.List
	err := <-channels.CronJobList.Error
	if err != nil {
		statusErr, ok := err.(*k8serrors.StatusError)
		if ok && statusErr.ErrStatus.Reason == "NotFound" {
			// NotFound - this means that the server does not support Job objects, which
			// is fine.
			emptyList := &CronJobList{
				CronJobs: make([]CronJob, 0),
			}
			return emptyList, nil
		}
		return nil, err
	}

	jobs := <-channels.JobList.List
	err = <-channels.JobList.Error
	if err != nil {
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

	joblists := job.CreateJobList(jobs.Items, pods.Items, events.Items, dsQuery, heapsterClient)
	return toCronJobList(cronJobs.Items, joblists, dsQuery, heapsterClient), nil
}

func toCronJobList(cronJobs []batch2.CronJob, jobs *job.JobList, dsQuery *dataselect.DataSelectQuery,
	heapsterClient *heapster.HeapsterClient) *CronJobList {

	cronJobList := &CronJobList{
		CronJobs: make([]CronJob, 0),
		ListMeta: common.ListMeta{TotalItems: len(cronJobs)},
	}

	cronJobList.ListMeta = common.ListMeta{TotalItems: len(cronJobs)}

	for _, cronJob := range cronJobs {
		matchingJob := FilterJobByAnnotation(cronJob, jobs.Jobs)

		cronJobList.CronJobs = append(cronJobList.CronJobs, toCronJob(&cronJob, matchingJob))
	}

	cronJobList.CumulativeMetrics = make([]metric.Metric, 0)

	log.Print("5'Getting list of all cronJobs in the cluster")
	return cronJobList
}

func toCronJob(cronJob *batch2.CronJob, jobs []job.Job) CronJob {

	cron := CronJob{
		ObjectMeta: common.NewObjectMeta(cronJob.ObjectMeta),
		TypeMeta:   common.NewTypeMeta(common.ResourceKindCronJob),
	}
	cron.Spec = cronJob.Spec
	cron.Status = cronJob.Status
	cron.PodList = pod.PodList{
		Pods: make([]pod.Pod, 0),
	}
	cron.Pods.Warnings = make([]common.Event, 0)
	for _, job := range jobs {
		// will fix plus action
		log.Printf("job %s, pod status %#v\n", job.ObjectMeta.Name, job.Pods)
		cron.Pods.Current = job.Pods.Succeeded + cron.Pods.Current
		cron.Pods.Desired = job.Pods.Desired + cron.Pods.Desired
		cron.Pods.Running = job.Pods.Running + cron.Pods.Running
		cron.Pods.Pending = job.Pods.Pending + cron.Pods.Pending
		cron.Pods.Failed = job.Pods.Failed + cron.Pods.Failed
		if job.Pods.Warnings != nil {
			cron.Pods.Warnings = append(cron.Pods.Warnings, job.Pods.Warnings...)
		}

		cron.PodList.Pods = append(cron.PodList.Pods, job.PodList.Pods...)
	}
	if cron.Pods.Desired < int32(len(cron.PodList.Pods)) {
		cron.Pods.Desired = int32(len(cron.PodList.Pods))
	}
	cron.PodList.ListMeta.TotalItems = len(cron.PodList.Pods)
	log.Printf("cronjob %s, cronjob status %#v\n", cron.ObjectMeta.Name, cron.Pods)
	return cron
}

func extractCreatedBy(annotation map[string]string) *api.ObjectReference {

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

func FilterJobByAnnotation(cronJob batch2.CronJob, jobs []job.Job) []job.Job {
	var matchingJobs []job.Job

	for _, job := range jobs {

		if extractCreatedBy(job.ObjectMeta.Annotations) == nil {
			continue
		}
		if extractCreatedBy(job.ObjectMeta.Annotations).Name == cronJob.Name &&
			cronJob.ObjectMeta.Namespace == job.ObjectMeta.Namespace {
			matchingJobs = append(matchingJobs, job)

		}
	}

	return matchingJobs
}
