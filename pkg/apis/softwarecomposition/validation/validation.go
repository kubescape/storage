/*
Copyright 2016 The Kubernetes Authors.

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

package validation

import (
	"k8s.io/apimachinery/pkg/util/validation/field"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// ValidateFlunder validates a Flunder.
func ValidateFlunder(f *softwarecomposition.SBOMSPDXv2p3) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateFlunderSpec(&f.Spec, field.NewPath("spec"))...)

	return allErrs
}

// ValidateFlunderSpec validates a FlunderSpec.
func ValidateFlunderSpec(s *softwarecomposition.SBOMSPDXv2p3Spec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	return allErrs
}

func ValidateSBOMSPDXv2p3Filtered(s *softwarecomposition.SBOMSPDXv2p3Filtered) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateFlunderSpec(&s.Spec, field.NewPath("spec"))...)

	return allErrs
}

func ValidateVulnerabilityManifestSpec(v *softwarecomposition.VulnerabilityManifestSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	return allErrs
}

func ValidateVulnerabilityManifest(v *softwarecomposition.VulnerabilityManifest) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateVulnerabilityManifestSpec(&v.Spec, field.NewPath("spec"))...)

	return allErrs
}

func ValidateSBOMSummarySpec(v *softwarecomposition.SBOMSummarySpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	return allErrs
}

func ValidateSBOMSummary(v *softwarecomposition.SBOMSummary) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateSBOMSummarySpec(&v.Spec, field.NewPath("spec"))...)

	return allErrs
}

func ValidateVulnerabilityManifestSummarySpec(v *softwarecomposition.VulnerabilityManifestSummarySpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	return allErrs
}

func ValidateVulnerabilityManifestSummary(v *softwarecomposition.VulnerabilityManifestSummary) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateVulnerabilityManifestSummarySpec(&v.Spec, field.NewPath("spec"))...)

	return allErrs
}
