package registry

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
)

// StatusREST exposes get, update and patch without registering create, delete,
// list or watch operations on a status subresource.
type StatusREST struct{ Store *genericregistry.Store }

var _ rest.Patcher = (*StatusREST)(nil)

func (r *StatusREST) New() runtime.Object   { return r.Store.New() }
func (r *StatusREST) Destroy()              {}
func (r *StatusREST) NamespaceScoped() bool { return r.Store.NamespaceScoped() }
func (r *StatusREST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	return r.Store.Get(ctx, name, options)
}
func (r *StatusREST) Update(ctx context.Context, name string, info rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, _ bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	return r.Store.Update(ctx, name, info, createValidation, updateValidation, false, options)
}
