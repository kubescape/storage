package integration_test_suite

import (
	"fmt"
	"log"
	"os/exec"
)

func EnsureKubescapeHelmRelease(updateIfPresent bool, extraHelmSetArgs []string) error {
	releaseName := "kubescape"
	chartName := "kubescape/kubescape-operator"
	repoName := "kubescape"
	repoURL := "https://kubescape.github.io/helm-charts/"

	// Add the kubescape repo (idempotent)
	log.Printf("Adding kubescape helm repo if not present...")
	cmd := exec.Command("helm", "repo", "add", repoName, repoURL)
	_ = cmd.Run() // ignore error if already exists

	// Update helm repos
	log.Printf("Updating helm repos...")
	cmd = exec.Command("helm", "repo", "update")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helm repo update failed: %v\n%s", err, string(out))
	}

	// Check if release exists
	cmd = exec.Command("helm", "status", releaseName, "-n", "kubescape")
	if err := cmd.Run(); err == nil && !updateIfPresent {
		log.Printf("Kubescape helm release already exists - not updating since updateIfPresent=false\n")
		return nil
	}

	// Get current cluster name from kubectl config
	cmd = exec.Command("kubectl", "config", "current-context")
	clusterNameBytes, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get current cluster name: %v", err)
	}
	clusterName := string(clusterNameBytes)
	clusterName = string([]byte(clusterName[:len(clusterName)-1])) // trim newline

	// Install/upgrade the helm release
	log.Printf("Installing/upgrading kubescape helm release...")
	args := []string{
		"upgrade", "--install", releaseName, chartName,
		"-n", "kubescape", "--create-namespace",
		"--set", fmt.Sprintf("clusterName=%s", clusterName),
		"--set", "capabilities.continuousScan=enable",
		"--set", "capabilities.runtimeDetection=enable",
		"--set", "alertCRD.installDefault=true",
		"--wait",
		"--timeout", "5m0s",
	}
	for _, arg := range extraHelmSetArgs {
		args = append(args, "--set", arg)
	}
	cmd = exec.Command("helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm upgrade/install failed: %v\n%s", err, string(out))
	}
	log.Printf("Kubescape helm release installed/upgraded successfully.")
	// Print installed chart version and parameters
	log.Printf("Getting installed chart details...")
	cmd = exec.Command("helm", "list", "-n", "kubescape", "--filter", "^kubescape$", "-o", "yaml")
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to get chart details: %v", err)
	} else {
		log.Printf("Installed chart details:\n%s", string(out))
	}
	return nil
}
