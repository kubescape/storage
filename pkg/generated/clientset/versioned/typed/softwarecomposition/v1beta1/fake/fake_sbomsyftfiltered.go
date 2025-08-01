/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	softwarecompositionv1beta1 "github.com/kubescape/storage/pkg/generated/applyconfiguration/softwarecomposition/v1beta1"
	typedsoftwarecompositionv1beta1 "github.com/kubescape/storage/pkg/generated/clientset/versioned/typed/softwarecomposition/v1beta1"
	gentype "k8s.io/client-go/gentype"
)

// fakeSBOMSyftFiltereds implements SBOMSyftFilteredInterface
type fakeSBOMSyftFiltereds struct {
	*gentype.FakeClientWithListAndApply[*v1beta1.SBOMSyftFiltered, *v1beta1.SBOMSyftFilteredList, *softwarecompositionv1beta1.SBOMSyftFilteredApplyConfiguration]
	Fake *FakeSpdxV1beta1
}

func newFakeSBOMSyftFiltereds(fake *FakeSpdxV1beta1, namespace string) typedsoftwarecompositionv1beta1.SBOMSyftFilteredInterface {
	return &fakeSBOMSyftFiltereds{
		gentype.NewFakeClientWithListAndApply[*v1beta1.SBOMSyftFiltered, *v1beta1.SBOMSyftFilteredList, *softwarecompositionv1beta1.SBOMSyftFilteredApplyConfiguration](
			fake.Fake,
			namespace,
			v1beta1.SchemeGroupVersion.WithResource("sbomsyftfiltereds"),
			v1beta1.SchemeGroupVersion.WithKind("SBOMSyftFiltered"),
			func() *v1beta1.SBOMSyftFiltered { return &v1beta1.SBOMSyftFiltered{} },
			func() *v1beta1.SBOMSyftFilteredList { return &v1beta1.SBOMSyftFilteredList{} },
			func(dst, src *v1beta1.SBOMSyftFilteredList) { dst.ListMeta = src.ListMeta },
			func(list *v1beta1.SBOMSyftFilteredList) []*v1beta1.SBOMSyftFiltered {
				return gentype.ToPointerSlice(list.Items)
			},
			func(list *v1beta1.SBOMSyftFilteredList, items []*v1beta1.SBOMSyftFiltered) {
				list.Items = gentype.FromPointerSlice(items)
			},
		),
		fake,
	}
}
