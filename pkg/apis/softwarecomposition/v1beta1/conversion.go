package v1beta1

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	conversion "k8s.io/apimachinery/pkg/conversion"
)

// Convert_softwarecomposition_NetworkPort_To_v1beta1_NetworkPort is manual:
// the internal type carries the gob-only PortZero marker (no v1beta1 peer),
// which must never leak to API clients. Restoring Port from the marker is the
// storage decode layer's job, not conversion's.
func Convert_softwarecomposition_NetworkPort_To_v1beta1_NetworkPort(in *softwarecomposition.NetworkPort, out *NetworkPort, s conversion.Scope) error {
	return autoConvert_softwarecomposition_NetworkPort_To_v1beta1_NetworkPort(in, out, s)
}
