package integration_test_suite

import (
	"context"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestJobProfileCreate() {
	description := `TestJobProfileCreate: Deploys a Job with a 5-minute learning period, waits for the job pod to be ready, and verifies that both the application and network neighbor profiles are marked as 'complete' and 'completed' after the learning period.\nGoal: Ensure that the profiling and learning period mechanism works as expected for Jobs and that profiles are finalized correctly after the learning period.`
	s.LogWithTimestamp(description)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-job-profile",
			Namespace: s.testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "test-job-profile",
						containerwatcher.MaxSniffingTimeLabel: "5m",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "sleep 10"},
						},
					},
				},
			},
		},
	}
	_, err := s.clientset.BatchV1().Jobs(s.testNamespace).Create(context.Background(), job, metav1.CreateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting for Job pod to be ready")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=test-job-profile")

	s.LogWithTimestamp("Waiting 6 minutes for learning period")
	time.Sleep(6 * time.Minute)

	s.LogWithTimestamp("Fetching application profile and network neighbor profile for Job")
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "job", "test-job-profile")
	s.Require().NoError(err)
	networkNeighborProfile, err := fetchNetworkNeighborProfile(s.ksObjectConnection, s.testNamespace, "job", "test-job-profile")
	s.Require().NoError(err)

	s.LogWithTimestamp("Verifying profiles are complete for Job")
	s.Require().Equal("complete", applicationProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", applicationProfile.Annotations["kubescape.io/status"])
	s.Require().Equal("complete", networkNeighborProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", networkNeighborProfile.Annotations["kubescape.io/status"])

	s.LogWithTimestamp("TestJobProfileCreate completed successfully")
}
