package file

import (
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildParseContainerProfileKey_RoundTrip pins that a ProfileIdentifier
// survives a Build->Parse round-trip for every supported HostType. The key is
// the only self-describing carrier of a profile's scope, so a Build/Parse
// mismatch would silently misroute or drop profiles for non-K8s hosts (ECS,
// Fargate, standalone Host), which have no other coverage.
func TestBuildParseContainerProfileKey_RoundTrip(t *testing.T) {
	const kind = ContainerProfileKind

	tests := []struct {
		name       string
		hostType   armotypes.HostType
		id         armotypes.ProfileIdentifier
		wantParsed armotypes.ProfileIdentifier // expected identifier after Parse
	}{
		{
			name:     "K8s new format (with cluster)",
			hostType: armotypes.HostTypeKubernetes,
			id: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:  armotypes.HostTypeKubernetes,
					Cluster:   "prod-cluster",
					Namespace: "kubescape",
				},
				Name: "replicaset-nginx-abc123-nginx-1a2b-3c4d",
			},
			wantParsed: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:  armotypes.HostTypeKubernetes,
					Cluster:   "prod-cluster",
					Namespace: "kubescape",
				},
				Name: "replicaset-nginx-abc123-nginx-1a2b-3c4d",
			},
		},
		{
			name:     "K8s legacy format (empty cluster)",
			hostType: armotypes.HostTypeKubernetes,
			id: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:  armotypes.HostTypeKubernetes,
					Namespace: "kubescape",
				},
				Name: "replicaset-redis-7c9f8d4b6-redis-5555-6666",
			},
			wantParsed: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:  armotypes.HostTypeKubernetes,
					Namespace: "kubescape",
				},
				Name: "replicaset-redis-7c9f8d4b6-redis-5555-6666",
			},
		},
		{
			name:     "empty HostType defaults to K8s",
			hostType: "",
			id: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					Cluster:   "c1",
					Namespace: "ns1",
				},
				Name: "obj1",
			},
			wantParsed: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:  armotypes.HostTypeKubernetes, // Parse normalizes "" -> kubernetes
					Cluster:   "c1",
					Namespace: "ns1",
				},
				Name: "obj1",
			},
		},
		{
			name:     "ECS EC2",
			hostType: armotypes.HostTypeEcsEc2,
			id: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:               armotypes.HostTypeEcsEc2,
					Cluster:                "ecs-cluster",
					CloudAccountIdentifier: "123456789012",
					Region:                 "us-east-1",
				},
				Name: "task-web",
			},
			wantParsed: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:               armotypes.HostTypeEcsEc2,
					Cluster:                "ecs-cluster",
					CloudAccountIdentifier: "123456789012",
					Region:                 "us-east-1",
				},
				Name: "task-web",
			},
		},
		{
			name:     "ECS Fargate",
			hostType: armotypes.HostTypeEcsFargate,
			id: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:               armotypes.HostTypeEcsFargate,
					Cluster:                "fargate-cluster",
					CloudAccountIdentifier: "210987654321",
					Region:                 "eu-west-1",
				},
				Name: "task-api",
			},
			wantParsed: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:               armotypes.HostTypeEcsFargate,
					Cluster:                "fargate-cluster",
					CloudAccountIdentifier: "210987654321",
					Region:                 "eu-west-1",
				},
				Name: "task-api",
			},
		},
		{
			name:     "standalone Host (EC2)",
			hostType: armotypes.HostTypeEc2,
			id: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:               armotypes.HostTypeEc2,
					HostID:                 "i-0abcdef1234567890",
					CloudAccountIdentifier: "555555555555",
					Region:                 "ap-south-1",
				},
				Name: "host-daemon",
			},
			wantParsed: armotypes.ProfileIdentifier{
				ProfileScope: armotypes.ProfileScope{
					HostType:               armotypes.HostTypeEc2,
					HostID:                 "i-0abcdef1234567890",
					CloudAccountIdentifier: "555555555555",
					Region:                 "ap-south-1",
				},
				Name: "host-daemon",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := BuildContainerProfileKey(tt.id, kind)

			gotID, prefix, root, gotKind, err := ParseContainerProfileKey(key, tt.hostType)
			require.NoError(t, err, "round-trip parse must succeed for key %q", key)

			assert.Equal(t, "", prefix, "prefix segment (leading empty)")
			assert.Equal(t, softwarecomposition.GroupName, root, "root segment")
			assert.Equal(t, kind, gotKind, "kind segment")
			assert.Equal(t, tt.wantParsed, gotID, "identifier must round-trip through Build/Parse")
		})
	}
}

// TestParseContainerProfileKey_Malformed pins the length-guard error branches for
// each host-type family.
func TestParseContainerProfileKey_Malformed(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		hostType armotypes.HostType
		wantErr  string
	}{
		{
			name:     "fewer than 3 parts",
			key:      "a/b",
			hostType: armotypes.HostTypeKubernetes,
			wantErr:  "at least 3 parts",
		},
		{
			name:     "K8s fewer than 5 parts",
			key:      "/root/kind/onlyfour",
			hostType: armotypes.HostTypeKubernetes,
			wantErr:  "at least 5 parts",
		},
		{
			name:     "ECS fewer than 7 parts",
			key:      "/root/kind/cluster/acct/region", // 6 parts
			hostType: armotypes.HostTypeEcsEc2,
			wantErr:  "expected 7 parts",
		},
		{
			name:     "Host fewer than 7 parts",
			key:      "/root/kind/hostid/acct/region", // 6 parts
			hostType: armotypes.HostTypeEc2,
			wantErr:  "expected 7 parts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := ParseContainerProfileKey(tt.key, tt.hostType)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestECSAndHostKeysToPath pins the literal key layout the ECS and Host builders
// emit, so a future edit that reorders segments is caught independently of Parse.
func TestECSAndHostKeysToPath(t *testing.T) {
	assert.Equal(t,
		"p/r/k/cluster/acct/region/name",
		ECSKeysToPath("p", "r", "k", "cluster", "acct", "region", "name"))
	assert.Equal(t,
		"p/r/k/hostid/acct/region/name",
		HostKeysToPath("p", "r", "k", "hostid", "acct", "region", "name"))
}
