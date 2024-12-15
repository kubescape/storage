package softwarecomposition

import (
	"net"

	"github.com/yl2chen/cidranger"
)

var _ IKnownServersFinder = (*KnownServersFinderImpl)(nil)
var _ IKnownServerEntry = (*KnownServersFinderEntry)(nil)

type KnownServersFinderImpl struct {
	ranger       cidranger.Ranger
	knownServers []KnownServer
}

type KnownServersFinderEntry struct {
	knownServer KnownServerEntry
	network     net.IPNet
}

func NewKnownServersFinderEntry(kse KnownServerEntry) (*KnownServersFinderEntry, error) {
	_, res, err := net.ParseCIDR(kse.IPBlock)
	if err != nil || res == nil {
		return nil, err
	}
	return &KnownServersFinderEntry{knownServer: kse, network: *res}, nil
}

func (k *KnownServersFinderEntry) GetServer() string {
	return k.knownServer.Server
}

func (k *KnownServersFinderEntry) GetName() string {
	return k.knownServer.Name
}

func (k *KnownServersFinderEntry) GetIPBlock() string {
	return k.knownServer.IPBlock
}

func (k *KnownServersFinderEntry) Network() net.IPNet {
	return k.network
}

func NewKnownServersFinderImpl(knownServers []KnownServer) IKnownServersFinder {
	// build the ranger for searching
	ranger := cidranger.NewPCTrieRanger()
	for _, knownServer := range knownServers {
		for _, knownServerEntry := range knownServer.Spec {
			if rangerEntry, err := NewKnownServersFinderEntry(knownServerEntry); err == nil && rangerEntry != nil {
				_ = ranger.Insert(rangerEntry)
			}
		}
	}
	return &KnownServersFinderImpl{
		ranger:       ranger,
		knownServers: knownServers,
	}
}

func (k *KnownServersFinderImpl) Contains(ip net.IP) ([]IKnownServerEntry, bool) {
	if k.ranger == nil {
		return nil, false
	}
	contains, _ := k.ranger.Contains(ip)
	if contains {
		if entries, err := k.ranger.ContainingNetworks(ip); err == nil && len(entries) > 0 {
			knownServersEntries := make([]IKnownServerEntry, 0, len(entries))
			for _, entry := range entries {
				if v, ok := entry.(*KnownServersFinderEntry); ok {
					knownServersEntries = append(knownServersEntries, v)
				}
			}
			return knownServersEntries, true
		}
	}
	return nil, false
}

func (k *KnownServersFinderImpl) GetKnownServers() []KnownServer {
	return k.knownServers
}
