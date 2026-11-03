package endpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"
)

// Cache durations of the per-read authorizer, the defaults of k8s.io/apiserver's
// delegated authorization.
const (
	allowCacheTTL = 10 * time.Second
	denyCacheTTL  = 10 * time.Second
)

// NewAuthorizer returns an authorizer that asks kube-apiserver with a
// SubjectAccessReview and caches the answer. Storage's ServiceAccount is bound
// to system:auth-delegator, which allows it to create SubjectAccessReviews.
func NewAuthorizer(client authorizationv1client.AuthorizationV1Interface) (authorizer.Authorizer, error) {
	return authorizerfactory.DelegatingAuthorizerConfig{
		SubjectAccessReviewClient: client,
		AllowCacheTTL:             allowCacheTTL,
		DenyCacheTTL:              denyCacheTTL,
		WebhookRetryBackoff:       genericoptions.DefaultAuthWebhookRetryBackoff(),
	}.New()
}

// listGetter is REST storage that can list and get.
type listGetter interface {
	rest.Lister
	rest.Getter
}

// reader implements tools.Reader on storage's REST storage, in process. Every
// read is first checked against the caller's RBAC, with the same verb,
// resource, namespace and name as the matching kubectl call.
type reader struct {
	authorizer             authorizer.Authorizer
	scheme                 *runtime.Scheme
	vulnerabilityManifests listGetter
	sbomSyfts              listGetter
	configurationScans     listGetter
}

func (r *reader) ListVulnerabilityManifests(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.VulnerabilityManifestList, error) {
	if err := r.authorize(ctx, "list", "vulnerabilitymanifests", namespace, ""); err != nil {
		return nil, err
	}
	obj, err := r.list(readContext(ctx, "list", "vulnerabilitymanifests", namespace, ""), r.vulnerabilityManifests, opts)
	if err != nil {
		return nil, err
	}
	out := &v1beta1.VulnerabilityManifestList{}
	if err := r.scheme.Convert(obj, out, nil); err != nil {
		return nil, fmt.Errorf("convert vulnerability manifests: %w", err)
	}
	return out, nil
}

func (r *reader) GetVulnerabilityManifest(ctx context.Context, namespace, name string) (*v1beta1.VulnerabilityManifest, error) {
	if err := r.authorize(ctx, "get", "vulnerabilitymanifests", namespace, name); err != nil {
		return nil, err
	}
	obj, err := r.vulnerabilityManifests.Get(readContext(ctx, "get", "vulnerabilitymanifests", namespace, name), name, &metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := &v1beta1.VulnerabilityManifest{}
	if err := r.scheme.Convert(obj, out, nil); err != nil {
		return nil, fmt.Errorf("convert vulnerability manifest: %w", err)
	}
	return out, nil
}

func (r *reader) ListSBOMSyfts(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.SBOMSyftList, error) {
	if err := r.authorize(ctx, "list", "sbomsyfts", namespace, ""); err != nil {
		return nil, err
	}
	obj, err := r.list(readContext(ctx, "list", "sbomsyfts", namespace, ""), r.sbomSyfts, opts)
	if err != nil {
		return nil, err
	}
	out := &v1beta1.SBOMSyftList{}
	if err := r.scheme.Convert(obj, out, nil); err != nil {
		return nil, fmt.Errorf("convert SBOMs: %w", err)
	}
	return out, nil
}

func (r *reader) GetSBOMSyft(ctx context.Context, namespace, name string) (*v1beta1.SBOMSyft, error) {
	if err := r.authorize(ctx, "get", "sbomsyfts", namespace, name); err != nil {
		return nil, err
	}
	obj, err := r.sbomSyfts.Get(readContext(ctx, "get", "sbomsyfts", namespace, name), name, &metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := &v1beta1.SBOMSyft{}
	if err := r.scheme.Convert(obj, out, nil); err != nil {
		return nil, fmt.Errorf("convert SBOM: %w", err)
	}
	return out, nil
}

func (r *reader) ListWorkloadConfigurationScans(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.WorkloadConfigurationScanList, error) {
	if err := r.authorize(ctx, "list", "workloadconfigurationscans", namespace, ""); err != nil {
		return nil, err
	}
	obj, err := r.list(readContext(ctx, "list", "workloadconfigurationscans", namespace, ""), r.configurationScans, opts)
	if err != nil {
		return nil, err
	}
	out := &v1beta1.WorkloadConfigurationScanList{}
	if err := r.scheme.Convert(obj, out, nil); err != nil {
		return nil, fmt.Errorf("convert configuration scans: %w", err)
	}
	return out, nil
}

func (r *reader) GetWorkloadConfigurationScan(ctx context.Context, namespace, name string) (*v1beta1.WorkloadConfigurationScan, error) {
	if err := r.authorize(ctx, "get", "workloadconfigurationscans", namespace, name); err != nil {
		return nil, err
	}
	obj, err := r.configurationScans.Get(readContext(ctx, "get", "workloadconfigurationscans", namespace, name), name, &metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := &v1beta1.WorkloadConfigurationScan{}
	if err := r.scheme.Convert(obj, out, nil); err != nil {
		return nil, fmt.Errorf("convert configuration scan: %w", err)
	}
	return out, nil
}

func (r *reader) list(ctx context.Context, lister rest.Lister, opts metav1.ListOptions) (runtime.Object, error) {
	internalOpts := &metainternalversion.ListOptions{}
	if err := metainternalversion.Convert_v1_ListOptions_To_internalversion_ListOptions(&opts, internalOpts, nil); err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	return lister.List(ctx, internalOpts)
}

// readContext is the context of one in-process read: the namespace and the
// request info of the matching kubectl call. The REST storage names the
// resource in its errors after the request info, which would otherwise be the
// one of the mcpservers/mcp call.
func readContext(ctx context.Context, verb, resource, namespace, name string) context.Context {
	return genericapirequest.WithRequestInfo(genericapirequest.WithNamespace(ctx, namespace), &genericapirequest.RequestInfo{
		IsResourceRequest: true,
		Verb:              verb,
		APIPrefix:         "apis",
		APIGroup:          softwarecomposition.GroupName,
		APIVersion:        v1beta1.SchemeGroupVersion.Version,
		Namespace:         namespace,
		Resource:          resource,
		Name:              name,
	})
}

// authorize returns a Forbidden error unless the caller may do verb on
// resource in namespace (and name, for a single object). It records the
// decision for the call's audit line.
func (r *reader) authorize(ctx context.Context, verb, resource, namespace, name string) error {
	u, ok := genericapirequest.UserFrom(ctx)
	if !ok {
		return apierrors.NewUnauthorized("no user in the request")
	}
	attrs := authorizer.AttributesRecord{
		User:            u,
		Verb:            verb,
		Namespace:       namespace,
		APIGroup:        softwarecomposition.GroupName,
		APIVersion:      v1beta1.SchemeGroupVersion.Version,
		Resource:        resource,
		Name:            name,
		ResourceRequest: true,
	}
	decision, reason, err := r.authorizer.Authorize(ctx, attrs)
	allowed := decision == authorizer.DecisionAllow
	recordRead(ctx, read{Verb: verb, Resource: resource, Namespace: namespace, Name: name, Allowed: allowed, Reason: reason})
	switch {
	case allowed:
		return nil
	case err != nil:
		return apierrors.NewInternalError(fmt.Errorf("checking access: %w", err))
	default:
		return responsewriters.ForbiddenStatusError(attrs, reason)
	}
}
