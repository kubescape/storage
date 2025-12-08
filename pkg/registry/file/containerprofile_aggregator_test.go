package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeStorage implements ContainerProfileStorage with minimal behavior for tests.
// Only GetContainerProfileMetadata is used by ComputeAggregatedData; other methods are stubs.
type fakeStorage struct {
	profiles map[string]softwarecomposition.ContainerProfile
}

func (f *fakeStorage) WithConnection(ctx context.Context) (context.Context, func(), error) {
	return ctx, func() {}, nil
}
func (f *fakeStorage) BeginTransaction(ctx context.Context) (func(*error), error) {
	return func(*error) {}, nil
}

// TimeSeriesOperations
func (f *fakeStorage) ListTimeSeriesExpired(ctx context.Context, threshold time.Duration) ([]string, error) {
	return nil, nil
}
func (f *fakeStorage) ListTimeSeriesWithData(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeStorage) ListTimeSeriesContainers(ctx context.Context, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	return nil, nil
}
func (f *fakeStorage) DeleteTimeSeriesContainerEntries(ctx context.Context, key string) error {
	return nil
}
func (f *fakeStorage) ReplaceTimeSeriesContainerEntries(ctx context.Context, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error {
	return nil
}

// ContainerProfileStorage methods (stubs except GetContainerProfileMetadata)
func (f *fakeStorage) DeleteContainerProfile(ctx context.Context, key string) error {
	return nil
}
func (f *fakeStorage) GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	var p softwarecomposition.ContainerProfile
	return p, nil
}
func (f *fakeStorage) GetContainerProfileMetadata(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	if p, ok := f.profiles[key]; ok {
		return p, nil
	}
	var empty softwarecomposition.ContainerProfile
	return empty, nil
}
func (f *fakeStorage) GetSbom(ctx context.Context, key string) (softwarecomposition.SBOMSyft, error) {
	return softwarecomposition.SBOMSyft{}, nil
}
func (f *fakeStorage) GetTsContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	return softwarecomposition.ContainerProfile{}, nil
}
func (f *fakeStorage) SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error {
	return nil
}
func (f *fakeStorage) UpdateApplicationProfile(ctx context.Context, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time) error {
	return nil
}
func (f *fakeStorage) UpdateNetworkNeighborhood(ctx context.Context, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time) error {
	return nil
}
func (f *fakeStorage) GetStorageImpl() *StorageImpl {
	return nil
}
func (f *fakeStorage) WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	return nil
}

// Test ComputeAggregatedData with a small set of fake profiles.
func TestComputeAggregatedData_Basic(t *testing.T) {
	// Prepare fake profiles
	// Two child keys; both are main containers (ContainerType == "containers"),
	// both have Completed status and Full completion, with specific sync checksums.
	child1 := "/spdx.softwarecomposition.kubescape.io/containerprofile/ns/a"
	child2 := "/spdx.softwarecomposition.kubescape.io/containerprofile/ns/b"

	p1 := softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "a",
			Namespace: "ns",
			Annotations: map[string]string{
				helpers.ContainerTypeMetadataKey:  "containers",
				helpers.StatusMetadataKey:         helpers.Completed,
				helpers.CompletionMetadataKey:     helpers.Full,
				helpers.SyncChecksumMetadataKey:   "child-checksum-1",
				helpers.ResourceSizeMetadataKey:   "10",
				helpers.InstanceIDMetadataKey:     "id-1",
				helpers.ReportSeriesIdMetadataKey: "",
			},
		},
	}
	p2 := softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b",
			Namespace: "ns",
			Annotations: map[string]string{
				helpers.ContainerTypeMetadataKey:  "containers",
				helpers.StatusMetadataKey:         helpers.Completed,
				helpers.CompletionMetadataKey:     helpers.Full,
				helpers.SyncChecksumMetadataKey:   "child-checksum-2",
				helpers.ResourceSizeMetadataKey:   "20",
				helpers.InstanceIDMetadataKey:     "id-2",
				helpers.ReportSeriesIdMetadataKey: "",
			},
		},
	}

	fs := &fakeStorage{
		profiles: map[string]softwarecomposition.ContainerProfile{
			child1: p1,
			child2: p2,
		},
	}

	parts := map[string]string{
		child1: "",
		child2: "",
	}

	ctx := context.Background()
	status, completion, checksum := ComputeAggregatedData(fs, ctx, "/aggregate/key", parts)

	// Expect both completed => aggregated status Completed
	if status != helpers.Completed {
		t.Fatalf("expected status %q, got %q", helpers.Completed, status)
	}
	// Both children Full => aggregated completion Full
	if completion != helpers.Full {
		t.Fatalf("expected completion %q, got %q", helpers.Full, completion)
	}

	// Expected checksum is sha256(child-checksum-1 + child-checksum-2) where order is sorted by key.
	// Ensure we compute the same ordering as ComputeAggregatedData: keys are sorted ascending.
	keys := []string{child1, child2}
	// sort keys to match behavior
	// compute hasher
	h := sha256.New()
	for _, k := range keys {
		// use the checksums present in our fake profiles
		c := fs.profiles[k].Annotations[helpers.SyncChecksumMetadataKey]
		h.Write([]byte(c))
	}
	expected := hex.EncodeToString(h.Sum(nil))

	if checksum != expected {
		t.Fatalf("expected checksum %q, got %q", expected, checksum)
	}

	// Ensure parts map was populated with child checksums
	if parts[child1] != p1.Annotations[helpers.SyncChecksumMetadataKey] {
		t.Fatalf("parts[%s] not updated correctly: got %q", child1, parts[child1])
	}
	if parts[child2] != p2.Annotations[helpers.SyncChecksumMetadataKey] {
		t.Fatalf("parts[%s] not updated correctly: got %q", child2, parts[child2])
	}
}
