package file

import (
	"context"
	"fmt"
	"strconv"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/k8s-interface/names"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file/callstack"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

const (
	OpenDynamicThreshold     = 50
	EndpointDynamicThreshold = 100
)

type ApplicationProfileProcessor struct {
	defaultNamespace          string
	maxApplicationProfileSize int
	storageImpl               *StorageImpl
}

func NewApplicationProfileProcessor(cfg config.Config) *ApplicationProfileProcessor {
	return &ApplicationProfileProcessor{
		defaultNamespace:          cfg.DefaultNamespace,
		maxApplicationProfileSize: cfg.MaxApplicationProfileSize,
	}
}

var _ Processor = (*ApplicationProfileProcessor)(nil)

func (a *ApplicationProfileProcessor) AfterCreate(_ context.Context, _ *sqlite.Conn, _ runtime.Object) error {
	return nil
}

func (a *ApplicationProfileProcessor) PreSave(ctx context.Context, conn *sqlite.Conn, object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.ApplicationProfile)
	if !ok {
		return fmt.Errorf("given object is not an ApplicationProfile")
	}

	// size is the sum of all fields in all containers
	var size int

	// Define a function to process a slice of containers
	processContainers := func(containers []softwarecomposition.ApplicationProfileContainer) []softwarecomposition.ApplicationProfileContainer {
		for i, container := range containers {
			var sbomSet mapset.Set[string]
			// get files from corresponding sbom
			sbomName, err := names.ImageInfoToSlug(container.ImageTag, container.ImageID)
			if err == nil {
				sbom := softwarecomposition.SBOMSyft{}
				key := keysToPath("", "spdx.softwarecomposition.kubescape.io", "sbomsyft", a.defaultNamespace, sbomName)
				if err := a.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &sbom); err == nil {
					// fill sbomSet
					sbomSet = mapset.NewSet[string]()
					for _, f := range sbom.Spec.Syft.Files {
						sbomSet.Add(f.Location.RealPath)
					}
				} else {
					logger.L().Debug("failed to get sbom", loggerhelpers.Error(err), loggerhelpers.String("key", key))
				}
			} else {
				logger.L().Debug("failed to get sbom name", loggerhelpers.Error(err), loggerhelpers.String("imageTag", container.ImageTag), loggerhelpers.String("imageID", container.ImageID))
			}
			containers[i] = deflateApplicationProfileContainer(container, sbomSet)
			size += len(containers[i].Execs)
			size += len(containers[i].Opens)
			size += len(containers[i].Syscalls)
			size += len(containers[i].Capabilities)
			size += len(containers[i].Endpoints)
			size += len(containers[i].IdentifiedCallStacks)
		}
		return containers
	}

	// Use the function for InitContainers, EphemeralContainers and Containers
	profile.Spec.EphemeralContainers = processContainers(profile.Spec.EphemeralContainers)
	profile.Spec.InitContainers = processContainers(profile.Spec.InitContainers)
	profile.Spec.Containers = processContainers(profile.Spec.Containers)

	profile.Spec.Architectures = DeflateSortString(profile.Spec.Architectures)

	// check the size of the profile
	if size > a.maxApplicationProfileSize {
		return fmt.Errorf("application profile size exceeds the limit of %d: %w", a.maxApplicationProfileSize, ObjectTooLargeError)
	}

	// make sure annotations are initialized
	if profile.Annotations == nil {
		profile.Annotations = make(map[string]string)
	}
	profile.Annotations[helpers.ResourceSizeMetadataKey] = strconv.Itoa(size)
	return nil
}

func (a *ApplicationProfileProcessor) SetStorage(storageImpl *StorageImpl) {
	a.storageImpl = storageImpl
}

func deflateApplicationProfileContainer(container softwarecomposition.ApplicationProfileContainer, sbomSet mapset.Set[string]) softwarecomposition.ApplicationProfileContainer {
	opens, err := dynamicpathdetector.AnalyzeOpens(container.Opens, dynamicpathdetector.NewPathAnalyzer(OpenDynamicThreshold), sbomSet)
	if err != nil {
		logger.L().Debug("falling back to DeflateStringer for opens", loggerhelpers.Error(err))
		opens = DeflateStringer(container.Opens)
	}
	endpoints := dynamicpathdetector.AnalyzeEndpoints(&container.Endpoints, dynamicpathdetector.NewPathAnalyzer(EndpointDynamicThreshold))
	identifiedCallStacks := callstack.UnifyIdentifiedCallStacks(container.IdentifiedCallStacks)

	return softwarecomposition.ApplicationProfileContainer{
		Name:                 container.Name,
		Capabilities:         DeflateSortString(container.Capabilities),
		Execs:                DeflateStringer(container.Execs),
		Opens:                opens,
		Syscalls:             DeflateSortString(container.Syscalls),
		SeccompProfile:       container.SeccompProfile,
		Endpoints:            endpoints,
		ImageTag:             container.ImageTag,
		ImageID:              container.ImageID,
		PolicyByRuleId:       DeflateRulePolicies(container.PolicyByRuleId),
		IdentifiedCallStacks: identifiedCallStacks,
	}
}
