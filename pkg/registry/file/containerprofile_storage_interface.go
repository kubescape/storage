package file

import (
	"context"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"zombiezen.com/go/sqlite"
)

type GetAggregatedDataSignature func(ctx context.Context, conn *sqlite.Conn, key string, parts map[string]string) (string, string, string)

type ContainerProfileStorage interface {
	DeleteContainerProfile(ctx context.Context, conn *sqlite.Conn, key string) error
	GetContainerProfile(ctx context.Context, conn *sqlite.Conn, key string) (softwarecomposition.ContainerProfile, error)
	GetContainerProfileMetadata(ctx context.Context, conn *sqlite.Conn, key string) (softwarecomposition.ContainerProfile, error)
	GetSbom(ctx context.Context, conn *sqlite.Conn, key string) (softwarecomposition.SBOMSyft, error) // return storage.ErrCodeKeyNotFound if not implemented
	GetTsContainerProfile(ctx context.Context, conn *sqlite.Conn, key string) (softwarecomposition.ContainerProfile, error)
	SaveContainerProfile(ctx context.Context, conn *sqlite.Conn, key string, profile *softwarecomposition.ContainerProfile) error
	UpdateApplicationProfile(ctx context.Context, conn *sqlite.Conn, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time, getAggregatedData GetAggregatedDataSignature) error
	UpdateNetworkNeighborhood(ctx context.Context, conn *sqlite.Conn, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time, getAggregatedData GetAggregatedDataSignature) error
}
