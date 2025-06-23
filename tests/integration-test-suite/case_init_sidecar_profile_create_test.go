package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestInitSidecarProfileCreate() {
	description := `TestInitSidecarProfileCreate: Deploys a test deployment with a sidecar implemented as an init container that never completes.
Goal: Verify that the application profile handles the modern K8s sidecar pattern correctly (init container that keeps running).
The profile should be complete after the learning period finishes and include both the main container and the sidecar container.`
	s.LogWithTimestamp(description)

	deplRestartPolicy := corev1.ContainerRestartPolicyAlways

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "init-sidecar-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "init-sidecar-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "init-sidecar-test-deployment",
						containerwatcher.MaxSniffingTimeLabel: "2m",
					},
				},
				Spec: corev1.PodSpec{
					// This is the modern way to implement sidecars in K8s
					InitContainers: []corev1.Container{
						{
							Name:    "sidecar",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", "while true; do echo 'Sidecar running...'; sleep 10; done"},
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot: &[]bool{true}[0],
								RunAsUser:    &[]int64{1000}[0],
							},
							RestartPolicy: &deplRestartPolicy,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}
	_, err := s.clientset.AppsV1().Deployments(s.testNamespace).Create(context.Background(), deployment, metav1.CreateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting for pod to be ready (both containers should be running)")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=init-sidecar-test-deployment")

	s.LogWithTimestamp("Waiting 30 seconds to ensure sidecar is running")
	time.Sleep(30 * time.Second)

	// Verify that profile exists and is in ready state
	s.LogWithTimestamp("Verifying profile exists and is in ready state")
	applicationProfile, err := fetchApplicationProfile(s.ksObjectConnection, s.testNamespace, "deployment", "init-sidecar-test-deployment")
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

	s.LogWithTimestamp("Waiting 3 minutes for main container learning period")
	time.Sleep(3 * time.Minute)

	s.LogWithTimestamp("Verifying profiles are complete")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "deployment", "init-sidecar-test-deployment")
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "deployment", "init-sidecar-test-deployment")

	s.LogWithTimestamp("TestInitSidecarProfileCreate completed successfully")
}
