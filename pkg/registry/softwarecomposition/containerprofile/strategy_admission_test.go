package containerprofile

// Admission-boundary tests for the restored ContainerProfile strategy
// behaviour: completed-profile immutability, complete->partial transition
// rejection, and network entry grammar validation. The deleted
// ApplicationProfile/NetworkNeighborhood strategies enforced these contracts;
// their removal left Validate/ValidateUpdate/PrepareForUpdate as no-ops.

import (
	"context"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func completedProfile() *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "replicaset-nginx-aaaa-nginx-1111-2222",
			Namespace: "default",
			Annotations: map[string]string{
				helpers.StatusMetadataKey:     helpers.Completed,
				helpers.CompletionMetadataKey: helpers.Full,
			},
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Syscalls: []string{"read"},
		},
	}
}

func TestPrepareForUpdate_CompletedProfileIsImmutable(t *testing.T) {
	s := NewStrategy(nil)
	old := completedProfile()

	updated := old.DeepCopy()
	updated.Spec.Syscalls = append(updated.Spec.Syscalls, "ptrace")
	updated.Labels = map[string]string{"tampered": "yes"}

	s.PrepareForUpdate(context.TODO(), updated, old)

	assert.Equal(t, old, updated,
		"an update to a completed profile must be reset to the stored object")
}

func TestPrepareForUpdate_CompleteToPartialRejected(t *testing.T) {
	s := NewStrategy(nil)
	old := completedProfile()
	// Only the completion annotation is Full; status differs so the
	// completed-immutability guard does not trigger and the transition
	// guard is exercised on its own.
	old.Annotations[helpers.StatusMetadataKey] = helpers.Learning

	updated := old.DeepCopy()
	updated.Annotations[helpers.CompletionMetadataKey] = helpers.Partial
	updated.Annotations[helpers.StatusMetadataKey] = helpers.Learning

	s.PrepareForUpdate(context.TODO(), updated, old)

	assert.Equal(t, helpers.Full, updated.Annotations[helpers.CompletionMetadataKey],
		"completion must not transition from complete to partial")
	assert.Equal(t, helpers.Learning, updated.Annotations[helpers.StatusMetadataKey],
		"status must be restored from the stored object")
}

func TestPrepareForUpdate_AuthoredProfileStaysEditable(t *testing.T) {
	s := NewStrategy(nil)
	// User-authored profiles carry no lifecycle annotations, so neither
	// guard applies and edits go through.
	old := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "redis-client", Namespace: "redis"},
		Spec: softwarecomposition.ContainerProfileSpec{
			Syscalls: []string{"read"},
		},
	}

	updated := old.DeepCopy()
	updated.Spec.Syscalls = append(updated.Spec.Syscalls, "write")
	want := updated.DeepCopy()

	s.PrepareForUpdate(context.TODO(), updated, old)

	assert.Equal(t, want, updated, "authored profile edits must not be reset")
}

func TestValidate_RejectsMalformedNetworkEntries(t *testing.T) {
	s := NewStrategy(nil)
	cp := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: softwarecomposition.ContainerProfileSpec{
			Ingress: []softwarecomposition.NetworkNeighbor{
				{
					Identifier:  "bad-ip",
					IPAddresses: []string{"999.999.1.2"},
				},
			},
			Egress: []softwarecomposition.NetworkNeighbor{
				{
					Identifier: "bad-dns",
					DNSNames:   []string{"foo..bar"},
				},
				{
					Identifier: "bad-legacy-ip",
					IPAddress:  "not-an-ip",
				},
			},
		},
	}

	errs := s.Validate(context.TODO(), cp)
	require.Len(t, errs, 3)
	fieldsSeen := []string{errs[0].Field, errs[1].Field, errs[2].Field}
	assert.Contains(t, fieldsSeen, "spec.ingress[0].ipAddresses[0]")
	assert.Contains(t, fieldsSeen, "spec.egress[0].dnsNames[0]")
	assert.Contains(t, fieldsSeen, "spec.egress[1].ipAddress")

	// ValidateUpdate applies the same grammar.
	updErrs := s.ValidateUpdate(context.TODO(), cp, completedProfile())
	assert.Len(t, updErrs, 3)
}

func TestValidate_AcceptsWildcardGrammar(t *testing.T) {
	s := NewStrategy(nil)
	cp := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: softwarecomposition.ContainerProfileSpec{
			Ingress: []softwarecomposition.NetworkNeighbor{
				{Identifier: "any", IPAddresses: []string{"*", "10.0.0.0/8", "10.1.2.3"}},
			},
			Egress: []softwarecomposition.NetworkNeighbor{
				{Identifier: "dns", DNSNames: []string{"*.example.com.", "kube-dns.kube-system.svc.cluster.local."}},
			},
		},
	}
	assert.Empty(t, s.Validate(context.TODO(), cp))
}

func TestValidate_RejectsInvalidLifecycleAnnotations(t *testing.T) {
	s := NewStrategy(nil)
	cp := completedProfile()
	cp.Annotations[helpers.CompletionMetadataKey] = "half-done"
	cp.Annotations[helpers.StatusMetadataKey] = "sort-of-ready"

	errs := s.Validate(context.TODO(), cp)
	assert.Len(t, errs, 2, "invalid completion and status annotation values must both be rejected")
}
