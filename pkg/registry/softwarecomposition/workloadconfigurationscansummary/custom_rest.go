package workloadconfigurationscansummary

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// This file wires the resource-agnostic genericrest.Store (see
// pkg/registry/genericrest/store.go) with
// workloadconfigurationscansummaries-specific configuration, as a parallel,
// gated alternative to the genericregistry.Store based implementation in
// etcd.go (which remains the reference implementation for differential
// testing -- see custom_rest_test.go). Namespaced, no-op Strategy. Not
// inverted: DefaultQualifiedResource is plural, SingularQualifiedResource
// is singular, matching normal convention.
type CustomREST = genericrest.Store

// NewCustomREST returns the custom rest.Storage implementation for
// workloadconfigurationscansummaries. Its signature intentionally matches
// NewREST's so it can be wired in as a drop-in alternative in
// pkg/apiserver/apiserver.go.
func NewCustomREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*CustomREST, error) {
	strategy := NewStrategy(scheme)
	qualifiedResource := softwarecomposition.Resource("workloadconfigurationscansummaries")
	singularQualifiedResource := softwarecomposition.Resource("workloadconfigurationscansummary")

	return genericrest.NewStore(storageImpl, optsGetter, genericrest.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.WorkloadConfigurationScanSummary{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.WorkloadConfigurationScanSummaryList{} },
		PredicateFunc:             MatchWorkloadConfigurationScanSummary,
		DefaultQualifiedResource:  qualifiedResource,
		SingularQualifiedResource: singularQualifiedResource,
		Strategy:                  strategy,
		// TODO: define table converter that exposes more than name/creation timestamp
		// (preserved verbatim from etcd.go's NewREST -- not addressed by this migration).
		TableConvertor: rest.NewDefaultTableConvertor(qualifiedResource),
	})
}
