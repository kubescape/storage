package file

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/utils/pointer"
)

func TestStorageImpl_Count(t *testing.T) {
	files := []string{
		"/other/type/ns/titi.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/titi.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/other/toto.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/other/toto.json",
	}
	tests := []struct {
		name    string
		key     string
		want    int64
		wantErr bool
	}{
		{
			name: "one object",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
			want: 1,
		},
		{
			name: "one ns",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape",
			want: 1,
		},
		{
			name: "one type",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s",
			want: 2,
		},
		{
			name: "all types",
			key:  "/spdx.softwarecomposition.kubescape.io",
			want: 4,
		},
		{
			name: "from top",
			key:  "/",
			want: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			fs.Mkdir(DefaultStorageRoot, 0755)
			for _, f := range files {
				afero.WriteFile(fs, DefaultStorageRoot+f, []byte(""), 0644)
			}
			s := NewStorageImpl(fs, DefaultStorageRoot)
			got, err := s.Count(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("Count() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Count() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStorageImpl_Create(t *testing.T) {
	type args struct {
		in0 context.Context
		key string
		obj runtime.Object
		out runtime.Object
		in4 uint64
	}
	tests := []struct {
		name     string
		readonly bool
		args     args
		wantErr  bool
	}{
		{
			name:     "readonly fs",
			readonly: true,
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
			},
			wantErr: true,
		},
		{
			name: "object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				obj: &v1beta1.SBOMSPDXv2p3{
					ObjectMeta: v1.ObjectMeta{
						Name: "toto",
					},
				},
			},
		},
		{
			name: "object and out",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				obj: &v1beta1.SBOMSPDXv2p3{
					ObjectMeta: v1.ObjectMeta{
						Name: "toto",
					},
				},
				out: &v1beta1.SBOMSPDXv2p3{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fs afero.Fs
			if tt.readonly {
				fs = afero.NewReadOnlyFs(afero.NewMemMapFs())
			} else {
				fs = afero.NewMemMapFs()
			}
			s := NewStorageImpl(fs, DefaultStorageRoot)
			err := s.Create(tt.args.in0, tt.args.key, tt.args.obj, tt.args.out, tt.args.in4)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			exists, _ := afero.Exists(fs, DefaultStorageRoot+tt.args.key+".json") // FIXME: use getPath instead
			assert.Truef(t, exists, "file %s should exist", DefaultStorageRoot+tt.args.key)
			if tt.args.out != nil {
				assert.Equal(t, tt.args.obj, tt.args.out)
			}
		})
	}
}

func TestStorageImpl_Delete(t *testing.T) {
	toto, _ := json.Marshal(v1beta1.SBOMSPDXv2p3{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
	})
	type args struct {
		in0 context.Context
		key string
		out runtime.Object
		in3 *storage.Preconditions
		in4 storage.ValidateObjectFunc
		in5 runtime.Object
	}
	tests := []struct {
		name    string
		args    args
		content string
		create  bool
		wantErr bool
		want    runtime.Object
	}{
		{
			name: "not found",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
			},
			wantErr: true,
		},
		{
			name: "empty string",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				out: &v1beta1.SBOMSPDXv2p3{},
			},
			create:  true,
			wantErr: true,
		},
		{
			name: "empty object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				out: &v1beta1.SBOMSPDXv2p3{},
			},
			content: "{}",
			create:  true,
			want:    &v1beta1.SBOMSPDXv2p3{},
		},
		{
			name: "real object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				out: &v1beta1.SBOMSPDXv2p3{},
			},
			content: string(toto),
			create:  true,
			want: &v1beta1.SBOMSPDXv2p3{
				ObjectMeta: v1.ObjectMeta{
					Name: "toto",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			if tt.create {
				afero.WriteFile(fs, DefaultStorageRoot+tt.args.key+".json", []byte(tt.content), 0644)
			}
			s := NewStorageImpl(fs, DefaultStorageRoot)
			if err := s.Delete(tt.args.in0, tt.args.key, tt.args.out, tt.args.in3, tt.args.in4, tt.args.in5); (err != nil) != tt.wantErr {
				t.Errorf("Delete() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.want != nil {
				assert.Equal(t, tt.want, tt.args.out)
			}
		})
	}
}

func TestStorageImpl_Get(t *testing.T) {
	toto, _ := json.Marshal(v1beta1.SBOMSPDXv2p3{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
	})
	type fields struct {
		eventBus  *EventBus
		lock      sync.RWMutex
		versioner storage.Versioner
	}
	type args struct {
		in0    context.Context
		key    string
		opts   storage.GetOptions
		objPtr runtime.Object
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		content string
		create  bool
		wantErr bool
		want    runtime.Object
	}{
		{
			name: "not found",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
			},
			wantErr: true,
		},
		{
			name: "empty string",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				objPtr: &v1beta1.SBOMSPDXv2p3{},
			},
			create:  true,
			wantErr: true,
		},
		{
			name: "empty object",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				objPtr: &v1beta1.SBOMSPDXv2p3{},
			},
			content: "{}",
			create:  true,
			want:    &v1beta1.SBOMSPDXv2p3{},
		},
		{
			name: "real object",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				objPtr: &v1beta1.SBOMSPDXv2p3{},
			},
			content: string(toto),
			create:  true,
			want: &v1beta1.SBOMSPDXv2p3{
				ObjectMeta: v1.ObjectMeta{
					Name: "toto",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			if tt.create {
				afero.WriteFile(fs, DefaultStorageRoot+tt.args.key+".json", []byte(tt.content), 0644)
			}
			s := NewStorageImpl(fs, DefaultStorageRoot)
			if err := s.Get(tt.args.in0, tt.args.key, tt.args.opts, tt.args.objPtr); (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.want != nil {
				assert.Equal(t, tt.want, tt.args.objPtr)
			}
		})
	}
}

func TestStorageImpl_GetList(t *testing.T) {
	objs := map[string]runtime.Object{
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto": &v1beta1.SBOMSPDXv2p3{
			ObjectMeta: v1.ObjectMeta{
				Name:      "toto",
				Namespace: "kubescape",
			},
		},
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/titi": &v1beta1.SBOMSPDXv2p3{
			ObjectMeta: v1.ObjectMeta{
				Name:      "titi",
				Namespace: "kubescape",
			},
		},
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/other/tata": &v1beta1.SBOMSPDXv2p3{
			ObjectMeta: v1.ObjectMeta{
				Name:      "tata",
				Namespace: "other",
			},
		},
	}
	type fields struct {
		eventBus  *EventBus
		lock      sync.RWMutex
		versioner storage.Versioner
	}
	type args struct {
		in0     context.Context
		key     string
		in2     storage.ListOptions
		listObj runtime.Object
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    int
	}{
		{
			name: "get object",
			args: args{
				key:     "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				listObj: &v1beta1.SBOMSPDXv2p3List{},
			},
			want: 1,
		},
		{
			name: "get ns",
			args: args{
				key:     "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape",
				listObj: &v1beta1.SBOMSPDXv2p3List{},
			},
			want: 2,
		},
		{
			name: "get all ns",
			args: args{
				key:     "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s",
				listObj: &v1beta1.SBOMSPDXv2p3List{},
			},
			want: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			for k, v := range objs {
				_ = s.Create(context.Background(), k, v, nil, 0)
			}
			if err := s.GetList(tt.args.in0, tt.args.key, tt.args.in2, tt.args.listObj); (err != nil) != tt.wantErr {
				t.Errorf("GetList() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.want, len(tt.args.listObj.(*v1beta1.SBOMSPDXv2p3List).Items))
		})
	}
}

func TestStorageImpl_GuaranteedUpdate(t *testing.T) {
	count := 0
	toto := &v1beta1.SBOMSPDXv2p3{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
	}
	totov2 := &v1beta1.SBOMSPDXv2p3{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "2",
		},
	}
	totov3 := &v1beta1.SBOMSPDXv2p3{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "3",
		},
	}
	type fields struct {
		eventBus  *EventBus
		lock      sync.RWMutex
		versioner storage.Versioner
	}
	type args struct {
		ctx                  context.Context
		key                  string
		destination          runtime.Object
		ignoreNotFound       bool
		preconditions        *storage.Preconditions
		tryUpdate            storage.UpdateFunc
		cachedExistingObject runtime.Object
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		want    *v1beta1.SBOMSPDXv2p3
	}{
		{
			name: "test",
			args: args{
				key:         "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				destination: &v1beta1.SBOMSPDXv2p3{},
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					return toto, nil, nil
				},
			},
			want: toto,
		},
		{
			name: "test with existing object",
			args: args{
				key:         "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				destination: &v1beta1.SBOMSPDXv2p3{},
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					return input, nil, nil
				},
				cachedExistingObject: toto,
			},
			want: toto,
		},
		{
			name: "test with failing precondition",
			args: args{
				key:         "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				destination: &v1beta1.SBOMSPDXv2p3{},
				preconditions: &storage.Preconditions{
					ResourceVersion: pointer.String("v123"),
				},
				cachedExistingObject: toto,
			},
			wantErr: true,
		},
		{
			name: "test with failing tryUpdate",
			args: args{
				key:         "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				destination: totov2,
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					if count == 0 {
						count++
						return nil, nil, fmt.Errorf("tryUpdate error")
					}
					return totov3, nil, nil
				},
				cachedExistingObject: toto,
			},
			want: totov3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			_ = s.Create(context.Background(), tt.args.key, tt.args.destination, nil, 0)
			err := s.GuaranteedUpdate(tt.args.ctx, tt.args.key, tt.args.destination, tt.args.ignoreNotFound, tt.args.preconditions, tt.args.tryUpdate, tt.args.cachedExistingObject)
			if tt.wantErr {
				if err == nil {
					t.Errorf("GuaranteedUpdate() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			} else {
				assert.Equal(t, tt.want, tt.args.destination)
				onDisk := &v1beta1.SBOMSPDXv2p3{}
				err = s.Get(context.Background(), tt.args.key, storage.GetOptions{}, onDisk)
				assert.NoError(t, err)
				assert.Equal(t, tt.want, onDisk)
			}
		})
	}
}

func TestStorageImpl_Versioner(t *testing.T) {
	tests := []struct {
		name string
		want storage.Versioner
	}{
		{
			name: "test versioner",
			want: storage.APIObjectVersioner{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			assert.Equal(t, tt.want, s.Versioner())
		})
	}
}

func TestStorageImpl_Watch(t *testing.T) {
	type args struct {
		ctx  context.Context
		key  string
		opts storage.ListOptions
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "test watch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			got, err := s.Watch(tt.args.ctx, tt.args.key, tt.args.opts)
			assert.NoError(t, err)
			assert.NotNilf(t, got, "Watch() got = %v", got)
			ch := got.ResultChan()
			assert.NotNilf(t, ch, "ResultChan() got = %v", got)
		})
	}
}
