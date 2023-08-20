package file

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// VulnerabilitySummaryStorage implements a storage for vulnerability summaries.
//
// It provides vulnerability summaries for scopes like namespace and cluster. To get these summaries, the storage fetches existing stored VulnerabilitySummary objects and aggregates them on the fly.
type VulnerabilitySummaryStorage struct {
	realStore StorageImpl
	versioner storage.Versioner
}

func NewVulnerabilitySummaryStorage(appFs afero.Fs, root string) storage.Interface {
	return &VulnerabilitySummaryStorage{
		realStore: StorageImpl{
			appFs:           appFs,
			watchDispatcher: newWatchDispatcher(),
			root:            root,
			versioner:       storage.APIObjectVersioner{},
		},
		versioner: storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *VulnerabilitySummaryStorage) Versioner() storage.Versioner {
	return s.versioner
}

func (s *VulnerabilitySummaryStorage) Create(ctx context.Context, key string, obj, out runtime.Object, _ uint64) error {
	return storage.NewMethodNotImplementedError(key, "")
}

func (s *VulnerabilitySummaryStorage) Delete(ctx context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	return storage.NewMethodNotImplementedError(key, "")
}

func (s *VulnerabilitySummaryStorage) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	return nil, storage.NewMethodNotImplementedError(key, "")
}

func initVulnSummary(scope string) softwarecomposition.VulnerabilitySummary {
	var vulnSummary softwarecomposition.VulnerabilitySummary
	vulnSummary.APIVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
	vulnSummary.Kind = "VulnerabilitySummary"
	if scope != "cluster" {
		vulnSummary.Labels = map[string]string{"kubescape.io/workload-namespace": scope}
		vulnSummary.Namespace = scope
	}
	vulnSummary.Annotations = map[string]string{"kubescape.io/status": ""}
	vulnSummary.Name = scope
	vulnSummary.CreationTimestamp.Time = time.Now()

	return vulnSummary
}

func (s *VulnerabilitySummaryStorage) summarizeVulnerabilities(ctx context.Context, vsms []softwarecomposition.VulnerabilityManifestSummary, scope string) softwarecomposition.VulnerabilitySummary {
	_, span := otel.Tracer("").Start(ctx, "VulnerabilitySummaryStorage.summarizeVulnerabilities")
	span.SetAttributes(attribute.String("scope", scope))
	defer span.End()

	fullVulnSummary := initVulnSummary(scope)

	for i := range vsms {
		fullVulnSummary.Merge(&vsms[i])
	}

	return fullVulnSummary
}

func (s *VulnerabilitySummaryStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "VulnerabilitySummaryStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	var objects []runtime.Object
	var vsms []softwarecomposition.VulnerabilityManifestSummary
	var err error

	scope := filepath.Base(key)
	kind := "vulnerabilitymanifestsummaries"
	switch scope {
	case "cluster":
		objects, err = s.realStore.GetByCluster(ctx, kind)
	default:
		objects, err = s.realStore.GetByNamespace(ctx, kind, scope)
	}

	if err != nil {
		return err
	}

	for i := range objects {
		vsm, ok := objects[i].(*softwarecomposition.VulnerabilityManifestSummary)
		if !ok {
			return storage.NewKeyNotFoundError(key, 0)
		}
		vsms = append(vsms, *vsm)
	}

	vulnSumm := s.summarizeVulnerabilities(ctx, vsms, scope)
	data, err := json.Marshal(vulnSumm)
	if err != nil {
		logger.L().Ctx(ctx).Error("json marshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	err = json.Unmarshal(data, objPtr)
	if err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	return nil
}

func (s *VulnerabilitySummaryStorage) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	return storage.NewMethodNotImplementedError(key, "")
}

func (s *VulnerabilitySummaryStorage) GuaranteedUpdate(
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return storage.NewMethodNotImplementedError(key, "")
}

func (s *VulnerabilitySummaryStorage) Count(key string) (int64, error) {
	return 0, storage.NewMethodNotImplementedError(key, "")
}
