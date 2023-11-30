package cleanup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_wlidWithoutClusterName(t *testing.T) {
	tests := []struct {
		name string
		wlid string
		want string
	}{
		{
			name: "wlid with cluster name",
			wlid: "wlid://cluster-docker-desktop/namespace-default/deployment-nginx-deployment",
			want: "namespace-default/deployment-nginx-deployment",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wlidWithoutClusterName(tt.wlid)
			assert.Equal(t, tt.want, got)
		})
	}
}
