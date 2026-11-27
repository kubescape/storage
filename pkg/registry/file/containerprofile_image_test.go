package file

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/storage"
)

func TestCompletedProfileImageRefresh(t *testing.T) {
	for label, setup := range map[string]func(*testing.T, processorWrap) *backend{
		"file": newLegacyBackend, "sqlite": newObjectBackend,
	} {
		t.Run(label, func(t *testing.T) {
			b := setup(t, nil)
			f := loadDiffFixtures(t)
			ctx := request.WithUser(request.WithNamespace(t.Context(), f.ns), &user.DefaultInfo{Name: "agent"})
			decision := authorizer.DecisionAllow
			s := WithProfileImageRefresh(b.store, authorizer.AuthorizerFunc(func(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
				require.Equal(t, "agent", a.GetUser().GetName())
				require.Equal(t, f.ns, a.GetNamespace())
				require.Equal(t, f.baseNm, a.GetName())
				require.Equal(t, "update", a.GetVerb())
				require.Equal(t, ContainerProfileKind, a.GetResource())
				return decision, "", nil
			}))
			oldID := "python@sha256:" + strings.Repeat("a", 64)
			newID := "python@sha256:" + strings.Repeat("b", 64)
			base := f.plain(f.baseNm)
			base.Spec.ImageTag, base.Spec.ImageID = "python:3.12", oldID
			base.Annotations[helpers.StatusMetadataKey], base.Annotations[helpers.CompletionMetadataKey] = helpers.Completed, helpers.Full
			require.NoError(t, s.Create(ctx, f.baseKey, base, &softwarecomposition.ContainerProfile{}, 0))
			get := func() *softwarecomposition.ContainerProfile {
				t.Helper()
				got := &softwarecomposition.ContainerProfile{}
				require.NoError(t, s.Get(ctx, f.baseKey, storage.GetOptions{}, got))
				return got
			}
			before := get()
			report := f.ts("newpod", 1, helpers.Learning, helpers.Full)
			report.Spec.ImageTag, report.Spec.ImageID = base.Spec.ImageTag, newID
			report.Spec.Capabilities = []string{"not-learned"}
			send := func() {
				t.Helper()
				require.ErrorIs(t, s.Create(ctx, f.key(report.Name), report.DeepCopy(), &softwarecomposition.ContainerProfile{}, 0), ObjectCompletedError)
				require.Zero(t, b.tsRows(t, f.baseKey))
			}
			decision = authorizer.DecisionDeny
			send()
			require.Equal(t, before, get())
			decision = authorizer.DecisionAllow
			send()
			after := get()
			want := before.DeepCopy()
			want.Spec.ImageID = newID
			require.Equal(t, want.Spec, after.Spec)
			require.Equal(t, helpers.Completed, after.Annotations[helpers.StatusMetadataKey])
			require.Equal(t, helpers.Full, after.Annotations[helpers.CompletionMetadataKey])
			require.NotEqual(t, before.ResourceVersion, after.ResourceVersion)
			send()
			require.Equal(t, after, get(), "redelivery")
			report.Spec.ImageID = oldID
			observed, ok := parseReportTimestamp(report.Annotations[helpers.ReportTimestampMetadataKey])
			require.True(t, ok)
			report.Annotations[helpers.ReportTimestampMetadataKey] = observed.Add(-time.Second).String()
			send()
			require.Equal(t, after, get(), "out-of-order observation")
			report.Annotations[helpers.ReportTimestampMetadataKey] = observed.Add(time.Second).String()
			report.Spec.ImageID = "python:3.12"
			send()
			require.Equal(t, after, get(), "mutable tag without digest")
		})
	}
}

func TestImageObservationDigest(t *testing.T) {
	sum := "sha256:" + strings.Repeat("c", 64)
	for _, id := range []string{sum, "repo/app@" + sum, "docker-pullable://repo/app@" + sum, "containerd://" + sum} {
		require.True(t, validImageDigest(id), id)
	}
	for _, id := range []string{"", "python:3.12", "sha256:abc", "repo/app@sha256:abc", "http://repo/app@" + sum} {
		require.False(t, validImageDigest(id), id)
	}
}

func TestMergeProfileImageAcrossSeries(t *testing.T) {
	f := loadDiffFixtures(t)
	base := f.plain(f.baseNm)
	newer := f.ts("newpod", 1, helpers.Learning, helpers.Full)
	newer.Spec.ImageTag = "python:3.12"
	newer.Spec.ImageID = "python@sha256:" + strings.Repeat("b", 64)
	older := newer.DeepCopy()
	at, ok := parseReportTimestamp(newer.Annotations[helpers.ReportTimestampMetadataKey])
	require.True(t, ok)
	older.Annotations[helpers.ReportTimestampMetadataKey] = at.Add(-time.Second).String()
	older.Spec.ImageID = "python@sha256:" + strings.Repeat("a", 64)
	mergeContainerProfileTS(base, newer)
	mergeContainerProfileTS(base, older)
	require.Equal(t, newer.Spec.ImageID, base.Spec.ImageID)
	require.Equal(t, at.UTC().Format(time.RFC3339Nano), base.Annotations[imageObservedAt])
}
