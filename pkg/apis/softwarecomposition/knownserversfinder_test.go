package softwarecomposition

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContains(t *testing.T) {
	knownServers := []KnownServer{
		{
			Spec: []KnownServerEntry{
				{Server: "server1", Name: "name1", IPBlock: "192.168.1.0/24"},
				{Server: "server2", Name: "name2", IPBlock: "10.0.0.0/8"},
				{Server: "server3", Name: "name3", IPBlock: ""},
				{Server: "server4", Name: "name4", IPBlock: "invalid"},
				{Server: "server5", Name: "name5", IPBlock: "192.168.1.128/25"},
			},
		},
	}

	finder := NewKnownServersFinderImpl(knownServers)

	tests := []struct {
		ip               string
		expected         bool
		expectedIPBlocks []string
		expectedServers  []string
	}{
		{"192.168.1.1", true, []string{"192.168.1.0/24"}, []string{"server1"}},
		{"10.0.0.1", true, []string{"10.0.0.0/8"}, []string{"server2"}},
		{"172.16.0.1", false, nil, nil},
		{"192.168.1.200", true, []string{"192.168.1.0/24", "192.168.1.128/25"}, []string{"server1", "server5"}},
	}

	for _, test := range tests {
		ip := net.ParseIP(test.ip)
		entries, contains := finder.Contains(ip)
		assert.Equal(t, test.expected, contains)
		if contains {
			assert.NotEmpty(t, entries)
			servers := []string{}
			for _, entry := range entries {
				servers = append(servers, entry.GetServer())
			}
			ipBlocks := []string{}
			for _, entry := range entries {
				ipBlocks = append(ipBlocks, entry.GetIPBlock())
			}
			assert.ElementsMatch(t, test.expectedIPBlocks, ipBlocks)
			assert.ElementsMatch(t, test.expectedServers, servers)
		} else {
			assert.Empty(t, entries)
		}
	}
}
