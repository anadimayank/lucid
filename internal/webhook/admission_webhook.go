package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"github.com/lucid-project/lucid/internal/sandbox"
	"github.com/lucid-project/lucid/internal/snapshotter"
	"github.com/lucid-project/lucid/internal/validator"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type UpgradeGatingWebhook struct {
	Client          client.Client
	Decoder         admission.Decoder
	VClusterManager *sandbox.VClusterManager
	CRSnapshotter   *snapshotter.CRSnapshotter
	SchemaValidator *validator.SchemaValidator
}

// +kubebuilder:webhook:path=/validate-olm-subscription,mutating=false,failurePolicy=fail,sideEffects=None,groups=operators.coreos.com,resources=subscriptions,verbs=create;update,versions=v1alpha1,name=subscription-validator.lucid-project.io,admissionReviewVersions=v1

// Handle implements the admission handler for OLM Subscription/InstallPlan
func (w *UpgradeGatingWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Parse the admission request
	decoder := admission.NewDecoder(w.Client.Scheme())

	switch req.Kind.Kind {
	case "Subscription":
		return w.handleSubscription(ctx, req, decoder)
	case "InstallPlan":
		return w.handleInstallPlan(ctx, req, decoder)
	default:
		return admission.Denied(fmt.Sprintf("Unsupported resource kind: %s", req.Kind.Kind))
	}
}

// handleSubscription processes OLM Subscription requests
func (w *UpgradeGatingWebhook) handleSubscription(ctx context.Context, req admission.Request, decoder admission.Decoder) admission.Response {
	var subscription struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`
		Spec              struct {
			Package    string `json:"package"`
			Channel    string `json:"channel"`
			StartingCSV string `json:"startingCSV"`
		} `json:"spec"`
	}

	if err := decoder.DecodeRaw(req.Object.Raw, &subscription); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Extract operator and version from subscription
	operator := subscription.Spec.Package
	version := w.extractVersionFromCSV(subscription.Spec.StartingCSV)

	if operator == "" || version == "" {
		return admission.Denied("Unable to determine operator or version from subscription")
	}

	return w.validateUpgrade(ctx, operator, version, req.Namespace)
}

// handleInstallPlan processes OLM InstallPlan requests
func (w *UpgradeGatingWebhook) handleInstallPlan(ctx context.Context, req admission.Request, decoder admission.Decoder) admission.Response {
	var installPlan struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`
		Spec              struct {
			ClusterServiceVersionNames []string `json:"clusterServiceVersionNames"`
			Approval                    string   `json:"approval"`
		} `json:"spec"`
	}

	if err := decoder.DecodeRaw(req.Object.Raw, &installPlan); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Extract operator and version from install plan
	if len(installPlan.Spec.ClusterServiceVersionNames) == 0 {
		return admission.Denied("No ClusterServiceVersion specified in InstallPlan")
	}

	csvName := installPlan.Spec.ClusterServiceVersionNames[0]
	operator, version := w.parseCSVName(csvName)

	if operator == "" || version == "" {
		return admission.Denied(fmt.Sprintf("Unable to parse operator/version from CSV: %s", csvName))
	}

	return w.validateUpgrade(ctx, operator, version, req.Namespace)
}

// validateUpgrade checks if the upgrade is certified
func (w *UpgradeGatingWebhook) validateUpgrade(ctx context.Context, operator, version, namespace string) admission.Response {
	// Construct certificate name
	certName := fmt.Sprintf("%s-%s-cert", strings.ToLower(operator), strings.ReplaceAll(version, ".", "-"))

	// Search for a valid certificate
	var cert lucidv1alpha1.OperatorUpgradeCertificate
	err := w.Client.Get(ctx, types.NamespacedName{Name: certName, Namespace: namespace}, &cert)

	if err != nil {
		return admission.Denied(fmt.Sprintf("Upgrade blocked: No LUCID certification found for %s version %s. Please run validation first.", operator, version))
	}

	// Check certificate status
	switch cert.Status.Summary {
	case "Certified":
		// Check if certificate is still valid
		if w.isCertificateStale(&cert) {
			return admission.Denied(fmt.Sprintf("Upgrade blocked: Certification for %s version %s is stale. Re-validation required.", operator, version))
		}
		return admission.Allowed(fmt.Sprintf("LUCID certification verified for %s version %s. Proceeding with upgrade.", operator, version))

	case "CertifiedWithWarnings":
		// Allow but warn about potential issues
		warning := fmt.Sprintf("LUCID certification for %s version %s has warnings: %s", operator, version, cert.Status.PolicyOutcome)
		return admission.Allowed(warning).WithWarnings(warning)

	case "Rejected":
		return admission.Denied(fmt.Sprintf("Upgrade blocked: Operator %s version %s was REJECTED by LUCID validation. Reason: %s", operator, version, cert.Status.PolicyOutcome))

	case "Stale":
		return admission.Denied(fmt.Sprintf("Upgrade blocked: Certification for %s version %s is stale. Production state has changed. Re-validation required.", operator, version))

	case "Pending":
		return admission.Denied(fmt.Sprintf("Upgrade blocked: Validation for %s version %s is still in progress. Please wait.", operator, version))

	default:
		return admission.Denied(fmt.Sprintf("Upgrade blocked: Unknown certificate status: %s", cert.Status.Summary))
	}
}

// isCertificateStale checks if the certificate has expired
func (w *UpgradeGatingWebhook) isCertificateStale(cert *lucidv1alpha1.OperatorUpgradeCertificate) bool {
	// Check if certificate status is already marked as stale
	if cert.Status.Summary == "Stale" {
		return true
	}

	// Parse validity window from spec (format: "24h")
	validityStr := cert.Spec.ValidityWindow
	validity := 24 * time.Hour // default to 24 hours
	if d, err := time.ParseDuration(validityStr); err == nil {
		validity = d
	}

	// Check if certificate has expired based on timestamp and validity window
	issuedAt := cert.Status.Timestamp
	expiresAt := issuedAt.Add(validity)

	return time.Now().After(expiresAt)
}

// extractVersionFromCSV extracts version from a ClusterServiceVersion name.
// CSV names follow the format: operator-name.vX.Y.Z (e.g., "prometheus-operator.v2.45.0").
// Returns the version without the "v" prefix (e.g., "2.45.0").
func (w *UpgradeGatingWebhook) extractVersionFromCSV(csv string) string {
	if csv == "" {
		return ""
	}

	// Scan forward for ".v" followed by a digit — that marks the version boundary
	for i := 0; i < len(csv)-2; i++ {
		if csv[i] == '.' && csv[i+1] == 'v' && csv[i+2] >= '0' && csv[i+2] <= '9' {
			candidate := csv[i+2:] // strip ".v"
			if isVersionLike(candidate) {
				return candidate
			}
		}
	}

	return ""
}

// isVersionLike checks if a string looks like a semver version number (X.Y.Z)
func isVersionLike(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 {
			return false
		}
		for _, c := range part {
			if !((c >= '0' && c <= '9') || c == '-' || (c >= 'a' && c <= 'z')) {
				return false
			}
		}
	}
	return true
}

// parseCSVName parses operator and version from CSV name.
// Input: "prometheus-operator.v2.45.0" → ("prometheus-operator", "2.45.0")
func (w *UpgradeGatingWebhook) parseCSVName(csvName string) (string, string) {
	if csvName == "" {
		return "", ""
	}

	// Find ".v" followed by a digit — that boundary separates operator from version
	for i := 0; i < len(csvName)-2; i++ {
		if csvName[i] == '.' && csvName[i+1] == 'v' && csvName[i+2] >= '0' && csvName[i+2] <= '9' {
			operator := csvName[:i]
			version := csvName[i+2:]
			if operator != "" && isVersionLike(version) {
				return operator, version
			}
		}
	}

	return "", ""
}

// InjectDecoder injects the decoder into the webhook
func (w *UpgradeGatingWebhook) InjectDecoder(d admission.Decoder) error {
	w.Decoder = d
	return nil
}

// SetupWebhook registers the webhook with the manager
func (w *UpgradeGatingWebhook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&operatorsv1alpha1.Subscription{}).
		WithDefaulter(w).
		WithValidator(w).
		Complete(w)
}

// Default implements admission.Defaulter interface
func (w *UpgradeGatingWebhook) Default(ctx context.Context, obj runtime.Object) error {
	// No defaulting needed for this webhook
	return nil
}

// ValidateCreate implements admission.Validator interface
func (w *UpgradeGatingWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	switch v := obj.(type) {
	case *operatorsv1alpha1.Subscription:
		operator := v.Spec.Package
		version := w.extractVersionFromCSV(v.Spec.StartingCSV)
		if operator == "" || version == "" {
			return nil, fmt.Errorf("unable to determine operator or version from subscription")
		}
		resp := w.validateUpgrade(ctx, operator, version, v.Namespace)
		if !resp.Allowed {
			return nil, fmt.Errorf(resp.Result.Message)
		}
		return resp.Warnings, nil

	case *operatorsv1alpha1.InstallPlan:
		if len(v.Spec.ClusterServiceVersionNames) == 0 {
			return nil, fmt.Errorf("no ClusterServiceVersion specified in InstallPlan")
		}
		csvName := v.Spec.ClusterServiceVersionNames[0]
		operator, version := w.parseCSVName(csvName)
		if operator == "" || version == "" {
			return nil, fmt.Errorf("unable to parse operator/version from CSV: %s", csvName)
		}
		resp := w.validateUpgrade(ctx, operator, version, v.Namespace)
		if !resp.Allowed {
			return nil, fmt.Errorf(resp.Result.Message)
		}
		return resp.Warnings, nil
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator interface
func (w *UpgradeGatingWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	switch v := newObj.(type) {
	case *operatorsv1alpha1.Subscription:
		operator := v.Spec.Package
		version := w.extractVersionFromCSV(v.Spec.StartingCSV)
		if operator == "" || version == "" {
			return nil, fmt.Errorf("unable to determine operator or version from subscription")
		}
		resp := w.validateUpgrade(ctx, operator, version, v.Namespace)
		if !resp.Allowed {
			return nil, fmt.Errorf(resp.Result.Message)
		}
		return resp.Warnings, nil

	case *operatorsv1alpha1.InstallPlan:
		if len(v.Spec.ClusterServiceVersionNames) == 0 {
			return nil, fmt.Errorf("no ClusterServiceVersion specified in InstallPlan")
		}
		csvName := v.Spec.ClusterServiceVersionNames[0]
		operator, version := w.parseCSVName(csvName)
		if operator == "" || version == "" {
			return nil, fmt.Errorf("unable to parse operator/version from CSV: %s", csvName)
		}
		resp := w.validateUpgrade(ctx, operator, version, v.Namespace)
		if !resp.Allowed {
			return nil, fmt.Errorf(resp.Result.Message)
		}
		return resp.Warnings, nil
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator interface
func (w *UpgradeGatingWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
