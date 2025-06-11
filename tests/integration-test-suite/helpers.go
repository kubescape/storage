package integration_test_suite

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	applicationProfileList, err := ksObjectConnection.ApplicationProfiles(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("kubescape.io/workload-name=%s", relatedName),
	})
	if err != nil {
		return nil, err
	}
	if len(applicationProfileList.Items) == 0 {
		return nil, fmt.Errorf("no application profile found for %s %s", relatedKind, relatedName)
	}
	var matchingProfile *spdxv1beta1.ApplicationProfile
	for _, profile := range applicationProfileList.Items {
		if strings.EqualFold(profile.Labels["kubescape.io/workload-kind"], relatedKind) {
			matchingProfile = &profile
			break
		}
	}
	if matchingProfile == nil {
		return nil, fmt.Errorf("no application profile found for %s %s with matching kind", relatedKind, relatedName)
	}
	applicationProfile, err := ksObjectConnection.ApplicationProfiles(namespace).Get(context.Background(), matchingProfile.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return applicationProfile, nil
}

func fetchNetworkNeighborProfile(ksObjectConnection spdxv1beta1client.SpdxV1beta1Interface, namespace string, relatedKind string, relatedName string) (*spdxv1beta1.NetworkNeighborhood, error) {
	networkNeighborProfileList, err := ksObjectConnection.NetworkNeighborhoods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("kubescape.io/workload-name=%s", relatedName),
	})
	if err != nil {
		return nil, err
	}
	if len(networkNeighborProfileList.Items) == 0 {
		return nil, fmt.Errorf("no network neighbor profile found for %s %s", relatedKind, relatedName)
	}
	var matchingProfile *spdxv1beta1.NetworkNeighborhood
	for _, profile := range networkNeighborProfileList.Items {
		if strings.EqualFold(profile.Labels["kubescape.io/workload-kind"], relatedKind) {
			matchingProfile = &profile
			break
		}
	}
	if matchingProfile == nil {
		return nil, fmt.Errorf("no network neighbor profile found for %s %s with matching kind", relatedKind, relatedName)
	}
	networkNeighborProfile, err := ksObjectConnection.NetworkNeighborhoods(namespace).Get(context.Background(), matchingProfile.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return networkNeighborProfile, nil
}
