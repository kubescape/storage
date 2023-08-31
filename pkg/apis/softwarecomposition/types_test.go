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
					Kind: "VulnerabilityManifestSummary",
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
							Kind:      "VulnerabilityManifestSummary",
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
		vulnSeverities             SeveritySummary
		aggregatedVulnSeverities   SeveritySummary
		expectedAllCritical        int
		expectedAllHigh            int
		expectedAllMedium          int
		expectedAllLow             int
		expectedAllNegligible      int
		expectedAllUnknown         int
		expectedRelevantCritical   int
		expectedRelevantHigh       int
		expectedRelevantMedium     int
		expectedRelevantLow        int
		expectedRelevantNegligible int
		expectedRelevantUnknown    int
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
			expectedAllCritical:        20,
			expectedAllHigh:            20,
			expectedAllMedium:          20,
			expectedAllLow:             20,
			expectedAllNegligible:      20,
			expectedAllUnknown:         20,
			expectedRelevantCritical:   6,
			expectedRelevantHigh:       6,
			expectedRelevantMedium:     6,
			expectedRelevantLow:        6,
			expectedRelevantNegligible: 6,
			expectedRelevantUnknown:    6,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			tt.aggregatedVulnSeverities.Add(&tt.vulnSeverities)
			assert.Equal(t, tt.aggregatedVulnSeverities.Critical.All, tt.expectedAllCritical)
			assert.Equal(t, tt.aggregatedVulnSeverities.Critical.Relevant, tt.expectedRelevantCritical)

			assert.Equal(t, tt.aggregatedVulnSeverities.High.All, tt.expectedAllHigh)
			assert.Equal(t, tt.aggregatedVulnSeverities.High.Relevant, tt.expectedRelevantHigh)

			assert.Equal(t, tt.aggregatedVulnSeverities.Medium.All, tt.expectedAllMedium)
			assert.Equal(t, tt.aggregatedVulnSeverities.Medium.Relevant, tt.expectedRelevantMedium)

			assert.Equal(t, tt.aggregatedVulnSeverities.Low.All, tt.expectedAllLow)
			assert.Equal(t, tt.aggregatedVulnSeverities.Low.Relevant, tt.expectedRelevantLow)

			assert.Equal(t, tt.aggregatedVulnSeverities.Negligible.All, tt.expectedAllNegligible)
			assert.Equal(t, tt.aggregatedVulnSeverities.Negligible.Relevant, tt.expectedRelevantNegligible)

			assert.Equal(t, tt.aggregatedVulnSeverities.Unknown.All, tt.expectedAllUnknown)
			assert.Equal(t, tt.aggregatedVulnSeverities.Unknown.Relevant, tt.expectedRelevantUnknown)

		})
	}
}

func Test_SeveritySummaryAdd(t *testing.T) {
	tests := []struct {
		vulnCounters           VulnerabilityCounters
		aggregatedVulnCounters VulnerabilityCounters
		expectedAll            int
		expectedRelevant       int
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
			expectedAll:      20,
			expectedRelevant: 6,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			tt.aggregatedVulnCounters.Add(&tt.vulnCounters)
			assert.Equal(t, tt.aggregatedVulnCounters.All, tt.expectedAll)
			assert.Equal(t, tt.aggregatedVulnCounters.Relevant, tt.expectedRelevant)
		})
	}

}
