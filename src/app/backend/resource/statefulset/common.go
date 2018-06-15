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

package statefulset

import (
	"github.com/kubernetes/dashboard/src/app/backend/resource/common"
	"github.com/kubernetes/dashboard/src/app/backend/resource/dataselect"
	"github.com/kubernetes/dashboard/src/app/backend/resource/metric"
	apps "k8s.io/client-go/pkg/apis/apps/v1beta1"
)

// The code below allows to perform complex data section on []apps.StatefulSet

type StatefulSetCell apps.StatefulSet

func (self StatefulSetCell) GetProperty(name dataselect.PropertyName) dataselect.ComparableValue {
	switch name {
	case dataselect.NameProperty:
		return dataselect.StdComparableString(self.ObjectMeta.Name)
	case dataselect.CreationTimestampProperty:
		return dataselect.StdComparableTime(self.ObjectMeta.CreationTimestamp.Time)
	case dataselect.NamespaceProperty:
		return dataselect.StdComparableString(self.ObjectMeta.Namespace)
	default:
		// if name is not supported then just return a constant dummy value, sort will have no effect.
		return nil
	}
}

func (self StatefulSetCell) GetResourceSelector() *metric.ResourceSelector {
	return &metric.ResourceSelector{
		Namespace:    self.ObjectMeta.Namespace,
		ResourceType: common.ResourceKindStatefulSet,
		ResourceName: self.ObjectMeta.Name,
		Selector:     self.Spec.Selector.MatchLabels,
	}
}

func ToCells(std []apps.StatefulSet) []dataselect.DataCell {
	cells := make([]dataselect.DataCell, len(std))
	for i := range std {
		cells[i] = StatefulSetCell(std[i])
	}
	return cells
}

func FromCells(cells []dataselect.DataCell) []apps.StatefulSet {
	std := make([]apps.StatefulSet, len(cells))
	for i := range std {
		std[i] = apps.StatefulSet(cells[i].(StatefulSetCell))
	}
	return std
}
