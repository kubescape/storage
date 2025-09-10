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
	description := `TestJobProfileCreate: Deploys a Job with a 2-minute learning period, waits for the job pod to be ready, and verifies that both the application and network neighbor profiles are marked as 'complete' and 'completed' after the learning period.\nGoal: Ensure that the profiling and learning period mechanism works as expected for Jobs and that profiles are finalized correctly after the learning period.`
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
						containerwatcher.MaxSniffingTimeLabel: "2m",
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

	s.LogWithTimestamp("Waiting for pod to be ready")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=test-job-profile")

	s.LogWithTimestamp("Waiting 3 minutes for learning period")
	time.Sleep(3 * time.Minute)

	s.LogWithTimestamp("Verifying profiles are complete")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "job", "test-job-profile")
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "job", "test-job-profile")

	s.LogWithTimestamp("TestJobProfileCreate completed successfully")
}
