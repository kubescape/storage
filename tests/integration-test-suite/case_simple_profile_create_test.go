package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestSimpleProfileCreate() {
	description := `TestSimpleProfileCreate: Deploys a test deployment with a 5-minute learning period and verifies that both the application and network neighbor profiles are marked as 'complete' and 'completed' after the learning period.
Goal: Ensure that the profiling and learning period mechanism works as expected and that profiles are finalized correctly after the learning period.`
	s.LogWithTimestamp(description)

	// Deploy a test deployment with a learning period of 5 minutes and make sure we get a profile
	// with a learning period of 5 minutes
	s.LogWithTimestamp("Creating test deployment")
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "simple-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "simple-test-deployment",
						containerwatcher.MaxSniffingTimeLabel: "5m",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
	_, err := s.clientset.AppsV1().Deployments(s.testNamespace).Create(context.Background(), deployment, metav1.CreateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting for pod to be ready")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=simple-test-deployment")

	s.LogWithTimestamp("Waiting 6 minutes for learning period")
	// Wait 6 minutes after pod is ready
	time.Sleep(6 * time.Minute)

	s.LogWithTimestamp("Fetching application profile and network neighbor profile")
	// Get the application profile and network neighbor profile
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "simple-test-deployment")
	s.Require().NoError(err)
	networkNeighborProfile, err := fetchNetworkNeighborProfile(s.ksObjectConnection, s.testNamespace, "deployment", "simple-test-deployment")
	s.Require().NoError(err)

	s.LogWithTimestamp("Verifying profiles are complete")
	// Verify profile is complete/completed
	s.Require().Equal("complete", applicationProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", applicationProfile.Annotations["kubescape.io/status"])
	s.Require().Equal("complete", networkNeighborProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", networkNeighborProfile.Annotations["kubescape.io/status"])

	s.LogWithTimestamp("TestSimpleProfileCreate completed successfully")
}
