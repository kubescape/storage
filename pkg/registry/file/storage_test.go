package file

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/utils/ptr"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

func getStoredPayloadFilepath(root, key string) string {
	return root + key + GobExt
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
						helpers.SyncChecksumMetadataKey: "0c8bc45306dae86e5a455fb117eae68f7a6903261ba7909979ec9e61806c7fd9",
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
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
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

// TestStorageImpl_Create_MetaOutContainsSpec is a regression test: Create's out param used to
// only ever contain ObjectMeta/SchemaVersion (the metadata-only object also written to SQLite),
// silently zeroing Spec -- a client chaining an Update off this response without an
// intervening Get would see an empty spec even though the actual persisted object was correct.
func TestStorageImpl_Create_MetaOutContainsSpec(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	obj := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{Name: "toto"},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{Tool: v1beta1.ToolMeta{Name: "titi"}},
		},
	}
	out := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Create(ctx, key, obj, out, 0))

	assert.Equal(t, "titi", out.Spec.Metadata.Tool.Name, "Create's out param must contain the persisted Spec, not just ObjectMeta")

	onDisk := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, onDisk))
	assert.Equal(t, onDisk, out, "Create's out param must match what was actually persisted")
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
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
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
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
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
			Annotations: map[string]string{
				helpers.SyncChecksumMetadataKey: "f34e9780b1cab33db857768ff07da40a09ddf003ee31e66cc70acaeb1bf44f40",
			},
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
			Annotations: map[string]string{
				helpers.SyncChecksumMetadataKey: "d378bc779bc63bf3877d24fec9a62b7739aec21cf7489202c461756c07169ae7",
			},
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
			Annotations: map[string]string{
				helpers.SyncChecksumMetadataKey: "685b93d26f3f89db026ab3af428beeb3f254a3d69e7a623ad94a1b712a3e8968",
			},
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
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
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

// TestStorageImpl_GuaranteedUpdate_MutateInPlacePersists is the regression
// test for the GuaranteedUpdateWithConn "before" snapshot bug: a tryUpdate
// closure that mutates its `existing` argument in place and returns that
// same object reference -- exactly what genericregistry.Store's own
// finalizer-delete tryUpdate does (vendor store.go's markAsDeleting,
// followed by `return existing, nil`) -- used to fool the no-op-update
// detection. The "before" snapshot (`orig := origState.obj.DeepCopyObject()`)
// used to be taken AFTER tryUpdate ran, so it observed the already-mutated
// state and reflect.DeepEqual(orig, ret) was spuriously true, silently
// skipping saveObject. The fix takes the snapshot before tryUpdate runs, on
// every retry-loop iteration. This test mutates the existing object in place
// (mirroring a finalizer-based Delete setting deletionTimestamp) and confirms
// a fresh Get genuinely observes the change.
func TestStorageImpl_GuaranteedUpdate_MutateInPlacePersists(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/mutate-in-place"
	original := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name:       "mutate-in-place",
			Finalizers: []string{"test/finalizer"},
		},
	}
	require.NoError(t, s.Create(ctx, key, original.DeepCopyObject(), nil, 0))

	now := v1.Now()
	err := s.GuaranteedUpdate(ctx, key, &v1beta1.SBOMSyft{}, false, nil,
		func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
			// Mutate `input` (== origState.obj) in place and return the SAME
			// reference, exactly like genericregistry.Store's markAsDeleting-based
			// finalizer-delete tryUpdate does.
			obj := input.(*v1beta1.SBOMSyft)
			obj.DeletionTimestamp = &now
			return obj, nil, nil
		}, nil)
	require.NoError(t, err)

	fresh := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, fresh))
	require.NotNil(t, fresh.DeletionTimestamp, "mutate-in-place update must be genuinely persisted, not silently dropped as a spurious no-op")
	assert.True(t, now.Time.Equal(fresh.DeletionTimestamp.Time))
}

// TestStorageImpl_GuaranteedUpdate_MetaOutContainsSpec is a regression test: GuaranteedUpdate's
// out param (metaOut) used to only ever contain ObjectMeta/SchemaVersion, silently zeroing
// Spec -- see TestStorageImpl_Create_MetaOutContainsSpec for the client-impact scenario.
func TestStorageImpl_GuaranteedUpdate_MetaOutContainsSpec(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	toto := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{Name: "toto"},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{Tool: v1beta1.ToolMeta{Name: "titi"}},
		},
	}
	require.NoError(t, s.Create(ctx, key, toto.DeepCopyObject(), nil, 0))

	destination := &v1beta1.SBOMSyft{}
	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		obj := *input.(*v1beta1.SBOMSyft)
		obj.Spec.Metadata.Tool.Name = "tutu"
		return &obj, nil, nil
	}
	require.NoError(t, s.GuaranteedUpdate(ctx, key, destination, false, nil, tryUpdate, toto.DeepCopyObject()))

	assert.Equal(t, "tutu", destination.Spec.Metadata.Tool.Name, "GuaranteedUpdate's out param must contain the persisted Spec, not just ObjectMeta")

	onDisk := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, onDisk))
	assert.Equal(t, onDisk, destination, "GuaranteedUpdate's out param must match what was actually persisted")
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
		_, _ = s.saveObject(conn, key, obj, metaOut, "")
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
			name: "containerprofile",
			obj: &softwarecomposition.ContainerProfile{
				ObjectMeta: v1.ObjectMeta{
					Name:      "toto",
					Namespace: "default",
					Annotations: map[string]string{
						"key": "value",
					},
				},
				Spec: softwarecomposition.ContainerProfileSpec{
					Architectures: []string{"amd64"},
					Execs: []softwarecomposition.ExecCalls{{
						Path: "/usr/sbin/nginx",
					}},
				},
			},
			want:    "264ce846e489f0e32634241e41ecbd96d53a8c52331fc02db13b0f627aed9a25",
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

func Test_newContentionTimeoutError(t *testing.T) {
	err := newContentionTimeoutError("get", "/spdx.softwarecomposition.kubescape.io/containerprofiles/kubescape/toto", fmt.Errorf("boom"))
	require.NotNil(t, err)
	assert.True(t, apierrors.IsServerTimeout(err), "expected a ServerTimeout error, got %v", err)
	status := err.Status()
	assert.Equal(t, int32(http.StatusInternalServerError), status.Code)
	assert.Equal(t, v1.StatusReasonServerTimeout, status.Reason)
	require.NotNil(t, status.Details)
	assert.EqualValues(t, 1, status.Details.RetryAfterSeconds)
}

// Test_newContentionTimeoutError_Cancelled covers the context.Canceled branch: a caller
// disconnect should not be reported as ServerTimeout with a Retry-After hint, since there is
// no client left to retry.
func Test_newContentionTimeoutError_Cancelled(t *testing.T) {
	err := newContentionTimeoutError("get", "/spdx.softwarecomposition.kubescape.io/containerprofiles/kubescape/toto", context.Canceled)
	require.NotNil(t, err)
	assert.False(t, apierrors.IsServerTimeout(err), "cancelled acquisition should not be reported as ServerTimeout, got %v", err)
	assert.True(t, apierrors.IsInternalError(err), "expected an InternalError, got %v", err)
	status := err.Status()
	require.NotNil(t, status.Details)
	assert.Zero(t, status.Details.RetryAfterSeconds, "cancelled acquisition should carry no RetryAfterSeconds hint")
}

func TestStorageImpl_LockContentionReturnsServerTimeout(t *testing.T) {
	old := lockTimeout
	lockTimeout = 10 * time.Millisecond
	defer func() { lockTimeout = old }()

	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch).(*StorageImpl)

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	// Hold the write lock on key so a concurrent Get contends and times out.
	require.NoError(t, s.locks.Lock(context.Background(), key))
	defer s.locks.Unlock(key)

	err := s.Get(context.Background(), key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	require.Error(t, err)
	assert.True(t, apierrors.IsServerTimeout(err), "expected a ServerTimeout error, got %v", err)

	statusErr, ok := err.(*apierrors.StatusError)
	require.True(t, ok, "expected *apierrors.StatusError, got %T", err)
	status := statusErr.Status()
	assert.Equal(t, int32(http.StatusInternalServerError), status.Code)
	require.NotNil(t, status.Details)
	assert.EqualValues(t, 1, status.Details.RetryAfterSeconds)
}

// TestStorageImpl_PoolContentionReturnsServerTimeout is the connection-pool analogue of
// TestStorageImpl_LockContentionReturnsServerTimeout above: it exhausts a size-1 pool by
// holding its only connection, then asserts a Get on that starved pool fails fast (bounded by
// the shrunk poolTimeout) as ServerTimeout+Retry-After, instead of hanging to the old ~60s
// poolTimeout (which, in production, is long enough to blow past the k8s apiserver's own
// outer non-long-running-request deadline before this package's own error ever fires).
func TestStorageImpl_PoolContentionReturnsServerTimeout(t *testing.T) {
	old := poolTimeout
	poolTimeout = 10 * time.Millisecond
	defer func() { poolTimeout = old }()

	pool := NewPool(filepath.Join(t.TempDir(), "test.sq3"), 1, 0)
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch).(*StorageImpl)

	// Take the pool's only connection directly, outside of any StorageImpl call, so the
	// pool is fully exhausted for any subsequent Take.
	heldConn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(heldConn)

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	getErr := s.Get(context.Background(), key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	require.Error(t, getErr)
	assert.True(t, apierrors.IsServerTimeout(getErr), "expected a ServerTimeout error, got %v", getErr)

	statusErr, ok := getErr.(*apierrors.StatusError)
	require.True(t, ok, "expected *apierrors.StatusError, got %T", getErr)
	status := statusErr.Status()
	assert.Equal(t, int32(http.StatusInternalServerError), status.Code)
	require.NotNil(t, status.Details)
	assert.EqualValues(t, 1, status.Details.RetryAfterSeconds)
}

// TestStorageImpl_Get_StalledLockWaitDoesNotPinPoolConnection is RC1's dedicated
// regression test (see .omc/plans/storage-locking-rewrite.md, Phase 1 step 1):
// Get used to take a pool connection *before* acquiring the per-key lock, so a
// stalled lock wait held that connection idle for the whole wait. With a
// size-1 pool, this proves the opposite: while Get is genuinely blocked
// waiting for a contended key lock, the pool's only connection remains free
// for an unrelated caller to take.
func TestStorageImpl_Get_StalledLockWaitDoesNotPinPoolConnection(t *testing.T) {
	pool := NewPool(filepath.Join(t.TempDir(), "test.sq3"), 1, 0)
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch).(*StorageImpl)

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"

	// Hold the write lock on key, as a concurrent writer would, so Get's
	// RLock call genuinely blocks rather than succeeding immediately.
	require.NoError(t, s.locks.Lock(context.Background(), key))

	getDone := make(chan error, 1)
	go func() {
		getDone <- s.Get(context.Background(), key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	}()

	// Give Get time to reach and genuinely block on the lock wait.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-getDone:
		t.Fatal("Get returned before the key lock was released -- it should still be blocked on the lock wait")
	default:
	}

	// The pool's only connection must still be available: a stalled Get
	// lock-wait must not have taken it first.
	takeCtx, takeCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer takeCancel()
	conn, err := pool.Take(takeCtx)
	require.NoError(t, err, "pool connection must remain available while Get is blocked waiting for a contended key lock")
	pool.Put(conn)

	// Release the lock and let the blocked Get complete.
	s.locks.Unlock(key)
	select {
	case err := <-getDone:
		assert.True(t, storage.IsNotFound(err), "expected NotFound for a key that was never created, got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Get did not complete after the key lock was released")
	}
}

// Note on acquireLockedConn's conn.SetInterrupt(ctx) rebind (see the doc
// comment on acquireLockedConn): binding it to the internal, poolTimeout-
// bounded budget context instead of the caller's real ctx would interrupt
// the connection out from under any Get/Delete whose total duration (e.g. a
// slow decode after a successful acquisition) exceeds poolTimeout, even
// though the caller's own ctx is still alive. This is deliberately not
// covered by a dedicated regression test: reliably forcing "acquisition
// succeeds, then the stale budget expires mid-use" requires the subsequent
// real work to straddle a sub-poolTimeout window, which isn't achievable
// without either a production-code delay hook or a flaky, machine-speed-
// dependent sleep -- the same class of problem as
// pkg/utils/mutex_test.go's fairness fix. The existing
// TestStorageImpl_PoolContentionReturnsServerTimeout,
// TestStorageImpl_LockContentionReturnsServerTimeout, and
// TestStorageImpl_Get_StalledLockWaitDoesNotPinPoolConnection tests above
// all still pass against the restructured acquireLockedConn, confirming no
// regression to the properties they do cover; this specific property rests
// on code inspection instead.

// installFakeMigrationTool points migrationBinaryPath (execMigrationTool's
// binary, used by the RC3 unlocked-exec path only) at a fixture shell script
// for the duration of the test, restoring the original afterward. The script
// sleeps briefly -- long enough for a test to perform a concurrent operation
// during the exec's unlocked window -- before either failing (if
// MIGRATION_FAKE_FAIL is set) or emitting the contents of
// MIGRATION_FAKE_OUTPUT_FILE to stdout.
func installFakeMigrationTool(t *testing.T) {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "fake-migration.sh")
	script := "#!/bin/sh\nsleep 0.3\nif [ -n \"$MIGRATION_FAKE_FAIL\" ]; then\n  echo \"fake migration failure\" >&2\n  exit 1\nfi\ncat \"$MIGRATION_FAKE_OUTPUT_FILE\"\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	old := migrationBinaryPath
	migrationBinaryPath = scriptPath
	t.Cleanup(func() { migrationBinaryPath = old })
}

// gobPayloadNeedingMigration returns bytes that gob-encode successfully but
// fail with a "gob: wrong type" error when decoded into *v1beta1.SBOMSyft --
// exactly the trigger get() uses to detect an object needing external
// migration (a nested scalar type mismatch on a same-named field triggers
// gob's per-field "wrong type" error; a struct-vs-non-struct mismatch at the
// top level instead triggers a different, non-matching "type mismatch"
// message, so the mismatch must be one level deep).
func gobPayloadNeedingMigration(t *testing.T) []byte {
	t.Helper()
	type fakeMeta struct {
		Name int // real ObjectMeta.Name is a string -- scalar type mismatch
	}
	type fakeTop struct {
		ObjectMeta fakeMeta
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(fakeTop{ObjectMeta: fakeMeta{Name: 42}}))
	return buf.Bytes()
}

// TestStorageImpl_MigrateObjectUnlocked_ConcurrentWriteWins is RC3's first
// dedicated regression test (N11, see .omc/plans/storage-locking-rewrite.md,
// Phase 1 step 3): a concurrent write landing during migration's unlocked
// exec window must win over the migration's own (now-stale) save branch,
// not be silently clobbered by it.
func TestStorageImpl_MigrateObjectUnlocked_ConcurrentWriteWins(t *testing.T) {
	installFakeMigrationTool(t)

	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, sch).(*StorageImpl)

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), gobPayloadNeedingMigration(t), 0644))

	outFile := filepath.Join(t.TempDir(), "migrated.json")
	migratedJSON, err := json.Marshal(&v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{Name: "toto"},
		Spec:       v1beta1.SBOMSyftSpec{Metadata: v1beta1.SPDXMeta{Tool: v1beta1.ToolMeta{Name: "stale-migrated"}}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(outFile, migratedJSON, 0644))
	t.Setenv("MIGRATION_FAKE_OUTPUT_FILE", outFile)

	getDone := make(chan error, 1)
	out := &v1beta1.SBOMSyft{}
	go func() {
		getDone <- s.Get(context.Background(), key, storage.GetOptions{}, out)
	}()

	// Land inside the fake tool's sleep window, while the write lock is
	// released for the exec: acquire it, write a new valid object directly
	// (simulating a concurrent writer's full Create/Update cycle), then
	// release it.
	time.Sleep(100 * time.Millisecond)
	lockAcquired := make(chan struct{})
	go func() {
		require.NoError(t, s.locks.Lock(context.Background(), key))
		close(lockAcquired)
		conn, connErr := pool.Take(context.Background())
		require.NoError(t, connErr)
		newObj := &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{Name: "toto"},
			Spec:       v1beta1.SBOMSyftSpec{Metadata: v1beta1.SPDXMeta{Tool: v1beta1.ToolMeta{Name: "concurrent-writer"}}},
		}
		_, saveErr := s.saveObject(conn, key, newObj, nil, "")
		require.NoError(t, saveErr)
		pool.Put(conn)
		s.locks.Unlock(key)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("concurrent writer could not acquire the key lock -- migration did not release it during the exec")
	}

	select {
	case err := <-getDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Get did not complete")
	}

	assert.Equal(t, "concurrent-writer", out.Spec.Metadata.Tool.Name, "Get must return the concurrent writer's object, not the stale migration result")

	onDisk := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(context.Background(), key, storage.GetOptions{}, onDisk))
	assert.Equal(t, "concurrent-writer", onDisk.Spec.Metadata.Tool.Name, "the migration save branch must not have overwritten the concurrent writer's object on disk")
}

// TestStorageImpl_MigrateObjectUnlocked_ConcurrentDeleteNotResurrected is
// RC3's second dedicated regression test (N11): a concurrent Delete landing
// during migration's unlocked exec window must not be resurrected by the
// migration's own (stale) save branch once the lock is re-acquired.
func TestStorageImpl_MigrateObjectUnlocked_ConcurrentDeleteNotResurrected(t *testing.T) {
	installFakeMigrationTool(t)

	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, sch).(*StorageImpl)

	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), gobPayloadNeedingMigration(t), 0644))

	outFile := filepath.Join(t.TempDir(), "migrated.json")
	migratedJSON, err := json.Marshal(&v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{Name: "toto"},
		Spec:       v1beta1.SBOMSyftSpec{Metadata: v1beta1.SPDXMeta{Tool: v1beta1.ToolMeta{Name: "stale-migrated"}}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(outFile, migratedJSON, 0644))
	t.Setenv("MIGRATION_FAKE_OUTPUT_FILE", outFile)

	getDone := make(chan error, 1)
	out := &v1beta1.SBOMSyft{}
	go func() {
		getDone <- s.Get(context.Background(), key, storage.GetOptions{}, out)
	}()

	// Land inside the fake tool's sleep window, while the write lock is
	// released for the exec: delete the key directly, simulating a
	// concurrent Delete winning the race.
	time.Sleep(100 * time.Millisecond)
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- s.Delete(context.Background(), key, &v1beta1.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{})
	}()
	select {
	case err := <-deleteDone:
		require.NoError(t, err, "concurrent Delete must succeed -- migration must have released the key lock during the exec")
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Delete did not complete -- migration may still be holding the key lock")
	}

	select {
	case err := <-getDone:
		assert.True(t, storage.IsNotFound(err), "expected NotFound: a concurrent Delete during migration must not be resurrected by the save branch, got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Get did not complete")
	}

	// Confirm the object stays gone: the migration's save branch must not
	// have resurrected it after the fact either.
	fresh := &v1beta1.SBOMSyft{}
	freshErr := s.Get(context.Background(), key, storage.GetOptions{}, fresh)
	assert.True(t, storage.IsNotFound(freshErr), "object must remain deleted, got err=%v obj=%+v", freshErr, fresh)
}

// TestSetPoolTimeout verifies the config-driven setter mirrors NewPool's fallback
// convention: a positive duration is applied verbatim, and a non-positive one resets
// poolTimeout to DefaultPoolTimeout rather than being applied literally.
func TestSetPoolTimeout(t *testing.T) {
	old := poolTimeout
	defer func() { poolTimeout = old }()

	SetPoolTimeout(250 * time.Millisecond)
	assert.Equal(t, 250*time.Millisecond, poolTimeout)

	SetPoolTimeout(0)
	assert.Equal(t, DefaultPoolTimeout, poolTimeout)

	SetPoolTimeout(-time.Second)
	assert.Equal(t, DefaultPoolTimeout, poolTimeout)
}

func TestStorageImpl_GetList_LabelSelectorWithPagination(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer pool.Close()

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)

	ctx := context.Background()
	for i := range 10 {
		label := "bar"
		if i%2 == 0 {
			label = "foo"
		}

		name := fmt.Sprintf("sbom-%02d", i)
		key := fmt.Sprintf("/spdx.softwarecomposition.kubescape.io/sbomsyfts/default/%s", name)

		obj := &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Labels: map[string]string{
					"app": label,
				},
			},
		}
		require.NoError(t, s.Create(ctx, key, obj, nil, 0))
	}

	predicate := storage.SelectionPredicate{
		Label: labels.SelectorFromSet(
			labels.Set{
				"app": "foo",
			},
		),
		Field:    fields.Everything(),
		GetAttrs: storage.DefaultNamespaceScopedAttr,
		Limit:    5,
	}

	var names []string
	continueToken := ""
	predicate.Continue = continueToken

	opts := storage.ListOptions{
		Predicate: predicate,
	}
	list := &v1beta1.SBOMSyftList{}
	err := s.GetList(ctx, "/spdx.softwarecomposition.kubescape.io/sbomsyfts/default", opts, list)
	require.NoError(t, err)

	for _, item := range list.Items {
		require.Equal(t, "foo", item.Labels["app"])
		names = append(names, item.Name)
	}

	assert.ElementsMatch(t,
		[]string{
			"sbom-00",
			"sbom-02",
			"sbom-04",
			"sbom-06",
			"sbom-08",
		},
		names,
	)
}

// TestStorageImpl_GetList_FullSpec_MultiPage verifies that the ResourceVersionFullSpec
// branch (which fetches full objects via s.get() rather than just SQLite metadata)
// still returns correct results when a single GetList call needs multiple internal
// pages to fill the requested limit.
func TestStorageImpl_GetList_FullSpec_MultiPage(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer pool.Close()

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)

	ctx := context.Background()
	keyPrefix := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/default"
	for i := range 4 {
		label := "bar"
		if i%2 == 1 {
			label = "foo"
		}
		name := fmt.Sprintf("sbom-%02d", i)
		key := fmt.Sprintf("%s/%s", keyPrefix, name)
		obj := &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Labels:    map[string]string{"app": label},
			},
		}
		require.NoError(t, s.Create(ctx, key, obj.DeepCopyObject(), nil, 0))
	}

	predicate := storage.SelectionPredicate{
		Label:    labels.SelectorFromSet(labels.Set{"app": "foo"}),
		Field:    fields.Everything(),
		GetAttrs: storage.DefaultNamespaceScopedAttr,
		Limit:    1,
	}
	opts := storage.ListOptions{
		Predicate:       predicate,
		ResourceVersion: softwarecomposition.ResourceVersionFullSpec,
	}
	list := &v1beta1.SBOMSyftList{}
	require.NoError(t, s.GetList(ctx, keyPrefix, opts, list))

	require.Len(t, list.Items, 1)
	assert.Equal(t, "sbom-01", list.Items[0].Name)
	assert.Equal(t, "foo", list.Items[0].Labels["app"])
}

// TestStorageImpl_GetList_ReleasesConnectionBetweenPages proves the fix for the
// GetListWithConn per-page pagination nuance: GetList (the connection-less wrapper)
// must not hold a pool connection across an internal page boundary while stalled on
// a per-key read lock for a later page's ResourceVersionFullSpec object fetch.
//
// With a size-1 connection pool, a single GetList call needing two internal pages
// is forced to stall on a held write lock while fetching page 1's object. A second
// goroutine blocks trying to Take() the pool's only connection throughout. Because
// Go delivers a channel send directly to an already-waiting receiver, that second
// goroutine is guaranteed to win the connection the instant page 1's Put() happens
// -- strictly before GetList's own goroutine can re-Take() it for page 2 -- if and
// only if GetList releases the connection after each page rather than holding it
// for the whole call. A second, independently-controlled lock stall on page 2's
// object proves the point isn't a fluke of scheduling: releasing the connection
// happens well before page 2 (and the whole call) is done.
func TestStorageImpl_GetList_ReleasesConnectionBetweenPages(t *testing.T) {
	dir := t.TempDir()
	pool := NewPool(filepath.Join(dir, "test.sq3"), 1, 0)
	require.NotNil(t, pool)
	defer pool.Close()

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch).(*StorageImpl)

	ctx := context.Background()
	keyPrefix := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/default"
	keyPage1 := keyPrefix + "/sbom-00" // label "bar" -- fetched but filtered out on page 1
	keyPage2 := keyPrefix + "/sbom-01" // label "foo" -- matches, fetched on page 2
	for i, label := range []string{"bar", "foo"} {
		name := fmt.Sprintf("sbom-%02d", i)
		key := fmt.Sprintf("%s/%s", keyPrefix, name)
		obj := &v1beta1.SBOMSyft{
			ObjectMeta: v1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Labels:    map[string]string{"app": label},
			},
		}
		require.NoError(t, s.Create(ctx, key, obj.DeepCopyObject(), nil, 0))
	}

	// Hold both objects' write locks up front so GetList's page-1 and page-2 full-spec
	// fetches each stall until explicitly released below.
	require.NoError(t, s.locks.Lock(ctx, keyPage1))
	require.NoError(t, s.locks.Lock(ctx, keyPage2))

	predicate := storage.SelectionPredicate{
		Label:    labels.SelectorFromSet(labels.Set{"app": "foo"}),
		Field:    fields.Everything(),
		GetAttrs: storage.DefaultNamespaceScopedAttr,
		Limit:    1,
	}
	opts := storage.ListOptions{
		Predicate:       predicate,
		ResourceVersion: softwarecomposition.ResourceVersionFullSpec,
	}

	listErrCh := make(chan error, 1)
	list := &v1beta1.SBOMSyftList{}
	go func() {
		listErrCh <- s.GetList(ctx, keyPrefix, opts, list)
	}()

	// Give GetList's goroutine time to Take() the pool's only connection for page 1
	// and block inside s.get() waiting on keyPage1's lock.
	time.Sleep(50 * time.Millisecond)

	watcherGotConnAt := make(chan time.Time, 1)
	go func() {
		conn, err := pool.Take(ctx)
		if err != nil {
			return
		}
		watcherGotConnAt <- time.Now()
		pool.Put(conn)
	}()

	// Give the watcher time to block on Take() (the pool is exhausted by page 1)
	// before we release page 1's lock.
	time.Sleep(50 * time.Millisecond)

	unlockedPage1At := time.Now()
	s.locks.Unlock(keyPage1)

	var gotConnAt time.Time
	select {
	case gotConnAt = <-watcherGotConnAt:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher never acquired the pool connection after page 1's lock was released")
	}

	// Page 2 is still stalled on its own lock at this point, and stays that way for
	// a further 200ms. If GetList had held the connection across the page boundary
	// (the pre-fix behavior), the watcher could not have gotten it until page 2 (and
	// the whole call) finished releasing it. A generous 300ms bound comfortably
	// separates "handed over right after page 1" from "held through page 2".
	assert.Less(t, gotConnAt.Sub(unlockedPage1At), 300*time.Millisecond,
		"pool connection was not released promptly after page 1 -- GetList may be holding it across the page boundary")

	time.Sleep(200 * time.Millisecond)
	s.locks.Unlock(keyPage2)

	select {
	case err := <-listErrCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("GetList never completed after page 2's lock was released")
	}

	require.Len(t, list.Items, 1)
	assert.Equal(t, "sbom-01", list.Items[0].Name)
}
