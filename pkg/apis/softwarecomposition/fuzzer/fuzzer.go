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
	"encoding/json"

	fuzz "github.com/google/gofuzz"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"

	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
)

func fuzzDocElementID(dei *softwarecomposition.DocElementID, c fuzz.Continue) {
	c.FuzzNoCustom(dei)

	dei.ElementRefID = softwarecomposition.ElementID("3ec7e593276354ae")
}

func fuzzSupplier(s *softwarecomposition.Supplier, c fuzz.Continue) {
	s.Supplier = "John Doe"
	s.SupplierType = "Person"
}

func fuzzAnnotator(a *softwarecomposition.Annotator, c fuzz.Continue) {
	a.Annotator = "Kubescape"
	a.AnnotatorType = "Tool"
}

func fuzzOriginator(o *softwarecomposition.Originator, c fuzz.Continue) {
	o.Originator = "John Doe"
	o.OriginatorType = "Person"
}

func fuzzFile(f *softwarecomposition.File, c fuzz.Continue) {
	c.FuzzNoCustom(f)

	// Snippets are not exported, not expected to round trip
	f.Snippets = nil
}

func fuzzDocument(d *softwarecomposition.Document, c fuzz.Continue) {
	c.FuzzNoCustom(d)

	// Reviews are not exported, not expected to round trip
	d.Reviews = nil
}

func fuzzCreator(cr *softwarecomposition.Creator, c fuzz.Continue) {
	c.FuzzNoCustom(cr)

	cr.Creator = "John Doe <johndoe@example.com>"
}

// fuzzJsonRawMessage returns a fuzzed value for the apiextensions JSON format
//
// At the moment this returns a valid JSON-encoded string
func fuzzJsonRawMessage(j *json.RawMessage, c fuzz.Continue) {
	encodedString, err := json.Marshal(c.RandString())
	if err != nil {
		panic(err)
	}

	err = j.UnmarshalJSON(encodedString)
	if err != nil {
		panic(err)
	}
}

// Funcs returns the fuzzer functions for the apps api group.
var Funcs = func(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(s *softwarecomposition.SBOMSPDXv2p3Spec, c fuzz.Continue) {
			c.FuzzNoCustom(s) // fuzz self without calling this function again
		},
		fuzzDocument,
		fuzzDocElementID,
		fuzzSupplier,
		fuzzAnnotator,
		fuzzOriginator,
		fuzzFile,
		fuzzCreator,
		fuzzJsonRawMessage,
	}
}
