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

	origArgs := os.Args[1:]
	testBinaryArgs := []string{}
	goTestArgs := origArgs
	for i, a := range origArgs {
		if a == "--" {
			testBinaryArgs = origArgs[i+1:]
			goTestArgs = origArgs[:i]
			// Keep only program name + go test args in os.Args so the test harness sees only
			// the flags it expects and not our custom ones.
			os.Args = append([]string{os.Args[0]}, goTestArgs...)
			break
		}
	}

	// Flags to control helm behavior in tests. Useful when running test suites in parallel.
	skipEnsureHelm := pflag.Bool("skip-ensure-helm", false, "Skip ensuring kubescape helm release is installed/upgraded")
	updateIfPresent := pflag.Bool("update-helm-if-present", true, "If false, do not perform helm upgrade if a release already exists")
	extraHelmSetArgs := pflag.String("extra-helm-set-args", "", "Comma-separated extra helm set args (e.g. foo=bar,bar=baz)")
	pflag.CommandLine.Parse(testBinaryArgs)

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

	if !*skipEnsureHelm {
		if err := EnsureKubescapeHelmRelease(*updateIfPresent, extraArgs); err != nil {
			panic(err)
		}
	} else {
		fmt.Println("Skipping EnsureKubescapeHelmRelease due to --skip-ensure-helm flag")
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
			if len(pod.OwnerReferences) > 0 && pod.OwnerReferences[0].Kind == "Job" {
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

	gracePeriod := int64(0)
	propagationPolicy := metav1.DeletePropagationForeground
	deleteOptions := metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		PropagationPolicy:  &propagationPolicy,
	}

	// Delete Deployments
	err = clientset.AppsV1().Deployments(s.testNamespace).DeleteCollection(context.Background(), deleteOptions, metav1.ListOptions{})
	if err != nil {
		s.T().Logf("Warning: Failed to delete deployments: %v", err)
	}

	// Delete StatefulSets
	err = clientset.AppsV1().StatefulSets(s.testNamespace).DeleteCollection(context.Background(), deleteOptions, metav1.ListOptions{})
	if err != nil {
		s.T().Logf("Warning: Failed to delete statefulsets: %v", err)
	}

	// Delete DaemonSets
	err = clientset.AppsV1().DaemonSets(s.testNamespace).DeleteCollection(context.Background(), deleteOptions, metav1.ListOptions{})
	if err != nil {
		s.T().Logf("Warning: Failed to delete daemonsets: %v", err)
	}

	// Delete Jobs
	err = clientset.BatchV1().Jobs(s.testNamespace).DeleteCollection(context.Background(), deleteOptions, metav1.ListOptions{})
	if err != nil {
		s.T().Logf("Warning: Failed to delete jobs: %v", err)
	}

	// Delete CronJobs
	err = clientset.BatchV1().CronJobs(s.testNamespace).DeleteCollection(context.Background(), deleteOptions, metav1.ListOptions{})
	if err != nil {
		s.T().Logf("Warning: Failed to delete cronjobs: %v", err)
	}

	// Delete Pods
	pods, err := clientset.CoreV1().Pods(s.testNamespace).List(context.Background(), metav1.ListOptions{})
	s.Require().NoError(err)
	for _, pod := range pods.Items {
		err = clientset.CoreV1().Pods(s.testNamespace).Delete(context.Background(), pod.Name, deleteOptions)
		if err != nil {
			s.T().Logf("Warning: Failed to delete pod %s: %v", pod.Name, err)
		}
	}

	// Finally delete the namespace
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
