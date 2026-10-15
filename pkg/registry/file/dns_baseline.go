package file

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"sort"
	"strings"
)

const MaxDNSBaselineNames = 4096
const MaxDNSAddressesPerName = 16

// DeflateDNSLookups merges names and unions paths and addresses across report batches.
func DeflateDNSLookups(entries []softwarecomposition.DNSLookup) []softwarecomposition.DNSLookup {
	if len(entries) == 0 {
		return nil
	}
	byName := make(map[string]softwarecomposition.DNSLookup)
	for _, entry := range entries {
		name := strings.ToLower(strings.TrimSuffix(entry.Name, "."))
		if name == "" {
			continue
		}
		name += "."
		merged := byName[name]
		merged.Name = name
		merged.ExePaths = DeflateSortString(append(merged.ExePaths, entry.ExePaths...))
		merged.Addresses = DeflateSortString(append(merged.Addresses, entry.Addresses...))
		if len(merged.Addresses) > MaxDNSAddressesPerName {
			merged.Addresses = merged.Addresses[:MaxDNSAddressesPerName]
		}
		byName[name] = merged
	}
	result := make([]softwarecomposition.DNSLookup, 0, len(byName))
	for _, entry := range byName {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func cappedDNSLookups(entries []softwarecomposition.DNSLookup) ([]softwarecomposition.DNSLookup, bool) {
	entries = DeflateDNSLookups(entries)
	if len(entries) > MaxDNSBaselineNames {
		return entries[:MaxDNSBaselineNames], true
	}
	return entries, false
}
