package v1alpha1

import (
	"crypto/sha256"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

// Status limits bound a single node snapshot rather than the number of nodes
// or rules a policy can target. Writers must report full Counts and set Truncated
// when details are omitted; long identifiers cannot be silently shortened.
const (
	MaxStatusRuleDetails        = 64
	MaxStatusConditions         = 8
	MaxRuleNameLength           = 256
	MaxStatusConditionKeyLength = 128
	MaxStatusDiagnosticLength   = 512
	MaxStatusErrorLength        = 2048
	MaxReferenceNameLength      = 253
	MaxReferenceUIDLength       = 128
)

// NodeStatusName returns the canonical DNS-safe report name for a policy/node
// UID pair. Length-prefixing avoids ambiguous identity concatenation. Writers
// must use it to avoid duplicate reports; admission cannot query other objects.
func NodeStatusName(policyUID, nodeUID types.UID) string {
	identity := fmt.Sprintf("%d:%s%d:%s", len(policyUID), policyUID, len(nodeUID), nodeUID)
	return fmt.Sprintf("sandboxpolicy-%x", sha256.Sum256([]byte(identity)))
}
