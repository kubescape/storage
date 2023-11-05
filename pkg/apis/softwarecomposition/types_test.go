package softwarecomposition

import (
	"testing"

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
