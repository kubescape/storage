package file

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// VulnSummaryStorageImpl offers a common interface for object marshaling/unmarshaling operations and
// hides all the storage-related operations behind it.
type VulnSummaryStorageImpl struct {
	appFs     afero.Fs
	root      string
	versioner storage.Versioner
}

var _ storage.Interface = &StorageImpl{}

func NewVulnSummaryStorageImpl(appFs afero.Fs, root string) storage.Interface {
	return &VulnSummaryStorageImpl{
		appFs:     appFs,
		root:      root,
		versioner: storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *VulnSummaryStorageImpl) Versioner() storage.Versioner {
	return s.versioner
}

func (s *VulnSummaryStorageImpl) Create(ctx context.Context, key string, obj, out runtime.Object, _ uint64) error {
	_, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.Create")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return storage.NewMethodNotImplementError(key, "")
}

func (s *VulnSummaryStorageImpl) Delete(ctx context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	_, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.Delete")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return storage.NewMethodNotImplementError(key, "")
}

// add uni test
func (s *VulnSummaryStorageImpl) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	_, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.Watch")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return nil, storage.NewMethodNotImplementError(key, "")
}

// add uni test
func (s *VulnSummaryStorageImpl) getOneVulnManifestSummary(ctx context.Context, path string) (*softwarecomposition.VulnerabilityManifestSummary, error) {
	var vulnManifestSummary softwarecomposition.VulnerabilityManifestSummary

	b, err := afero.ReadFile(s.appFs, path)
	if err != nil {
		if errors.Is(err, afero.ErrFileNotFound) {
			return nil, storage.NewKeyNotFoundError(path, 0)
		}
		logger.L().Ctx(ctx).Error("read file failed", helpers.Error(err), helpers.String("key", path))
		return nil, err
	}
	err = json.Unmarshal(b, &vulnManifestSummary)
	if err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", path))
		return nil, err
	}
	if _, exist := vulnManifestSummary.GetLabels()["kubescape.io/workload-namespace"]; !exist {
		return nil, storage.NewKeyNotFoundError(path, 0)
	}
	return &vulnManifestSummary, nil
}

// add uni test
func initNamespaceVulnSummary(key string) softwarecomposition.VulnerabilitySummary {
	namespace := filepath.Base(key)
	var vulnSummaryByNamespace softwarecomposition.VulnerabilitySummary
	vulnSummaryByNamespace.APIVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
	vulnSummaryByNamespace.Kind = "VulnerabilitySummary"
	vulnSummaryByNamespace.Labels = map[string]string{"kubescape.io/workload-namespace": namespace}
	vulnSummaryByNamespace.Annotations = map[string]string{"kubescape.io/status": ""}
	vulnSummaryByNamespace.Name = namespace
	vulnSummaryByNamespace.Namespace = namespace
	vulnSummaryByNamespace.CreationTimestamp.Time = time.Now()

	return vulnSummaryByNamespace
}

func updateVulnCounters(aggregatedCounters *softwarecomposition.VulnerabilityCounters, counters *softwarecomposition.VulnerabilityCounters) {
	aggregatedCounters.All += counters.All
	aggregatedCounters.Relevant += counters.Relevant
}

func updateSeverities(aggregatedSeverities *softwarecomposition.SeveritySummary, severities *softwarecomposition.SeveritySummary) {
	updateVulnCounters(&aggregatedSeverities.Critical, &severities.Critical)
	updateVulnCounters(&aggregatedSeverities.High, &severities.High)
	updateVulnCounters(&aggregatedSeverities.Medium, &severities.Medium)
	updateVulnCounters(&aggregatedSeverities.Low, &severities.Low)
	updateVulnCounters(&aggregatedSeverities.Negligible, &severities.Negligible)
	updateVulnCounters(&aggregatedSeverities.Unknown, &severities.Unknown)
}

func updateFullVulnSumm(fullVulnSumm *softwarecomposition.VulnerabilitySummary, vulnManifestSumm *softwarecomposition.VulnerabilityManifestSummary) {
	updateSeverities(&fullVulnSumm.Spec.Severities, &vulnManifestSumm.Spec.Severities)
	if vulnManifestSumm.Spec.Vulnerabilities.ImageVulnerabilitiesObj != (softwarecomposition.VulnerabilitiesObjScope{}) {
		fullVulnSumm.Spec.WorkloadVulnerabilitiesObj = append(fullVulnSumm.Spec.WorkloadVulnerabilitiesObj, vulnManifestSumm.Spec.Vulnerabilities.ImageVulnerabilitiesObj)
	}
	if vulnManifestSumm.Spec.Vulnerabilities.WorkloadVulnerabilitiesObj != (softwarecomposition.VulnerabilitiesObjScope{}) {
		fullVulnSumm.Spec.WorkloadVulnerabilitiesObj = append(fullVulnSumm.Spec.WorkloadVulnerabilitiesObj, vulnManifestSumm.Spec.Vulnerabilities.WorkloadVulnerabilitiesObj)
	}
}

func (s *VulnSummaryStorageImpl) getVulnManifestSummaryDirPath(key string) string {
	return filepath.Join(s.root, key, "..", "..", "..", "vulnerabilitymanifestsummaries")
}

// add uni test
func (s *VulnSummaryStorageImpl) aggregateVulnSummaryOverNamespace(ctx context.Context, fullVulnSumm *softwarecomposition.VulnerabilitySummary, path string) error {
	var err error

	vulnSumm, err := s.getOneVulnManifestSummary(ctx, path)
	if err != nil {
		return err
	}
	updateFullVulnSumm(fullVulnSumm, vulnSumm)

	return nil
}

// add uni test
func (s *VulnSummaryStorageImpl) aggregateVulnSummary(ctx context.Context, key string) (*softwarecomposition.VulnerabilitySummary, []error) {
	var errs []error
	fullVulnSumm := initNamespaceVulnSummary(key)

	namespace := filepath.Base(key)
	afero.Walk(s.appFs, s.getVulnManifestSummaryDirPath(key), func(path string, info os.FileInfo, err error) error {
		if namespace != filepath.Base(filepath.Dir(path)) {
			return nil
		}
		if !strings.HasSuffix(path, jsonExt) {
			return nil
		}
		e := s.aggregateVulnSummaryOverNamespace(ctx, &fullVulnSumm, path)
		if err != nil {
			errs = append(errs, e)
		}
		return nil
	})

	if len(fullVulnSumm.Spec.WorkloadVulnerabilitiesObj) == 0 {
		return nil, errs
	}

	return &fullVulnSumm, errs
}

// add uni test
func (s *VulnSummaryStorageImpl) validateKeySupported(ctx context.Context, key string) error {
	_, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.Get.validateKeySupported")
	defer span.End()

	dataFullPath := filepath.Join(s.root, key)
	if filepath.Base(dataFullPath) == "vulnerabilitysummaries" {
		return storage.NewMethodNotImplementError(key, "")
	}
	return nil
}

// add uni test
func (s *VulnSummaryStorageImpl) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	err := s.validateKeySupported(ctx, key)
	if err != nil {
		return err
	}

	vulnSumm, errs := s.aggregateVulnSummary(ctx, key)
	for i := range errs {
		logger.L().Ctx(ctx).Warning("error per vuln Manifest file", helpers.Error(errs[i]), helpers.String("key", key))
	}
	if vulnSumm == nil {
		return storage.NewKeyNotFoundError(key, 0)
	}

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

// add uni test
func (s *VulnSummaryStorageImpl) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	_, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return storage.NewMethodNotImplementError(key, "")
}

// add uni test
func (s *VulnSummaryStorageImpl) GuaranteedUpdate(
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	_, span := otel.Tracer("").Start(ctx, "VulnSummaryStorageImpl.GuaranteedUpdate")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return storage.NewMethodNotImplementError(key, "")
}

// add uni test
func (s *VulnSummaryStorageImpl) Count(key string) (int64, error) {
	return 0, nil
}
