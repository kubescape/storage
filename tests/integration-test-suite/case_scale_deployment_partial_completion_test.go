package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestScaleDeploymentPartialCompletion() {
	description := `TestScaleDeploymentPartialCompletion: Tests the scenario where a deployment is created with node-agent not working initially,
then node-agent is started, deployment is scaled up, and verifies partial completion behavior.
Goal: Verify that application profiles handle partial completion correctly when scaling deployments and that
profiles transition from partial to complete after the learning period.`
	s.LogWithTimestamp(description)

	// Step 1: Create deployment with 3-minute learning period
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scale-partial-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "scale-partial-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "scale-partial-test-deployment",
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
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=scale-partial-test-deployment")

	// Step 2: Kill node-agent on the same node to simulate it not working initially
	s.LogWithTimestamp("Killing node-agent pod on same node to simulate node-agent not working initially")
	DeleteNodeAgentPodOnSameNode(s.T(), s.clientset, s.testNamespace, "app=scale-partial-test-deployment")

	// Wait for node-agent to be restarted (it should restart automatically)
	s.LogWithTimestamp("Waiting for node-agent to restart")
	time.Sleep(30 * time.Second)

	// Step 3: Wait for application profile to be created and check it's partial
	s.LogWithTimestamp("Waiting for application profile to be created and checking partial completion")
	time.Sleep(2 * time.Minute)

	// Verify that profile exists and is partial
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "scale-partial-test-deployment")
	s.Require().NoError(err)
	s.Require().NotNil(applicationProfile)
	s.Require().Equal("partial", applicationProfile.Annotations["kubescape.io/completion"], "Profile should be marked as partial")
	s.Require().Equal("ready", applicationProfile.Annotations["kubescape.io/status"], "Profile should be marked as ready")

	// Step 4: Scale up deployment to 2 replicas
	s.LogWithTimestamp("Scaling deployment to 2 replicas")
	scaleDep, err := s.clientset.AppsV1().Deployments(s.testNamespace).Get(context.Background(), "scale-partial-test-deployment", metav1.GetOptions{})
	s.Require().NoError(err)
	two := int32(2)
	scaleDep.Spec.Replicas = &two
	_, err = s.clientset.AppsV1().Deployments(s.testNamespace).Update(context.Background(), scaleDep, metav1.UpdateOptions{})
	s.Require().NoError(err)

	// Wait for the second pod to be ready
	s.LogWithTimestamp("Waiting for second pod to be ready")
	time.Sleep(30 * time.Second)

	// Step 5: Wait a minute and check that application profile is still partial
	s.LogWithTimestamp("Waiting 1 minute and checking profile is still partial")
	time.Sleep(1 * time.Minute)

	applicationProfile, err = fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "scale-partial-test-deployment")
	s.Require().NoError(err)
	s.Require().NotNil(applicationProfile)
	s.Require().Equal("partial", applicationProfile.Annotations["kubescape.io/completion"], "Profile should still be marked as partial after scaling")

	// Step 6: Wait 3 minutes (learning period has passed) and validate application profile is now complete
	s.LogWithTimestamp("Waiting 3 minutes for learning period to complete")
	time.Sleep(3 * time.Minute)

	// Verify that the application profile is now complete
	s.LogWithTimestamp("Verifying application profile is now complete")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "deployment", "scale-partial-test-deployment")

	// Verify network neighbor profile is also complete
	s.LogWithTimestamp("Verifying network neighbor profile is complete")
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "deployment", "scale-partial-test-deployment")

	s.LogWithTimestamp("TestScaleDeploymentPartialCompletion completed successfully")
}
