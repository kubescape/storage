package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const GroupName = "kubescape.io"

var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&SandboxPolicy{}, &SandboxPolicyList{},
		&ClusterSandboxPolicy{}, &ClusterSandboxPolicyList{},
		&SandboxPolicyClasses{}, &SandboxPolicyClassesList{},
		&SandboxPolicyNodeStatus{}, &SandboxPolicyNodeStatusList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
