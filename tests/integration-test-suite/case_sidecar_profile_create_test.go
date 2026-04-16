package integration_test_suite

import (
	"context"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *IntegrationTestSuite) TestSidecarProfileCreate() {
	description := `TestSidecarProfileCreate: Deploys a test deployment with a sidecar container that runs for 30 seconds alongside the main nginx container.
Goal: Verify that the application profile handles parallel container execution correctly, and both containers are profiled simultaneously.
The profile should be ready when both containers are running and complete after the learning period finishes.`
	s.LogWithTimestamp(description)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sidecar-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "sidecar-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                "sidecar-test-deployment",
						MaxSniffingTimeLabel: "5m",
					},
				},
				Spec: corev1.PodSpec{
					// Enable process namespace sharing so containers can see each other
					ShareProcessNamespace: &[]bool{true}[0],
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
						{
							Name:    "sidecar",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "sleep 30"},
						},
					},
				},
			},
		},
	}
	_, err := s.clientset.AppsV1().Deployments(s.testNamespace).Create(context.Background(), deployment, metav1.CreateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting for pod to be ready (both containers should be running)")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=sidecar-test-deployment")

	// Verify that profile exists and is in learning state
	s.LogWithTimestamp("Verifying profile exists and is in ready state")
	var applicationProfile *v1beta1.ApplicationProfile
	if s.isRapid7 {
		s.LogWithTimestamp("Waiting 3 minutes for sidecar container to complete in storage backend while main container keeps running")
		time.Sleep(3 * time.Minute)
		applicationProfile, err = fetchApplicationProfileFromStorageBackend(s.testNamespace, "deployment", "sidecar-test-deployment", s.accountID, s.accessKey)
	} else {
		s.LogWithTimestamp("Waiting 90 seconds for sidecar container to complete while main container keeps running")
		time.Sleep(90 * time.Second)
		applicationProfile, err = fetchApplicationProfileFromCluster(s.ksObjectConnection, s.testNamespace, "deployment", "sidecar-test-deployment")
	}
	s.Require().NoError(err)
	s.Require().NotNil(applicationProfile)
	s.Require().Equal("complete", applicationProfile.Annotations["kubescape.io/completion"])
	s.Require().Equal("ready", applicationProfile.Annotations["kubescape.io/status"])

	// Verify both containers are in the profile
	s.LogWithTimestamp("Verifying both containers are in the profile")
	nginxFound := false
	sidecarFound := false
	for _, container := range applicationProfile.Spec.Containers {
		if container.Name == "nginx" {
			nginxFound = true
		}
		if container.Name == "sidecar" {
			sidecarFound = true
		}
	}
	s.Require().True(nginxFound, "Nginx container should be present in the profile")
	s.Require().True(sidecarFound, "Sidecar container should be present in the profile")

	s.LogWithTimestamp("Waiting 4 minutes for main container learning period")
	time.Sleep(4 * time.Minute)

	s.LogWithTimestamp("Verifying profiles are complete")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "deployment", "sidecar-test-deployment", s.accountID, s.accessKey, s.isRapid7)
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "deployment", "sidecar-test-deployment", s.accountID, s.accessKey, s.isRapid7)

	s.LogWithTimestamp("TestSidecarProfileCreate completed successfully")
}
