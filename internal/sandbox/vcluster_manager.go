package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type VClusterManager struct {
	client client.Client
}

func NewVClusterManager(c client.Client) *VClusterManager {
	return &VClusterManager{client: c}
}

// CreateVirtualCluster initializes a vcluster instance for a sandbox
func (m *VClusterManager) CreateVirtualCluster(ctx context.Context, sandbox *lucidv1alpha1.UpgradeSandbox) (string, error) {
	l := log.FromContext(ctx)

	vclusterName := fmt.Sprintf("vcluster-%s-%s", sandbox.Name, strings.ReplaceAll(sandbox.Spec.TargetVersion, ".", "-"))
	namespace := fmt.Sprintf("lucid-sandbox-%s", sandbox.Name)

	l.Info("Creating vcluster", "name", vclusterName, "namespace", namespace, "isolationMode", sandbox.Spec.EnvironmentConfig.IsolationMode)

	// Create namespace for the vcluster
	createNamespaceCmd := exec.CommandContext(ctx, "kubectl", "create", "namespace", namespace)
	if output, err := createNamespaceCmd.CombinedOutput(); err != nil {
		if !strings.Contains(string(output), "already exists") {
			return "", fmt.Errorf("failed to create namespace %s: %w: %s", namespace, err, string(output))
		}
	}

	// Create vcluster using vcluster CLI
	args := []string{
		"create", vclusterName,
		"--namespace", namespace,
		"--connect", "false",
	}

	// Add resource limits if specified
	if sandbox.Spec.EnvironmentConfig.ResourceLimits.CPU != "" {
		args = append(args, "--cpu", sandbox.Spec.EnvironmentConfig.ResourceLimits.CPU)
	}
	if sandbox.Spec.EnvironmentConfig.ResourceLimits.Memory != "" {
		args = append(args, "--memory", sandbox.Spec.EnvironmentConfig.ResourceLimits.Memory)
	}

	createCmd := exec.CommandContext(ctx, "vcluster", args...)
	output, err := createCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create vcluster: %w: %s", err, string(output))
	}

	// Wait for vcluster to be ready
	if err := m.waitForVClusterReady(ctx, vclusterName, namespace); err != nil {
		return "", fmt.Errorf("vcluster not ready: %w", err)
	}

	l.Info("vcluster created successfully", "name", vclusterName)
	return vclusterName, nil
}

// DeployOperator deploys the target operator version into the vcluster
func (m *VClusterManager) DeployOperator(ctx context.Context, vclusterName string, operatorRef string, version string) error {
	l := log.FromContext(ctx)

	l.Info("Deploying operator", "operator", operatorRef, "version", version, "vcluster", vclusterName)

	// Get vcluster kubeconfig
	kubeconfigCmd := exec.CommandContext(ctx, "vcluster", "connect", vclusterName, "--print")
	kubeconfigOutput, err := kubeconfigCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get vcluster kubeconfig: %w: %s", err, string(kubeconfigOutput))
	}

	// Use OLM to install the operator
	// This is a simplified implementation - in production you'd want to:
	// 1. Apply OperatorGroup, Subscription, etc.
	// 2. Wait for operator to be ready
	// 3. Check for deployment success

	installCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	installCmd.Env = append([]string{fmt.Sprintf("KUBECONFIG=%s", string(kubeconfigOutput))})

	operatorManifest := fmt.Sprintf(`
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: %s
  namespace: operators
spec:
  channel: stable
  name: %s
  source: operatorhubio-catalog
  sourceNamespace: olm
  startingCSV: %s.v%s
`, operatorRef, operatorRef, operatorRef, version)

	installCmd.Stdin = strings.NewReader(operatorManifest)
	if output, err := installCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to deploy operator: %w: %s", err, string(output))
	}

	// Wait for operator to be ready
	if err := m.waitForOperatorReady(ctx, vclusterName, operatorRef); err != nil {
		l.Error(err, "operator not ready after deployment")
		return err
	}

	l.Info("Operator deployed successfully", "operator", operatorRef, "version", version)
	return nil
}

// DeleteVirtualCluster tears down the vcluster and its namespace
func (m *VClusterManager) DeleteVirtualCluster(ctx context.Context, vclusterName, namespace string) error {
	l := log.FromContext(ctx)

	l.Info("Deleting vcluster", "name", vclusterName, "namespace", namespace)

	// Delete vcluster using vcluster CLI
	deleteCmd := exec.CommandContext(ctx, "vcluster", "delete", vclusterName, "--namespace", namespace)
	output, err := deleteCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete vcluster: %w: %s", err, string(output))
	}

	// Delete the namespace
	deleteNsCmd := exec.CommandContext(ctx, "kubectl", "delete", "namespace", namespace, "--ignore-not-found")
	if nsOutput, nsErr := deleteNsCmd.CombinedOutput(); nsErr != nil {
		l.Error(nsErr, "failed to delete namespace", "namespace", namespace, "output", string(nsOutput))
		return fmt.Errorf("failed to delete namespace %s: %w: %s", namespace, nsErr, string(nsOutput))
	}

	l.Info("vcluster and namespace deleted successfully", "name", vclusterName, "namespace", namespace)
	return nil
}

// waitForVClusterReady waits for the vcluster to be ready
func (m *VClusterManager) waitForVClusterReady(ctx context.Context, vclusterName, namespace string) error {
	l := log.FromContext(ctx)

	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for vcluster %s to be ready", vclusterName)
		case <-ticker.C:
			// Check if vcluster pod is ready
			checkCmd := exec.CommandContext(ctx, "kubectl", "get", "pod",
				"-n", namespace,
				"-l", "app=vcluster",
				"-o", "jsonpath={.items[*].status.conditions[?(@.type==\"Ready\")].status}")
			output, err := checkCmd.CombinedOutput()
			if err != nil {
				l.Info("Waiting for vcluster to be ready...", "error", err)
				continue
			}

			if strings.Contains(string(output), "True") {
				l.Info("vcluster is ready")
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// waitForOperatorReady waits for the operator to be ready in the vcluster
func (m *VClusterManager) waitForOperatorReady(ctx context.Context, vclusterName, operatorRef string) error {
	l := log.FromContext(ctx)

	timeout := time.After(10 * time.Minute)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for operator %s to be ready", operatorRef)
		case <-ticker.C:
			// Check if operator deployment is ready
			checkCmd := exec.CommandContext(ctx, "kubectl", "get", "deployment",
				"-n", "operators",
				"-l", fmt.Sprintf("operator.coreos.com/%s=", operatorRef),
				"-o", "jsonpath={.items[0].status.readyReplicas}")
			output, err := checkCmd.CombinedOutput()
			if err != nil {
				l.Info("Waiting for operator to be ready...", "error", err)
				continue
			}

			if strings.TrimSpace(string(output)) == "1" {
				l.Info("Operator is ready", "operator", operatorRef)
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
