package file

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

const (
	containerProfilesResource = "containerprofiles"
	knownServersResource      = "knownservers"
	// containerProfileListLimit bounds the internal namespace listing used to
	// resolve a single workload-level Get; a namespace's container-profile count
	// is comfortably below this.
	containerProfileListLimit = 10000
)

// GeneratedNetworkPolicyStorage offers a storage solution for GeneratedNetworkPolicy objects, implementing custom business logic for these objects and using the underlying default storage implementation.
type GeneratedNetworkPolicyStorage struct {
	immutableStorage
	realStore StorageQuerier
}

func (s *GeneratedNetworkPolicyStorage) EnableResourceSizeEstimation(keysFunc storage.KeysFunc) error {
	return nil
}

func (s *GeneratedNetworkPolicyStorage) Stats(_ context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (s *GeneratedNetworkPolicyStorage) SetKeysFunc(_ storage.KeysFunc) {}

func (s *GeneratedNetworkPolicyStorage) CompactRevision() int64 {
	return 0
}

var _ storage.Interface = (*GeneratedNetworkPolicyStorage)(nil)

func NewGeneratedNetworkPolicyStorage(realStore StorageQuerier) storage.Interface {
	return &GeneratedNetworkPolicyStorage{
		realStore: realStore,
	}
}

func (s *GeneratedNetworkPolicyStorage) GetCurrentResourceVersion(_ context.Context) (uint64, error) {
	return 0, nil
}

// workloadPolicyName derives the workload-level GeneratedNetworkPolicy name for a
// ContainerProfile: <lower(kind)>-<workload-name>. ContainerProfiles are stored
// per-container (slug-named <lower(kind)>-<workload-name>-<container>-<hash>-<hash>),
// but a NetworkPolicy is workload-level — all containers of a pod share the pod's
// network namespace — so every container profile of a workload maps to the SAME
// name and their neighbors are unioned into one policy. This mirrors the name
// GenerateNetworkPolicy derives from the same labels, so the request name, the
// group key, and the generated policy name all agree.
func workloadPolicyName(cp *softwarecomposition.ContainerProfile) (string, bool) {
	kind, ok := cp.Labels[helpersv1.RelatedKindMetadataKey]
	if !ok {
		return "", false
	}
	name, ok := cp.Labels[helpersv1.RelatedNameMetadataKey]
	if !ok {
		name = cp.Name
	}
	return fmt.Sprintf("%s-%s", strings.ToLower(kind), name), true
}

// mergeWorkloadContainerProfiles unions the ingress/egress neighbors of a
// workload's per-container ContainerProfiles into a single profile, so one
// workload-level policy can be generated from it. The pod selector and workload
// labels are shared across a workload's containers, so they are taken from the
// first profile; the per-container name/hash labels are dropped. The result is a
// fresh object — the stored profiles are never mutated.
func mergeWorkloadContainerProfiles(group []*softwarecomposition.ContainerProfile) *softwarecomposition.ContainerProfile {
	base := group[0]

	// GenerateNetworkPolicy derives the policy name as <lower(kind)>-<name> where
	// name is the workload-name label, falling back to the object name. Set the
	// merged object's name to that fallback (the name PART only) so a profile
	// missing the workload-name label still yields <kind>-<name> and never a
	// doubled prefix.
	name, ok := base.Labels[helpersv1.RelatedNameMetadataKey]
	if !ok {
		name = base.Name
	}

	merged := &softwarecomposition.ContainerProfile{
		TypeMeta: base.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: base.Namespace,
			Labels:    map[string]string{},
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey: helpersv1.Completed,
			},
		},
	}
	for k, v := range base.Labels {
		if k == helpersv1.ContainerNameMetadataKey || k == helpersv1.TemplateHashKey {
			continue
		}
		merged.Labels[k] = v
	}
	// The pod selector is a property of the workload, identical across its
	// containers.
	merged.Spec.LabelSelector = base.Spec.LabelSelector
	for _, cp := range group {
		merged.Spec.Ingress = append(merged.Spec.Ingress, cp.Spec.Ingress...)
		merged.Spec.Egress = append(merged.Spec.Egress, cp.Spec.Egress...)
	}
	return merged
}

// aggregateWorkloadPolicy generates one workload-level GeneratedNetworkPolicy
// from a group of a workload's per-container ContainerProfiles and stamps it with
// the workload-level name.
func aggregateWorkloadPolicy(name string, group []*softwarecomposition.ContainerProfile, knownServers softwarecomposition.IKnownServersFinder, now metav1.Time) (softwarecomposition.GeneratedNetworkPolicy, error) {
	gnp, err := networkpolicy.GenerateNetworkPolicy(mergeWorkloadContainerProfiles(group), knownServers, now)
	if err != nil {
		return softwarecomposition.GeneratedNetworkPolicy{}, err
	}
	gnp.Name = name
	return gnp, nil
}

// listNamespaceContainerProfiles returns every ContainerProfile under the given
// container-profiles list key (a namespace-scoped prefix). It forces a full-spec
// listing: the default list path returns metadata only, but policy generation
// needs each profile's Spec (ingress/egress neighbors, pod selector).
func (s *GeneratedNetworkPolicyStorage) listNamespaceContainerProfiles(ctx context.Context, listKey string, opts storage.ListOptions) ([]softwarecomposition.ContainerProfile, error) {
	opts.ResourceVersion = softwarecomposition.ResourceVersionFullSpec
	cpList := &softwarecomposition.ContainerProfileList{}
	if err := s.realStore.GetList(ctx, listKey, opts, cpList); err != nil {
		return nil, err
	}
	return cpList.Items, nil
}

// Get generates and returns a single workload-level GeneratedNetworkPolicy.
//
// The requested key carries the workload-level name (<lower(kind)>-<workload-name>,
// matching the pre-migration NetworkNeighborhood naming). Because ContainerProfiles
// are per-container, we cannot look one up by the workload name directly: we list
// the namespace's profiles, select the ones belonging to the requested workload,
// union their neighbors, and generate one policy.
func (s *GeneratedNetworkPolicyStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "GeneratedNetworkPolicyStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	logger.L().Debug("GeneratedNetworkPolicyStorage.Get", helpers.String("key", key))

	cpKey := replaceKeyForKind(key, containerProfilesResource)
	keyParts := strings.Split(cpKey, "/")
	requestedName := keyParts[len(keyParts)-1]
	listKey := strings.Join(keyParts[:len(keyParts)-1], "/")

	items, err := s.listNamespaceContainerProfiles(ctx, listKey, storage.ListOptions{Predicate: storage.SelectionPredicate{Limit: containerProfileListLimit}})
	if err != nil {
		return err
	}

	var group []*softwarecomposition.ContainerProfile
	for i := range items {
		cp := &items[i]
		if !networkpolicy.IsAvailable(cp) {
			continue
		}
		if name, ok := workloadPolicyName(cp); ok && name == requestedName {
			group = append(group, cp)
		}
	}
	if len(group) == 0 {
		return storage.NewKeyNotFoundError(cpKey, 0)
	}

	knownServersListObjPtr := &softwarecomposition.KnownServerList{}
	if err := s.realStore.GetByCluster(ctx, softwarecomposition.GroupName, knownServersResource, knownServersListObjPtr); err != nil {
		return err
	}

	generatedNetworkPolicy, err := aggregateWorkloadPolicy(requestedName, group, softwarecomposition.NewKnownServersFinderImpl(knownServersListObjPtr.Items), metav1.Now())
	if err != nil {
		return fmt.Errorf("error generating network policy: %w", err)
	}

	data, err := json.Marshal(generatedNetworkPolicy)
	if err != nil {
		logger.L().Ctx(ctx).Error("json marshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	if err = json.Unmarshal(data, objPtr); err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	return nil
}

// GetList generates and returns one workload-level GeneratedNetworkPolicy per
// workload in the given namespace. The namespace's per-container ContainerProfiles
// are grouped by workload (<lower(kind)>-<workload-name>) and each group's
// neighbors are unioned into a single policy — one policy per workload, not one
// per container.
func (s *GeneratedNetworkPolicyStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	generatedNetworkPolicyList := &softwarecomposition.GeneratedNetworkPolicyList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: StorageV1Beta1ApiVersion,
		},
	}

	items, err := s.listNamespaceContainerProfiles(ctx, replaceKeyForKind(key, containerProfilesResource), opts)
	if err != nil {
		return err
	}

	knownServersListObjPtr := &softwarecomposition.KnownServerList{}
	if err := s.realStore.GetByCluster(ctx, softwarecomposition.GroupName, knownServersResource, knownServersListObjPtr); err != nil {
		return err
	}
	knownServers := softwarecomposition.NewKnownServersFinderImpl(knownServersListObjPtr.Items)

	// Group the per-container profiles by workload, preserving first-seen order so
	// the emitted list is deterministic.
	groups := map[string][]*softwarecomposition.ContainerProfile{}
	var order []string
	for i := range items {
		cp := &items[i]
		if !networkpolicy.IsAvailable(cp) {
			continue
		}
		name, ok := workloadPolicyName(cp)
		if !ok {
			continue
		}
		if _, seen := groups[name]; !seen {
			order = append(order, name)
		}
		groups[name] = append(groups[name], cp)
	}

	for _, name := range order {
		generatedNetworkPolicy, err := aggregateWorkloadPolicy(name, groups[name], knownServers, metav1.Now())
		if err != nil {
			logger.L().Ctx(ctx).Error("generate network policy failed", helpers.Error(err), helpers.String("workload", name))
			continue
		}
		generatedNetworkPolicyList.Items = append(generatedNetworkPolicyList.Items, generatedNetworkPolicy)
	}

	data, err := json.Marshal(generatedNetworkPolicyList)
	if err != nil {
		logger.L().Ctx(ctx).Error("json marshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	if err = json.Unmarshal(data, listObj); err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	return nil
}
