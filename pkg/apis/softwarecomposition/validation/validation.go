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
	"fmt"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
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

func ValidateGeneratedNetworkPolicySpec(v *softwarecomposition.GeneratedNetworkPolicySpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	return allErrs
}

func ValidateGeneratedNetworkPolicy(v *softwarecomposition.GeneratedNetworkPolicy) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateGeneratedNetworkPolicySpec(&v.Spec, field.NewPath("spec"))...)

	return allErrs
}

func ValidateNetworkNeighbors(v *softwarecomposition.NetworkNeighbors) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateNetworkNeighborsSpec(&v.Spec, field.NewPath("spec"))...)

	return allErrs
}

func ValidateNetworkNeighborsSpec(nns *softwarecomposition.NetworkNeighborsSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	for i, ingress := range nns.Ingress {
		allErrs = append(allErrs, ValidateNetworkNeighbor(&ingress, fldPath.Child("ingress").Index(i))...)
	}

	for i, egress := range nns.Egress {
		allErrs = append(allErrs, ValidateNetworkNeighbor(&egress, fldPath.Child("egress").Index(i))...)
	}

	return allErrs

}

func ValidateNetworkNeighbor(nns *softwarecomposition.NetworkNeighbor, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	for i, networkPort := range nns.Ports {
		allErrs = append(allErrs, ValidateNetworkNeighborsPort(&networkPort, fldPath.Child("ports").Index(i))...)
	}
	return allErrs
}

func ValidateNetworkNeighborsPort(p *softwarecomposition.NetworkPort, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validatePortNumber(*p.Port, fldPath.Child("port"))...)

	allErrs = append(allErrs, validatePortName(p, fldPath.Child("name"))...)

	return allErrs
}

func validatePortNumber(port int32, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if port < 0 || port > 65535 {
		allErrs = append(allErrs, field.Invalid(fldPath, port, "port must be in range 0-65535"))
	}
	return allErrs
}

func validatePortName(p *softwarecomposition.NetworkPort, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	expectedPortName := fmt.Sprintf("%s-%d", p.Protocol, *p.Port)
	if p.Name != expectedPortName {
		allErrs = append(allErrs, field.Invalid(fldPath, p.Name, "port name must be in the format {protocol}-{port}"))
	}

	return allErrs
}

func AlwaysValid(o runtime.Object) field.ErrorList {
	return field.ErrorList{}
}
