package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestSimpleProfileStorageFailover() {
	description := `TestSimpleProfileStorageFailover: Deploys a test deployment with a 10-minute learning period, kills the kubescape storage pod after 5 minutes, and verifies that both the application and network neighbor profiles are marked as 'complete' and 'completed' after 11 minutes.
Goal: Ensure that the system can recover from a storage pod failover and still produce finalized profiles after the learning period, demonstrating robustness to storage disruptions.`
	s.LogWithTimestamp(description)

	// Deploy a test deployment with a learning period of 10 minutes
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "failover-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "failover-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "failover-test-deployment",
						containerwatcher.MaxSniffingTimeLabel: "4m",
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
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=failover-test-deployment")

	s.LogWithTimestamp("Waiting 2 minutes before killing storage pod")
	time.Sleep(2 * time.Minute)

	s.LogWithTimestamp("Killing kubescape storage pod for failover test")
	DeleteKubescapeStoragePod(s.T(), s.clientset)

	s.LogWithTimestamp("Waiting 3 more minutes for learning period to complete after failover")
	time.Sleep(3 * time.Minute)

	s.LogWithTimestamp("Fetching application profile and network neighbor profile after failover")
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "failover-test-deployment")
	s.Require().NoError(err)
	networkNeighborProfile, err := fetchNetworkNeighborProfile(s.ksObjectConnection, s.testNamespace, "deployment", "failover-test-deployment")
	s.Require().NoError(err)

	s.LogWithTimestamp("Verifying profiles are complete after failover")
	s.Require().Equal("complete", applicationProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", applicationProfile.Annotations["kubescape.io/status"])
	s.Require().Equal("complete", networkNeighborProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", networkNeighborProfile.Annotations["kubescape.io/status"])

	s.LogWithTimestamp("TestSimpleProfileStorageFailover completed successfully")
}
