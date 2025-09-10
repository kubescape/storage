package integration_test_suite

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	spdxv1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	spdxv1beta1client "github.com/kubescape/storage/pkg/generated/clientset/versioned/typed/softwarecomposition/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func getKubeconfig() (*rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.ExpandEnv("$HOME/.kube/config")
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func GetKubeClient() (*kubernetes.Clientset, error) {
	config, err := getKubeconfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

func CreateKubscapeObjectConnection() (spdxv1beta1client.SpdxV1beta1Interface, error) {
	cfg, err := getKubeconfig()
	if err != nil {
		return nil, err
	}

	// disable rate limiting
	cfg.QPS = 0
	cfg.RateLimiter = nil
	// force GRPC
	cfg.AcceptContentTypes = "application/vnd.kubernetes.protobuf"
	cfg.ContentType = "application/vnd.kubernetes.protobuf"

	return spdxv1beta1client.NewForConfig(cfg)
}

func ListPodsInNamespace(clientset *kubernetes.Clientset, namespace string) ([]*corev1.Pod, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []*corev1.Pod
	for i := range pods.Items {
		result = append(result, &pods.Items[i])
	}
	return result, nil
}

func fetchApplicationProfile(ksObjectConnection spdxv1beta1client.SpdxV1beta1Interface, namespace string, relatedKind string, relatedName string) (*spdxv1beta1.ApplicationProfile, error) {
	applicationProfileList, err := ksObjectConnection.ApplicationProfiles(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var matchingProfile *spdxv1beta1.ApplicationProfile
	for _, profile := range applicationProfileList.Items {
		if strings.EqualFold(profile.Labels["kubescape.io/workload-name"], relatedName) &&
			strings.EqualFold(profile.Labels["kubescape.io/workload-kind"], relatedKind) {
			matchingProfile = &profile
			break
		}
	}
	if matchingProfile == nil {
		return nil, fmt.Errorf("no application profile found for %s %s", relatedKind, relatedName)
	}
	applicationProfile, err := ksObjectConnection.ApplicationProfiles(namespace).Get(context.Background(), matchingProfile.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return applicationProfile, nil
}

func fetchNetworkNeighborProfile(ksObjectConnection spdxv1beta1client.SpdxV1beta1Interface, namespace string, relatedKind string, relatedName string) (*spdxv1beta1.NetworkNeighborhood, error) {
	networkNeighborProfileList, err := ksObjectConnection.NetworkNeighborhoods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var matchingProfile *spdxv1beta1.NetworkNeighborhood
	for _, profile := range networkNeighborProfileList.Items {
		if strings.EqualFold(profile.Labels["kubescape.io/workload-name"], relatedName) &&
			strings.EqualFold(profile.Labels["kubescape.io/workload-kind"], relatedKind) {
			matchingProfile = &profile
			break
		}
	}
	if matchingProfile == nil {
		return nil, fmt.Errorf("no network neighbor profile found for %s %s", relatedKind, relatedName)
	}
	networkNeighborProfile, err := ksObjectConnection.NetworkNeighborhoods(namespace).Get(context.Background(), matchingProfile.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return networkNeighborProfile, nil
}

// DeleteKubescapeStoragePod finds and deletes the first pod with label app=storage in the 'kubescape' namespace.
func DeleteKubescapeStoragePod(t *testing.T, clientset *kubernetes.Clientset) string {
	storagePods, err := clientset.CoreV1().Pods("kubescape").List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=storage",
	})
	if err != nil || len(storagePods.Items) == 0 {
		t.Fatalf("Failed to find storage pod with app=storage label: %v", err)
	}
	podName := storagePods.Items[0].Name
	err = clientset.CoreV1().Pods("kubescape").Delete(context.Background(), podName, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete storage pod %s: %v", podName, err)
	}
	t.Logf("Deleted storage pod: %s", podName)
	return podName
}

// WaitForPodWithLabelReady waits for a pod with the given label in the given namespace to be ready.
func WaitForPodWithLabelReady(t *testing.T, clientset *kubernetes.Clientset, namespace, labelSelector string) {
	ctx := context.Background()
	t.Logf("Waiting for pod with label '%s' in namespace '%s' to be ready", labelSelector, namespace)
	deadline := time.Now().Add(10 * time.Minute)
	for {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			t.Fatalf("Error listing pods: %v", err)
		}
		if len(pods.Items) > 0 {
			pod := pods.Items[0]
			isReady := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					isReady = true
					break
				}
			}
			if isReady {
				t.Log("Pod is ready")
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Timed out waiting for pod with label '%s' in namespace '%s' to be ready", labelSelector, namespace)
		}
		t.Log("Pod not ready yet, sleeping 10s...")
		time.Sleep(10 * time.Second)
	}
}

// DeleteNodeAgentPodOnSameNode finds the node where a given pod is running, then deletes the node-agent pod on that node in the 'kubescape' namespace.
func DeleteNodeAgentPodOnSameNode(t *testing.T, clientset *kubernetes.Clientset, testNamespace, testPodLabelSelector string) string {
	ctx := context.Background()
	pods, err := clientset.CoreV1().Pods(testNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: testPodLabelSelector,
	})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("Failed to find test pod with label %s in namespace %s: %v", testPodLabelSelector, testNamespace, err)
	}
	testPod := pods.Items[0]
	nodeName := testPod.Spec.NodeName
	if nodeName == "" {
		t.Fatalf("Test pod %s is not scheduled on any node", testPod.Name)
	}

	nodeAgentPods, err := clientset.CoreV1().Pods("kubescape").List(ctx, metav1.ListOptions{
		LabelSelector: "app=node-agent",
	})
	if err != nil || len(nodeAgentPods.Items) == 0 {
		t.Fatalf("Failed to find node-agent pods in kubescape namespace: %v", err)
	}
	for _, pod := range nodeAgentPods.Items {
		if pod.Spec.NodeName == nodeName {
			err := clientset.CoreV1().Pods("kubescape").Delete(ctx, pod.Name, metav1.DeleteOptions{})
			if err != nil {
				t.Fatalf("Failed to delete node-agent pod %s: %v", pod.Name, err)
			}
			t.Logf("Deleted node-agent pod: %s on node: %s", pod.Name, nodeName)
			return pod.Name
		}
	}
	t.Fatalf("No node-agent pod found on node %s", nodeName)
	return ""
}

func verifyApplicationProfileCompleted(t *testing.T, ksObjectConnection spdxv1beta1client.SpdxV1beta1Interface, expectedCompletness, testNamespace, relatedKind, relatedName string) {
	applicationProfile, err := fetchApplicationProfile(ksObjectConnection, testNamespace, relatedKind, relatedName)
	if err != nil {
		t.Fatalf("Failed to fetch application profile: %v", err)
	}
	if applicationProfile.Annotations["kubescape.io/status"] != "completed" {
		t.Fatalf("Application profile %s %s is not completed", relatedKind, relatedName)
	}
	if applicationProfile.Annotations["kubescape.io/completion"] != expectedCompletness {
		t.Fatalf("Application profile %s %s is not %s", relatedKind, relatedName, expectedCompletness)
	}
	// Check that there are contents in the profile
	if len(applicationProfile.Spec.Containers) == 0 {
		t.Fatalf("Application profile %s %s has no containers", relatedKind, relatedName)
	}
	// Check exec, open, syscall
	execCount := 0
	openCount := 0
	syscallCount := 0
	for _, container := range applicationProfile.Spec.Containers {
		execCount += len(container.Execs)
		openCount += len(container.Opens)
		syscallCount += len(container.Syscalls)
	}
	if execCount == 0 {
		t.Fatalf("Application profile %s %s has no execs", relatedKind, relatedName)
	}
	if openCount == 0 {
		t.Fatalf("Application profile %s %s has no opens", relatedKind, relatedName)
	}
	if syscallCount == 0 {
		t.Fatalf("Application profile %s %s has no syscalls", relatedKind, relatedName)
	}
}

func verifyNetworkNeighborProfileCompleted(t *testing.T, ksObjectConnection spdxv1beta1client.SpdxV1beta1Interface, expectEgress, expectIngress bool, expectedCompletness, testNamespace, relatedKind, relatedName string) {
	networkNeighborProfile, err := fetchNetworkNeighborProfile(ksObjectConnection, testNamespace, relatedKind, relatedName)
	if err != nil {
		t.Fatalf("Failed to fetch network neighbor profile: %v", err)
	}
	if networkNeighborProfile.Annotations["kubescape.io/status"] != "completed" {
		t.Fatalf("Network neighbor profile %s %s is not completed", relatedKind, relatedName)
	}
	if networkNeighborProfile.Annotations["kubescape.io/completion"] != expectedCompletness {
		t.Fatalf("Network neighbor profile %s %s is not %s", relatedKind, relatedName, expectedCompletness)
	}
	// Check that there are contents in the profile
	if len(networkNeighborProfile.Spec.Containers) == 0 {
		t.Fatalf("Network neighbor profile %s %s has no neighbors", relatedKind, relatedName)
	}

	egressCount := 0
	ingressCount := 0
	for _, container := range networkNeighborProfile.Spec.Containers {
		egressCount += len(container.Egress)
		ingressCount += len(container.Ingress)
	}
	if expectEgress && egressCount == 0 {
		t.Fatalf("Network neighbor profile %s %s has no egress", relatedKind, relatedName)
	}
	if expectIngress && ingressCount == 0 {
		t.Fatalf("Network neighbor profile %s %s has no ingress", relatedKind, relatedName)
	}
}
