package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CertificateController manages the validity and freshness of OperatorUpgradeCertificates
type CertificateController struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=lucid.lucid-project.io,resources=operatorupgradecertificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lucid.lucid-project.io,resources=operatorupgradecertificates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lucid.lucid-project.io,resources=validationruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *CertificateController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	var cert lucidv1alpha1.OperatorUpgradeCertificate
	if err := r.Get(ctx, req.NamespacedName, &cert); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Continuous Revalidation Logic:
	// In a real implementation, this controller would watch a list of mirrored CRs.
	// If any production CR is updated, the certificate is marked as stale.

	isChanged := r.checkProductionStateChanged(ctx, &cert)
	if isChanged {
		l.Info("Production state change detected. Invalidating certificate", "cert", cert.Name)
		cert.Status.Summary = "Stale"
		if err := r.Status().Update(ctx, &cert); err != nil {
			l.Error(err, "failed to update certificate status to Stale")
			return ctrl.Result{}, err
		}
		// Trigger a new ValidationRun
		return r.triggerRevalidation(ctx, &cert)
	}

	return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
}

func (r *CertificateController) triggerRevalidation(ctx context.Context, cert *lucidv1alpha1.OperatorUpgradeCertificate) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	// Create a new ValidationRun to revalidate the certificate
	validationRun := &lucidv1alpha1.ValidationRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("revalidation-%s-%d", cert.Name, time.Now().Unix()),
			Namespace: cert.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: lucidv1alpha1.GroupVersion.String(),
					Kind:       "OperatorUpgradeCertificate",
					Name:       cert.Name,
					UID:        cert.UID,
					Controller: &[]bool{true}[0],
				},
			},
		},
		Spec: lucidv1alpha1.ValidationRunSpec{
			SandboxRef: fmt.Sprintf("sandbox-%s", cert.Spec.OperatorRef),
			Procedure:  "full-validation",
		},
	}

	if err := r.Create(ctx, validationRun); err != nil {
		l.Error(err, "failed to create ValidationRun")
		return ctrl.Result{}, err
	}

	l.Info("Created ValidationRun for revalidation", "name", validationRun.Name)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *CertificateController) checkProductionStateChanged(ctx context.Context, cert *lucidv1alpha1.OperatorUpgradeCertificate) bool {
	l := log.FromContext(ctx)

	operator := cert.Spec.OperatorRef
	configMapName := fmt.Sprintf("lucid-state-%s", operator)
	namespace := cert.Namespace

	// List all CRs related to this operator by label
	var sandboxList lucidv1alpha1.UpgradeSandboxList
	if err := r.List(ctx, &sandboxList, client.InNamespace(namespace), client.MatchingLabels{
		"lucid.lucid-project.io/operator": operator,
	}); err != nil {
		l.Error(err, "failed to list sandboxes for state check")
		return false
	}

	// Compute a hash of the current production state
	currentHash := r.computeStateHash(sandboxList.Items)

	// Retrieve the last-known hash from a ConfigMap
	var cm corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, &cm)
	if errors.IsNotFound(err) {
		// First time — store the current hash
		cm = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: namespace,
			},
			Data: map[string]string{
				"stateHash": currentHash,
			},
		}
		if createErr := r.Create(ctx, &cm); createErr != nil {
			l.Error(createErr, "failed to create state ConfigMap")
		}
		return false
	} else if err != nil {
		l.Error(err, "failed to get state ConfigMap")
		return false
	}

	previousHash := cm.Data["stateHash"]
	if currentHash != previousHash {
		l.Info("Production state change detected", "operator", operator, "previousHash", previousHash, "currentHash", currentHash)
		cm.Data["stateHash"] = currentHash
		if updateErr := r.Update(ctx, &cm); updateErr != nil {
			l.Error(updateErr, "failed to update state ConfigMap")
		}
		return true
	}

	return false
}

func (r *CertificateController) computeStateHash(items []lucidv1alpha1.UpgradeSandbox) string {
	var names []string
	for _, item := range items {
		names = append(names, fmt.Sprintf("%s/%s@%s", item.Namespace, item.Name, item.ResourceVersion))
	}
	sort.Strings(names)
	data, _ := json.Marshal(names)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8])
}

func (r *CertificateController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lucidv1alpha1.OperatorUpgradeCertificate{}).
		Complete(r)
}

// NewCertificateController creates a new CertificateController
func NewCertificateController(mgr ctrl.Manager) *CertificateController {
	return &CertificateController{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
}
