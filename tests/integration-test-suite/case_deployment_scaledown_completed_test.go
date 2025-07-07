package integration_test_suite

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestProfileCompletionOnScaleDown() {
	deploymentName := "scale-down-test-deployment"
	labels := map[string]string{
		"app": deploymentName,
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 deploymentName,
						containerwatcher.MaxSniffingTimeLabel: "10m",
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

	// Wait for pod to be ready
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=scale-down-test-deployment")

	s.LogWithTimestamp("Pod is ready, sleeping for 2 minutes to allow learning...")
	time.Sleep(3 * time.Minute)

	// Scale deployment to 0
	s.LogWithTimestamp("Scaling deployment to 0 replicas...")
	s.updateDeploymentReplicas(deploymentName, 0)

	// Wait for pod to disappear
	WaitForPodWithLabelDeleted(s.T(), s.clientset, s.testNamespace, "app=scale-down-test-deployment")

	s.LogWithTimestamp("Pod disappeared, checking profiles...")

	// Get the application profile and network neighbor profile
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", deploymentName)
	s.Require().NoError(err)
	networkNeighborProfile, err := fetchNetworkNeighborProfile(s.ksObjectConnection, s.testNamespace, "deployment", deploymentName)
	s.Require().NoError(err)

	// Verify profile is complete/completed
	s.Require().Equal("complete", applicationProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", applicationProfile.Annotations["kubescape.io/status"])
	s.Require().Equal("complete", networkNeighborProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("completed", networkNeighborProfile.Annotations["kubescape.io/status"])
}

func (s *IntegrationTestSuite) updateDeploymentReplicas(deploymentName string, replicas int32) {
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	_, err := s.clientset.AppsV1().Deployments(s.testNamespace).Patch(context.Background(), deploymentName, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	s.Require().NoError(err)
}
