package softwarecomposition

import (
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/consts"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_VulnerabilitySummaryMerge(t *testing.T) {
	tests := []struct {
		fullVulnSumm         *VulnerabilitySummary
		vulnManifestSumm     *VulnerabilityManifestSummary
		expectedFullVulnSumm *VulnerabilitySummary
	}{
		{
			fullVulnSumm: &VulnerabilitySummary{
				Spec: VulnerabilitySummarySpec{
					Severities: SeveritySummary{
						Critical: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						High: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Medium: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Low: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Negligible: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Unknown: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
					},
					WorkloadVulnerabilitiesObj: []VulnerabilitiesObjScope{},
				},
			},
			vulnManifestSumm: &VulnerabilityManifestSummary{
				ObjectMeta: v1.ObjectMeta{
					Name:      "aaa",
					Namespace: "bbb",
				},
				TypeMeta: v1.TypeMeta{
					Kind: "vulnerabilitymanifestsummary",
				},
				Spec: VulnerabilityManifestSummarySpec{
					Severities: SeveritySummary{
						Critical: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						High: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Medium: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Low: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Negligible: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Unknown: VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
					},
					Vulnerabilities: VulnerabilitiesComponents{
						ImageVulnerabilitiesObj: VulnerabilitiesObjScope{
							Name:      "aaa",
							Namespace: "bbb",
							Kind:      "any",
						},
						WorkloadVulnerabilitiesObj: VulnerabilitiesObjScope{
							Name:      "ccc",
							Namespace: "ddd",
							Kind:      "many",
						},
					},
				},
			},
			expectedFullVulnSumm: &VulnerabilitySummary{
				Spec: VulnerabilitySummarySpec{
					Severities: SeveritySummary{
						Critical: VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						High: VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Medium: VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Low: VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Negligible: VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Unknown: VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
					},
					WorkloadVulnerabilitiesObj: []VulnerabilitiesObjScope{
						VulnerabilitiesObjScope{
							Name:      "aaa",
							Namespace: "bbb",
							Kind:      "vulnerabilitymanifestsummary",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			tt.fullVulnSumm.Merge(tt.vulnManifestSumm)
			assert.Equal(t, tt.expectedFullVulnSumm, tt.fullVulnSumm)
		})
	}
}

func Test_VulnerabilityCountersAdd(t *testing.T) {
	tests := []struct {
		vulnSeverities                   SeveritySummary
		aggregatedVulnSeverities         SeveritySummary
		expectedAggregatedVulnSeverities SeveritySummary
	}{
		{
			vulnSeverities: SeveritySummary{
				Critical: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Medium: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Low: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				High: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Negligible: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Unknown: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
			},
			aggregatedVulnSeverities: SeveritySummary{
				Critical: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Medium: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Low: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				High: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Negligible: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Unknown: VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
			},
			expectedAggregatedVulnSeverities: SeveritySummary{
				Critical: VulnerabilityCounters{
					All:      20,
					Relevant: 6,
				},
				Medium: VulnerabilityCounters{
					All:      20,
					Relevant: 6,
				},
				Low: VulnerabilityCounters{
					All:      20,
					Relevant: 6,
				},
				High: VulnerabilityCounters{
					All:      20,
					Relevant: 6,
				},
				Negligible: VulnerabilityCounters{
					All:      20,
					Relevant: 6,
				},
				Unknown: VulnerabilityCounters{
					All:      20,
					Relevant: 6,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			tt.aggregatedVulnSeverities.Add(&tt.vulnSeverities)
			assert.Equal(t, tt.aggregatedVulnSeverities, tt.expectedAggregatedVulnSeverities)
		})
	}
}

func Test_SeveritySummaryAdd(t *testing.T) {
	tests := []struct {
		vulnCounters                   VulnerabilityCounters
		aggregatedVulnCounters         VulnerabilityCounters
		expectedAggregatedVulnCounters VulnerabilityCounters
	}{
		{
			vulnCounters: VulnerabilityCounters{
				All:      10,
				Relevant: 3,
			},
			aggregatedVulnCounters: VulnerabilityCounters{
				All:      10,
				Relevant: 3,
			},
			expectedAggregatedVulnCounters: VulnerabilityCounters{
				All:      20,
				Relevant: 6,
			},
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			tt.aggregatedVulnCounters.Add(&tt.vulnCounters)
			assert.Equal(t, tt.aggregatedVulnCounters, tt.expectedAggregatedVulnCounters)
		})
	}

}

func TestExecCalls_String(t *testing.T) {
	tests := []struct {
		name string
		e    ExecCalls
		want string
	}{
		{
			name: "Empty",
			e:    ExecCalls{},
			want: "",
		},
		{
			name: "Path only",
			e: ExecCalls{
				Path: "ls",
			},
			want: "ls",
		},
		{
			name: "Path and args",
			e: ExecCalls{
				Path: "ls",
				Args: []string{"-l", "-a"},
			},
			want: "ls␟-l␟-a",
		},
		{
			name: "Path and args and env",
			e: ExecCalls{
				Path: "ls",
				Args: []string{"-l", "-a"},
				Envs: []string{"HOME=/home/user", "USER=user"},
			},
			want: "ls␟-l␟-a␟HOME=/home/user␟USER=user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.e.String(), "String()")
		})
	}
}

func TestOpenCalls_String(t *testing.T) {
	tests := []struct {
		name string
		o    OpenCalls
		want string
	}{
		{
			name: "Empty",
			o:    OpenCalls{},
			want: "",
		},
		{
			name: "Path only",
			o: OpenCalls{
				Path: "/etc/passwd",
			},
			want: "/etc/passwd",
		},
		{
			name: "Path and flags",
			o: OpenCalls{
				Path:  "/etc/passwd",
				Flags: []string{"O_RDONLY"},
			},
			want: "/etc/passwd␟O_RDONLY",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.o.String(), "String()")
		})
	}
}

func TestHTTPEndpoint_String(t *testing.T) {
	headers := map[string][]string{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer token123", "ApiKey abcdef"},
	}

	rawJSON, _ := json.Marshal(headers)

	tests := []struct {
		name string
		e    HTTPEndpoint
		want string
	}{
		{
			name: "Empty",
			e:    HTTPEndpoint{},
			want: "",
		},
		{
			name: "Endpoint and Methods only",
			e: HTTPEndpoint{
				Endpoint: "/api/v1/users",
				Methods:  []string{"GET", "POST"},
			},
			want: "/api/v1/users␟GET,POST",
		},
		{

			name: "Full HTTPEndpoint",
			e: HTTPEndpoint{
				Endpoint:  "/api/v1/users",
				Methods:   []string{"GET", "POST"},
				Internal:  true,
				Direction: consts.Inbound,
				Headers:   rawJSON,
			},
			want: "/api/v1/users␟GET,POST␟Internal␟Inbound␟Content-Type: application/json␟Authorization: Bearer token123,ApiKey abcdef",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.e.String(), "String()")
		})
	}
}

func TestApplicationProfileContainerExtra(t *testing.T) {
	tests := []struct {
		name      string
		container ApplicationProfileContainer
		wantJSON  string
	}{
		{
			name: "Empty Extra",
			container: ApplicationProfileContainer{
				Name: "test-container",
				SeccompProfile: SingleSeccompProfile{
					Spec: SingleSeccompProfileSpec{
						SpecBase: SpecBase{
							Disabled: false,
						},
					},
				},
			},
			wantJSON: `{"Name":"test-container","Capabilities":null,"Execs":null,"Opens":null,"Syscalls":null,"SeccompProfile":{"Name":"","Path":"","Spec":{"Disabled":false,"BaseProfileName":"","DefaultAction":"","Architectures":null,"ListenerPath":"","ListenerMetadata":"","Syscalls":null,"Flags":null}},"Endpoints":null,"Extra":null, "ImageID": "", "ImageTag": ""}`,
		},
		{
			name: "Extra with string data",
			container: ApplicationProfileContainer{
				Name:  "test-container",
				Extra: json.RawMessage(`"extra string data"`),
				SeccompProfile: SingleSeccompProfile{
					Spec: SingleSeccompProfileSpec{
						SpecBase: SpecBase{
							Disabled: false,
						},
					},
				},
			},
			wantJSON: `{"Name":"test-container","Capabilities":null,"Execs":null,"Opens":null,"Syscalls":null,"SeccompProfile":{"Name":"","Path":"","Spec":{"Disabled":false,"BaseProfileName":"","DefaultAction":"","Architectures":null,"ListenerPath":"","ListenerMetadata":"","Syscalls":null,"Flags":null}},"Endpoints":null,"Extra":"extra string data", "ImageID": "", "ImageTag": ""}`,
		},
		{
			name: "Extra with object data",
			container: ApplicationProfileContainer{
				Name:  "test-container",
				Extra: json.RawMessage(`{"key1":"value1","key2":42}`),
				SeccompProfile: SingleSeccompProfile{
					Spec: SingleSeccompProfileSpec{
						SpecBase: SpecBase{
							Disabled: false,
						},
					},
				},
			},
			wantJSON: `{"Name":"test-container","Capabilities":null,"Execs":null,"Opens":null,"Syscalls":null,"SeccompProfile":{"Name":"","Path":"","Spec":{"Disabled":false,"BaseProfileName":"","DefaultAction":"","Architectures":null,"ListenerPath":"","ListenerMetadata":"","Syscalls":null,"Flags":null}},"Endpoints":null,"Extra":{"key1":"value1","key2":42}, "ImageID": "", "ImageTag": ""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test marshaling
			got, err := json.Marshal(tt.container)
			assert.NoError(t, err)
			assert.JSONEq(t, tt.wantJSON, string(got))

			// Test unmarshaling
			var unmarshaledContainer ApplicationProfileContainer
			err = json.Unmarshal([]byte(tt.wantJSON), &unmarshaledContainer)
			assert.NoError(t, err)

			// Compare the unmarshaled container with the original
			assert.Equal(t, tt.container.Name, unmarshaledContainer.Name)
			assert.Equal(t, tt.container.Capabilities, unmarshaledContainer.Capabilities)
			assert.Equal(t, tt.container.Execs, unmarshaledContainer.Execs)
			assert.Equal(t, tt.container.Opens, unmarshaledContainer.Opens)
			assert.Equal(t, tt.container.Syscalls, unmarshaledContainer.Syscalls)
			assert.Equal(t, tt.container.SeccompProfile, unmarshaledContainer.SeccompProfile)
			assert.Equal(t, tt.container.Endpoints, unmarshaledContainer.Endpoints)

			// Compare Extra field separately
			if tt.container.Extra == nil {
				assert.Equal(t, json.RawMessage("null"), unmarshaledContainer.Extra)
			} else {
				assert.JSONEq(t, string(tt.container.Extra), string(unmarshaledContainer.Extra))
			}
		})
	}
}
