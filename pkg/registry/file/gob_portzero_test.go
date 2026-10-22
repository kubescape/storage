package file

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

func ptr32(v int32) *int32 { return &v }

func portZeroCP() *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: softwarecomposition.ContainerProfileSpec{
			Egress: []softwarecomposition.NetworkNeighbor{{
				Identifier: "explicit-zero-port",
				IPAddress:  "9.9.9.9",
				Ports:      []softwarecomposition.NetworkPort{{Name: "TCP-0", Protocol: "TCP", Port: ptr32(0)}},
			}},
			Containers: []softwarecomposition.ContainerProfileContainer{{
				Name: "app",
				Egress: []softwarecomposition.NetworkNeighbor{{
					Identifier: "dns",
					IPAddress:  "1.1.1.1",
					Ports: []softwarecomposition.NetworkPort{
						{Name: "TCP-0", Protocol: "TCP", Port: ptr32(0)},
						{Name: "UDP-53", Protocol: "UDP", Port: ptr32(53)},
					},
				}},
			}},
		},
	}
}

// gob flattens *int32 and omits zeros: without the stamp/restore pair an
// explicit port-0 literal decodes as nil, indistinguishable from an absent port.
func TestGobRoundTrip_PortZeroSurvives(t *testing.T) {
	in := portZeroCP()
	stampPortZero(in)
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(in))
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, gob.NewDecoder(&buf).Decode(out))
	restorePortZero(out)

	require.NotNil(t, out.Spec.Egress[0].Ports[0].Port, "spec-level explicit port 0 must survive the payload round-trip")
	assert.Equal(t, int32(0), *out.Spec.Egress[0].Ports[0].Port)
	require.NotNil(t, out.Spec.Containers[0].Egress[0].Ports[0].Port, "section-level explicit port 0 must survive")
	assert.Equal(t, int32(0), *out.Spec.Containers[0].Egress[0].Ports[0].Port)
	require.NotNil(t, out.Spec.Containers[0].Egress[0].Ports[1].Port, "nonzero port unaffected")
	assert.Equal(t, int32(53), *out.Spec.Containers[0].Egress[0].Ports[1].Port)
}

// Legacy payloads carry no PortZero marker: nonzero ports round-trip as
// before, an absent port stays absent — restore must not invent pointers.
func TestGobRoundTrip_LegacyPayloadUntouched(t *testing.T) {
	legacy := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "old", Namespace: "ns"},
		Spec: softwarecomposition.ContainerProfileSpec{
			Egress: []softwarecomposition.NetworkNeighbor{{
				Identifier: "mixed",
				IPAddress:  "8.8.8.8",
				Ports: []softwarecomposition.NetworkPort{
					{Name: "TCP-443", Protocol: "TCP", Port: ptr32(443)},
					{Name: "TCP-any", Protocol: "TCP"}, // port already lost at rest pre-fix
				},
			}},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(legacy)) // no stamp: legacy writer
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, gob.NewDecoder(&buf).Decode(out))
	restorePortZero(out)

	require.NotNil(t, out.Spec.Egress[0].Ports[0].Port)
	assert.Equal(t, int32(443), *out.Spec.Egress[0].Ports[0].Port)
	assert.Nil(t, out.Spec.Egress[0].Ports[1].Port, "restore must not invent a port where no marker exists")
}

// The migration and downgrade export cross both codecs. Each transition must
// retain explicit zero, nonzero, and absent ports without leaking the gob marker.
func TestPortZero_MigrationAndExportCodecs(t *testing.T) {
	fs := afero.NewMemMapFs()
	const root = "/data"
	const key = "/spdx.softwarecomposition.kubescape.io/containerprofiles/ns/p"
	in := portZeroCP()
	in.Spec.Egress[0].Ports = append(in.Spec.Egress[0].Ports, softwarecomposition.NetworkPort{Name: "absent"})
	require.NoError(t, writeLegacyPayloadFile(fs, filepath.Join(root, key), in))
	migrated, exists, err := decodeLegacyFileAt(t.Context(), fs, root, key)
	require.NoError(t, err)
	require.True(t, exists)
	sch := runtime.NewScheme()
	install.Install(sch)
	body, err := encodePayloadBody(sch, migrated)
	require.NoError(t, err)
	require.NotContains(t, string(body), "PortZero")
	exported := &softwarecomposition.ContainerProfile{}
	require.NoError(t, decodePayloadBody(sch, PayloadEncodingJSONV1Beta1, body, exported))
	// JSON drops the internal marker; export must stamp it again.
	require.False(t, exported.Spec.Egress[0].Ports[0].PortZero)
	require.NoError(t, writeLegacyPayloadFile(fs, filepath.Join(root, key), exported))
	restored, exists, err := decodeLegacyFileAt(t.Context(), fs, root, key)
	require.NoError(t, err)
	require.True(t, exists)
	require.NotNil(t, restored.Spec.Egress[0].Ports[0].Port)
	require.Zero(t, *restored.Spec.Egress[0].Ports[0].Port)
	require.Nil(t, restored.Spec.Egress[0].Ports[1].Port)
	require.NotNil(t, restored.Spec.Containers[0].Egress[0].Ports[0].Port)
	require.Zero(t, *restored.Spec.Containers[0].Egress[0].Ports[0].Port)
	require.Equal(t, int32(53), *restored.Spec.Containers[0].Egress[0].Ports[1].Port)
}

func TestPortZero_SingleWriterRoundTrip(t *testing.T) {
	enableSingleWriter(t)
	s, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()
	in := portZeroCP()
	key := "/spdx.softwarecomposition.kubescape.io/containerprofiles/ns/p"
	require.NoError(t, s.Create(t.Context(), key, in, nil, 0))
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, s.Get(t.Context(), key, storage.GetOptions{}, out))
	require.NotNil(t, out.Spec.Egress[0].Ports[0].Port)
	require.Zero(t, *out.Spec.Egress[0].Ports[0].Port)
}

// A marker from an earlier save must not resurrect a subsequently removed port.
func TestPortZero_RepeatedRoundTripAndRemoval(t *testing.T) {
	for _, port := range []*int32{nil, ptr32(0), ptr32(53)} {
		in := portZeroCP()
		stampPortZero(in)
		in.Spec.Egress[0].Ports[0].Port = port
		stampPortZero(in)
		var buf bytes.Buffer
		require.NoError(t, gob.NewEncoder(&buf).Encode(in))
		out := &softwarecomposition.ContainerProfile{}
		require.NoError(t, gob.NewDecoder(&buf).Decode(out))
		restorePortZero(out)
		require.Equal(t, port, out.Spec.Egress[0].Ports[0].Port)
		require.False(t, out.Spec.Egress[0].Ports[0].PortZero)
		body, err := json.Marshal(in)
		require.NoError(t, err)
		require.NotContains(t, string(body), "PortZero")
	}
}

func TestPortZero_AllProfileSections(t *testing.T) {
	neighbor := []softwarecomposition.NetworkNeighbor{{Ports: []softwarecomposition.NetworkPort{{Port: ptr32(0)}}}}
	section := []softwarecomposition.ContainerProfileContainer{{Ingress: neighbor, Egress: neighbor}}
	in := &softwarecomposition.ContainerProfile{Spec: softwarecomposition.ContainerProfileSpec{
		Ingress: neighbor, Egress: neighbor, Containers: section, InitContainers: section, EphemeralContainers: section,
	}}
	stampPortZero(in)
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(in))
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, gob.NewDecoder(&buf).Decode(out))
	restorePortZero(out)
	visited := 0
	forEachNetworkPort(out, func(p *softwarecomposition.NetworkPort) {
		require.NotNil(t, p.Port)
		require.Zero(t, *p.Port)
		require.False(t, p.PortZero)
		visited++
	})
	require.Equal(t, 8, visited)
}

func TestPortZero_StorageReadPaths(t *testing.T) {
	for _, writer := range []bool{false, true} {
		name := "legacy"
		if writer {
			name = "single-writer"
		}
		t.Run(name, func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = writer
			t.Cleanup(func() { singleWriterEnabled = old })
			s, cleanup := newSingleWriterTestStorage(t)
			defer cleanup()
			const key = "/spdx.softwarecomposition.kubescape.io/containerprofiles/ns/p"
			require.NoError(t, s.Create(t.Context(), key, portZeroCP(), nil, 0))
			out := &softwarecomposition.ContainerProfile{}
			decoded, err := s.tryDecodePayload(key, out)
			require.NoError(t, err)
			require.True(t, decoded)
			require.NotNil(t, out.Spec.Egress[0].Ports[0].Port)
			require.Zero(t, *out.Spec.Egress[0].Ports[0].Port)
			list := &softwarecomposition.ContainerProfileList{}
			require.NoError(t, s.GetList(t.Context(), filepath.Dir(key), storage.ListOptions{
				Recursive: true, Predicate: storage.Everything, ResourceVersion: softwarecomposition.ResourceVersionFullSpec,
			}, list))
			require.Len(t, list.Items, 1)
			require.NotNil(t, list.Items[0].Spec.Egress[0].Ports[0].Port)
			require.Zero(t, *list.Items[0].Spec.Egress[0].Ports[0].Port)
		})
	}
}
