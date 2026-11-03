package file

import (
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// node-agent renders the report timestamp with time.Time.String(), not RFC3339:
// pkg/containerprofilemanager/v1/monitoring.go:350-352 assigns time.Now() and
// stores its .String(). That spelling carries a " m=+..." monotonic suffix, and
// in a zone whose offset is not a whole number of hours it renders a bare
// numeric abbreviation in place of MST.
func productionReportTimestamp(t time.Time) string {
	return t.String()
}

// TestConsolidateContinuousTimeSeries_BackdatesCreationFromProductionStamp pins
// the creation-timestamp backdating in consolidateContinuousTimeSeries against
// the format node-agent actually writes. The profile's creationTimestamp is
// meant to be the earliest report in the series, not the tick that consolidated
// it (updateProfile only applies it when the profile has none yet).
func TestConsolidateContinuousTimeSeries_BackdatesCreationFromProductionStamp(t *testing.T) {
	zone := time.FixedZone("IST", 5*60*60+30*60) // +05:30, not a whole hour
	// Whole seconds: time.RFC3339 renders no fraction, so a sub-second fixture
	// could not round-trip through it and the comparison would be about the
	// layout's precision rather than about the parse.
	reported := time.Date(2026, 11, 1, 11, 22, 47, 0, zone)
	tick := metav1.NewTime(reported.Add(6 * time.Hour))

	for _, tc := range []struct {
		name  string
		stamp string
	}{
		{"node-agent .String() with monotonic suffix", productionReportTimestamp(reported) + " m=+0.000000001"},
		{"node-agent .String() without monotonic suffix", productionReportTimestamp(reported)},
		{"RFC3339, still accepted", reported.Format(time.RFC3339)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &ContainerProfileProcessor{}
			series := []softwarecomposition.TimeSeriesContainers{{
				Status:                  helpersv1.Learning,
				HasData:                 true,
				ReportTimestamp:         tc.stamp,
				PreviousReportTimestamp: "",
				TsSuffix:                "s1",
			}}
			creation := tick

			out := a.consolidateContinuousTimeSeries(series, &creation)

			require.Len(t, out, 1)
			assert.Truef(t, creation.Time.Equal(reported),
				"creationTimestamp was not backdated to the report time\n  stamp:    %q\n  want:     %s\n  got:      %s",
				tc.stamp, reported.UTC(), creation.Time.UTC())
		})
	}
}

// TestParseReportTimestamp covers the spellings this field is known to carry.
// The two zero spellings are the ones isZeroTime already tolerates, which is
// the existing evidence that both formats reach this column.
func TestParseReportTimestamp(t *testing.T) {
	ist := time.FixedZone("IST", 5*60*60+30*60)
	want := time.Date(2026, 11, 1, 11, 22, 47, 0, ist)

	for _, tc := range []struct {
		name string
		in   string
		ok   bool
	}{
		{"String() with monotonic", want.String() + " m=+0.000000001", true},
		{"String()", want.String(), true},
		{"RFC3339", want.Format(time.RFC3339), true},
		{"RFC3339Nano", want.Format(time.RFC3339Nano), true},
		{"empty", "", false},
		{"garbage", "not a time", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseReportTimestamp(tc.in)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Truef(t, got.Equal(want), "want %s, got %s", want.UTC(), got.UTC())
			}
		})
	}
}
