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

package storageclass

import (
	"reflect"
	"testing"

	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	storage "k8s.io/client-go/pkg/apis/storage/v1beta1"
)

func TestToStorageClass(t *testing.T) {
	cases := []struct {
		storage  *storage.StorageClass
		expected StorageClass
	}{
		{
			storage: &storage.StorageClass{},
			expected: StorageClass{
				TypeMeta: common.TypeMeta{Kind: common.ResourceKindStorageClass},
			},
		}, {
			storage: &storage.StorageClass{
				ObjectMeta: metaV1.ObjectMeta{Name: "test-storage"}},
			expected: StorageClass{
				ObjectMeta: common.ObjectMeta{Name: "test-storage"},
				TypeMeta:   common.TypeMeta{Kind: common.ResourceKindStorageClass},
			},
		},
	}

	for _, c := range cases {
		actual := ToStorageClass(c.storage)

		if !reflect.DeepEqual(actual, c.expected) {
			t.Errorf("ToStorageClass(%#v) == \ngot %#v, \nexpected %#v", c.storage, actual,
				c.expected)
		}
	}
}
