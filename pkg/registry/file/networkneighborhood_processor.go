package file

import (
	"context"
	"fmt"
	"strconv"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"k8s.io/apimachinery/pkg/runtime"
)

type NetworkNeighborhoodProcessor struct {
	maxNetworkNeighborhoodSize int
	// collapseSettings is the lookup hook the deflate path consults for
	// per-prefix thresholds. Defaults to dynamicpathdetector.DefaultCollapseSettings;
	// production wiring may override via SetCollapseSettings to a provider that
	// reads the cluster-scoped CollapseConfiguration "default" CR.
	collapseSettings dynamicpathdetector.CollapseSettingsProvider
}

func NewNetworkNeighborhoodProcessor(cfg config.Config) *NetworkNeighborhoodProcessor {
	return &NetworkNeighborhoodProcessor{
		maxNetworkNeighborhoodSize: cfg.MaxNetworkNeighborhoodSize,
		collapseSettings:           dynamicpathdetector.DefaultCollapseSettings,
	}
}

// SetCollapseSettings overrides the provider the deflate path uses to fetch
// effective thresholds. Pass dynamicpathdetector.DefaultCollapseSettings to
// fall back to compiled-in defaults; production wiring passes a provider
// that reads the CollapseConfiguration CR.
func (a *NetworkNeighborhoodProcessor) SetCollapseSettings(p dynamicpathdetector.CollapseSettingsProvider) {
	if p == nil {
		a.collapseSettings = dynamicpathdetector.DefaultCollapseSettings
		return
	}
	a.collapseSettings = p
}

// effectiveCollapseSettings is the safe accessor for the deflate path. It
// returns the result of the configured provider, or — when the processor
// was constructed without using NewNetworkNeighborhoodProcessor (zero-value
// field, no factory call) — the compiled-in defaults.
func (a NetworkNeighborhoodProcessor) effectiveCollapseSettings() dynamicpathdetector.CollapseSettings {
	if a.collapseSettings == nil {
		return dynamicpathdetector.DefaultCollapseSettings()
	}
	return a.collapseSettings()
}

var _ Processor = (*NetworkNeighborhoodProcessor)(nil)

func (a NetworkNeighborhoodProcessor) AfterCreate(_ context.Context, _ runtime.Object) error {
	return nil
}

func (a NetworkNeighborhoodProcessor) PreSave(_ context.Context, object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.NetworkNeighborhood)
	if !ok {
		return fmt.Errorf("given object is not an NetworkNeighborhood")
	}

	// set schema version
	profile.SchemaVersion = SchemaVersion

	// size is the sum of all ingress/egress in all containers
	var size int

	settings := a.effectiveCollapseSettings()

	// Define a function to process a slice of containers
	processContainers := func(containers []softwarecomposition.NetworkNeighborhoodContainer) []softwarecomposition.NetworkNeighborhoodContainer {
		for i, container := range containers {
			containers[i] = deflateNetworkNeighborhoodContainer(container, settings)
			size += len(containers[i].Ingress)
			size += len(containers[i].Egress)
		}
		return containers
	}

	// Use the function for InitContainers, EphemeralContainers and Containers
	profile.Spec.EphemeralContainers = processContainers(profile.Spec.EphemeralContainers)
	profile.Spec.InitContainers = processContainers(profile.Spec.InitContainers)
	profile.Spec.Containers = processContainers(profile.Spec.Containers)

	// check the size of the profile
	if size > a.maxNetworkNeighborhoodSize {
		return fmt.Errorf("application profile size exceeds the limit of %d: %w", a.maxNetworkNeighborhoodSize, ObjectTooLargeError)
	}

	// make sure annotations are initialized
	if profile.Annotations == nil {
		profile.Annotations = make(map[string]string)
	}
	profile.Annotations[helpers.ResourceSizeMetadataKey] = strconv.Itoa(size)
	return nil
}

func (a NetworkNeighborhoodProcessor) SetStorage(_ ContainerProfileStorage) {}

func deflateNetworkNeighborhoodContainer(container softwarecomposition.NetworkNeighborhoodContainer, settings dynamicpathdetector.CollapseSettings) softwarecomposition.NetworkNeighborhoodContainer {
	return softwarecomposition.NetworkNeighborhoodContainer{
		Name:    container.Name,
		Ingress: deflateNetworkNeighbors(container.Ingress, settings),
		Egress:  deflateNetworkNeighbors(container.Egress, settings),
	}
}

// NetworkNeighbors are merged on Identifier
// DNSNames are deduplicated
// Ports are merged on Name
// Then, groups of entries differing only by IP are collapsed into CIDR-bearing
// entries once their count exceeds settings.NetworkIPGroupThreshold (see
// collapseIPGroups). That second pass is a fixpoint (AC10): re-running it on
// its own output leaves already-collapsed entries untouched, so repeated saves
// are idempotent.
func deflateNetworkNeighbors(in []softwarecomposition.NetworkNeighbor, settings dynamicpathdetector.CollapseSettings) []softwarecomposition.NetworkNeighbor {
	if in == nil {
		return nil
	}
	out := make([]softwarecomposition.NetworkNeighbor, 0)
	seen := map[string]int{}
	toDeflate := mapset.NewThreadUnsafeSet[int]()
	for _, item := range in {
		if index, ok := seen[item.Identifier]; ok {
			out[index].DNSNames = append(out[index].DNSNames, item.DNSNames...)
			out[index].Ports = append(out[index].Ports, item.Ports...)
			toDeflate.Add(index)
		} else {
			out = append(out, item)
			seen[item.Identifier] = len(out) - 1 // index of the appended item
		}
	}
	for _, i := range mapset.Sorted(toDeflate) {
		out[i].DNSNames = DeflateSortString(out[i].DNSNames)
		out[i].Ports = DeflateStringer(out[i].Ports)
	}
	return collapseIPGroups(out, settings)
}
