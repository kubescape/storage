package file

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/storage"
)

const imageObservedAt = "kubescape.io/image-observed-at"

type imageRefreshContextKey struct{}

type profileImageStorage struct {
	storage.Interface
	auth authorizer.Authorizer
}

// WithProfileImageRefresh handles the first observation from a new container
// after a learned profile has completed. The report itself remains terminal
// and never becomes part of the learned behavior.
func WithProfileImageRefresh(store storage.Interface, auth authorizer.Authorizer) storage.Interface {
	return &profileImageStorage{Interface: store, auth: auth}
}

func (s *profileImageStorage) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	err := s.Interface.Create(ctx, key, obj, out, ttl)
	if !errors.Is(err, ObjectCompletedError) {
		return err
	}
	report, ok := obj.(*softwarecomposition.ContainerProfile)
	if !ok || report.Annotations[helpers.ReportSeriesIdMetadataKey] == "" ||
		!isZeroTime(report.Annotations[helpers.PreviousReportTimestampMetadataKey]) ||
		report.Annotations[helpers.InstanceIDMetadataKey] == "" || report.Spec.ImageTag == "" {
		return err
	}
	observed, ok := parseReportTimestamp(report.Annotations[helpers.ReportTimestampMetadataKey])
	if !ok || observed.IsZero() || !validImageDigest(report.Spec.ImageID) {
		return err
	}
	name, suffix := SplitProfileName(report.Name)
	if name == "" || suffix == "" || path.Base(key) != report.Name {
		return err
	}
	// Creating a TS object and updating the named consolidated object are
	// separate permissions. Scope the check to the actual request namespace.
	user, ok := request.UserFrom(ctx)
	ns, _ := request.NamespaceFrom(ctx)
	if !ok || s.auth == nil || ns == "" || ns != report.Namespace {
		return err
	}
	allowed, _, authErr := s.auth.Authorize(ctx, authorizer.AttributesRecord{
		User: user, Verb: "update", Namespace: ns, APIGroup: softwarecomposition.GroupName,
		APIVersion: "v1beta1", Resource: ContainerProfileKind, Name: name, ResourceRequest: true,
	})
	if authErr != nil {
		return fmt.Errorf("authorize profile image update: %w", authErr)
	}
	if allowed != authorizer.DecisionAllow {
		return err
	}

	// GuaranteedUpdate serializes with other observations and rechecks on
	// conflict. Use only the request key's parent, never a report-supplied scope.
	baseKey := strings.TrimSuffix(key, "-"+suffix)
	refreshCtx := context.WithValue(ctx, imageRefreshContextKey{}, true)
	updateErr := s.GuaranteedUpdate(refreshCtx, baseKey, &softwarecomposition.ContainerProfile{}, false, nil,
		func(obj runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			base := obj.(*softwarecomposition.ContainerProfile)
			if !softwarecomposition.IsCompletedFull(base.Annotations) || isUserManaged(&base.ObjectMeta) ||
				base.Spec.ImageID == "" ||
				base.Annotations[helpers.InstanceIDMetadataKey] != report.Annotations[helpers.InstanceIDMetadataKey] ||
				base.Spec.ImageTag != report.Spec.ImageTag || !observed.After(base.CreationTimestamp.Time) {
				return base, nil, nil
			}
			if last, ok := parseReportTimestamp(base.Annotations[imageObservedAt]); ok && !observed.After(last) {
				return base, nil, nil
			}
			// Advance the observation time even if the digest agrees. Otherwise
			// an older report replayed from another node can revert the image.
			base.Annotations[imageObservedAt] = observed.UTC().Format(time.RFC3339Nano)
			base.Spec.ImageID = report.Spec.ImageID
			return base, nil, nil
		}, nil)
	if updateErr != nil && !storage.IsNotFound(updateErr) {
		return fmt.Errorf("refresh completed profile image: %w", updateErr)
	}
	return err
}

func validImageDigest(id string) bool {
	for _, prefix := range []string{"docker-pullable://", "docker://", "containerd://", "cri-o://"} {
		if strings.HasPrefix(id, prefix) {
			id = strings.TrimPrefix(id, prefix)
			break
		}
	}
	if strings.Contains(id, "://") {
		return false
	}
	if strings.Contains(id, "@") {
		_, err := name.NewDigest(id)
		return err == nil
	}
	_, err := digest.Parse(id)
	return err == nil
}
