package integration_test_suite

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *IntegrationTestSuite) TestStatefulSetProfileCleanup() {
	description := `TestStatefulSetProfileCleanup: Deploys a StatefulSet with a single container running for 1 minute over the learning period.
Goal: Verify that the application profile is created and completed, and is deleted automatically after the StatefulSet is deleted and 2 minutes have passed.`
	s.LogWithTimestamp(description)

	learningPeriod := 2 * time.Minute
	runPeriod := learningPeriod + 1*time.Minute

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-test-statefulset",
			Namespace: s.testNamespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    func() *int32 { i := int32(1); return &i }(),
			ServiceName: "cleanup-test-service",
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "cleanup-test-statefulset",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                "cleanup-test-statefulset",
						MaxSniffingTimeLabel: "2m",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   "busybox",
							Command: []string{"/bin/sh", "-c", fmt.Sprintf("sleep %d", int(runPeriod.Seconds()))},
						},
					},
				},
			},
		},
	}
	_, err := s.clientset.AppsV1().StatefulSets(s.testNamespace).Create(context.Background(), statefulSet, metav1.CreateOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting for pod to be ready")
	WaitForPodWithLabelReady(s.T(), s.clientset, s.testNamespace, "app=cleanup-test-statefulset")

	s.LogWithTimestamp("Waiting for learning period to end and pod to finish running")
	time.Sleep(runPeriod)

	s.LogWithTimestamp("Verifying application profile is complete and completed")
	verifyApplicationProfileCompleted(s.T(), s.ksObjectConnection, "complete", s.testNamespace, "statefulset", "cleanup-test-statefulset", s.accountID, s.accessKey, s.isRapid7)

	s.LogWithTimestamp("Deleting StatefulSet")
	err = s.clientset.AppsV1().StatefulSets(s.testNamespace).Delete(context.Background(), "cleanup-test-statefulset", metav1.DeleteOptions{})
	s.Require().NoError(err)

	s.LogWithTimestamp("Waiting 2 minutes for profile cleanup")
	time.Sleep(2 * time.Minute)

	// We don't do cleanup with storage backend (Rapid7)
	if !s.isRapid7 {
		s.LogWithTimestamp("Verifying application profile is deleted")
		_, err = fetchApplicationProfileFromCluster(s.ksObjectConnection, s.testNamespace, "statefulset", "cleanup-test-statefulset")
		s.Require().Error(err, "Application profile should be deleted after StatefulSet removal and 2 minutes")
	}

	s.LogWithTimestamp("TestStatefulSetProfileCleanup completed successfully")
}
