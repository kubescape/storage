package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestCrashLoopProfileIncomplete() {
	description := `TestCrashLoopProfileIncomplete: Deploys a deployment with a single container that exits with code 1 after 60 seconds and restarts.
Goal: Verify that a crashlooping container does not result in a completed application profile after the learning period.`
	s.LogWithTimestamp(description)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "crashloop-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "crashloop-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "crashloop-test-deployment",
						containerwatcher.MaxSniffingTimeLabel: "2m",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "crashloop",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "sleep 60; exit 1"},
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}
	_, err := s.clientset.AppsV1().Deployments(s.testNamespace).Create(context.Background(), deployment, metav1.CreateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting for pod to be ready (container will crashloop)")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=crashloop-test-deployment")

	s.LogWithTimestamp("Waiting 3 minutes for learning period to end (container will restart at least once)")
	time.Sleep(3 * time.Minute)

	s.LogWithTimestamp("Fetching application profile and verifying it is not complete/completed")
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "crashloop-test-deployment")
	s.Require().NoError(err)
	s.Require().NotNil(applicationProfile)
	// The profile should NOT be marked as completed/complete
	status := applicationProfile.Annotations["kubescape.io/status"]
	completion := applicationProfile.Annotations["kubescape.io/completion"]
	s.Require().NotEqual("completed", status, "Profile should not be marked as completed")
	s.Require().Equal("complete", completion, "Profile should be marked as complete")

	s.LogWithTimestamp("TestCrashLoopProfileIncomplete completed successfully")
}
