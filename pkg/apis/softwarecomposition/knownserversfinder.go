package softwarecomposition

import (
	"net"

	"github.com/yl2chen/cidranger"
)

var _ IKnownServersFinder = (*KnownServersFinderImpl)(nil)
var _ IKnownServerEntry = (*KnownServersFinderEntry)(nil)

type KnownServersFinderImpl struct {
	ranger cidranger.Ranger
}

type KnownServersFinderEntry struct {
	knownServer KnownServerEntry
	network     net.IPNet
}

func NewKnownServersFinderImpl(knownServers []KnownServer) IKnownServersFinder {
	// build the ranger for searching
	ranger := cidranger.NewPCTrieRanger()
	for _, knownServer := range knownServers {
		for _, knownServerEntry := range knownServer.Spec {
			if v := NewKnownServersFinderEntry(knownServerEntry); v != nil {
				_ = ranger.Insert(v)
			}
		}
	}
	return &KnownServersFinderImpl{
		ranger: ranger,
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

func NewKnownServersFinderEntry(kse KnownServerEntry) *KnownServersFinderEntry {
	_, res, err := net.ParseCIDR(kse.IPBlock)
	if err != nil || res == nil {
		return nil
	}
	return &KnownServersFinderEntry{knownServer: kse, network: *res}
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
