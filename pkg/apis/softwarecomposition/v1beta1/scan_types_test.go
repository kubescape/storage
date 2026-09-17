package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	softwarecomposition "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func workloadScanReportTime() metav1.Time {
	return metav1.NewTime(time.Date(2026, time.January, 15, 10, 11, 12, 0, time.UTC))
}

func TestWorkloadConfigurationScanReportTimestampJSONRoundTrip(t *testing.T) {
	original := WorkloadConfigurationScan{
		Spec: WorkloadConfigurationScanSpec{
			Metadata: &WorkloadConfigurationScanMeta{
				Report: ReportMeta{CreatedAt: workloadScanReportTime()},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded WorkloadConfigurationScan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded.Spec.Metadata, original.Spec.Metadata) {
		t.Fatalf("JSON round-trip mismatch: got %#v, want %#v", decoded.Spec.Metadata, original.Spec.Metadata)
	}
}

func TestWorkloadConfigurationScanReportTimestampProtobufRoundTrip(t *testing.T) {
	original := &WorkloadConfigurationScan{
		Spec: WorkloadConfigurationScanSpec{
			Metadata: &WorkloadConfigurationScanMeta{
				Report: ReportMeta{CreatedAt: workloadScanReportTime()},
			},
		},
	}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded WorkloadConfigurationScan
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded.Spec.Metadata.Report.CreatedAt.Time.Equal(original.Spec.Metadata.Report.CreatedAt.Time) {
		t.Fatalf("protobuf round-trip changed createdAt: got %v, want %v", decoded.Spec.Metadata.Report.CreatedAt, original.Spec.Metadata.Report.CreatedAt)
	}
}

func TestWorkloadConfigurationScanReportTimestampConversionRoundTrip(t *testing.T) {
	original := &softwarecomposition.WorkloadConfigurationScan{
		Spec: softwarecomposition.WorkloadConfigurationScanSpec{
			Metadata: &softwarecomposition.WorkloadConfigurationScanMeta{
				Report: softwarecomposition.ReportMeta{CreatedAt: workloadScanReportTime()},
			},
		},
	}

	var versioned WorkloadConfigurationScan
	if err := Convert_softwarecomposition_WorkloadConfigurationScan_To_v1beta1_WorkloadConfigurationScan(original, &versioned, nil); err != nil {
		t.Fatalf("to v1beta1: %v", err)
	}
	if versioned.Spec.Metadata == nil || !versioned.Spec.Metadata.Report.CreatedAt.Time.Equal(original.Spec.Metadata.Report.CreatedAt.Time) {
		t.Fatalf("internal to v1beta1 conversion dropped createdAt: %#v", versioned.Spec.Metadata)
	}

	var roundTrip softwarecomposition.WorkloadConfigurationScan
	if err := Convert_v1beta1_WorkloadConfigurationScan_To_softwarecomposition_WorkloadConfigurationScan(&versioned, &roundTrip, nil); err != nil {
		t.Fatalf("to internal: %v", err)
	}
	if roundTrip.Spec.Metadata == nil || !roundTrip.Spec.Metadata.Report.CreatedAt.Time.Equal(original.Spec.Metadata.Report.CreatedAt.Time) {
		t.Fatalf("v1beta1 to internal conversion dropped createdAt: %#v", roundTrip.Spec.Metadata)
	}
}

func TestWorkloadConfigurationScanReportTimestampDeepCopy(t *testing.T) {
	versioned := &WorkloadConfigurationScan{
		Spec: WorkloadConfigurationScanSpec{
			Metadata: &WorkloadConfigurationScanMeta{Report: ReportMeta{CreatedAt: workloadScanReportTime()}},
		},
	}
	versionedCopy := versioned.DeepCopy()
	versionedCopy.Spec.Metadata.Report.CreatedAt = metav1.NewTime(time.Unix(1, 0).UTC())
	if versioned.Spec.Metadata.Report.CreatedAt.Time.Equal(versionedCopy.Spec.Metadata.Report.CreatedAt.Time) {
		t.Fatal("v1beta1 DeepCopy shares report metadata")
	}

	internal := &softwarecomposition.WorkloadConfigurationScan{
		Spec: softwarecomposition.WorkloadConfigurationScanSpec{
			Metadata: &softwarecomposition.WorkloadConfigurationScanMeta{Report: softwarecomposition.ReportMeta{CreatedAt: workloadScanReportTime()}},
		},
	}
	internalCopy := internal.DeepCopy()
	internalCopy.Spec.Metadata.Report.CreatedAt = metav1.NewTime(time.Unix(1, 0).UTC())
	if internal.Spec.Metadata.Report.CreatedAt.Time.Equal(internalCopy.Spec.Metadata.Report.CreatedAt.Time) {
		t.Fatal("internal DeepCopy shares report metadata")
	}
}

func TestWorkloadConfigurationScanReportTimestampAbsentJSONRemainsAbsent(t *testing.T) {
	data, err := json.Marshal(WorkloadConfigurationScan{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(object["spec"], &spec); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	if _, present := spec["metadata"]; present {
		t.Fatalf("absent scan metadata was serialized: %s", data)
	}
}
