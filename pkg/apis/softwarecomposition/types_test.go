package softwarecomposition

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
