package file

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	instanceidhandlerv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

// Annotation keys used to mark which user-managed (ug-) AP/NN ResourceVersions
// have already been merged into the consolidated ContainerProfile. They make
// the per-tick merge idempotent: when the marker matches the live ug- RV, the
// merge is skipped to avoid duplicate slice entries.
const (
	lastMergedUserAPResourceVersionKey = "kubescape.io/last-merged-ug-ap-rv"
	lastMergedUserNNResourceVersionKey = "kubescape.io/last-merged-ug-nn-rv"
)

// userManagedConnWarnOnce makes the type-assert miss in userManagedConn surface
// loudly the first time it happens in a process — silent no-ops are fine in
// tests using a stub backend, but in production they would mask a config bug.
var userManagedConnWarnOnce sync.Once

// mergeUserManagedProfiles fetches the user-managed (ug-prefix) ApplicationProfile
// and NetworkNeighborhood for the workload owning this ContainerProfile and
// merges the matching per-container entry into profile.Spec. Best-effort:
// missing user-managed CRDs are not an error. Status/completion annotations on
// the base ContainerProfile are untouched — user-managed data is additive.
//
// This mirrors the merge math previously performed client-side in
// node-agent/pkg/objectcache/containerprofilecache/projection.go. Centralising
// it here lets every node-agent consume the consolidated CP as a single source
// of truth and drop the per-tick ug- fetch.
//
// Note: this fires once per per-container CP per consolidation tick, so a
// workload with N containers does N×2 reads of the same ug- objects. A
// short-lived per-tick cache was considered (kubescape/storage#315 thread)
// but rejected — ug- objects can be large and the memory overhead isn't
// justified at the 30s cadence. Revisit if disk I/O shows up in profiling.
func (a *ContainerProfileProcessor) mergeUserManagedProfiles(ctx context.Context, profile *softwarecomposition.ContainerProfile, id armotypes.ProfileIdentifier) {
	instanceIDStr, ok := profile.Annotations[helpers.InstanceIDMetadataKey]
	if !ok {
		return
	}
	instanceID, err := instanceidhandlerv1.GenerateInstanceIDFromString(instanceIDStr)
	if err != nil {
		logger.L().Debug("ContainerProfileProcessor.mergeUserManagedProfiles - failed to parse instance ID", loggerhelpers.Error(err))
		return
	}
	workloadSlug, err := instanceID.GetSlug(true)
	if err != nil {
		logger.L().Debug("ContainerProfileProcessor.mergeUserManagedProfiles - failed to derive workload slug", loggerhelpers.Error(err))
		return
	}
	containerName := instanceID.GetContainerName()
	if containerName == "" {
		return
	}

	storageImpl, conn, ok := a.userManagedConn(ctx)
	if !ok {
		return
	}

	// AP and NN have separate prefix constants for future-proofing even though
	// they currently share the same "ug-" string.
	apID := id
	apID.Name = helpers.UserApplicationProfilePrefix + workloadSlug
	apKey := BuildContainerProfileKey(apID, "applicationprofiles")
	nnID := id
	nnID.Name = helpers.UserNetworkNeighborhoodPrefix + workloadSlug
	nnKey := BuildContainerProfileKey(nnID, "networkneighborhoods")

	// ApplicationProfile merge
	apCtx, apCancel := context.WithTimeout(ctx, 5*time.Second)
	defer apCancel()
	var userAP softwarecomposition.ApplicationProfile
	if err := storageImpl.GetWithConn(apCtx, conn, apKey, storage.GetOptions{}, &userAP); err != nil {
		if !storage.IsNotFound(err) {
			logger.L().Debug("ContainerProfileProcessor.mergeUserManagedProfiles - failed to get user-managed AP", loggerhelpers.Error(err), loggerhelpers.String("key", apKey))
		}
	} else if profile.Annotations[lastMergedUserAPResourceVersionKey] != userAP.ResourceVersion {
		mergeUserAPIntoCP(profile, &userAP, containerName)
		profile.Annotations[lastMergedUserAPResourceVersionKey] = userAP.ResourceVersion
	}

	// NetworkNeighborhood merge
	nnCtx, nnCancel := context.WithTimeout(ctx, 5*time.Second)
	defer nnCancel()
	var userNN softwarecomposition.NetworkNeighborhood
	if err := storageImpl.GetWithConn(nnCtx, conn, nnKey, storage.GetOptions{}, &userNN); err != nil {
		if !storage.IsNotFound(err) {
			logger.L().Debug("ContainerProfileProcessor.mergeUserManagedProfiles - failed to get user-managed NN", loggerhelpers.Error(err), loggerhelpers.String("key", nnKey))
		}
	} else if profile.Annotations[lastMergedUserNNResourceVersionKey] != userNN.ResourceVersion {
		mergeUserNNIntoCP(profile, &userNN, containerName)
		profile.Annotations[lastMergedUserNNResourceVersionKey] = userNN.ResourceVersion
	}
}

// userManagedConn extracts the StorageImpl and sqlite connection from the
// processor's storage and the supplied context. Returns ok=false when the
// underlying storage is not the SQLite-backed implementation (e.g. tests
// using a stub backend) or when the connection is unavailable. The first such
// miss in a process is logged at warning level so a production
// misconfiguration is visible.
func (a *ContainerProfileProcessor) userManagedConn(ctx context.Context) (*StorageImpl, *sqlite.Conn, bool) {
	impl, ok := a.ContainerProfileStorage.(*ContainerProfileStorageImpl)
	if !ok {
		userManagedConnWarnOnce.Do(func() {
			logger.L().Warning("ContainerProfileProcessor.mergeUserManagedProfiles disabled - unexpected storage backend type",
				loggerhelpers.Interface("type", a.ContainerProfileStorage))
		})
		return nil, nil, false
	}
	conn, ok := ctx.Value(connKey).(*sqlite.Conn)
	if !ok {
		userManagedConnWarnOnce.Do(func() {
			logger.L().Warning("ContainerProfileProcessor.mergeUserManagedProfiles disabled - missing sqlite connection on context (WithConnection not applied)")
		})
		return nil, nil, false
	}
	return impl.GetStorageImpl(), conn, true
}

// mergeUserAPIntoCP locates the ApplicationProfileContainer in userAP whose
// Name matches containerName and appends its fields onto cp.Spec. PolicyByRuleId
// entries are merged via mergePolicies on collision (same union semantics as
// the time-series merge).
//
// IdentifiedCallStacks is intentionally NOT merged — node-agent's
// projection.go (the reference implementation) does not project them either,
// so server- and client-side merges stay in sync.
func mergeUserAPIntoCP(cp *softwarecomposition.ContainerProfile, userAP *softwarecomposition.ApplicationProfile, containerName string) {
	matched := findUserAPContainerByName(userAP, containerName)
	if matched == nil {
		return
	}
	// Defensive copy: the returned matched.* slices alias userAP, which is
	// the caller's CRD object. DeepCopy isolates the merge from concurrent
	// reads of the same cached object.
	c := matched.DeepCopy()
	cp.Spec.Capabilities = append(cp.Spec.Capabilities, c.Capabilities...)
	cp.Spec.Execs = append(cp.Spec.Execs, c.Execs...)
	cp.Spec.Opens = append(cp.Spec.Opens, c.Opens...)
	cp.Spec.Syscalls = append(cp.Spec.Syscalls, c.Syscalls...)
	cp.Spec.Endpoints = append(cp.Spec.Endpoints, c.Endpoints...)
	if cp.Spec.PolicyByRuleId == nil && len(c.PolicyByRuleId) > 0 {
		cp.Spec.PolicyByRuleId = make(map[string]softwarecomposition.RulePolicy, len(c.PolicyByRuleId))
	}
	for k, v := range c.PolicyByRuleId {
		if existing, ok := cp.Spec.PolicyByRuleId[k]; ok {
			cp.Spec.PolicyByRuleId[k] = mergePolicies(existing, v)
		} else {
			cp.Spec.PolicyByRuleId[k] = v
		}
	}
}

// mergeUserNNIntoCP merges the matching NetworkNeighborhoodContainer's
// Ingress/Egress and the NN's pod LabelSelector into cp.Spec. Ingress/Egress
// entries are unioned by Identifier; matching entries are deep-merged via
// mergeUserNetworkNeighbor (DNS names are set-unioned and sorted, ports are
// keyed by Name with user values winning on collision, selectors are
// field-merged with user keys overriding base).
func mergeUserNNIntoCP(cp *softwarecomposition.ContainerProfile, userNN *softwarecomposition.NetworkNeighborhood, containerName string) {
	matched := findUserNNContainerByName(userNN, containerName)
	if matched != nil {
		c := matched.DeepCopy()
		cp.Spec.Ingress = mergeUserNetworkNeighbors(cp.Spec.Ingress, c.Ingress)
		cp.Spec.Egress = mergeUserNetworkNeighbors(cp.Spec.Egress, c.Egress)
	}

	// NetworkNeighborhoodSpec embeds metav1.LabelSelector; ContainerProfileSpec
	// stores the same selector denormalised as MatchLabels/MatchExpressions
	// inside Spec.LabelSelector.
	cp.Spec.LabelSelector.MatchLabels = overrideMerge(cp.Spec.LabelSelector.MatchLabels, userNN.Spec.LabelSelector.MatchLabels)
	cp.Spec.LabelSelector.MatchExpressions = appendDedupSortedMatchExpressions(cp.Spec.LabelSelector.MatchExpressions, userNN.Spec.LabelSelector.MatchExpressions)
}

// overrideMerge returns base extended with user's keys; on key collision the
// user value wins. Distinct from utils.MergeMaps which preserves base on
// collision (other callers depend on that semantic, so we don't change it).
func overrideMerge(base, user map[string]string) map[string]string {
	if len(user) == 0 {
		return base
	}
	if base == nil {
		base = map[string]string{}
	}
	for k, v := range user {
		base[k] = v
	}
	return base
}

// appendDedupSortedMatchExpressions appends user expressions to base and
// returns a deduplicated, deterministically-ordered slice. Dedup key is
// (Key, Operator, sorted Values) so semantically-equal expressions collapse
// regardless of input ordering. Determinism keeps the consolidated CP's
// SyncChecksum stable across re-merges of the same content.
func appendDedupSortedMatchExpressions(base, user []metav1.LabelSelectorRequirement) []metav1.LabelSelectorRequirement {
	// Allocate a fresh backing array so the in-place dedup below cannot
	// mutate base's storage (append(base, user...) would reuse it whenever
	// cap(base) is large enough).
	combined := make([]metav1.LabelSelectorRequirement, 0, len(base)+len(user))
	combined = append(combined, base...)
	combined = append(combined, user...)
	if len(combined) == 0 {
		return combined
	}
	type key struct {
		k, op, vals string
	}
	seen := make(map[key]struct{}, len(combined))
	out := combined[:0]
	for _, r := range combined {
		vs := append([]string(nil), r.Values...)
		sort.Strings(vs)
		var b strings.Builder
		for i, v := range vs {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(v)
		}
		k := key{string(r.Key), string(r.Operator), b.String()}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		// Store with sorted values to keep serialisation stable.
		r.Values = vs
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		if out[i].Operator != out[j].Operator {
			return out[i].Operator < out[j].Operator
		}
		return joinSorted(out[i].Values) < joinSorted(out[j].Values)
	})
	return out
}

func joinSorted(vs []string) string {
	var b strings.Builder
	for i, v := range vs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(v)
	}
	return b.String()
}

func findUserAPContainerByName(userAP *softwarecomposition.ApplicationProfile, name string) *softwarecomposition.ApplicationProfileContainer {
	if userAP == nil {
		return nil
	}
	for i := range userAP.Spec.Containers {
		if userAP.Spec.Containers[i].Name == name {
			return &userAP.Spec.Containers[i]
		}
	}
	for i := range userAP.Spec.InitContainers {
		if userAP.Spec.InitContainers[i].Name == name {
			return &userAP.Spec.InitContainers[i]
		}
	}
	for i := range userAP.Spec.EphemeralContainers {
		if userAP.Spec.EphemeralContainers[i].Name == name {
			return &userAP.Spec.EphemeralContainers[i]
		}
	}
	return nil
}

func findUserNNContainerByName(userNN *softwarecomposition.NetworkNeighborhood, name string) *softwarecomposition.NetworkNeighborhoodContainer {
	if userNN == nil {
		return nil
	}
	for i := range userNN.Spec.Containers {
		if userNN.Spec.Containers[i].Name == name {
			return &userNN.Spec.Containers[i]
		}
	}
	for i := range userNN.Spec.InitContainers {
		if userNN.Spec.InitContainers[i].Name == name {
			return &userNN.Spec.InitContainers[i]
		}
	}
	for i := range userNN.Spec.EphemeralContainers {
		if userNN.Spec.EphemeralContainers[i].Name == name {
			return &userNN.Spec.EphemeralContainers[i]
		}
	}
	return nil
}

func mergeUserNetworkNeighbors(base, user []softwarecomposition.NetworkNeighbor) []softwarecomposition.NetworkNeighbor {
	idx := make(map[string]int, len(base))
	for i, n := range base {
		idx[n.Identifier] = i
	}
	for _, u := range user {
		if i, exists := idx[u.Identifier]; exists {
			base[i] = mergeUserNetworkNeighbor(base[i], u)
		} else {
			base = append(base, u)
			idx[u.Identifier] = len(base) - 1
		}
	}
	return base
}

func mergeUserNetworkNeighbor(base, user softwarecomposition.NetworkNeighbor) softwarecomposition.NetworkNeighbor {
	merged := *base.DeepCopy()

	dnsSet := mapset.NewSet[string]()
	for _, d := range merged.DNSNames {
		dnsSet.Add(d)
	}
	for _, d := range user.DNSNames {
		dnsSet.Add(d)
	}
	merged.DNSNames = merged.DNSNames[:0]
	for d := range dnsSet.Iter() {
		merged.DNSNames = append(merged.DNSNames, d)
	}
	// mapset iteration order is randomised; sort for stable serialisation so
	// the consolidated CP's SyncChecksum doesn't churn across re-merges of
	// the same content.
	sort.Strings(merged.DNSNames)

	merged.Ports = mergeUserNetworkPorts(merged.Ports, user.Ports)

	if user.PodSelector != nil {
		if merged.PodSelector == nil {
			merged.PodSelector = &metav1.LabelSelector{}
		}
		merged.PodSelector.MatchLabels = overrideMerge(merged.PodSelector.MatchLabels, user.PodSelector.MatchLabels)
		merged.PodSelector.MatchExpressions = appendDedupSortedMatchExpressions(merged.PodSelector.MatchExpressions, user.PodSelector.MatchExpressions)
	}

	if user.NamespaceSelector != nil {
		if merged.NamespaceSelector == nil {
			merged.NamespaceSelector = &metav1.LabelSelector{}
		}
		merged.NamespaceSelector.MatchLabels = overrideMerge(merged.NamespaceSelector.MatchLabels, user.NamespaceSelector.MatchLabels)
		merged.NamespaceSelector.MatchExpressions = appendDedupSortedMatchExpressions(merged.NamespaceSelector.MatchExpressions, user.NamespaceSelector.MatchExpressions)
	}

	if user.IPAddress != "" {
		merged.IPAddress = user.IPAddress
	}
	if user.Type != "" {
		merged.Type = user.Type
	}

	return merged
}

// mergeUserNetworkPorts merges user ports onto base ports, keyed by Name.
//
// On collision the user port wins — intentional even when "base" comes from
// observed time-series traffic. ug- profiles encode the operator's policy
// intent (e.g. an authoritative port spec for an exception), and that intent
// must override observation. This matches node-agent's
// projection.go:mergeNetworkPorts. Revisit only if this discards observations
// the operator actually wanted to keep.
func mergeUserNetworkPorts(base, user []softwarecomposition.NetworkPort) []softwarecomposition.NetworkPort {
	idx := make(map[string]int, len(base))
	for i, p := range base {
		idx[p.Name] = i
	}
	for _, u := range user {
		if i, exists := idx[u.Name]; exists {
			base[i] = u
		} else {
			base = append(base, u)
			idx[u.Name] = len(base) - 1
		}
	}
	return base
}
