package utils

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kinbiko/jsonassert"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func FileContent(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}

func FileToUnstructured(path string) *unstructured.Unstructured {
	b, _ := os.ReadFile(path)
	u := &unstructured.Unstructured{}
	_ = u.UnmarshalJSON(b)
	return u
}

func TestCanonicalHash(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    string
		wantErr bool
	}{
		{
			name:    "error",
			in:      []byte("test"),
			wantErr: true,
		},
		{
			name: "empty",
			in:   []byte("{}"),
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "simple",
			in:   []byte(`{"a":"b"}`),
			want: "baf4fd048ca2e8f75d531af13c5869eaa8e38c3020e1dfcebe3c3ac019a3bab2",
		},
		{
			name: "pod",
			in:   FileContent("testdata/pod.json"),
			want: "1ae52b23166388144c602360fb73dd68736e88943f6e16fab1bf07347484f8e8",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalHash(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("CanonicalHash() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRemoveManagedFields(t *testing.T) {
	tests := []struct {
		name string
		obj  *unstructured.Unstructured
		want []byte
	}{
		{
			name: "Remove fields from networkPolicy",
			obj:  FileToUnstructured("testdata/networkPolicy.json"),
			want: FileContent("testdata/networkPolicyCleaned.json"),
		},
		{
			name: "Do nothing if no managedFields",
			obj:  FileToUnstructured("testdata/pod.json"),
			want: FileContent("testdata/pod.json"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RemoveManagedFields(tt.obj)
			ja := jsonassert.New(t)
			b, err := json.Marshal(tt.obj.Object)
			assert.NoError(t, err)
			ja.Assert(string(b), string(tt.want))
		})
	}
}

func TestRemoveSpecificFields(t *testing.T) {
	tests := []struct {
		name   string
		fields [][]string
		obj    *unstructured.Unstructured
		want   []byte
	}{
		{
			name:   "remove fields from node",
			fields: [][]string{{"status", "conditions"}},
			obj:    FileToUnstructured("testdata/node.json"),
			want:   FileContent("testdata/nodeCleaned.json"),
		},
		{
			name:   "remove no fields from pod",
			fields: [][]string{},
			obj:    FileToUnstructured("testdata/pod.json"),
			want:   FileContent("testdata/pod.json"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RemoveSpecificFields(tt.obj, tt.fields)
			assert.NoError(t, err)
			ja := jsonassert.New(t)
			b, err := json.Marshal(tt.obj.Object)
			assert.NoError(t, err)
			ja.Assert(string(b), string(tt.want))
		})
	}
}
