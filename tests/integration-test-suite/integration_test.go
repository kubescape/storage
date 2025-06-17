package integration_test_suite

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	spdxv1beta1 "github.com/kubescape/storage/pkg/generated/clientset/versioned/typed/softwarecomposition/v1beta1"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type IntegrationTestSuite struct {
	suite.Suite
	testNamespace      string
	clientset          *kubernetes.Clientset
	ksObjectConnection spdxv1beta1.SpdxV1beta1Interface
}

func TestIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func TestMain(m *testing.M) {
	// Verify kubernetes cluster is available before running tests
	if _, err := GetKubeClient(); err != nil {
		fmt.Println("Error: Unable to connect to Kubernetes cluster. Please ensure a cluster is available and properly configured")
		os.Exit(1)
	}
	// Verify helm is installed
	if _, err := exec.LookPath("helm"); err != nil {
		fmt.Println("Error: helm is not installed or not in PATH. Please install helm before running tests")
		os.Exit(1)
	}

	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	updateIfPresent := pflag.Bool("update-if-present", false, "Update helm release if already present")
	extraHelmSetArgs := pflag.String("extra-helm-set-args", "", "Comma-separated extra helm set args (e.g. foo=bar,bar=baz)")
	pflag.CommandLine.Parse(args)

	// Parse comma-separated extra helm set args
	extraArgs := []string{}
	if *extraHelmSetArgs != "" {
		for _, arg := range strings.Split(*extraHelmSetArgs, ",") {
			arg = strings.TrimSpace(arg)
			if arg != "" {
				extraArgs = append(extraArgs, arg)
			}
		}
	}

	if err := EnsureKubescapeHelmRelease(*updateIfPresent, extraArgs); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// SetupSuite runs once before any tests in the suite
func (s *IntegrationTestSuite) SetupSuite() {
	clientset, err := GetKubeClient()
	s.Require().NoError(err)
	s.clientset = clientset

	ksObjectConnection, err := CreateKubscapeObjectConnection()
	s.Require().NoError(err)
	s.ksObjectConnection = ksObjectConnection

	// Verify all pods are ready
	pods, err := ListPodsInNamespace(s.clientset, "kubescape")
	s.Require().NoError(err)
	for _, pod := range pods {
		isReady := false
		readyContainers := 0
		totalContainers := len(pod.Status.ContainerStatuses)
		containerStatuses := pod.Status.ContainerStatuses
		for _, cond := range pod.Status.Conditions {
			if cond.Type == v1.PodReady && cond.Status == v1.ConditionTrue {
				isReady = true
				break
			}
			if pod.OwnerReferences != nil && len(pod.OwnerReferences) > 0 && pod.OwnerReferences[0].Kind == "Job" {
				isReady = true
				break
			}
		}
		for _, cs := range containerStatuses {
			if cs.Ready {
				readyContainers++
			}
		}
		if !isReady {
			reason := pod.Status.Reason
			message := pod.Status.Message
			if reason == "" && len(containerStatuses) > 0 {
				// Try to get reason/message from first container if available
				if containerStatuses[0].State.Waiting != nil {
					reason = containerStatuses[0].State.Waiting.Reason
					message = containerStatuses[0].State.Waiting.Message
				}
			}
			s.FailNow(
				fmt.Sprintf(
					"pod %s is not ready (%d/%d ready)",
					pod.Name,
					readyContainers,
					totalContainers,
				),
				reason,
				message,
			)
		}
	}
}

// TearDownSuite runs once after all tests in the suite
func (s *IntegrationTestSuite) TearDownSuite() {
}

// SetupTest runs before every test in the suite
func (s *IntegrationTestSuite) SetupTest() {
	// Create a unique namespace for each test
	nsName := fmt.Sprintf("test-ns-%d-%d", time.Now().UnixNano(), os.Getpid())
	s.testNamespace = nsName
	clientset, err := GetKubeClient()
	s.Require().NoError(err)
	s.clientset = clientset

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
		},
	}
	_, err = clientset.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	s.Require().NoError(err)

}

// TearDownTest runs after every test in the suite
func (s *IntegrationTestSuite) TearDownTest() {
	clientset, err := GetKubeClient()
	s.Require().NoError(err)
	err = clientset.CoreV1().Namespaces().Delete(context.Background(), s.testNamespace, metav1.DeleteOptions{})
	s.Require().NoError(err)
}

// SetupSubTest runs before every subtest
func (s *IntegrationTestSuite) SetupSubTest() {
}

// TearDownSubTest runs after every subtest
func (s *IntegrationTestSuite) TearDownSubTest() {
}

func (s *IntegrationTestSuite) LogWithTimestamp(msg string) {
	s.T().Logf("[%s] %s", time.Now().Format("2006-01-02 15:04:05"), msg)
}
