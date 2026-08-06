package file

import (
	"context"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestPreSave_ConsolidatedCompletedImmutability pins the completed-immutability
// contract for the consolidated (non-time-series) ContainerProfile path of
// PreSave. A direct patch of the consolidated profile carries no
// ReportSeriesId, so it must not be able to regress a Completed profile back to
// Learning/Ready. The patch still succeeds (PreSave returns no error); only the
// incoming status annotation is reverted to Completed.
func TestPreSave_ConsolidatedCompletedImmutability(t *testing.T) {
	const (
		ns   = "ns"
		name = "replicaset-nginx-abc123-nginx-1a2b-3c4d"
	)

	// consolidatedKey mirrors the key PreSave builds for the consolidated
	// profile: same scope, Name == profile.Name, kind "containerprofile".
	consolidatedKey := BuildContainerProfileKey(armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{
			HostType:  armotypes.HostTypeKubernetes,
			Namespace: ns,
		},
		Name: name,
	}, "containerprofile")

	newIncoming := func(status string) *softwarecomposition.ContainerProfile {
		return &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Annotations: map[string]string{
					// no ReportSeriesIdMetadataKey => consolidated (non-TS) path
					helpersv1.StatusMetadataKey: status,
				},
			},
		}
	}

	storedWith := func(status string) map[string]softwarecomposition.ContainerProfile {
		return map[string]softwarecomposition.ContainerProfile{
			consolidatedKey: {
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: status,
					},
				},
			},
		}
	}

	tests := []struct {
		name         string
		storedStatus string
		hasStored    bool
		incoming     string
		wantStatus   string
	}{
		{
			name:         "completed profile cannot regress to learning",
			storedStatus: helpersv1.Completed,
			hasStored:    true,
			incoming:     helpersv1.Learning,
			wantStatus:   helpersv1.Completed, // reverted
		},
		{
			name:         "learning stays learning (no spurious revert)",
			storedStatus: helpersv1.Learning,
			hasStored:    true,
			incoming:     helpersv1.Learning,
			wantStatus:   helpersv1.Learning, // unchanged
		},
		{
			name:         "completed amended completed stays completed",
			storedStatus: helpersv1.Completed,
			hasStored:    true,
			incoming:     helpersv1.Completed,
			wantStatus:   helpersv1.Completed, // unchanged, amendment allowed
		},
		{
			name:       "no existing profile is a create (learning allowed)",
			hasStored:  false,
			incoming:   helpersv1.Learning,
			wantStatus: helpersv1.Learning, // unchanged, nothing to guard
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &fakeStorage{}
			if tt.hasStored {
				fs.profiles = storedWith(tt.storedStatus)
			} else {
				fs.profiles = map[string]softwarecomposition.ContainerProfile{}
			}

			processor := ContainerProfileProcessor{
				HostType:                armotypes.HostTypeKubernetes,
				MaxContainerProfileSize: 40000,
				ContainerProfileStorage: fs,
			}

			incoming := newIncoming(tt.incoming)
			err := processor.PreSave(context.TODO(), incoming)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantStatus, incoming.Annotations[helpersv1.StatusMetadataKey],
				"consolidated status after PreSave")
		})
	}
}
