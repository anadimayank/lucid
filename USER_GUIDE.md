# LUCID User Guide

A comprehensive guide for operating the LUCID Upgrade Certification Platform.

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Core Concepts](#core-concepts)
3. [Installation](#installation)
4. [Basic Operations](#basic-operations)
5. [Advanced Configuration](#advanced-configuration)
6. [Monitoring & Observability](#monitoring--observability)
7. [Troubleshooting](#troubleshooting)
8. [Best Practices](#best-practices)
9. [Security Considerations](#security-considerations)
10. [API Reference](#api-reference)

---

## Getting Started

### What Problem Does LUCID Solve?

Operator upgrades in Kubernetes/OpenShift environments carry inherent risks:
- Schema incompatibilities can break existing resources
- Behavioral changes may cause unexpected application failures
- No standardized way to validate upgrades before applying them

LUCID addresses this by:
1. Creating an isolated sandbox environment
2. Mirroring your production state into the sandbox
3. Testing the upgrade in isolation
4. Issuing a certificate only if validation passes
5. Blocking uncertified upgrades at the admission layer

### Who Should Use This Guide?

- **Platform Engineers**: Deploying and configuring LUCID
- **SREs**: Monitoring and troubleshooting certification failures
- **Developers**: Understanding how to request and interpret certifications
- **Security Teams**: Auditing upgrade compliance

---

## Core Concepts

### The Certification Lifecycle

```
┌─────────────────────────────────────────────────────────────────┐
│                    LUCID Certification Flow                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. REQUEST         2. ISOLATE        3. MIRROR                 │
│  ┌─────────┐       ┌─────────┐       ┌─────────┐                │
│  │Upgrade  │  ───▶ │ vcluster│  ───▶ │   CR    │                │
│  │Sandbox  │       │ Create  │       │Snapshot │                │
│  └─────────┘       └─────────┘       └─────────┘                │
│       ▲                                      │                  │
│       │                                      ▼                  │
│  6. ENFORCE    ◀─────────────────────    4. VALIDATE            │
│  ┌─────────┐                              ┌─────────┐            │
│  │Admission │  ◀─── Certificate ───────── │ Schema  │            │
│  │ Webhook  │       Created               │  Check  │            │
│  └─────────┘                              └─────────┘            │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### Key Resources

| Resource | Purpose | Lifecycle |
|----------|---------|-----------|
| `UpgradeSandbox` | Requests a validation environment | Created by user, managed by controller |
| `OperatorUpgradeCertificate` | Certification artifact | Auto-created after validation |
| `ValidationRun` | Execution audit trail | Auto-created during validation |

### Certificate States

```
                    ┌──────────────┐
                    │   Pending    │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
       ┌───────────┐ ┌───────────┐ ┌───────────┐
       │Certified  │ │  Rejected │ │  Stale    │
       └─────┬─────┘ └─────┬─────┘ └─────┬─────┘
             │             │             │
             │             │             ▼
             │             │       ┌───────────┐
             │             │       │Re-validate│
             │             │       └───────────┘
             │             │
             ▼             ▼
       ┌─────────────────────────┐
       │   Upgrade Allowed?      │
       │  Certified: YES ✓       │
       │  Others: NO ✗           │
       └─────────────────────────┘
```

---

## Installation

### Prerequisites Checklist

- [ ] Kubernetes 1.25+ or OpenShift 4.12+
- [ ] OLM (Operator Lifecycle Manager) installed
- [ ] `vcluster` CLI available in PATH
- [ ] `kubectl` configured with cluster access
- [ ] Storage class available for vcluster PVCs
- [ ] Network policies allow inter-namespace communication

### Step 1: Install CRDs

```bash
# Apply LUCID CRDs
kubectl apply -f config/crd/bases.yaml

# Verify installation
kubectl get crd | grep lucid
# Expected output:
# operatorupgradecertificates.lucid.lucid-project.io
# upgradesandboxes.lucid.lucid-project.io
# validationruns.lucid.lucid-project.io
```

### Step 2: Deploy RBAC

```bash
# Apply RBAC configuration
kubectl apply -f config/rbac/

# Verify service account and roles
kubectl get serviceaccount -n lucid-system
kubectl get clusterrole | grep lucid
kubectl get clusterrolebinding | grep lucid
```

### Step 3: Deploy the Operator

```bash
# Deploy to cluster
kubectl apply -f config/manager/

# Watch deployment status
kubectl rollout status deployment/lucid-controller-manager -n lucid-system

# Verify pods are running
kubectl get pods -n lucid-system
# NAME                                      READY   STATUS    RESTARTS   AGE
# lucid-controller-manager-5d4f6b7c8-abc12   1/1     Running   0          2m
```

### Step 4: Verify Webhook Configuration

```bash
# Check mutating webhook
kubectl get mutatingwebhookconfigurations subscription-validator.lucid-project.io

# Check webhook service
kubectl get svc -n lucid-system lucid-webhook-service

# Verify certificates are mounted
kubectl exec -n lucid-system deployment/lucid-controller-manager -- ls -la /tmp/k8s-webhook-server/serving-certs/
```

### Step 5: Install vcluster (if not present)

```bash
# Download vcluster CLI
curl -L -o vcluster "https://github.com/loft-sh/vcluster/releases/latest/download/vcluster-linux-amd64"
sudo install -c -m 0755 vcluster /usr/local/bin
vcluster --version
```

---

## Basic Operations

### Creating Your First UpgradeSandbox

#### Example: Prometheus Operator Upgrade

**Scenario**: You want to upgrade Prometheus Operator from v2.44.0 to v2.45.0 in your production cluster.

**Step 1: Create the Sandbox**

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: UpgradeSandbox
metadata:
  name: prometheus-245-upgrade
  namespace: monitoring
spec:
  # Target operator and version
  operatorRef: prometheus-operator
  targetVersion: "2.45.0"
  
  # Scope: what to mirror from production
  scope:
    namespaces:
      - monitoring
      - default
    labels:
      app.kubernetes.io/name: prometheus
    crdGroups:
      - monitoring.coreos.com
  
  # Environment configuration
  environmentConfig:
    isolationMode: vcluster
    resourceLimits:
      cpu: "2"
      memory: "4Gi"
```

```bash
# Apply the sandbox
kubectl apply -f prometheus-sandbox.yaml

# Watch the status
kubectl get upgradesandbox prometheus-245-upgrade -n monitoring -w
```

**Step 2: Monitor Progress**

```bash
# Check sandbox phase
kubectl describe upgradesandbox prometheus-245-upgrade -n monitoring

# Expected status progression:
# Phase: Creating → Running → Idle
# Health: Unknown → Healthy
```

**Step 3: Review the Certificate**

```bash
# List certificates
kubectl get operatorupgradecertificates -n monitoring

# Check certification result
kubectl describe operatorupgradecertificate prometheus-operator-2-45-0-cert -n monitoring

# View validation logs
kubectl get validationruns -n monitoring -o jsonpath='{.items[*].status.logs}'
```

**Step 4: Proceed with Upgrade**

If certified:
```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: prometheus-operator
  namespace: operators
spec:
  package: prometheus-operator
  channel: stable
  startingCSV: prometheus-operator.v2.45.0
```

### Interpreting Certificate Status

#### Certified ✓
```yaml
status:
  summary: Certified
  policyOutcome: Allow
  metrics:
    totalCRsAssessed: 25
    failures: 0
    coverage: 100
```
**Action**: Safe to proceed with upgrade.

#### CertifiedWithWarnings ⚠
```yaml
status:
  summary: CertifiedWithWarnings
  policyOutcome: Conditional
  metrics:
    totalCRsAssessed: 25
    failures: 0
    coverage: 100
```
**Action**: Review warnings in ValidationRun logs. Upgrade allowed but monitor closely.

#### Rejected ✗
```yaml
status:
  summary: Rejected
  policyOutcome: Deny
  metrics:
    totalCRsAssessed: 25
    failures: 3
    coverage: 88
```
**Action**: Do NOT upgrade. Review ValidationRun for specific failures:

```bash
kubectl get validationruns -n monitoring
kubectl describe validationrun <run-name> -n monitoring
```

#### Stale 🔄
```yaml
status:
  summary: Stale
  policyOutcome: Deny
```
**Action**: Production state changed since validation. Re-validation triggered automatically. Wait for new certificate.

---

## Advanced Configuration

### Customizing Resource Limits

For large operators or extensive CR sets:

```yaml
spec:
  environmentConfig:
    resourceLimits:
      cpu: "4"        # 4 CPU cores
      memory: "8Gi"   # 8GB RAM
```

### Multi-Namespace Scoping

Mirror CRs from multiple namespaces:

```yaml
spec:
  scope:
    namespaces:
      - production
      - staging
      - monitoring
    crdGroups:
      - apps.example.com
      - database.example.com
```

### Label-Based Filtering

Only mirror specific resources:

```yaml
spec:
  scope:
    labels:
      environment: production
      team: platform
    crdGroups:
      - apps.example.com
```

### Isolation Modes

#### vcluster (Recommended)
```yaml
environmentConfig:
  isolationMode: vcluster
```
Full API server isolation. Best for production validation.

#### namespace
```yaml
environmentConfig:
  isolationMode: namespace
```
Namespace-level isolation only. Faster but less isolated.

#### in-process
```yaml
environmentConfig:
  isolationMode: in-process
```
Schema-only validation without deployment. Fastest but no behavioral testing.

### Validity Window Configuration

Control how long certificates remain valid:

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: OperatorUpgradeCertificate
metadata:
  name: custom-validity-cert
spec:
  operatorRef: my-operator
  targetVersion: "1.0.0"
  validityWindow: "72h"  # Certificate valid for 72 hours
```

### Custom Validation Procedures

```yaml
apiVersion: lucid.lucid-project.io/v1alpha1
kind: ValidationRun
metadata:
  name: custom-validation
spec:
  sandboxRef: my-sandbox
  procedure: schema-validation  # Options:
                                # - full-validation
                                # - schema-validation
                                # - behavioral-validation
                                # - quick-check
```

---

## Monitoring & Observability

### Metrics Endpoints

LUCID exposes Prometheus metrics at `:8080/metrics`:

```bash
# Port-forward for local access
kubectl port-forward svc/lucid-controller-manager -n lucid-system 8080:8080

# View metrics
curl http://localhost:8080/metrics
```

#### Key Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `lucid_sandboxes_total` | Counter | Total sandboxes created |
| `lucid_sandboxes_active` | Gauge | Currently active sandboxes |
| `lucid_certificates_issued` | Counter | Total certificates issued |
| `lucid_certificates_rejected` | Counter | Certificates rejected |
| `lucid_validations_duration_seconds` | Histogram | Validation duration |
| `lucid_webhook_requests_total` | Counter | Webhook admission requests |
| `lucid_webhook_denials_total` | Counter | Denied admission requests |

### Log Aggregation

```bash
# Stream controller logs
kubectl logs -f deployment/lucid-controller-manager -n lucid-system

# Filter by sandbox name
kubectl logs deployment/lucid-controller-manager -n lucid-system | grep "prometheus-245-upgrade"

# Export logs for audit
kubectl logs deployment/lucid-controller-manager -n lucid-system --since=24h > lucid-audit.log
```

### Alerting Rules (Prometheus)

```yaml
groups:
  - name: lucid-alerts
    rules:
      - alert: LUCIDValidationFailing
        expr: rate(lucid_certificates_rejected[5m]) > 0.5
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "High rate of LUCID validation rejections"
          
      - alert: LUCIDWebhookLatency
        expr: histogram_quantile(0.99, rate(lucid_webhook_request_duration_bucket[5m])) > 5
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "LUCID webhook latency exceeding 5 seconds"
          
      - alert: LUCIDStaleCertificates
        expr: sum(lucid_certificates_stale) > 10
        for: 15m
        labels:
          severity: info
        annotations:
          summary: "Multiple stale certificates detected"
```

### Grafana Dashboard

Import the pre-built dashboard:

```bash
kubectl create configmap lucid-dashboard \
  --from-file=dashboard.json=config/monitoring/grafana-dashboard.json \
  -n monitoring
```

---

## Troubleshooting

### Sandbox Stuck in "Creating" Phase

**Symptoms**:
```bash
kubectl get upgradesandbox my-sandbox
# PHASE: Creating (unchanged for >10 minutes)
```

**Diagnosis**:
```bash
# Check vcluster creation
kubectl describe upgradesandbox my-sandbox

# Check for vcluster pod
kubectl get pods -n lucid-sandbox-my-sandbox

# Check events
kubectl get events -n lucid-sandbox-my-sandbox --sort-by='.lastTimestamp'
```

**Common Causes**:
1. **Insufficient resources**: Check node capacity
2. **PVC provisioning failure**: Verify StorageClass
3. **vcluster not installed**: Ensure vcluster CLI is available

**Resolution**:
```bash
# Delete and recreate sandbox
kubectl delete upgradesandbox my-sandbox
kubectl apply -f my-sandbox.yaml
```

### Certificate Shows "Rejected"

**Symptoms**:
```yaml
status:
  summary: Rejected
  policyOutcome: Deny
```

**Diagnosis**:
```bash
# Find the validation run
kubectl get validationruns -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.logs}{"\n"}{end}'

# Get detailed failure info
kubectl describe validationrun <run-name>
```

**Common Causes**:
1. **Schema mismatch**: CR structure incompatible with target version
2. **Missing required fields**: New version requires additional fields
3. **Type errors**: Field types changed between versions

**Resolution**:
```bash
# Review specific errors in validation logs
kubectl get validationrun <run-name> -o jsonpath='{.status.logs}'

# Update CRs to match new schema
# Or pin to a compatible version
```

### Certificate Shows "Stale"

**Symptoms**:
```yaml
status:
  summary: Stale
```

**Diagnosis**:
```bash
# Check state ConfigMap
kubectl get configmap lucid-state-<operator> -o yaml

# Check recent production changes
kubectl get events --field-selector involvedObject.kind=UpgradeSandbox
```

**Common Causes**:
1. Production CR modified after validation
2. New CRs created in scoped namespaces
3. Namespace labels changed

**Resolution**:
- Automatic re-validation is triggered
- Wait for new certificate (typically 5-10 minutes)
- If persistent, create new UpgradeSandbox

### Webhook Blocking All Upgrades

**Symptoms**:
```
Error from server: admission webhook "subscription-validator.lucid-project.io" denied the request
```

**Diagnosis**:
```bash
# Check webhook configuration
kubectl get mutatingwebhookconfigurations subscription-validator.lucid-project.io -o yaml

# Verify certificate exists
kubectl get operatorupgradecertificates

# Check webhook logs
kubectl logs -n lucid-system -l app=lucid-webhook
```

**Resolution**:
```bash
# Temporary bypass (USE WITH CAUTION)
kubectl label namespace <namespace> lucid-bypass=true

# Or disable webhook temporarily
kubectl patch mutatingwebhookconfigurations subscription-validator.lucid-project.io \
  --type='json' -p='[{"op": "replace", "path": "/webhooks/0/failurePolicy", "value": "Ignore"}]'
```

### vcluster Creation Fails

**Symptoms**:
```
failed to create vcluster: exit status 1: Error: namespace "lucid-sandbox-xxx" already exists
```

**Resolution**:
```bash
# Clean up orphaned namespaces
kubectl get namespaces -l lucid.lucid-project.io/sandbox-cleanup=pending
kubectl delete namespace lucid-sandbox-orphaned-name

# Retry sandbox creation
```

---

## Best Practices

### Sandbox Design

1. **One Sandbox Per Upgrade**: Create separate sandboxes for each operator/version combination
2. **Scoped Resource Selection**: Only mirror necessary CRs to reduce validation time
3. **Appropriate Resource Limits**: Allocate sufficient CPU/memory for accurate testing

```yaml
# Good: Focused scope
spec:
  scope:
    namespaces: ["production-db"]
    crdGroups: ["database.example.com"]
    
# Avoid: Too broad
spec:
  scope:
    namespaces: ["*"]  # Don't mirror everything
```

### Certificate Management

1. **Monitor Validity Windows**: Set alerts for expiring certificates
2. **Re-validate After Changes**: Any production change invalidates certificates
3. **Archive ValidationRuns**: Keep audit trails for compliance

```bash
# Export validation artifacts
kubectl get validationruns -o yaml > validation-archive-$(date +%Y%m%d).yaml
```

### Production Safety

1. **Test LUCID First**: Validate LUCID itself in a staging environment
2. **Gradual Rollout**: Enable webhooks namespace by namespace
3. **Maintain Break-Glass**: Document webhook bypass procedures

### Performance Optimization

| Scenario | Recommendation |
|----------|----------------|
| Large CR count (>1000) | Use label filtering, increase resource limits |
| Multiple operators | Parallel sandboxes with namespace isolation |
| Frequent upgrades | Extend validity window to 48-72h |
| Limited cluster resources | Use `in-process` isolation for schema-only checks |

---

## Security Considerations

### RBAC Configuration

LUCID requires specific permissions:

```yaml
# Minimum required permissions
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: lucid-minimal
rules:
  - apiGroups: ["lucid.lucid-project.io"]
    resources: ["*"]
    verbs: ["*"]
  - apiGroups: ["operators.coreos.com"]
    resources: ["subscriptions", "installplans"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["namespaces", "configmaps"]
    verbs: ["*"]
```

### Network Policies

Restrict sandbox network access:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: lucid-sandbox-isolation
  namespace: lucid-sandbox-*
spec:
  podSelector: {}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: lucid-system
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              name: lucid-system
```

### Secret Management

Webhook certificates are auto-generated. For production:

```bash
# Use cert-manager integration
kubectl apply -f config/webhook/cert-manager-certificate.yaml

# Or provide your own
kubectl create secret tls lucid-webhook-certs \
  --cert=path/to/tls.crt \
  --key=path/to/tls.key \
  -n lucid-system
```

### Audit Compliance

LUCID maintains audit trails:

```bash
# Export all certification artifacts
kubectl get operatorupgradecertificates -o yaml > certs-audit.yaml
kubectl get validationruns -o yaml > validations-audit.yaml

# Include in compliance reports
```

---

## API Reference

### UpgradeSandbox

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.operatorRef` | string | Yes | Operator package name |
| `spec.targetVersion` | string | Yes | Semver version to validate |
| `spec.scope.namespaces` | []string | No | Namespaces to mirror |
| `spec.scope.labels` | map[string]string | No | Label selector for resources |
| `spec.scope.crdGroups` | []string | Yes | API groups to include |
| `spec.environmentConfig.isolationMode` | string | Yes | vcluster\|namespace\|in-process |
| `spec.environmentConfig.resourceLimits.cpu` | string | Yes | CPU limit |
| `spec.environmentConfig.resourceLimits.memory` | string | Yes | Memory limit |

### OperatorUpgradeCertificate

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.operatorRef` | string | Yes | Operator name |
| `spec.targetVersion` | string | Yes | Certified version |
| `spec.validityWindow` | string | Yes | Duration (e.g., "24h") |
| `status.summary` | string | Auto | Certified\|Rejected\|Stale\|Pending |
| `status.policyOutcome` | string | Auto | Allow\|Deny\|Conditional |
| `status.metrics.totalCRsAssessed` | int | Auto | CRs validated |
| `status.metrics.failures` | int | Auto | Validation failures |
| `status.metrics.coverage` | int | Auto | Coverage percentage |

### ValidationRun

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.sandboxRef` | string | Yes | Reference to UpgradeSandbox |
| `spec.procedure` | string | Yes | Validation type |
| `status.result` | string | Auto | Success\|Failure\|Running |
| `status.logs` | string | Auto | Execution logs |

---

## Support

### Getting Help

- **Documentation**: https://github.com/lucid-project/lucid/docs
- **Issues**: https://github.com/lucid-project/lucid/issues
- **Discussions**: https://github.com/lucid-project/lucid/discussions

### Reporting Bugs

Include:
1. LUCID version (`kubectl get deployment lucid-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}'`)
2. Kubernetes/OpenShift version
3. Sandbox YAML (redacted)
4. Relevant logs (`kubectl logs -n lucid-system --tail=100`)

### Feature Requests

Open a GitHub issue with:
- Use case description
- Expected behavior
- Current workaround (if any)
