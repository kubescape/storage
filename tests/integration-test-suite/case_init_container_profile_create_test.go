package integration_test_suite

import (
	"context"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *IntegrationTestSuite) TestInitContainerProfileCreate() {
	description := `TestInitContainerProfileCreate: Deploys a test deployment with an init container that sleeps for 30 seconds and a main nginx container with a 2-minute learning period.
Goal: Verify that the application profile is created after the init container completes but remains incomplete (ready) until the main container's learning period finishes.
The profile should be complete after the main container's learning period finishes.`
	s.LogWithTimestamp(description)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "init-container-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "init-container-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                "init-container-test-deployment",
						MaxSniffingTimeLabel: "5m",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name:    "init-sleep",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "sleep 30"},
						},
					},
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

	s.LogWithTimestamp("Waiting for pod to be ready (init container should be running)")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=init-container-test-deployment")

	// Verify that profile exists but is not complete yet
	s.LogWithTimestamp("Verifying profile exists but is not complete after init container")
	var applicationProfile *v1beta1.ApplicationProfile
	if s.isRapid7 {
		s.LogWithTimestamp("Waiting 3 minutes for init container to complete in storage backend (main container should be running)")
		time.Sleep(3 * time.Minute)
		applicationProfile, err = fetchApplicationProfileFromStorageBackend(s.testNamespace, "deployment", "init-container-test-deployment", s.accountID, s.accessKey)
	} else {
		s.LogWithTimestamp("Waiting 90 seconds for init container to complete in cluster (main container should be running)")
		time.Sleep(90 * time.Second)
		applicationProfile, err = fetchApplicationProfileFromCluster(s.ksObjectConnection, s.testNamespace, "deployment", "init-container-test-deployment")
	}
	s.Require().NoError(err)
	s.Require().NotNil(applicationProfile)
	s.Require().Equal("complete", applicationProfile.Annotations["kubescape.io/completion"])

	// This is the main test: the profile should be ready after the init container completes - not completed yet
	s.Require().Equal("ready", applicationProfile.Annotations["kubescape.io/status"])

	// Verify init container is in the profile
	s.LogWithTimestamp("Verifying init container is in the profile")
	initContainerFound := false
	for _, container := range applicationProfile.Spec.InitContainers {
		if container.Name == "init-sleep" {
			initContainerFound = true
			break
		}
	}
	s.Require().True(initContainerFound, "Init container should be present in the profile")

	s.LogWithTimestamp("Waiting 4 minutes for main container learning period")
	time.Sleep(4 * time.Minute)

	s.LogWithTimestamp("Verifying profiles are complete")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "deployment", "init-container-test-deployment", s.accountID, s.accessKey, s.isRapid7)
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "deployment", "init-container-test-deployment", s.accountID, s.accessKey, s.isRapid7)

	s.LogWithTimestamp("TestInitContainerProfileCreate completed successfully")
}
