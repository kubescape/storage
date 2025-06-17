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
	description := `TestSimpleProfileCreate: Deploys a test deployment with a 2-minute learning period and verifies that both the application and network neighbor profiles are marked as 'complete' and 'completed' after the learning period.
Goal: Ensure that the profiling and learning period mechanism works as expected and that profiles are finalized correctly after the learning period.`
	s.LogWithTimestamp(description)

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
						containerwatcher.MaxSniffingTimeLabel: "2m",
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

	s.LogWithTimestamp("Waiting 3 minutes for learning period")
	time.Sleep(3 * time.Minute)

	s.LogWithTimestamp("Verifying profiles are complete")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "deployment", "simple-test-deployment")
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "deployment", "simple-test-deployment")

	s.LogWithTimestamp("TestSimpleProfileCreate completed successfully")
}
