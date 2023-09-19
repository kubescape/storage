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

package apiserver

import (
	"regexp"
	"testing"

	wardlefuzzer "github.com/kubescape/storage/pkg/apis/softwarecomposition/fuzzer"
	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	"k8s.io/apimachinery/pkg/api/apitesting/roundtrip"
	metafuzzer "k8s.io/apimachinery/pkg/apis/meta/fuzzer"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"math/rand"
)

func TestRoundTripTypes(t *testing.T) {
	fuzzingFuncs := wardlefuzzer.Funcs
	scheme := Scheme

	codecFactory := runtimeserializer.NewCodecFactory(scheme)
	f := fuzzer.FuzzerFor(
		fuzzer.MergeFuzzerFuncs(metafuzzer.Funcs, fuzzingFuncs),
		rand.NewSource(rand.Int63()),
		codecFactory,
	)

	f.NumElements(1, 2)
	f.NilChance(0)

	skippedFields := []string{
		"SnippetAttributionTexts",
		"SpecialID",
		"IsUnpackaged",
		"IsFilesAnalyzedTagPresent",
		"ManagedFields",
		"SnippetSPDXIdentifier",
		"Snippets",
		"DocumentRefID",
		"ElementRefID",
		// Not exported
		"AnnotationSPDXIdentifier",
	}
	for idx := range skippedFields {
		skipPattern := regexp.MustCompile(skippedFields[idx])
		f.SkipFieldsWithPattern(skipPattern)
	}

	roundtrip.RoundTripTypesWithoutProtobuf(t, scheme, codecFactory, f, nil)
}
