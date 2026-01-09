package integration_test_suite

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

func flattenHelmGetValuesOutput(prefix string, m map[string]interface{}, result *[]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[interface{}]interface{}:
			// Convert map[interface{}]interface{} to map[string]interface{}
			strMap := make(map[string]interface{})
			for mk, mv := range val {
				strMap[fmt.Sprintf("%v", mk)] = mv
			}
			flattenHelmGetValuesOutput(key, strMap, result)
		case map[string]interface{}:
			flattenHelmGetValuesOutput(key, val, result)
		default:
			*result = append(*result, fmt.Sprintf("%s=%v", key, v))
		}
	}
}

func EnsureKubescapeHelmRelease(updateIfPresent bool, extraHelmSetArgs []string) error {
	releaseName := "kubescape"
	chartName := "kubescape/kubescape-operator"
	repoName := "kubescape"
	repoURL := "https://kubescape.github.io/helm-charts/"

	// These values must be set so the test will work
	expectedHelmSetArgs := []string{
		"capabilities.runtimeDetection=enable",
		"alertCRD.installDefault=true",
		"nodeAgent.config.learningPeriod=30s",
		"nodeAgent.config.updatePeriod=1m",
		"storage.cleanupInterval=1m",
	}

	// Check if release exists
	cmd := exec.Command("helm", "status", releaseName, "-n", "kubescape")
	if err := cmd.Run(); err == nil && !updateIfPresent {
		log.Printf("Kubescape helm release already exists - not updating since updateIfPresent=false\n")
		// Verify the right helm parameters are set
		cmd = exec.Command("helm", "get", "values", releaseName, "-n", "kubescape")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to get helm values: %v\n%s", err, string(out))
		}
		// Discard the first line of the output
		lines := strings.Split(string(out), "\n")
		if len(lines) > 1 {
			out = []byte(strings.Join(lines[1:], "\n"))
		}
		// Convert the string from YAML to a map
		var m map[string]interface{}
		err = yaml.Unmarshal(out, &m)
		if err != nil {
			return fmt.Errorf("failed to unmarshal helm values: %v\n%s", err, string(out))
		}
		// Flatten the output
		var result []string
		flattenHelmGetValuesOutput("", m, &result)
		// Make sure all expected values are present
		for _, expected := range expectedHelmSetArgs {
			found := false
			for _, actual := range result {
				if actual == expected {
					found = true
					break
				}
			}
			if !found {
				log.Printf("WARNING: expected helm value %s not found in current installation - test may fail", expected)
			}
		}

		return nil
	}

	// Add the kubescape repo (idempotent)
	log.Printf("Adding kubescape helm repo if not present...")
	cmd = exec.Command("helm", "repo", "add", repoName, repoURL)
	_ = cmd.Run() // ignore error if already exists

	// Update helm repos
	log.Printf("Updating helm repos...")
	cmd = exec.Command("helm", "repo", "update")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helm repo update failed: %v\n%s", err, string(out))
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
		"--set", "capabilities.runtimeDetection=enable",
		"--set", "alertCRD.installDefault=true",
		"--set", "nodeAgent.config.learningPeriod=30s",
		"--set", "nodeAgent.config.updatePeriod=1m",
		"--set", "storage.cleanupInterval=1m",
		"--wait",
		"--timeout", "5m0s",
	}
	for _, arg := range extraHelmSetArgs {
		args = append(args, "--set", arg)
	}
	log.Printf("Running helm command: %v", args)
	cmd = exec.Command("helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		// If the error looks like a concurrent install/upgrade race (release already exists),
		// wait for the release to appear/become deployed (best-effort).
		if strings.Contains(outStr, "already exists") || strings.Contains(outStr, "another operation") {
			log.Printf("Detected concurrent helm operation while running helm upgrade/install: %s", outStr)
			// Wait (best-effort) for another process to finish installing/upgrading the release.
			timeout := 3 * time.Minute
			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				statusCmd := exec.Command("helm", "list", "-n", "kubescape", "--filter", fmt.Sprintf("^%s$", releaseName), "-o", "yaml")
				statusOut, _ := statusCmd.CombinedOutput()
				var list []map[string]interface{}
				_ = yaml.Unmarshal(statusOut, &list)
				if len(list) > 0 {
					if st, ok := list[0]["status"].(string); ok && strings.ToLower(st) == "deployed" {
						log.Printf("Kubescape helm release %s is now deployed (detected after concurrent operation)", releaseName)
						goto INSTALLED_OK
					}
					log.Printf("Helm release %s present but not yet deployed: status=%v; waiting...", releaseName, list[0]["status"])
				} else {
					log.Printf("Helm release %s not present yet; waiting...", releaseName)
				}
				time.Sleep(2 * time.Second)
			}
			return fmt.Errorf("helm upgrade/install failed: %v\n%s\nTimed out waiting for concurrent install to finish", err, outStr)
		}
		return fmt.Errorf("helm upgrade/install failed: %v\n%s", err, outStr)
	}
INSTALLED_OK:
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
