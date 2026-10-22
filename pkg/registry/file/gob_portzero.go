package file

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
)

// gob flattens *int32 and omits zero values, so an explicit port 0 decodes as
// nil, losing the distinction between an explicit zero and an absent port.
// stampPortZero marks explicit zeros before encode; restorePortZero rebuilds
// the pointer after decode. Legacy payloads carry no PortZero and are
// untouched: nonzero ports never need the marker, and their lost *0s were
// already nil at rest.

func stampPortZero(obj runtime.Object) {
	forEachNetworkPort(obj, func(p *softwarecomposition.NetworkPort) {
		p.PortZero = p.Port != nil && *p.Port == 0
	})
}

func restorePortZero(obj runtime.Object) {
	forEachNetworkPort(obj, func(p *softwarecomposition.NetworkPort) {
		if p.Port == nil && p.PortZero {
			zero := int32(0)
			p.Port = &zero
		}
		p.PortZero = false
	})
}

func forEachNetworkPort(obj runtime.Object, fn func(*softwarecomposition.NetworkPort)) {
	cp, ok := obj.(*softwarecomposition.ContainerProfile)
	if !ok {
		return
	}
	visit := func(neighbors []softwarecomposition.NetworkNeighbor) {
		for i := range neighbors {
			for j := range neighbors[i].Ports {
				fn(&neighbors[i].Ports[j])
			}
		}
	}
	visit(cp.Spec.Ingress)
	visit(cp.Spec.Egress)
	for _, group := range [][]softwarecomposition.ContainerProfileContainer{cp.Spec.Containers, cp.Spec.InitContainers, cp.Spec.EphemeralContainers} {
		for i := range group {
			visit(group[i].Ingress)
			visit(group[i].Egress)
		}
	}
}
