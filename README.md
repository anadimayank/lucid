# LUCID - Kubernetes Operator Upgrade Certification Plane

LUCID is a Kubernetes-native **Certification Plane** implemented as an OpenShift Operator. It prevents unsafe operator upgrades by validating them in an isolated sandbox using live cluster state.

## Overview

LUCID decouples upgrade orchestration from validation, providing deterministic and audit-ready guarantees for operator upgrades. It creates isolated virtual clusters (using vcluster) to test upgrades against mirrored production CRs before allowing the actual upgrade to proceed.

### Core Flow

```
UpgradeSandbox (Request)
    ↓
VClusterManager (Isolation)
    ↓
CRSnapshotter (Data Mirroring)
    ↓
SchemaValidator (Validation)
    ↓
OperatorUpgradeCertificate (Artifact)
    ↓
AdmissionWebhook (Gating)
```

## Architecture

### Components

| Component | Package | Description |
|-----------|---------|-------------|
| **API** | `api/v1alpha1/` | Defines CRDs: `UpgradeSandbox`, `OperatorUpgradeCertificate`, `ValidationRun` |
| **UpgradeSandbox Controller** | `internal/controller/` | Manages sandbox lifecycle and validation execution |
| **Certificate Controller** | `internal/controller/` | Monitors production state, invalidates stale certificates |
| **VClusterManager** | `internal/sandbox/` | Creates/manages virtual clusters using vcluster |
| **CRSnapshotter** | `internal/snapshotter/` | Deep-copies production CRs into sandboxes |
| **SchemaValidator** | `internal/validator/` | Validates CRs against OpenAPIv3 schemas |
| **AdmissionWebhook** | `internal/webhook/` | Intercepts OLM Subscription/InstallPlan to enforce certification |

### Custom Resources

#### UpgradeSandbox
Creates an isolated environment for testing operator upgrades.

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: UpgradeSandbox
metadata:
  name: prometheus-upgrade
  namespace: default
spec:
  operatorRef: prometheus-operator
  targetVersion: "2.45.0"
  scope:
    namespaces: ["monitoring", "default"]
    labels:
      app: prometheus
    crdGroups: ["monitoring.coreos.com"]
  environmentConfig:
    isolationMode: vcluster
    resourceLimits:
      cpu: "1"
      memory: "1Gi"
```

#### OperatorUpgradeCertificate
Represents certification result for a specific operator version.

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: OperatorUpgradeCertificate
metadata:
  name: prometheus-operator-2-45-0-cert
  namespace: default
spec:
  operatorRef: prometheus-operator
  targetVersion: "2.45.0"
  validityWindow: "24h"
status:
  summary: Certified          # Certified | CertifiedWithWarnings | Rejected | Stale | Pending
  policyOutcome: Allow        # Allow | Deny | Conditional
  timestamp: "2024-01-15T10:00:00Z"
  metrics:
    totalCRsAssessed: 15
    failures: 0
    coverage: 100
```

#### ValidationRun
Tracks validation execution details.

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: ValidationRun
metadata:
  name: validation-prometheus-001
  namespace: default
spec:
  sandboxRef: prometheus-upgrade
  procedure: full-validation    # full-validation | schema-validation | behavioral-validation | quick-check
status:
  result: Success
  logs: "All 15 objects validated successfully"
```

## Installation

### Prerequisites

- Kubernetes 1.25+ or OpenShift 4.12+
- `vcluster` CLI installed for sandbox creation
- OLM (Operator Lifecycle Manager) installed
- Go 1.21+ (for development)

### Deploy LUCID

```bash
# Install CRDs
make install

# Deploy the operator
make deploy

# Or apply manifests directly
kubectl apply -f config/crd/bases.yaml
kubectl apply -f config/rbac/
kubectl apply -f config/manager/
```

### Verify Installation

```bash
# Check pods are running
kubectl get pods -n lucid-system

# Verify webhooks are configured
kubectl get validatingwebhookconfigurations
kubectl get mutatingwebhookconfigurations
```

## Usage

### 1. Create an UpgradeSandbox

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: UpgradeSandbox
metadata:
  name: my-operator-test
spec:
  operatorRef: my-operator
  targetVersion: "1.2.3"
  scope:
    crdGroups: ["myapp.example.com"]
    namespaces: ["production"]
  environmentConfig:
    isolationMode: vcluster
    resourceLimits:
      cpu: "2"
      memory: "2Gi"
```

### 2. Wait for Validation

The controller will:
1. Create a vcluster sandbox
2. Snapshot production CRs
3. Apply them to the sandbox
4. Run schema validation
5. Create an `OperatorUpgradeCertificate`

```bash
# Check sandbox status
kubectl get upgradesandbox my-operator-test -o yaml

# Check certificate
kubectl get operatorupgradecertificate my-operator-test-cert -o yaml
```

### 3. Proceed with Upgrade

Once certified, the admission webhook allows OLM upgrades:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: my-operator
  namespace: operators
spec:
  package: my-operator
  startingCSV: my-operator.v1.2.3
```

## Development

### Build

```bash
# Build binary
make build

# Build Docker image
make docker-build IMG=ghcr.io/lucid-project/lucid-operator:latest
```

### Run Locally

```bash
# Run controller locally (for development)
make run

# With custom schema directory
go run ./main.go --schema-dir=./config/crd/bases
```

### Testing

```bash
# Run all tests
make test

# Run specific test
go test -v ./internal/snapshotter -run TestSnapshotLogic

# View coverage
go tool cover -html=cover.out
```

### Linting

```bash
make lint
```

### Generate Code

```bash
# Generate CRDs and webhooks
make manifests

# Generate DeepCopy code
make generate
```

## Lifecycle States

### UpgradeSandbox Phases

| Phase | Description |
|-------|-------------|
| `Creating` | VCluster and operator deployment in progress |
| `Running` | Sandbox is active, validation executing |
| `Idle` | Validation completed, awaiting certificate |
| `Failed` | Error occurred, retry scheduled |
| `TearingDown` | Cleanup in progress |

### Certificate Status

| Status | Description |
|--------|-------------|
| `Certified` | All validations passed |
| `CertifiedWithWarnings` | Passed with non-critical warnings |
| `Rejected` | Validation failures detected |
| `Stale` | Production state changed, re-validation needed |
| `Pending` | Validation in progress |

## Configuration

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `:8080` | Metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Health probe endpoint |
| `--leader-elect` | `false` | Enable leader election |
| `--webhook-cert-dir` | `/tmp/k8s-webhook-server/serving-certs` | Webhook certificates |
| `--schema-dir` | `/etc/lucid/schemas` | CRD schemas for validation |

## Security

- Webhook uses TLS with auto-generated certificates
- RBAC configured with least-privilege permissions
- Sandboxes run in isolated namespaces
- Production CRs are deep-copied (no direct access)

## Troubleshooting

### Check Sandbox Status

```bash
kubectl describe upgradesandbox <name>
```

### View Controller Logs

```bash
kubectl logs -n lucid-system -l control-plane=controller-manager
```

### Webhook Debugging

```bash
# Check webhook configuration
kubectl get mutatingwebhookconfigurations subscription-validator.lucid-project.io -o yaml

# Test webhook connectivity
kubectl run test --rm -it --image=busybox -- wget -O- https://lucid-webhook.lucid-system.svc:443/validate-olm-subscription
```

## License

Apache 2.0
