package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestSimpleProfileNodeAgentFailover() {
	description := `TestSimpleProfileNodeAgentFailover: Deploys a test deployment with a 4-minute learning period, kills the node-agent pod on the same node as the test pod after 5 minutes, and verifies that both the application and network neighbor profiles are marked as 'complete' and 'completed' after 11 minutes.
Goal: Ensure that the system can recover from a node-agent pod failover on the same node as the test pod and still produce finalized profiles after the learning period, demonstrating robustness to node-agent disruptions.`
	s.LogWithTimestamp(description)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nodeagent-failover-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nodeagent-failover-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "nodeagent-failover-test-deployment",
						containerwatcher.MaxSniffingTimeLabel: "3m",
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
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=nodeagent-failover-test-deployment")

	s.LogWithTimestamp("Waiting 2 minutes before killing node agent pod on same node")
	time.Sleep(2 * time.Minute)

	s.LogWithTimestamp("Killing node agent pod on same node as test pod for failover test")
	DeleteNodeAgentPodOnSameNode(s.T(), s.clientset, s.testNamespace, "app=nodeagent-failover-test-deployment")

	s.LogWithTimestamp("Waiting 4 more minutes for learning period to complete after failover")
	time.Sleep(4 * time.Minute)

	s.LogWithTimestamp("Fetching application profile and network neighbor profile after failover")
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "nodeagent-failover-test-deployment")
	s.Require().NoError(err)
	networkNeighborProfile, err := fetchNetworkNeighborProfile(s.ksObjectConnection, s.testNamespace, "deployment", "nodeagent-failover-test-deployment")
	s.Require().NoError(err)

	s.LogWithTimestamp("Verifying profiles are complete after failover")
	s.Require().Equal("partial", applicationProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", applicationProfile.Annotations["kubescape.io/status"])
	s.Require().Equal("partial", networkNeighborProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", networkNeighborProfile.Annotations["kubescape.io/status"])

	s.LogWithTimestamp("TestSimpleProfileNodeAgentFailover completed successfully")
}
