/*
Copyright 2017 The Kubernetes Authors.

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

package fuzzer

import (
	fuzz "github.com/google/gofuzz"
	"k8s.io/sample-apiserver/pkg/apis/wardle"

	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
)

func prepareFile(file *wardle.File) {
	// Snippets are not exported, and should not be round-tripped
	file.Snippets = nil
}

func prepareReviews(reviews *[]*wardle.Review) {
	// Reviews are deprecated and not exported, so they should not be round tripped
	*reviews = nil
}

func prepareCreationInfo(creationInfo *wardle.CreationInfo) {
	if creationInfo == nil {
		return
	}

	for i := range creationInfo.Creators {
		creator := &creationInfo.Creators[i]
		creator.Creator = "kubescape"
		creator.CreatorType = "Tool"
	}
}

func prepareSPDXIdentifier(SPDXIdentifier *wardle.ElementID) {
	*SPDXIdentifier = "DOCUMENT"
}

func fuzzDocElementID(dei *wardle.DocElementID, c fuzz.Continue) {
	dei.DocumentRefID = ""
	dei.ElementRefID = wardle.ElementID("dummyvalue")
}

func fuzzSupplier(s *wardle.Supplier, c fuzz.Continue) {
	s.Supplier = "John Doe"
	s.SupplierType = "Person"
}

func fuzzAnnotator(a *wardle.Annotator, c fuzz.Continue) {
	a.Annotator = "Kubescape"
	a.AnnotatorType = "Tool"
}

func fuzzOriginator(o *wardle.Originator, c fuzz.Continue) {
	o.Originator = "John Doe"
	o.OriginatorType = "Person"
}

func fuzzFile(f *wardle.File, c fuzz.Continue) {
	// Snippets are not exported, should not be checked
	// somechange
	f.Snippets = map[wardle.ElementID]*wardle.Snippet{}
}

// Funcs returns the fuzzer functions for the apps api group.
var Funcs = func(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(s *wardle.FlunderSpec, c fuzz.Continue) {
			c.FuzzNoCustom(s) // fuzz self without calling this function again

			prepareSPDXIdentifier(&s.SPDX.SPDXIdentifier)

			prepareCreationInfo(s.SPDX.CreationInfo)

			prepareReviews(&s.SPDX.Reviews)

			for _, file := range s.SPDX.Files {
				if file == nil {
					continue
				}
				prepareFile(file)
			}
		},
		fuzzDocElementID,
		fuzzSupplier,
		fuzzAnnotator,
		fuzzOriginator,
		fuzzFile,
	}
}
