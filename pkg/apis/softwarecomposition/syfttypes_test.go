package softwarecomposition

import (
	"testing"

	_ "embed"

	"github.com/stretchr/testify/assert"
)

//go:embed testdata/artifact.json
var artifact []byte

//go:embed testdata/artifact-v01011.json
var artifactV01011 []byte

func TestUpdateSBOMSyft(t *testing.T) {
	type args struct {
		id           string
		metadataType string
	}
	tests := []struct {
		name    string
		input   []byte
		args    args
		wantErr bool
	}{
		{
			name:    "TestUpdateSBOMSyft",
			input:   artifact,
			args:    args{id: "8a49897e59f569c2", metadataType: "dpkg-db-entry"},
			wantErr: false,
		},
		{
			name:    "TestUpdateSBOMSyftV01011",
			input:   artifactV01011,
			args:    args{id: "8a49897e59f569c2", metadataType: "dpkg-db-entry"},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := SyftPackage{}
			err := c.UnmarshalJSON(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.args.id, c.ID)
			assert.Equal(t, tt.args.metadataType, c.MetadataType)
		})
	}
}
