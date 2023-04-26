package cask

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

type Store struct {
	NewFunc                   func() runtime.Object
	NewListFunc               func() runtime.Object
	PredicateFunc             func(label labels.Selector, field fields.Selector) storage.SelectionPredicate
	DefaultQualifiedResource  schema.GroupResource
	SingularQualifiedResource schema.GroupResource
	CreateStrategy            interface{}
	UpdateStrategy            interface{}
	DeleteStrategy            interface{}
	TableConvertor            rest.TableConvertor
}

var _ rest.StandardStorage = &Store{}
var _ rest.TableConvertor = &Store{}
var _ registry.GenericStore = &Store{}
var _ rest.SingularNameProvider = &Store{}

func (c Store) GetSingularName() string {
	//TODO implement me
	panic("implement me")
}

func (c Store) GetCreateStrategy() rest.RESTCreateStrategy {
	//TODO implement me
	panic("implement me")
}

func (c Store) GetUpdateStrategy() rest.RESTUpdateStrategy {
	//TODO implement me
	panic("implement me")
}

func (c Store) GetDeleteStrategy() rest.RESTDeleteStrategy {
	//TODO implement me
	panic("implement me")
}

func (c Store) Get(ctx context.Context, name string, options *v1.GetOptions) (runtime.Object, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) NewList() runtime.Object {
	//TODO implement me
	panic("implement me")
}

func (c Store) List(ctx context.Context, options *internalversion.ListOptions) (runtime.Object, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*v1.Table, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) New() runtime.Object {
	//TODO implement me
	panic("implement me")
}

func (c Store) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *v1.CreateOptions) (runtime.Object, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *v1.UpdateOptions) (runtime.Object, bool, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *v1.DeleteOptions) (runtime.Object, bool, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) DeleteCollection(ctx context.Context, deleteValidation rest.ValidateObjectFunc, options *v1.DeleteOptions, listOptions *internalversion.ListOptions) (runtime.Object, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) Watch(ctx context.Context, options *internalversion.ListOptions) (watch.Interface, error) {
	//TODO implement me
	panic("implement me")
}

func (c Store) Destroy() {
	//TODO implement me
	panic("implement me")
}

func (c Store) CompleteWithOptions(options *generic.StoreOptions) error {
	return nil
}
