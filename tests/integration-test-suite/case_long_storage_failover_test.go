package integration_test_suite

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	containerwatcher "github.com/kubescape/node-agent/pkg/containerwatcher/v1"
)

func (s *IntegrationTestSuite) TestLongStorageFailover() {
	description := `TestLongStorageFailover: Deploys a test deployment with a 15-minute learning period, scales down the storage deployment to 0 for 10 minutes after 2 minutes, then scales it back to 1 and waits 5 more minutes.
Goal: Ensure that the system can recover from a long storage failover and still produce a complete and completed application profile after the learning period.`
	s.LogWithTimestamp(description)

	// Deploy a test deployment with a learning period of 15 minutes
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "long-storage-failover-test-deployment",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &[]int32{1}[0],
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "long-storage-failover-test-deployment",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                 "long-storage-failover-test-deployment",
						containerwatcher.MaxSniffingTimeLabel: "15m",
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
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=long-storage-failover-test-deployment")

	s.LogWithTimestamp("Waiting 2 minutes before scaling down storage deployment")
	time.Sleep(2 * time.Minute)

	s.LogWithTimestamp("Scaling down storage deployment to 0 replicas for 10 minutes")
	storageDep, err := s.clientset.AppsV1().Deployments("kubescape").Get(context.Background(), "storage", metav1.GetOptions{})
	s.Require().NoError(err)
	zero := int32(0)
	storageDep.Spec.Replicas = &zero
	_, err = s.clientset.AppsV1().Deployments("kubescape").Update(context.Background(), storageDep, metav1.UpdateOptions{})
	s.Require().NoError(err)

	time.Sleep(10 * time.Minute)

	s.LogWithTimestamp("Scaling storage deployment back to 1 replica")
	one := int32(1)
	storageDep.Spec.Replicas = &one
	_, err = s.clientset.AppsV1().Deployments("kubescape").Update(context.Background(), storageDep, metav1.UpdateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting 5 more minutes for learning period to complete after storage is back")
	time.Sleep(5 * time.Minute)

	s.LogWithTimestamp("Verifying profiles are complete after long storage failover")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "deployment", "long-storage-failover-test-deployment")
	verifyNetworkNeighborProfileCompleted(s.T(), s.ksObjectConnection, false, false, "complete", s.testNamespace, "deployment", "long-storage-failover-test-deployment")

	s.LogWithTimestamp("TestLongStorageFailover completed successfully")
}
