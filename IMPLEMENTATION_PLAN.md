# Project LUCID: Implementation Specification
## Local Upgrade Certification and Isolation Domain

This document provides the technical specification for implementing the LUCID system as a shippable product for OpenShift clusters.

## 1. System Architecture Overview
LUCID is implemented as a Kubernetes-native "Certification Plane". It operates as a control loop that ensures operator upgrades are verified against live cluster state before execution.

### High-Level Component Diagram
`Production Cluster` $\rightarrow$ `LUCID Controller` $\rightarrow$ `LUCID Sandbox (vcluster)` $\rightarrow$ `Validation Engine` $\rightarrow$ `Certification Artifact` $\rightarrow$ `OLM Gate (Admission Webhook)`

---

## 2. Data Model (CRDs)

### 2.1 `UpgradeSandbox`
Represents the intent to validate a specific operator version.
```yaml
apiVersion: lucid.io/v1alpha1
kind: UpgradeSandbox
metadata:
  name: db-operator-v2-sandbox
spec:
  operatorRef: "database-operator"
  targetVersion: "2.0.0"
  scope:
    namespaces: ["prod-db-1", "prod-db-2"]
    labels: {"app": "database"}
    crdGroups: ["database.example.com"]
  environmentConfig:
    isolationMode: "vcluster" # options: vcluster, namespace, in-process
    resourceLimits:
      cpu: "500m"
      memory: "1Gi"
status:
  phase: "Running" # Creating, Running, Idle, TearingDown
  health: "Healthy"
  sandboxRef: "vcluster-db-v2-xyz"
```

### 2.2 `OperatorUpgradeCertificate`
The machine-verifiable artifact that gates the upgrade.
```yaml
apiVersion: lucid.io/v1alpha1
kind: OperatorUpgradeCertificate
metadata:
  name: db-operator-v2-cert
spec:
  operatorRef: "database-operator"
  targetVersion: "2.0.0"
  validityWindow: "1h"
status:
  summary: "Certified" # Certified, CertifiedWithWarnings, Rejected, Stale
  metrics:
    totalCRsAssessed: 142
    failures: 0
    coverage: "100%"
  timestamp: "2026-04-25T10:00:00Z"
  policyOutcome: "Pass"
  evidenceRef: "val-run-xyz"
```

### 2.3 `ValidationRun`
The execution record for auditing and debugging.
```yaml
apiVersion: lucid.io/v1alpha1
kind: ValidationRun
metadata:
  name: val-run-xyz
spec:
  sandboxRef: "vcluster-db-v2-xyz"
  procedure: "full-reconcile-simulation"
status:
  result: "Success"
  logs: "s3://lucid-evidence/logs/val-run-xyz.log"
  artifacts:
    - name: "schema-diff"
      path: "s3://lucid-evidence/artifacts/val-run-xyz/diff.json"
```

---

## 3. Core Component Specifications

### 3.1 The Sandbox Plane Controller (Go/KubeBuilder)
*   **Responsibility:** Orchestrate the lifecycle of `UpgradeSandbox` resources.
*   **Logic Flow:** 
    1. Watch for new `UpgradeSandbox` requests.
    2. Call `vcluster` API to instantiate a virtual cluster.
    3. Trigger the **CR Snapshotter** to populate the sandbox.
    4. Deploy the candidate Operator image into the sandbox.
    5. Trigger the **Validation Engine**.
    6. Update the `OperatorUpgradeCertificate`.

### 3.2 The CR Snapshotter (Mirroring Engine)
*   **Logic:**
    *   Identify all objects in production matching the `scope`.
    *   Perform a "Deep Copy" of objects.
    *   **Translation Layer:** Rewrite `namespace` and `secret` references to match the sandbox's internal mapping.
    *   Inject objects into the sandbox API server.

### 3.3 The Validation Engine
*   **Schema Check:** Compare current production CRs against the target version's CRD using `kube-val`.
*   **Simulation:** Start the operator in the sandbox; monitor for `Panic` or `CrashLoopBackOff` for 5 minutes.
*   **Reconciliation Test:** Check if the operator's `status` fields on mirrored CRs reach the expected "Ready" state.

### 3.4 The Orchestrator Integration Adapter (Admission Webhook)
*   **Hook Point:** `MutatingAdmissionWebhook` on `Subscription` and `InstallPlan` resources.
*   **Logic:**
    ```go
    if certificate := getCertificate(request.Operator, request.Version); 
       certificate.Status != "Certified" || certificate.IsStale(); {
        return denyRequest("Upgrade blocked: No valid LUCID certificate found.");
    }
    return allowRequest();
    ```

---

## 4. OpenShift Deployment Strategy

### 4.1 Infrastructure Stack
*   **Deployment:** OpenShift Operator (deployed via OLM).
*   **Isolation:** `vcluster` running as pods within the LUCID namespace.
*   **Evidence Storage:** OpenShift Data Storage (ODS) or S3-compatible store for logs/artifacts.

### 4.2 CI/CD Pipeline (Tekton + GitOps)
1.  **Build:** Tekton Pipeline $\rightarrow$ Go Build $\rightarrow$ Container Image $\rightarrow$ Internal Registry.
2.  **Deploy:** ArgoCD $\rightarrow$ Applies `LUCID-Operator` manifests $\rightarrow$ Deploys to Cluster.
3.  **Verify:** Automated smoke tests verify the Admission Webhook blocks an intentionally broken operator version.

---

## 5. Implementation Roadmap (T-Shirt Sizing)

| Feature | Complexity | Priority | Milestone |
| :--- | :--- | :--- | :--- |
| CRD Definitions & Controller Skeleton | S | P0 | M1: Framework |
| Basic CR Snapshotter (Namespace Copy) | M | P0 | M1: Framework |
| Schema Validation Engine | S | P1 | M2: MVP |
| vcluster Integration | L | P1 | M2: MVP |
| Continuous Revalidation (Watch Loop) | M | P2 | M3: Beta |
| OLM Admission Webhook | M | P1 | M3: Beta |
| UI Dashboard for Certificates | L | P3 | M4: GA |
