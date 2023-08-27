package file

import "testing"

func TestGetNamespaceFromKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{
			name:     "namespace1",
			key:      "/spdx.softwarecomposition.kubescape.io/ConfigurationScanSummary/namespace1",
			expected: "namespace1",
		},
		{
			name:     "no namespace",
			key:      "/spdx.softwarecomposition.kubescape.io/ConfigurationScanSummary/",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := getNamespaceFromKey(test.key)
			if actual != test.expected {
				t.Errorf("Expected %s, got %s", test.expected, actual)
			}
		})
	}

}
