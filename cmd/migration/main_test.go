package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// The migration binary is a one-shot gob->JSON decoder. Its whole reason to
// exist is to read legacy gob streams whose numeric fields were encoded as
// uint64 (LegacyArg.Index/Value/ValueTwo, LegacySyscall.ErrnoRet) and re-emit
// them as JSON so they can be re-imported under the current int64 layout. These
// tests pin that contract end-to-end by driving the real built binary against
// fixtures, exercising both the happy path (legacy uint64 fields survive the
// round-trip, ContainerProfile spec shape is preserved) and the error branches
// (unsupported type, corrupt stream, missing file).

var (
	migrationBinOnce sync.Once
	migrationBinPath string
	migrationBinErr  error
)

// buildMigrationBinary compiles cmd/migration once per test binary and returns
// the path to the built executable. It does not restructure or modify main.go.
func buildMigrationBinary(t *testing.T) string {
	t.Helper()
	migrationBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "migration-bin")
		if err != nil {
			migrationBinErr = err
			return
		}
		bin := filepath.Join(dir, "migration")
		// Build the current package (cmd/migration). The test's working
		// directory is the package directory.
		cmd := exec.CommandContext(t.Context(), "go", "build", "-mod=mod", "-o", bin, ".")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			migrationBinErr = err
			t.Logf("go build stderr: %s", stderr.String())
			return
		}
		migrationBinPath = bin
	})
	require.NoError(t, migrationBinErr, "failed to build migration binary")
	return migrationBinPath
}

// writeGobFixture gob-encodes the given value to a temp file, mirroring how the
// legacy storage binary persisted these structures. Because the fixture is
// encoded with the very LegacyContainerProfile/LegacySeccompProfile types the
// migration binary decodes into, gob's structural, name-scoped matching lets the
// stream decode cleanly - exactly as a real legacy stream would.
func writeGobFixture(t *testing.T, v interface{}) string {
	t.Helper()
	// The migration binary registers these types before decoding; register the
	// same set here so the encoder and decoder agree on the wire format.
	gob.Register(map[string]interface{}{})
	gob.Register([]interface{}{})
	gob.Register(metav1.Time{})

	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(v))

	path := filepath.Join(t.TempDir(), "fixture.gob")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// runMigration runs the built binary with the given args and returns
// stdout, stderr, and the process exit code.
func runMigration(t *testing.T, bin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run migration binary: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

// legacyContainerProfileFixture builds a known legacy ContainerProfile that
// populates every legacy uint64-bearing field the migration must preserve.
func legacyContainerProfileFixture() *LegacyContainerProfile {
	cp := &LegacyContainerProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ContainerProfile",
			APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "replicaset-nginx-abc123-nginx-1a2b-3c4d",
			Namespace: "kubescape",
			Labels: map[string]string{
				"kubescape.io/workload-kind": "Deployment",
				"kubescape.io/workload-name": "nginx",
			},
		},
	}
	cp.Spec.Architectures = []string{"amd64", "arm64"}
	cp.Spec.Capabilities = []string{"NET_ADMIN", "SYS_PTRACE"}
	cp.Spec.Execs = []LegacyExecCalls{{Path: "/bin/sh", Args: []string{"-c", "echo hi"}, Envs: []string{"PATH=/bin"}}}
	cp.Spec.Opens = []LegacyOpenCalls{{Path: "/etc/passwd", Flags: []string{"O_RDONLY"}}}
	cp.Spec.Syscalls = []string{"read", "write", "openat"}
	cp.Spec.ImageID = "sha256:deadbeef"
	cp.Spec.ImageTag = "nginx:1.25"

	// Legacy uint64 fields live under the seccomp profile's syscalls/args.
	cp.Spec.SeccompProfile = LegacySingleSeccompProfile{
		Name: "nginx",
		Path: "/nginx",
	}
	cp.Spec.SeccompProfile.Spec.DefaultAction = "SCMP_ACT_ERRNO"
	cp.Spec.SeccompProfile.Spec.Architectures = []string{"SCMP_ARCH_X86_64"}
	cp.Spec.SeccompProfile.Spec.Syscalls = []*LegacySyscall{
		{
			Names:    []string{"ptrace"},
			Action:   "SCMP_ACT_ERRNO",
			ErrnoRet: 1, // legacy uint64
			Args: []*LegacyArg{
				{Index: 0, Value: 18446744073709551615, ValueTwo: 42, Op: "SCMP_CMP_EQ"}, // max uint64
			},
		},
	}

	// A representative ingress neighbor to prove the network shape survives.
	port := int32(443)
	cp.Spec.Ingress = []LegacyNetworkNeighbor{
		{
			Identifier: "in-1",
			Type:       "internal",
			IPAddress:  "10.0.0.5",
			Ports:      []LegacyNetworkPort{{Name: "TCP-443", Protocol: "TCP", Port: &port}},
		},
	}
	return cp
}

// TestMigration_GoldenContainerProfile is the golden migration test: a known
// legacy ContainerProfile gob stream decoded by the real binary must round-trip
// every legacy uint64 field and preserve the ContainerProfile spec shape as JSON.
func TestMigration_GoldenContainerProfile(t *testing.T) {
	bin := buildMigrationBinary(t)
	fixture := legacyContainerProfileFixture()
	path := writeGobFixture(t, fixture)

	stdout, stderr, code := runMigration(t, bin, "-file", path, "-type", "ContainerProfile")
	require.Equal(t, 0, code, "decode must succeed; stderr=%s", stderr)
	require.NotEmpty(t, stdout)

	// The JSON must decode back into the same legacy layout: this proves each
	// legacy field round-trips through gob->JSON without loss or type change.
	var got LegacyContainerProfile
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "output must be valid JSON: %s", stdout)

	// Object identity / spec shape.
	assert.Equal(t, fixture.Name, got.Name)
	assert.Equal(t, fixture.Namespace, got.Namespace)
	assert.Equal(t, fixture.Labels, got.Labels)
	assert.Equal(t, fixture.Spec.Architectures, got.Spec.Architectures)
	assert.Equal(t, fixture.Spec.Capabilities, got.Spec.Capabilities)
	assert.Equal(t, fixture.Spec.Syscalls, got.Spec.Syscalls)
	assert.Equal(t, fixture.Spec.ImageID, got.Spec.ImageID)
	assert.Equal(t, fixture.Spec.ImageTag, got.Spec.ImageTag)
	require.Len(t, got.Spec.Execs, 1)
	assert.Equal(t, "/bin/sh", got.Spec.Execs[0].Path)
	require.Len(t, got.Spec.Opens, 1)
	assert.Equal(t, "/etc/passwd", got.Spec.Opens[0].Path)

	// Legacy uint64 fields: the whole reason the tool exists.
	require.Len(t, got.Spec.SeccompProfile.Spec.Syscalls, 1)
	sc := got.Spec.SeccompProfile.Spec.Syscalls[0]
	assert.Equal(t, uint64(1), sc.ErrnoRet, "LegacySyscall.ErrnoRet must round-trip")
	require.Len(t, sc.Args, 1)
	arg := sc.Args[0]
	assert.Equal(t, uint64(0), arg.Index, "LegacyArg.Index must round-trip")
	assert.Equal(t, uint64(18446744073709551615), arg.Value, "LegacyArg.Value must round-trip full uint64 range")
	assert.Equal(t, uint64(42), arg.ValueTwo, "LegacyArg.ValueTwo must round-trip")
	assert.Equal(t, "SCMP_CMP_EQ", arg.Op)

	// Network shape.
	require.Len(t, got.Spec.Ingress, 1)
	assert.Equal(t, "10.0.0.5", got.Spec.Ingress[0].IPAddress)
	require.Len(t, got.Spec.Ingress[0].Ports, 1)
	require.NotNil(t, got.Spec.Ingress[0].Ports[0].Port)
	assert.Equal(t, int32(443), *got.Spec.Ingress[0].Ports[0].Port)

	// Also assert the raw JSON carries the legacy uint64 as a plain number under
	// the documented json tag, so a consumer re-importing the JSON sees the full
	// value verbatim (a float64 round-trip would lose the low bits of maxuint64).
	assert.Contains(t, stdout, `"value":18446744073709551615`, "value json tag must carry full uint64")
}

// TestMigration_GoldenSeccompProfile pins the standalone SeccompProfile decode
// path (the other supported -type), which also carries legacy uint64 ErrnoRet/Args.
func TestMigration_GoldenSeccompProfile(t *testing.T) {
	bin := buildMigrationBinary(t)

	sp := &LegacySeccompProfile{
		TypeMeta:   metav1.TypeMeta{Kind: "SeccompProfile"},
		ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "kubescape"},
	}
	single := LegacySingleSeccompProfile{Name: "nginx"}
	single.Spec.DefaultAction = "SCMP_ACT_ERRNO"
	single.Spec.Syscalls = []*LegacySyscall{
		{
			Names:    []string{"ptrace"},
			Action:   "SCMP_ACT_ERRNO",
			ErrnoRet: 13,
			Args:     []*LegacyArg{{Index: 1, Value: 100, ValueTwo: 200, Op: "SCMP_CMP_GE"}},
		},
	}
	sp.Spec.Containers = []LegacySingleSeccompProfile{single}

	path := writeGobFixture(t, sp)
	stdout, stderr, code := runMigration(t, bin, "-file", path, "-type", "SeccompProfile")
	require.Equal(t, 0, code, "decode must succeed; stderr=%s", stderr)

	var got LegacySeccompProfile
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got.Spec.Containers, 1)
	require.Len(t, got.Spec.Containers[0].Spec.Syscalls, 1)
	sc := got.Spec.Containers[0].Spec.Syscalls[0]
	assert.Equal(t, uint64(13), sc.ErrnoRet)
	require.Len(t, sc.Args, 1)
	assert.Equal(t, uint64(100), sc.Args[0].Value)
	assert.Equal(t, uint64(200), sc.Args[0].ValueTwo)
}

// TestMigration_UnsupportedType exercises the unsupported -type branch: the
// binary must reject it with a non-zero exit and a diagnostic on stderr.
func TestMigration_UnsupportedType(t *testing.T) {
	bin := buildMigrationBinary(t)
	// Any existing file works; the type is rejected before decode.
	path := writeGobFixture(t, legacyContainerProfileFixture())
	stdout, stderr, code := runMigration(t, bin, "-file", path, "-type", "NotAType")
	assert.NotEqual(t, 0, code, "unsupported type must be a non-zero exit")
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "unsupported type")
}

// TestMigration_CorruptStream exercises the decode-failure branch: a stream that
// is not a valid gob for the requested type must fail with a non-zero exit.
func TestMigration_CorruptStream(t *testing.T) {
	bin := buildMigrationBinary(t)
	path := filepath.Join(t.TempDir(), "corrupt.gob")
	require.NoError(t, os.WriteFile(path, []byte("this is not a gob stream at all"), 0o600))

	stdout, stderr, code := runMigration(t, bin, "-file", path, "-type", "ContainerProfile")
	assert.NotEqual(t, 0, code, "corrupt stream must be a non-zero exit")
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "decode failed")
}

// TestMigration_MissingFile exercises the open-failure branch.
func TestMigration_MissingFile(t *testing.T) {
	bin := buildMigrationBinary(t)
	stdout, stderr, code := runMigration(t, bin, "-file", filepath.Join(t.TempDir(), "does-not-exist.gob"), "-type", "ContainerProfile")
	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "failed to open file")
}

// Ensure the softwarecomposition import is used (the legacy types reference it),
// keeping the fixture honest about the shared spec types.
var _ = softwarecomposition.HTTPEndpoint{}
