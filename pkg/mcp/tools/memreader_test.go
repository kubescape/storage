package tools

import (
	"context"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
)

// memReader is a Reader over fixed objects that answers like storage: a list
// returns metadata only and honours the label selector, a missing object is
// NotFound, and any read in the namespace "restricted" is Forbidden.
type memReader struct {
	manifests []v1beta1.VulnerabilityManifest
	sboms     []v1beta1.SBOMSyft
	scans     []v1beta1.WorkloadConfigurationScan
	profiles  []v1beta1.ContainerProfile
}

func newMemReader(manifests ...v1beta1.VulnerabilityManifest) *memReader {
	return &memReader{manifests: manifests}
}

const restrictedNamespace = "restricted"

func forbidden(verb, resource, namespace, name string) error {
	return responsewriters.ForbiddenStatusError(authorizer.AttributesRecord{
		User:            &user.DefaultInfo{Name: "system:serviceaccount:kagent:agent"},
		Verb:            verb,
		Namespace:       namespace,
		APIGroup:        softwarecomposition.GroupName,
		APIVersion:      v1beta1.SchemeGroupVersion.Version,
		Resource:        resource,
		Name:            name,
		ResourceRequest: true,
	}, "")
}

func (r *memReader) ListVulnerabilityManifests(_ context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.VulnerabilityManifestList, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("list", "vulnerabilitymanifests", namespace, "")
	}
	selector, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	out := &v1beta1.VulnerabilityManifestList{}
	for _, m := range r.manifests {
		if m.Namespace == namespace && selector.Matches(labels.Set(m.Labels)) {
			out.Items = append(out.Items, v1beta1.VulnerabilityManifest{ObjectMeta: m.ObjectMeta})
		}
	}
	return out, nil
}

func (r *memReader) GetVulnerabilityManifest(_ context.Context, namespace, name string) (*v1beta1.VulnerabilityManifest, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("get", "vulnerabilitymanifests", namespace, name)
	}
	for i := range r.manifests {
		if r.manifests[i].Namespace == namespace && r.manifests[i].Name == name {
			return r.manifests[i].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(v1beta1.Resource("vulnerabilitymanifests"), name)
}

func (r *memReader) ListSBOMSyfts(_ context.Context, namespace string, _ metav1.ListOptions) (*v1beta1.SBOMSyftList, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("list", "sbomsyfts", namespace, "")
	}
	out := &v1beta1.SBOMSyftList{}
	for _, s := range r.sboms {
		if s.Namespace == namespace {
			out.Items = append(out.Items, v1beta1.SBOMSyft{ObjectMeta: s.ObjectMeta})
		}
	}
	return out, nil
}

func (r *memReader) GetSBOMSyft(_ context.Context, namespace, name string) (*v1beta1.SBOMSyft, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("get", "sbomsyfts", namespace, name)
	}
	for i := range r.sboms {
		if r.sboms[i].Namespace == namespace && r.sboms[i].Name == name {
			return r.sboms[i].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(v1beta1.Resource("sbomsyfts"), name)
}

func (r *memReader) ListWorkloadConfigurationScans(_ context.Context, namespace string, _ metav1.ListOptions) (*v1beta1.WorkloadConfigurationScanList, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("list", "workloadconfigurationscans", namespace, "")
	}
	out := &v1beta1.WorkloadConfigurationScanList{}
	for _, s := range r.scans {
		if s.Namespace == namespace {
			out.Items = append(out.Items, v1beta1.WorkloadConfigurationScan{ObjectMeta: s.ObjectMeta})
		}
	}
	return out, nil
}

func (r *memReader) GetWorkloadConfigurationScan(_ context.Context, namespace, name string) (*v1beta1.WorkloadConfigurationScan, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("get", "workloadconfigurationscans", namespace, name)
	}
	for i := range r.scans {
		if r.scans[i].Namespace == namespace && r.scans[i].Name == name {
			return r.scans[i].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(v1beta1.Resource("workloadconfigurationscans"), name)
}

func (r *memReader) ListContainerProfiles(_ context.Context, namespace string, _ metav1.ListOptions) (*v1beta1.ContainerProfileList, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("list", "containerprofiles", namespace, "")
	}
	out := &v1beta1.ContainerProfileList{}
	for _, p := range r.profiles {
		if p.Namespace == namespace {
			out.Items = append(out.Items, v1beta1.ContainerProfile{ObjectMeta: p.ObjectMeta})
		}
	}
	return out, nil
}

func (r *memReader) GetContainerProfile(_ context.Context, namespace, name string) (*v1beta1.ContainerProfile, error) {
	if namespace == restrictedNamespace {
		return nil, forbidden("get", "containerprofiles", namespace, name)
	}
	for i := range r.profiles {
		if r.profiles[i].Namespace == namespace && r.profiles[i].Name == name {
			return r.profiles[i].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(v1beta1.Resource("containerprofiles"), name)
}
