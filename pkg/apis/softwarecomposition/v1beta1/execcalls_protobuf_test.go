package v1beta1

import (
	"testing"
)

// TestExecCalls_ArgsRequired_ProtobufRoundtrip pins the wire-contract for
// the new ArgsRequired field. The codec hand-edits in generated.pb.go
// (MarshalToSizedBuffer / Size / Unmarshal / String) were not run through
// go-to-protobuf because the protoc image is x86_64-only and we are on
// aarch64 (see README §"Changes to the Types"). This test makes sure the
// hand-edit is wire-compatible by round-tripping all four ArgsRequired/Args
// combinations through the gogoproto codec.
func TestExecCalls_ArgsRequired_ProtobufRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		in   ExecCalls
	}{
		{
			name: "ArgsRequired=false (default), Args nil",
			in:   ExecCalls{Path: "/bin/ls"},
		},
		{
			name: "ArgsRequired=false, Args populated",
			in:   ExecCalls{Path: "/bin/sh", Args: []string{"-c", "echo"}},
		},
		{
			name: "ArgsRequired=true, Args empty (the new expressible state)",
			in:   ExecCalls{Path: "/usr/bin/true", Args: []string{}, ArgsRequired: true},
		},
		{
			name: "ArgsRequired=true, Args populated",
			in:   ExecCalls{Path: "/bin/sh", Args: []string{"-c", "*"}, ArgsRequired: true},
		},
		{
			name: "ArgsRequired=true, Envs populated, Args nil",
			in:   ExecCalls{Path: "/bin/sh", Envs: []string{"FOO=bar"}, ArgsRequired: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := tc.in.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var out ExecCalls
			if err := out.Unmarshal(wire); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if out.Path != tc.in.Path {
				t.Errorf("Path: got %q, want %q", out.Path, tc.in.Path)
			}
			if !stringSlicesEqual(out.Args, tc.in.Args) {
				t.Errorf("Args: got %v, want %v", out.Args, tc.in.Args)
			}
			if !stringSlicesEqual(out.Envs, tc.in.Envs) {
				t.Errorf("Envs: got %v, want %v", out.Envs, tc.in.Envs)
			}
			if out.ArgsRequired != tc.in.ArgsRequired {
				t.Errorf("ArgsRequired: got %v, want %v (the entire point of this test)", out.ArgsRequired, tc.in.ArgsRequired)
			}

			// Size must match the actual marshaled length.
			if got, want := tc.in.Size(), len(wire); got != want {
				t.Errorf("Size() = %d, but Marshal produced %d bytes", got, want)
			}
		})
	}
}

// TestExecCalls_ArgsRequired_ProtobufFieldNumber pins the field number
// chosen for ArgsRequired (field 4, wire-type 0 = varint). The wire tag
// byte for field=4, wire-type=0 is (4 << 3) | 0 = 0x20. Catching a tag
// drift here avoids a silent breaking change for downstream protobuf
// consumers compiling against generated.proto.
func TestExecCalls_ArgsRequired_ProtobufFieldNumber(t *testing.T) {
	in := ExecCalls{ArgsRequired: true}
	wire, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Codec writes field 1 (Path tag 0xa, len 0), then ArgsRequired
	// (tag 0x20, value 0x01). Find the 0x20 tag and verify the next byte
	// is the bool value (0x01 for true).
	found := false
	for i := 0; i < len(wire)-1; i++ {
		if wire[i] == 0x20 {
			if wire[i+1] != 0x01 {
				t.Errorf("ArgsRequired wire value at offset %d: got 0x%02x, want 0x01 (true)", i+1, wire[i+1])
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("did not find tag 0x20 (field=4 varint) in wire bytes: %x", wire)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true // nil == [] for protobuf-roundtrip purposes
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
