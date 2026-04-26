package controller

import (
	"context"
	"fmt"
	"time"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"github.com/lucid-project/lucid/internal/sandbox"
	"github.com/lucid-project/lucid/internal/snapshotter"
	"github.com/lucid-project/lucid/internal/validator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	sandboxFinalizerName = "lucid.lucid-project.io/sandbox-cleanup"
)

// UpgradeSandboxReconciler reconciles UpgradeSandbox objects
type UpgradeSandboxReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	VClusterManager *sandbox.VClusterManager
	CRSnapshotter   *snapshotter.CRSnapshotter
	SchemaValidator *validator.SchemaValidator
}

// +kubebuilder:rbac:groups=lucid.lucid-project.io,resources=upgradesandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lucid.lucid-project.io,resources=upgradesandboxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lucid.lucid-project.io,resources=upgradesandboxes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *UpgradeSandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	// 1. Fetch the UpgradeSandbox instance
	var sandbox lucidv1alpha1.UpgradeSandbox
	if err := r.Get(ctx, req.NamespacedName, &sandbox); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	l.Info("Reconciling UpgradeSandbox", "name", sandbox.Name, "phase", sandbox.Status.Phase)

	// Handle deletion with finalizer
	if sandbox.ObjectMeta.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, &sandbox)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&sandbox, sandboxFinalizerName) {
		controllerutil.AddFinalizer(&sandbox, sandboxFinalizerName)
		if err := r.Update(ctx, &sandbox); err != nil {
			l.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// 2. Handle Lifecycle State Machine
	switch sandbox.Status.Phase {
	case "":
		// Initial state: Transition to Creating
		sandbox.Status.Phase = "Creating"
		if err := r.Status().Update(ctx, &sandbox); err != nil {
			l.Error(err, "failed to update status to Creating")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil

	case "Creating":
		l.Info("Creating sandbox environment...")
		vclusterName, err := r.VClusterManager.CreateVirtualCluster(ctx, &sandbox)
		if err != nil {
			l.Error(err, "failed to create virtual cluster")
			sandbox.Status.Phase = "Failed"
			sandbox.Status.Health = "Unhealthy"
			if updateErr := r.Status().Update(ctx, &sandbox); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		// Deploy operator into the vcluster
		if err := r.VClusterManager.DeployOperator(ctx, vclusterName, sandbox.Spec.OperatorRef, sandbox.Spec.TargetVersion); err != nil {
			l.Error(err, "failed to deploy operator to virtual cluster")
			sandbox.Status.Phase = "Failed"
			sandbox.Status.Health = "Unhealthy"
			if updateErr := r.Status().Update(ctx, &sandbox); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		sandbox.Status.Phase = "Running"
		sandbox.Status.Health = "Healthy"
		sandbox.Status.SandboxRef = vclusterName
		if err := r.Status().Update(ctx, &sandbox); err != nil {
			l.Error(err, "failed to update status to Running")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case "Running":
		// Perform validation if not already done
		if sandbox.Status.ValidationCompleted != true {
			return r.performValidation(ctx, &sandbox)
		}
		l.Info("Sandbox is active and validation completed. Waiting for certificate creation...")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

	case "Failed":
		l.Info("Sandbox in failed state. Attempting recovery...")
		// Attempt recovery by recreating resources
		if sandbox.Status.SandboxRef != "" {
			_ = r.VClusterManager.DeleteVirtualCluster(ctx, sandbox.Status.SandboxRef, fmt.Sprintf("lucid-sandbox-%s", sandbox.Name))
		}
		// Reset to Creating for retry
		sandbox.Status.Phase = "Creating"
		if err := r.Status().Update(ctx, &sandbox); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// performValidation executes the validation flow and creates a certificate
func (r *UpgradeSandboxReconciler) performValidation(ctx context.Context, sandbox *lucidv1alpha1.UpgradeSandbox) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	l.Info("Starting validation procedure", "operator", sandbox.Spec.OperatorRef, "version", sandbox.Spec.TargetVersion)

	// Step 1: Snapshot production CRs
	objects, err := r.CRSnapshotter.Snapshot(ctx, sandbox)
	if err != nil {
		l.Error(err, "failed to snapshot CRs")
		sandbox.Status.Phase = "Failed"
		sandbox.Status.Health = "Unhealthy"
		if updateErr := r.Status().Update(ctx, sandbox); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	// Step 2: Apply snapshot to sandbox
	if err := r.CRSnapshotter.ApplySnapshot(ctx, objects); err != nil {
		l.Error(err, "failed to apply snapshot to sandbox")
		sandbox.Status.Phase = "Failed"
		sandbox.Status.Health = "Unhealthy"
		if updateErr := r.Status().Update(ctx, sandbox); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{}, err
	}

	// Step 3: Run schema validation
	total, failures, err := r.SchemaValidator.Validate(ctx, sandbox.Spec.TargetVersion, objects)
	if err != nil {
		l.Error(err, "validation failed")
		return ctrl.Result{}, err
	}

	// Step 4: Create OperatorUpgradeCertificate
	summary := "Certified"
	policyOutcome := "Allow"
	if failures > 0 {
		summary = "Rejected"
		policyOutcome = "Deny"
	}

	cert := &lucidv1alpha1.OperatorUpgradeCertificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-cert", sandbox.Spec.OperatorRef, sandbox.Status.SandboxRef),
			Namespace: sandbox.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: lucidv1alpha1.GroupVersion.String(),
					Kind:       "UpgradeSandbox",
					Name:       sandbox.Name,
					UID:        sandbox.UID,
					Controller: &[]bool{true}[0],
				},
			},
		},
		Spec: lucidv1alpha1.OperatorUpgradeCertificateSpec{
			OperatorRef:    sandbox.Spec.OperatorRef,
			TargetVersion:  sandbox.Spec.TargetVersion,
			ValidityWindow: "24h",
		},
		Status: lucidv1alpha1.OperatorUpgradeCertificateStatus{
			Summary: summary,
			Timestamp: metav1.Now(),
			PolicyOutcome: policyOutcome,
			Metrics: lucidv1alpha1.CertificateMetrics{
				TotalCRsAssessed: total,
				Failures:         failures,
				Coverage:         100 - (failures * 100 / total),
			},
		},
	}

	if err := r.Create(ctx, cert); err != nil {
		l.Error(err, "failed to create certificate")
		return ctrl.Result{}, err
	}

	// Update sandbox status
	sandbox.Status.ValidationCompleted = true
	sandbox.Status.CertificateRef = cert.Name
	if err := r.Status().Update(ctx, sandbox); err != nil {
		l.Error(err, "failed to update sandbox status")
		return ctrl.Result{}, err
	}

	l.Info("Validation completed successfully", "certificate", cert.Name, "failures", failures)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *UpgradeSandboxReconciler) handleDeletion(ctx context.Context, sandbox *lucidv1alpha1.UpgradeSandbox) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(sandbox, sandboxFinalizerName) {
		l.Info("Cleaning up sandbox resources before deletion", "name", sandbox.Name)

		// Clean up vcluster and namespace
		if sandbox.Status.SandboxRef != "" {
			if err := r.VClusterManager.DeleteVirtualCluster(ctx, sandbox.Status.SandboxRef, fmt.Sprintf("lucid-sandbox-%s", sandbox.Name)); err != nil {
				l.Error(err, "failed to delete virtual cluster during cleanup")
				return ctrl.Result{}, err
			}
		}

		// Remove the finalizer
		controllerutil.RemoveFinalizer(sandbox, sandboxFinalizerName)
		if err := r.Update(ctx, sandbox); err != nil {
			l.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
		l.Info("Sandbox cleanup completed", "name", sandbox.Name)
	}

	return ctrl.Result{}, nil
}

func (r *UpgradeSandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lucidv1alpha1.UpgradeSandbox{}).
		Complete(r)
}

// NewUpgradeSandboxReconciler creates a new UpgradeSandboxReconciler
func NewUpgradeSandboxReconciler(mgr ctrl.Manager, vclusterManager *sandbox.VClusterManager, crSnapshotter *snapshotter.CRSnapshotter, schemaValidator *validator.SchemaValidator) *UpgradeSandboxReconciler {
	return &UpgradeSandboxReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		VClusterManager: vclusterManager,
		CRSnapshotter:   crSnapshotter,
		SchemaValidator: schemaValidator,
	}
}
