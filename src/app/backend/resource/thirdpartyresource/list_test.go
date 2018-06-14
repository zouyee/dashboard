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

package thirdpartyresource

import (
	"reflect"
	"testing"

	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/common"
	"gerrit.cmss.com/BC-PaaS/backend/src/app/backend/resource/dataselect"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	extensions "k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

func TestGetThirdPartyResourceList(t *testing.T) {
	cases := []struct {
		thirdPartyResources []extensions.ThirdPartyResource
		expected            *ThirdPartyResourceList
	}{
		{nil, &ThirdPartyResourceList{ThirdPartyResources: []ThirdPartyResource{}}},
		{
			[]extensions.ThirdPartyResource{
				{
					ObjectMeta:  metaV1.ObjectMeta{Name: "foo"},
					Description: "test",
					Versions: []extensions.APIVersion{
						{
							Name: "v1",
						},
					},
				},
			},
			&ThirdPartyResourceList{
				ListMeta: common.ListMeta{TotalItems: 1},
				ThirdPartyResources: []ThirdPartyResource{{
					TypeMeta:   common.TypeMeta{Kind: common.ResourceKindThirdPartyResource},
					ObjectMeta: common.ObjectMeta{Name: "foo"},
				}},
			},
		},
	}
	for _, c := range cases {
		actual := getThirdPartyResourceList(c.thirdPartyResources, dataselect.NoDataSelect)
		if !reflect.DeepEqual(actual, c.expected) {
			t.Errorf("getThirdPartyResourceList(%#v) == \n%#v\nexpected \n%#v\n",
				c.thirdPartyResources, actual, c.expected)
		}
	}
}
