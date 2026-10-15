package file

import (
	"fmt"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDNSBaselineConsolidation(t *testing.T) {
	profile := &softwarecomposition.ContainerProfile{}
	for _, path := range []string{"/app", "/usr/bin/python3"} {
		mergeContainerProfileTS(profile, &softwarecomposition.ContainerProfile{Spec: softwarecomposition.ContainerProfileSpec{DNSBaselineVersion: "v1", DNSLookups: []softwarecomposition.DNSLookup{{Name: "API.VENDOR.COM", ExePaths: []string{path}, Addresses: []string{"192.0.2.1"}}, {Name: "cdn.vendor.com.", ExePaths: []string{path}, Addresses: []string{"192.0.2.1"}}}}})
	}
	result := DeflateContainerProfileSpec(profile.Spec, nil, dynamicpathdetector.CollapseSettings{})
	require.Len(t, result.DNSLookups, 2)
	require.Equal(t, "api.vendor.com.", result.DNSLookups[0].Name)
	require.Equal(t, []string{"/app", "/usr/bin/python3"}, result.DNSLookups[0].ExePaths)
	require.Equal(t, "v1", result.DNSBaselineVersion)
	require.Empty(t, result.Egress)
}

func TestDNSBaselineAddressCap(t *testing.T) {
	entry := softwarecomposition.DNSLookup{Name: "example.com."}
	for i := 0; i < 100; i++ {
		entry.Addresses = append(entry.Addresses, fmt.Sprintf("192.0.2.%d", i))
	}
	require.Len(t, DeflateDNSLookups([]softwarecomposition.DNSLookup{entry})[0].Addresses, MaxDNSAddressesPerName)
}

func TestDNSBaselineProtobufAndDeepCopy(t *testing.T) {
	profile := &v1beta1.ContainerProfile{Spec: v1beta1.ContainerProfileSpec{DNSBaselineVersion: "v1", DNSBaselineTruncated: true, DNSLookups: []v1beta1.DNSLookup{{Name: "example.com.", ExePaths: []string{"/app"}, Addresses: []string{"192.0.2.1"}}}, Containers: []v1beta1.ContainerProfileContainer{{Name: "app", DNSBaselineVersion: "v1", DNSLookups: []v1beta1.DNSLookup{{Name: "internal.svc.cluster.local."}}}}}}
	wire, err := profile.Marshal()
	require.NoError(t, err)
	var decoded v1beta1.ContainerProfile
	require.NoError(t, decoded.Unmarshal(wire))
	require.Equal(t, profile.Spec, decoded.Spec)
	copy := profile.DeepCopy()
	copy.Spec.DNSLookups[0].ExePaths[0] = "changed"
	require.Equal(t, "/app", profile.Spec.DNSLookups[0].ExePaths[0])
}

func TestDNSBaselineMixedVersionsStayUnavailable(t *testing.T) {
	for _, versions := range [][]string{{"", "v1"}, {"v1", ""}} {
		profile := &softwarecomposition.ContainerProfile{}
		for _, version := range versions {
			mergeContainerProfileTS(profile, &softwarecomposition.ContainerProfile{Spec: softwarecomposition.ContainerProfileSpec{DNSBaselineVersion: version}})
		}
		require.True(t, profile.Spec.DNSBaselineTruncated)
	}
	profile := &softwarecomposition.ContainerProfile{Spec: softwarecomposition.ContainerProfileSpec{Architectures: []string{"amd64"}}}
	mergeContainerProfileTS(profile, &softwarecomposition.ContainerProfile{Spec: softwarecomposition.ContainerProfileSpec{DNSBaselineVersion: "v1"}})
	require.Equal(t, "legacy", profile.Spec.DNSBaselineVersion)
	require.True(t, profile.Spec.DNSBaselineTruncated)
}
