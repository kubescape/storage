package file

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/utils/ptr"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

func getStoredPayloadFilepath(root, key string) string {
	return root + key + GobExt
}

func TestStorageImpl_Count(t *testing.T) {
	keys := []string{
		"/other/type/ns/titi",
		"/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds/kubescape/titi",
		"/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds/other/toto",
		"/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
		"/spdx.softwarecomposition.kubescape.io/sbomsyfts/other/toto",
	}
	tests := []struct {
		name    string
		key     string
		want    int64
		wantErr bool
	}{
		{
			name: "one object",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
			want: 1,
		},
		{
			name: "one ns",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape",
			want: 1,
		},
		{
			name: "one type",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomsyfts",
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)

			conn, err := pool.Take(context.TODO())
			require.NoError(t, err)
			for _, k := range keys {
				_ = writeMetadata(conn, k, &v1beta1.SBOMSyft{})
			}
			pool.Put(conn)

			s := NewStorageImpl(nil, DefaultStorageRoot, pool, nil, nil)
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
		want     runtime.Object
	}{
		{
			name:     "readonly fs",
			readonly: true,
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				obj: &v1beta1.SBOMSyft{},
			},
			wantErr: true,
		},
		{
			name: "object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				obj: &v1beta1.SBOMSyft{
					ObjectMeta: v1.ObjectMeta{
						Name: "toto",
					},
				},
			},
		},
		{
			name: "object and out",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				obj: &v1beta1.SBOMSyft{
					ObjectMeta: v1.ObjectMeta{
						Name:          "toto",
						ManagedFields: []v1.ManagedFieldsEntry{{Manager: "node-agent"}},
					},
				},
				out: &v1beta1.SBOMSyft{},
			},
			want: &v1beta1.SBOMSyft{
				ObjectMeta: v1.ObjectMeta{
					Name:            "toto",
					ResourceVersion: "1",
					Annotations: map[string]string{
						"kubescape.io/sync-checksum": "c1cabafe2019d04e697774db7bc943c2d9012ff3ccf5ea78af2179f5558e764d",
					},
				},
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
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)
			sch := scheme.Scheme
			require.NoError(t, softwarecomposition.AddToScheme(sch))
			s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, sch)
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			err := s.Create(ctx, tt.args.key, tt.args.obj, tt.args.out, tt.args.in4)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			expectedPath := getStoredPayloadFilepath(DefaultStorageRoot, tt.args.key)
			exists, _ := afero.Exists(fs, expectedPath)
			assert.Truef(t, exists, "file %s should exist", expectedPath)
			if tt.want != nil {
				assert.Equal(t, tt.want, tt.args.out)
			}

			conn, err := pool.Take(context.TODO())
			require.NoError(t, err)
			l, _, err := listMetadata(conn, tt.args.key, "", int64(500))
			assert.NoError(t, err)
			assert.Len(t, l, 1)
			pool.Put(conn)
		})
	}
}

func TestStorageImpl_Delete(t *testing.T) {
	empty := v1beta1.SBOMSyft{}
	toto := v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
	}
	type args struct {
		key string
		out runtime.Object
		in3 *storage.Preconditions
		in4 storage.ValidateObjectFunc
		in5 runtime.Object
		in6 storage.DeleteOptions
	}
	tests := []struct {
		name    string
		args    args
		content runtime.Object
		create  bool
		wantErr bool
		want    runtime.Object
	}{
		{
			name: "not found",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
			},
		},
		{
			name: "empty object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				out: &v1beta1.SBOMSyft{},
			},
			content: &empty,
			create:  true,
			want:    &v1beta1.SBOMSyft{},
		},
		{
			name: "real object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				out: &v1beta1.SBOMSyft{},
			},
			content: &toto,
			create:  true,
			want: &v1beta1.SBOMSyft{
				ObjectMeta: v1.ObjectMeta{
					Name: "toto",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)

			conn, err := pool.Take(context.TODO())
			require.NoError(t, err)
			if tt.create {
				_ = writeMetadata(conn, tt.args.key, tt.content)
			}
			pool.Put(conn)

			s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, nil)
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			if err := s.Delete(ctx, tt.args.key, tt.args.out, tt.args.in3, tt.args.in4, tt.args.in5, tt.args.in6); (err != nil) != tt.wantErr {
				t.Errorf("Delete() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.want != nil {
				assert.Equal(t, tt.want, tt.args.out)
			}
		})
	}
}

func isNotFoundError(_ assert.TestingT, err error, _ ...any) bool {
	return storage.IsNotFound(err)
}

func TestStorageImpl_Get(t *testing.T) {
	var emptyObj bytes.Buffer
	_ = gob.NewEncoder(&emptyObj).Encode(v1beta1.SBOMSyft{})
	var realMeta bytes.Buffer
	_ = json.NewEncoder(&realMeta).Encode(v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
	})
	var realObj bytes.Buffer
	_ = gob.NewEncoder(&realObj).Encode(v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{
				Tool: v1beta1.ToolMeta{
					Name: "syft"},
			},
		},
	})
	type args struct {
		key    string
		opts   storage.GetOptions
		objPtr runtime.Object
	}
	tests := []struct {
		name        string
		args        args
		content     []byte
		contentMeta []byte
		create      bool
		wantErr     assert.ErrorAssertionFunc
		want        runtime.Object
	}{
		{
			name: "not found",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
			},
			wantErr: isNotFoundError,
		},
		{
			name: "empty string",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				objPtr: &v1beta1.SBOMSyft{},
			},
			create:  true,
			wantErr: isNotFoundError,
		},
		{
			name: "empty object",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				objPtr: &v1beta1.SBOMSyft{},
			},
			content: emptyObj.Bytes(),
			create:  true,
			wantErr: assert.NoError,
			want:    &v1beta1.SBOMSyft{},
		},
		{
			name: "real object",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				objPtr: &v1beta1.SBOMSyft{},
			},
			content: realObj.Bytes(),
			create:  true,
			wantErr: assert.NoError,
			want: &v1beta1.SBOMSyft{
				ObjectMeta: v1.ObjectMeta{
					Name: "toto",
				},
				Spec: v1beta1.SBOMSyftSpec{
					Metadata: v1beta1.SPDXMeta{
						Tool: v1beta1.ToolMeta{
							Name: "syft"},
					},
				},
			},
		},
		{
			name: "real object - metadata only",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				objPtr: &v1beta1.SBOMSyft{},
				opts:   storage.GetOptions{ResourceVersion: "metadata"},
			},
			contentMeta: realMeta.Bytes(),
			create:      true,
			wantErr:     assert.NoError,
			want: &v1beta1.SBOMSyft{
				ObjectMeta: v1.ObjectMeta{
					Name: "toto",
				},
			},
		},
		{
			name: "truncated object",
			args: args{
				key:    "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				objPtr: &v1beta1.SBOMSyft{},
			},
			content: realObj.Bytes()[10:10],
			create:  true,
			wantErr: isNotFoundError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)
			s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, nil)
			if tt.create {
				conn, err := pool.Take(context.TODO())
				require.NoError(t, err)
				require.NoError(t, WriteJSON(conn, tt.args.key, tt.contentMeta))
				require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, tt.args.key), tt.content, 0644))
				pool.Put(conn)
			}
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			if err := s.Get(ctx, tt.args.key, tt.args.opts, tt.args.objPtr); !tt.wantErr(t, err) {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr(t, err))
			}
			if tt.want != nil {
				assert.Equal(t, tt.want, tt.args.objPtr)
			}
		})
	}
}

func TestStorageImpl_GetList(t *testing.T) {
	objs := map[string]runtime.Object{
		"/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto": &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{
				Name:      "toto",
				Namespace: "kubescape",
			},
		},
		"/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/titi": &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{
				Name:      "titi",
				Namespace: "kubescape",
			},
		},
		"/spdx.softwarecomposition.kubescape.io/sbomsyfts/other/tata": &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{
				Name:      "tata",
				Namespace: "other",
			},
		},
	}
	type args struct {
		key     string
		listObj runtime.Object
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		want    int
	}{
		{
			name: "get ns",
			args: args{
				key:     "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape",
				listObj: &v1beta1.SBOMSyftList{},
			},
			want: 2,
		},
		{
			name: "get all ns",
			args: args{
				key:     "/spdx.softwarecomposition.kubescape.io/sbomsyfts",
				listObj: &v1beta1.SBOMSyftList{},
			},
			want: 3,
		},
	}
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	for k, v := range objs {
		err := s.Create(ctx, k, v.DeepCopyObject(), nil, 0)
		assert.NoError(t, err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := storage.ListOptions{Predicate: storage.SelectionPredicate{Limit: 500}} // this is the limit
			if err := s.GetList(ctx, tt.args.key, opts, tt.args.listObj); (err != nil) != tt.wantErr {
				t.Errorf("GetList() error = %v, wantErr %v", err, tt.wantErr)
			}
			assert.Equal(t, tt.want, len(tt.args.listObj.(*v1beta1.SBOMSyftList).Items))
		})
	}
}

func TestStorageImpl_GuaranteedUpdate(t *testing.T) {
	count := 0
	toto := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{
				Tool: v1beta1.ToolMeta{
					Name: "titi",
				},
			},
		},
	}
	totov1 := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "1",
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{
				Tool: v1beta1.ToolMeta{
					Name: "titi",
				},
			},
		},
	}
	totov3 := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "3",
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{
				Tool: v1beta1.ToolMeta{
					Name: "tutu",
				},
			},
		},
	}
	tutu := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "1",
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{
				Tool: v1beta1.ToolMeta{
					Name: "tutu",
				},
			},
		},
	}
	type args struct {
		key                  string
		ignoreNotFound       bool
		preconditions        *storage.Preconditions
		tryUpdate            storage.UpdateFunc
		cachedExistingObject runtime.Object
	}
	tests := []struct {
		name         string
		args         args
		create       bool
		wantErr      bool
		wantNotFound bool
		want         *v1beta1.SBOMSyft
	}{
		{
			name: "test",
			args: args{
				key:            "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				ignoreNotFound: true,
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					return toto.DeepCopyObject(), nil, nil
				},
			},
			want: totov1,
		},
		{
			name: "test with existing object",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					obj := *input.(*v1beta1.SBOMSyft)
					obj.Spec.Metadata.Tool.Name = "tutu"
					return &obj, nil, nil
				},
				cachedExistingObject: toto.DeepCopyObject(),
			},
			want: tutu,
		},
		{
			name: "test with existing object, no change",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					return input, nil, nil
				},
				cachedExistingObject: toto.DeepCopyObject(),
			},
			wantNotFound: true, // no change, not found because we don't call saveObject
		},
		{
			name: "test with failing precondition",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				preconditions: &storage.Preconditions{
					ResourceVersion: ptr.To("v123"),
				},
				cachedExistingObject: toto.DeepCopyObject(),
			},
			wantErr: true,
		},
		{
			name: "test with failing tryUpdate",
			args: args{
				key:            "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				ignoreNotFound: true,
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					if count == 0 {
						count++
						return nil, nil, fmt.Errorf("tryUpdate error")
					}
					obj := *input.(*v1beta1.SBOMSyft)
					obj.ResourceVersion = "2"
					obj.Spec.Metadata.Tool.Name = "tutu"
					return &obj, nil, nil
				},
				cachedExistingObject: toto.DeepCopyObject(),
			},
			create: true,
			want:   totov3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)
			sch := scheme.Scheme
			require.NoError(t, softwarecomposition.AddToScheme(sch))
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			if tt.create {
				err := s.Create(ctx, tt.args.key, toto.DeepCopyObject(), nil, 0)
				assert.NoError(t, err)
			}
			destination := &v1beta1.SBOMSyft{}
			err := s.GuaranteedUpdate(ctx, tt.args.key, destination, tt.args.ignoreNotFound, tt.args.preconditions, tt.args.tryUpdate, tt.args.cachedExistingObject)
			if tt.wantErr {
				if err == nil {
					t.Errorf("GuaranteedUpdate() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			} else {
				onDisk := &v1beta1.SBOMSyft{}
				err = s.Get(ctx, tt.args.key, storage.GetOptions{}, onDisk)
				if tt.wantNotFound {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					assert.Equal(t, tt.want, onDisk)
				}
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
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, nil, nil, nil)
			assert.Equal(t, tt.want, s.Versioner())
		})
	}
}

func BenchmarkWriteFiles(b *testing.B) {
	pool := NewTestPool(b.TempDir())
	require.NotNil(b, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(b, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch).(*StorageImpl)
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	obj := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name: "toto",
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{
				Tool: v1beta1.ToolMeta{Name: "titi"},
			},
		},
	}
	metaOut := &v1beta1.SBOMSyft{}
	conn, _ := s.pool.Take(context.Background())
	for i := 0; i < b.N; i++ {
		_ = s.saveObject(conn, key, obj, metaOut, "")
	}
	s.pool.Put(conn)
	b.ReportAllocs()
}

func Test_calculateChecksum(t *testing.T) {
	tests := []struct {
		name    string
		obj     runtime.Object
		want    string
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "applicationprofile",
			obj: &softwarecomposition.ApplicationProfile{
				ObjectMeta: v1.ObjectMeta{
					Name:      "toto",
					Namespace: "default",
					Annotations: map[string]string{
						"key": "value",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Architectures: []string{"amd64"},
					Containers: []softwarecomposition.ApplicationProfileContainer{{
						Name: "nginx",
						Execs: []softwarecomposition.ExecCalls{{
							Path: "/usr/sbin/nginx",
						}},
					}},
				},
			},
			want:    "e0d53970c4c25450026c60aae0125653a63cafc8c9173ffba1db298572594247",
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sch := scheme.Scheme
			require.NoError(t, softwarecomposition.AddToScheme(sch))
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, nil, nil, sch)
			got, err := s.CalculateChecksum(tt.obj)
			if !tt.wantErr(t, err, fmt.Sprintf("CalculateChecksum(%v)", tt.obj)) {
				return
			}
			assert.Equalf(t, tt.want, got, "CalculateChecksum(%v)", tt.obj)
		})
	}
}
