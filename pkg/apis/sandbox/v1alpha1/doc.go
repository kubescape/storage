// +k8s:deepcopy-gen=package
// +groupName=kubescape.io

// Package v1alpha1 defines the SandboxPolicy CRD API. These resources are served
// by Kubernetes, not by the storage aggregated API server. The API does not
// itself install an enforcement agent or an ownership admission webhook.
package v1alpha1
