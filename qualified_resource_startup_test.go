package main

import (
	"context"

	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file"
	cp "github.com/kubescape/storage/pkg/registry/softwarecomposition/containerprofile"
	sf "github.com/kubescape/storage/pkg/registry/softwarecomposition/sbomsyftfiltereds"
	ss "github.com/kubescape/storage/pkg/registry/softwarecomposition/sbomsyfts"
	sp "github.com/kubescape/storage/pkg/registry/softwarecomposition/seccompprofiles"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternal "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/audit"
	"k8s.io/apiserver/pkg/endpoints"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/client-go/restmapper"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestQualifiedResourceRESTPersistence(t *testing.T) {
	sch := runtime.NewScheme()
	install.Install(sch)
	opts := &options.StorageFactoryRestOptionsFactory{StorageFactory: &options.SimpleStorageFactory{}}
	type factory func(storage.Interface, bool) (rest.StandardStorage, error)
	cases := []struct {
		singular, plural string
		obj              runtime.Object
		makeREST         factory
	}{
		{"containerprofile", "containerprofiles", &softwarecomposition.ContainerProfile{Spec: softwarecomposition.ContainerProfileSpec{ImageTag: "example:1"}}, func(s storage.Interface, custom bool) (rest.StandardStorage, error) {
			if custom {
				return cp.NewCustomREST(sch, s, opts)
			}
			return cp.NewREST(sch, s, opts)
		}},
		{"sbomsyft", "sbomsyfts", &softwarecomposition.SBOMSyft{}, func(s storage.Interface, custom bool) (rest.StandardStorage, error) {
			if custom {
				return ss.NewCustomREST(sch, s, opts)
			}
			return ss.NewREST(sch, s, opts)
		}},
		{"sbomsyftfiltered", "sbomsyftfiltereds", &softwarecomposition.SBOMSyftFiltered{}, func(s storage.Interface, custom bool) (rest.StandardStorage, error) {
			if custom {
				return sf.NewCustomREST(sch, s, opts)
			}
			return sf.NewREST(sch, s, opts)
		}},
		{"seccompprofile", "seccompprofiles", &softwarecomposition.SeccompProfile{}, func(s storage.Interface, custom bool) (rest.StandardStorage, error) {
			if custom {
				return sp.NewCustomREST(sch, s, opts)
			}
			return sp.NewREST(sch, s, opts)
		}},
	}
	for _, tc := range cases {
		for _, native := range []bool{false, true} {

			name := tc.plural + "/file"
			if native {
				name = tc.plural + "/sqlite"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				db := filepath.Join(root, "metadata.sq3")
				pool := file.NewPool(db, 3, 0)
				defer func() { require.NoError(t, pool.Close()) }()
				fs := afero.NewOsFs()
				dispatcher := file.NewWatchDispatcher()
				var s storage.Interface
				var gate *file.WriteGate
				var objectStore *file.ObjectStore
				open := func() {
					s = file.NewStorageImpl(fs, root, pool, dispatcher, sch)
					if native {
						var err error
						gate, err = file.NewWriteGate(context.Background(), pool)
						require.NoError(t, err)
						if tc.singular == "containerprofile" {
							p := file.NewContainerProfileProcessor(config.Config{DefaultNamespace: "ns", MaxContainerProfileSize: 40000}, nil)
							p.Interval = 0
							objectStore, err = file.NewObjectStore(pool, db, dispatcher, sch, p, s, gate, file.ObjectStoreOptions{CheckpointInterval: time.Hour})
							require.NoError(t, err)
							s = objectStore
						} else {
							s.(*file.StorageImpl).SetWriteGate(gate)
						}
					}
				}
				closeStore := func() {
					if native {
						if objectStore != nil {
							require.NoError(t, objectStore.Close())
							objectStore = nil
						}
						require.NoError(t, gate.Close())
						gate = nil
					}
				}
				open()
				defer closeStore()
				ctx := request.WithNamespace(context.Background(), "ns")
				oldKey := "/" + softwarecomposition.GroupName + "/" + tc.singular + "/ns/existing"
				obj := tc.obj.DeepCopyObject()
				m, err := meta.Accessor(obj)
				require.NoError(t, err)
				m.SetName("existing")
				m.SetNamespace("ns")
				m.SetUID("preserved-uid")
				m.SetLabels(map[string]string{"keep": "yes"})
				require.NoError(t, s.Create(ctx, oldKey, obj, nil, 0))
				before := tc.obj.DeepCopyObject()
				require.NoError(t, s.Get(ctx, oldKey, storage.GetOptions{}, before))
				closeStore()
				require.NoError(t, pool.Close())
				// Seed actual legacy SQL even when the new backend normalizes
				// caller keys; otherwise the native migration check is vacuous.
				if tc.singular == "containerprofile" {
					c, e := sqlite.OpenConn(db)
					require.NoError(t, e)
					for _, table := range []string{"metadata", "payloads", "time_series"} {
						require.NoError(t, sqlitex.Execute(c, `UPDATE `+table+` SET kind=? WHERE kind=?`, &sqlitex.ExecOptions{Args: []any{tc.singular, tc.plural}}))
					}
					require.NoError(t, c.Close())
				}
				pool = file.NewPool(db, 3, 0)
				if native {
					gate, err = file.NewWriteGate(ctx, pool)
					require.NoError(t, err)
				}
				err = migratePersistentStorage(ctx, pool, gate, fs, root, config.Config{ContainerProfileSqliteBackend: native, PoolTimeout: time.Second})
				require.NoError(t, err)
				if gate != nil {
					require.NoError(t, gate.Close())
					gate = nil
				}
				require.NoError(t, pool.Close())
				pool = file.NewPool(db, 3, 0)
				open()
				for _, custom := range []bool{false, true} {
					r, err := tc.makeREST(s, custom)
					require.NoError(t, err)
					require.Equal(t, tc.singular, r.(rest.SingularNameProvider).GetSingularName())
					verifyPrefixDiscovery(t, sch, r, tc.plural, tc.singular)
					got, err := r.Get(ctx, "existing", &metav1.GetOptions{})
					require.NoError(t, err)
					require.Equal(t, before, got)
					list, err := r.List(ctx, &metainternal.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec})
					require.NoError(t, err)
					items, err := meta.ExtractList(list)
					require.NoError(t, err)
					require.Len(t, items, 1)
					require.Equal(t, before, items[0])
				}
				for _, custom := range []bool{false, true} {
					r, err := tc.makeREST(s, custom)
					require.NoError(t, err)
					fresh := tc.obj.DeepCopyObject()
					fm, _ := meta.Accessor(fresh)
					newName := "legacy-rest"
					if custom {
						newName = "custom-rest"
					}
					fm.SetName(newName)
					_, err = r.Create(ctx, fresh, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
					require.NoError(t, err)
					other, err := tc.makeREST(s, !custom)
					require.NoError(t, err)
					_, err = other.Get(ctx, newName, &metav1.GetOptions{})
					require.NoError(t, err)
					if !native || tc.singular != "containerprofile" {
						require.FileExists(t, filepath.Join(root, softwarecomposition.GroupName, tc.plural, "ns", newName+".g"))
					}
					w, err := r.Watch(ctx, &metainternal.ListOptions{ResourceVersion: "0", FieldSelector: fields.OneTermEqualSelector("metadata.name", newName)})
					require.NoError(t, err)
					current, err := r.Get(ctx, newName, &metav1.GetOptions{})
					require.NoError(t, err)
					cm, _ := meta.Accessor(current)
					cm.SetLabels(map[string]string{"updated": "yes"})
					_, _, err = r.Update(ctx, newName, rest.DefaultUpdatedObjectInfo(current), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
					require.NoError(t, err)
					awaitPrefixEvent(t, w, watch.Modified)
					handler := verifyPrefixDiscovery(t, sch, r, tc.plural, tc.singular)
					gv := v1beta1.SchemeGroupVersion
					patch := httptest.NewRequest("PATCH", "/apis/"+gv.String()+"/namespaces/ns/"+tc.plural+"/"+newName, strings.NewReader(`{"metadata":{"labels":{"patched":"yes"}}}`))
					patch.Header.Set("Content-Type", "application/merge-patch+json")
					patch = patch.WithContext(request.WithRequestInfo(audit.WithAuditContext(patch.Context()), &request.RequestInfo{IsResourceRequest: true, Verb: "patch", APIGroup: gv.Group, APIVersion: gv.Version, Resource: tc.plural, Namespace: "ns", Name: newName}))
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, patch)
					require.Equal(t, 200, response.Code, response.Body.String())
					awaitPrefixEvent(t, w, watch.Modified)
					patched, err := other.Get(ctx, newName, &metav1.GetOptions{})
					require.NoError(t, err)
					pm, _ := meta.Accessor(patched)
					require.Equal(t, "yes", pm.GetLabels()["patched"])

					_, _, err = r.Delete(ctx, newName, rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
					require.NoError(t, err)
					awaitPrefixEvent(t, w, watch.Deleted)
					w.Stop()
					_, err = other.Get(ctx, newName, &metav1.GetOptions{})
					require.True(t, apierrors.IsNotFound(err), "%v", err)

				}
				c, err := pool.Take(ctx)
				require.NoError(t, err)
				require.NoError(t, sqlitex.Execute(c, `SELECT kind,count(*) FROM metadata GROUP BY kind`, &sqlitex.ExecOptions{ResultFunc: func(st *sqlite.Stmt) error {
					require.Equal(t, tc.plural, st.ColumnText(0))
					require.Equal(t, 1, st.ColumnInt(1))
					return nil
				}}))
				pool.Put(c)
				require.NoDirExists(t, filepath.Join(root, softwarecomposition.GroupName, tc.singular))
			})
		}
	}
}

// Exercise Kubernetes' actual discovery installer and the REST mapper used by
// clients, without opening a socket or requiring a cluster.
func verifyPrefixDiscovery(t *testing.T, sch *runtime.Scheme, r rest.Storage, plural, singular string) *restful.Container {
	t.Helper()
	container := restful.NewContainer()
	gv := v1beta1.SchemeGroupVersion
	group := endpoints.APIGroupVersion{
		Root: "/apis", GroupVersion: gv, Storage: map[string]rest.Storage{plural: r},
		Serializer: serializer.NewCodecFactory(sch), ParameterCodec: runtime.NewParameterCodec(sch),
		Creater: sch, Convertor: sch, UnsafeConvertor: runtime.UnsafeObjectConvertor(sch),
		Typer: sch, Defaulter: sch, Namer: meta.NewAccessor(),
		TypeConverter:              managedfields.NewDeducedTypeConverter(),
		EquivalentResourceRegistry: runtime.NewEquivalentResourceRegistry(),
	}
	_, _, err := group.InstallREST(container)
	require.NoError(t, err)
	response := httptest.NewRecorder()
	container.ServeHTTP(response, httptest.NewRequest("GET", "/apis/"+gv.String(), nil))
	require.Equal(t, 200, response.Code, response.Body.String())
	var resources metav1.APIResourceList
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &resources))
	require.Len(t, resources.APIResources, 1)
	require.Equal(t, plural, resources.APIResources[0].Name)
	require.Equal(t, singular, resources.APIResources[0].SingularName)
	version := metav1.GroupVersionForDiscovery{GroupVersion: gv.String(), Version: gv.Version}
	mapper := restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{{
		Group:              metav1.APIGroup{Name: gv.Group, Versions: []metav1.GroupVersionForDiscovery{version}, PreferredVersion: version},
		VersionedResources: map[string][]metav1.APIResource{gv.Version: resources.APIResources},
	}})
	resolved, err := mapper.ResourceFor(schema.GroupVersionResource{Group: gv.Group, Resource: singular})
	require.NoError(t, err)
	require.Equal(t, gv.WithResource(plural), resolved)
	return container
}

func awaitPrefixEvent(t *testing.T, w watch.Interface, want watch.EventType) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-w.ResultChan():
			require.True(t, ok, "watch closed before %s", want)
			if e.Type == want {
				return
			}
		case <-timer.C:
			t.Fatalf("watch did not deliver %s", want)
		}
	}
}

func TestQualifiedResourceStartupRefusesBeforeBackendMigration(t *testing.T) {
	for _, state := range []string{"rollback:pending", "conflict", "dry-run"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			pool := file.NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
			defer pool.Close()
			ctx := context.Background()
			c, err := pool.Take(ctx)
			require.NoError(t, err)
			require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('containerprofile','ns','old')`, nil))
			if state == "conflict" {
				require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('containerprofiles','ns','new')`, nil))
			}
			if state == "rollback:pending" {
				require.NoError(t, sqlitex.Execute(c, `INSERT INTO migration_state(name,state) VALUES('qualified-resource/containerprofile','rollback:pending')`, nil))
			}
			pool.Put(c)
			err = migratePersistentStorage(ctx, pool, nil, afero.NewOsFs(), root, config.Config{ContainerProfileMigrationDryRun: state == "dry-run", PoolTimeout: time.Second})
			require.Error(t, err)
			c, err = pool.Take(ctx)
			require.NoError(t, err)
			defer pool.Put(c)
			require.NoError(t, sqlitex.Execute(c, `SELECT count(*) FROM migration_state WHERE name='containerprofile'`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error {
				require.Zero(t, s.ColumnInt(0), "backend migration must not run after prefix failure")
				return nil
			}}))
		})
	}
}
