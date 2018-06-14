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

package cluster

import (
	"log"

	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/dataselect"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/namespace"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/node"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/persistentvolume"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/rbacroles"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/storageclass"
	"k8s.io/client-go/kubernetes"
)

// Cluster structure contains all resource lists grouped into the cluster category.
type Cluster struct {
	NamespaceList        namespace.NamespaceList               `json:"namespaceList"`
	NodeList             node.NodeList                         `json:"nodeList"`
	PersistentVolumeList persistentvolume.PersistentVolumeList `json:"persistentVolumeList"`
	RoleList             rbacroles.RbacRoleList                `json:"roleList"`
	StorageClassList     storageclass.StorageClassList         `json:"storageClassList"`
}

// GetCluster returns a list of all cluster resources in the cluster.
func GetCluster(client *kubernetes.Clientset) (*Cluster, error) {
	log.Print("Getting cluster category")
	channels := &common.ResourceChannels{
		NamespaceList:        common.GetNamespaceListChannel(client, 1),
		NodeList:             common.GetNodeListChannel(client, 1),
		PersistentVolumeList: common.GetPersistentVolumeListChannel(client, 1),
		RoleList:             common.GetRoleListChannel(client, 1),
		ClusterRoleList:      common.GetClusterRoleListChannel(client, 1),
		StorageClassList:     common.GetStorageClassListChannel(client, 1),
	}

	return GetClusterFromChannels(channels)
}

// GetClusterFromChannels returns a list of all cluster in the cluster, from the
// channel sources.
func GetClusterFromChannels(channels *common.ResourceChannels) (*Cluster, error) {

	nsChan := make(chan *namespace.NamespaceList)
	nodeChan := make(chan *node.NodeList)
	pvChan := make(chan *persistentvolume.PersistentVolumeList)
	roleChan := make(chan *rbacroles.RbacRoleList)
	storageChan := make(chan *storageclass.StorageClassList)
	numErrs := 4
	errChan := make(chan error, numErrs)

	go func() {
		items, err := namespace.GetNamespaceListFromChannels(channels,
			dataselect.DefaultDataSelect)
		errChan <- err
		nsChan <- items
	}()

	go func() {
		items, err := node.GetNodeListFromChannels(channels, dataselect.DefaultDataSelect, nil)
		errChan <- err
		nodeChan <- items
	}()

	go func() {
		items, err := persistentvolume.GetPersistentVolumeListFromChannels(channels,
			dataselect.DefaultDataSelect)
		errChan <- err
		pvChan <- items
	}()

	go func() {
		items, err := rbacroles.GetRbacRoleListFromChannels(channels,
			dataselect.DefaultDataSelect)
		errChan <- err
		roleChan <- items
	}()

	go func() {
		items, err := storageclass.GetStorageClassListFromChannels(channels,
			dataselect.DefaultDataSelect)
		errChan <- err
		storageChan <- items
	}()

	for i := 0; i < numErrs; i++ {
		err := <-errChan
		if err != nil {
			return nil, err
		}
	}

	cluster := &Cluster{
		NamespaceList:        *(<-nsChan),
		NodeList:             *(<-nodeChan),
		PersistentVolumeList: *(<-pvChan),
		RoleList:             *(<-roleChan),
		StorageClassList:     *(<-storageChan),
	}

	return cluster, nil
}
