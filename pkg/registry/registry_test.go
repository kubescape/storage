package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apiserver/pkg/registry/rest"
)

// TestReadOnlyRESTInterfaceSurface pins the verb surface the endpoints installer derives by
// type assertion (k8s.io/apiserver/pkg/endpoints/installer.go): a ReadOnlyREST resource must
// advertise get and list only — no watch (computed resources have nothing to watch) and no
// mutating verbs (the storage layer rejects all mutations).
func TestReadOnlyRESTInterfaceSurface(t *testing.T) {
	var s rest.Storage = &ReadOnlyREST{}

	_, isWatcher := s.(rest.Watcher)
	assert.False(t, isWatcher, "ReadOnlyREST must not advertise the watch verb")
	_, isCreater := s.(rest.Creater)
	assert.False(t, isCreater, "ReadOnlyREST must not advertise the create verb")
	_, isUpdater := s.(rest.Updater)
	assert.False(t, isUpdater, "ReadOnlyREST must not advertise the update/patch verbs")
	_, isGracefulDeleter := s.(rest.GracefulDeleter)
	assert.False(t, isGracefulDeleter, "ReadOnlyREST must not advertise the delete verb")
	_, isCollectionDeleter := s.(rest.CollectionDeleter)
	assert.False(t, isCollectionDeleter, "ReadOnlyREST must not advertise the deletecollection verb")

	_, isGetter := s.(rest.Getter)
	assert.True(t, isGetter, "ReadOnlyREST must advertise the get verb")
	_, isLister := s.(rest.Lister)
	assert.True(t, isLister, "ReadOnlyREST must advertise the list verb")
	_, isScoper := s.(rest.Scoper)
	assert.True(t, isScoper)
	_, isSingular := s.(rest.SingularNameProvider)
	assert.True(t, isSingular)
}

// TestRESTKeepsWatchVerb is the keep-side regression guard of the hybrid watch strategy:
// dispatcher-backed resources registered through REST must keep advertising watch — only
// the computed resources wrapped in ReadOnlyREST drop it.
func TestRESTKeepsWatchVerb(t *testing.T) {
	var s rest.Storage = &REST{}

	_, isWatcher := s.(rest.Watcher)
	assert.True(t, isWatcher, "REST (dispatcher-backed resources) must keep advertising the watch verb")
	_, isLister := s.(rest.Lister)
	assert.True(t, isLister)
}
