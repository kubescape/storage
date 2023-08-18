package file

import (
	"context"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

func TestVulnSummaryStorageImpl_Count(t *testing.T) {
	files := []string{
		"/other/type/ns/titi.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/titi.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/other/toto.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto.json",
		"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/other/toto.json",
	}
	tests := []struct {
		name    string
		key     string
		want    int64
		wantErr bool
	}{
		{
			name: "one object",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
			want: 1,
		},
		{
			name: "one ns",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape",
			want: 1,
		},
		{
			name: "one type",
			key:  "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s",
			want: 2,
		},
		{
			name: "all types",
			key:  "/spdx.softwarecomposition.kubescape.io",
			want: 4,
		},
		{
			name: "from top",
			key:  "/",
			want: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			_ = fs.Mkdir(DefaultStorageRoot, 0755)
			for _, f := range files {
				_ = afero.WriteFile(fs, DefaultStorageRoot+f, []byte(""), 0644)
			}
			s := NewStorageImpl(fs, DefaultStorageRoot)
			got, err := s.Count(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("Count() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Count() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVulnSummaryStorageImpl_Create(t *testing.T) {
	type args struct {
		key string
		obj runtime.Object
		out runtime.Object
		in4 uint64
	}
	tests := []struct {
		name     string
		readonly bool
		args     args
		wantErr  bool
		want     runtime.Object
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/kubescape/toto",
				obj: &v1beta1.VulnerabilitySummary{},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnSummaryStorageImpl(fs, DefaultStorageRoot)
			err := s.Create(context.TODO(), tt.args.key, tt.args.obj, tt.args.out, tt.args.in4)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
		})
	}
}

func TestVulnSummaryStorageImpl_Delete(t *testing.T) {
	type args struct {
		key                  string
		obj                  runtime.Object
		precondition         *storage.Preconditions
		validateDeletionFunc storage.ValidateObjectFunc
		cachedObj            runtime.Object
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "not supported",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
			s := NewVulnSummaryStorageImpl(fs, DefaultStorageRoot)
			err := s.Delete(context.TODO(), tt.args.key, tt.args.obj, tt.args.precondition, tt.args.validateDeletionFunc, tt.args.cachedObj)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
		})
	}
}

func Test_getVulnManifestSummaryDirPath(t *testing.T) {

	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries/default/default",
			expected: "/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries",
		},
	}

	s := VulnSummaryStorageImpl{
		appFs: afero.NewMemMapFs(),
		root:  "/data",
	}

	for _, td := range tests {
		res := s.getVulnManifestSummaryDirPath(td.input)
		assert.Equal(t, td.expected, res)
	}
}

func Test_updateFullVulnSumm(t *testing.T) {
	tests := []struct {
		fullVulnSumm         *softwarecomposition.VulnerabilitySummary
		vulnManifestSumm     *softwarecomposition.VulnerabilityManifestSummary
		expectedFullVulnSumm *softwarecomposition.VulnerabilitySummary
	}{
		{
			fullVulnSumm: &softwarecomposition.VulnerabilitySummary{
				Spec: softwarecomposition.VulnerabilitySummarySpec{
					Severities: softwarecomposition.SeveritySummary{
						Critical: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						High: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Medium: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Low: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Negligible: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Unknown: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
					},
					WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{},
				},
			},
			vulnManifestSumm: &softwarecomposition.VulnerabilityManifestSummary{
				Spec: softwarecomposition.VulnerabilityManifestSummarySpec{
					Severities: softwarecomposition.SeveritySummary{
						Critical: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						High: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Medium: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Low: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Negligible: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
						Unknown: softwarecomposition.VulnerabilityCounters{
							All:      10,
							Relevant: 3,
						},
					},
					Vulnerabilities: softwarecomposition.VulnerabilitiesComponents{
						ImageVulnerabilitiesObj: softwarecomposition.VulnerabilitiesObjScope{
							Name:      "aaa",
							Namespace: "bbb",
							Kind:      "any",
						},
						WorkloadVulnerabilitiesObj: softwarecomposition.VulnerabilitiesObjScope{
							Name:      "ccc",
							Namespace: "ddd",
							Kind:      "many",
						},
					},
				},
			},
			expectedFullVulnSumm: &softwarecomposition.VulnerabilitySummary{
				Spec: softwarecomposition.VulnerabilitySummarySpec{
					Severities: softwarecomposition.SeveritySummary{
						Critical: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						High: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Medium: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Low: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Negligible: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
						Unknown: softwarecomposition.VulnerabilityCounters{
							All:      20,
							Relevant: 6,
						},
					},
					WorkloadVulnerabilitiesObj: []softwarecomposition.VulnerabilitiesObjScope{
						softwarecomposition.VulnerabilitiesObjScope{
							Name:      "aaa",
							Namespace: "bbb",
							Kind:      "any",
						},
						softwarecomposition.VulnerabilitiesObjScope{
							Name:      "ccc",
							Namespace: "ddd",
							Kind:      "many",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			updateFullVulnSumm(tt.fullVulnSumm, tt.vulnManifestSumm)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Critical.All, tt.expectedFullVulnSumm.Spec.Severities.Critical.All)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Critical.Relevant, tt.expectedFullVulnSumm.Spec.Severities.Critical.Relevant)

			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.High.All, tt.expectedFullVulnSumm.Spec.Severities.High.All)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.High.Relevant, tt.expectedFullVulnSumm.Spec.Severities.High.Relevant)

			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Medium.All, tt.expectedFullVulnSumm.Spec.Severities.Medium.All)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Medium.Relevant, tt.expectedFullVulnSumm.Spec.Severities.Medium.Relevant)

			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Low.All, tt.expectedFullVulnSumm.Spec.Severities.Low.All)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Low.Relevant, tt.expectedFullVulnSumm.Spec.Severities.Low.Relevant)

			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Negligible.All, tt.expectedFullVulnSumm.Spec.Severities.Negligible.All)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Negligible.Relevant, tt.expectedFullVulnSumm.Spec.Severities.Negligible.Relevant)

			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Unknown.All, tt.expectedFullVulnSumm.Spec.Severities.Unknown.All)
			assert.Equal(t, tt.fullVulnSumm.Spec.Severities.Unknown.Relevant, tt.expectedFullVulnSumm.Spec.Severities.Unknown.Relevant)

			assert.Equal(t, len(tt.fullVulnSumm.Spec.WorkloadVulnerabilitiesObj), len(tt.expectedFullVulnSumm.Spec.WorkloadVulnerabilitiesObj))
			for i := range tt.fullVulnSumm.Spec.WorkloadVulnerabilitiesObj {
				assert.Equal(t, tt.fullVulnSumm.Spec.WorkloadVulnerabilitiesObj[i], tt.expectedFullVulnSumm.Spec.WorkloadVulnerabilitiesObj[i])
			}
		})
	}

}

func Test_updateSeverities(t *testing.T) {
	tests := []struct {
		vulnSeverities             softwarecomposition.SeveritySummary
		aggregatedVulnSeverities   softwarecomposition.SeveritySummary
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
			vulnSeverities: softwarecomposition.SeveritySummary{
				Critical: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Medium: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Low: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				High: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Negligible: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Unknown: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
			},
			aggregatedVulnSeverities: softwarecomposition.SeveritySummary{
				Critical: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Medium: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Low: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				High: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Negligible: softwarecomposition.VulnerabilityCounters{
					All:      10,
					Relevant: 3,
				},
				Unknown: softwarecomposition.VulnerabilityCounters{
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
			updateSeverities(&tt.aggregatedVulnSeverities, &tt.vulnSeverities)
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
func Test_updateVulnCounters(t *testing.T) {
	tests := []struct {
		vulnCounters           softwarecomposition.VulnerabilityCounters
		aggregatedVulnCounters softwarecomposition.VulnerabilityCounters
		expectedAll            int
		expectedRelevant       int
	}{
		{
			vulnCounters: softwarecomposition.VulnerabilityCounters{
				All:      10,
				Relevant: 3,
			},
			aggregatedVulnCounters: softwarecomposition.VulnerabilityCounters{
				All:      10,
				Relevant: 3,
			},
			expectedAll:      20,
			expectedRelevant: 6,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			updateVulnCounters(&tt.aggregatedVulnCounters, &tt.vulnCounters)
			assert.Equal(t, tt.aggregatedVulnCounters.All, tt.expectedAll)
			assert.Equal(t, tt.aggregatedVulnCounters.Relevant, tt.expectedRelevant)
		})
	}

}
