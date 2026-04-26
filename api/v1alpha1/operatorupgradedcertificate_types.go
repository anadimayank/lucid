package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OperatorUpgradeCertificateSpec defines the desired state of OperatorUpgradeCertificate
type OperatorUpgradeCertificateSpec struct {
	// OperatorRef is the name of the operator being certified
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	OperatorRef string `json:"operatorRef"`

	// TargetVersion is the version being certified
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+)?$
	TargetVersion string `json:"targetVersion"`

	// ValidityWindow defines how long the certificate remains valid
	// +kubebuilder:validation:Required
	// +kubebuilder:default="24h"
	// +kubebuilder:validation:Pattern=^[0-9]+(h|m|s)$
	ValidityWindow string `json:"validityWindow"`
}

// OperatorUpgradeCertificateStatus defines the observed state of OperatorUpgradeCertificate
type OperatorUpgradeCertificateStatus struct {
	// Summary is the overall certification result
	// +kubebuilder:validation:Enum=Certified;CertifiedWithWarnings;Rejected;Stale;Pending
	Summary string `json:"summary"`

	// Metrics contains validation metrics
	Metrics CertificateMetrics `json:"metrics"`

	// Timestamp is when the certificate was issued
	Timestamp metav1.Time `json:"timestamp"`

	// PolicyOutcome describes the policy decision
	// +kubebuilder:validation:Enum=Allow;Deny;Conditional
	PolicyOutcome string `json:"policyOutcome,omitempty"`

	// EvidenceRef points to validation evidence artifacts
	EvidenceRef string `json:"evidenceRef,omitempty"`
}

// CertificateMetrics contains validation statistics
type CertificateMetrics struct {
	// TotalCRsAssessed is the number of CRs evaluated
	// +kubebuilder:validation:Minimum=0
	TotalCRsAssessed int `json:"totalCRsAssessed"`

	// Failures is the number of validation failures
	// +kubebuilder:validation:Minimum=0
	Failures int `json:"failures"`

	// Coverage is the percentage of CRs successfully validated
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Coverage int `json:"coverage"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=operatorupgradecertificates,shortName=ouc,scope=Namespaced

// OperatorUpgradeCertificate represents the outcome of certification for a specific operator version.
type OperatorUpgradeCertificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OperatorUpgradeCertificateSpec   `json:"spec"`
	Status OperatorUpgradeCertificateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OperatorUpgradeCertificateList contains a list of OperatorUpgradeCertificate
type OperatorUpgradeCertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OperatorUpgradeCertificate `json:"items"`
}
