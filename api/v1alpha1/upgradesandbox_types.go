package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpgradeSandboxSpec defines the desired state of UpgradeSandbox
type UpgradeSandboxSpec struct {
	// OperatorRef is the name of the operator being upgraded
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	OperatorRef string `json:"operatorRef"`

	// TargetVersion is the version to be validated
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+)?$
	TargetVersion string `json:"targetVersion"`

	// Scope defines which resources to mirror into the sandbox
	// +kubebuilder:validation:Required
	Scope SandboxScope `json:"scope"`

	// EnvironmentConfig provides parameters for the isolation layer
	// +kubebuilder:validation:Required
	EnvironmentConfig EnvironmentConfig `json:"environmentConfig"`
}

// SandboxScope defines the scope of resources to mirror
type SandboxScope struct {
	// Namespaces specifies which namespaces to include in the snapshot
	// +kubebuilder:validation:MinItems=1
	Namespaces []string `json:"namespaces,omitempty"`

	// Labels filters resources by labels
	Labels map[string]string `json:"labels,omitempty"`

	// CrdGroups specifies which CRD groups to include
	// +kubebuilder:validation:MinItems=1
	CrdGroups []string `json:"crdGroups,omitempty"`
}

// EnvironmentConfig defines the isolation environment configuration
type EnvironmentConfig struct {
	// IsolationMode specifies the isolation strategy
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=vcluster;namespace;in-process
	// +kubebuilder:default=vcluster
	IsolationMode string `json:"isolationMode"`

	// ResourceLimits defines resource constraints for the sandbox
	// +kubebuilder:validation:Required
	ResourceLimits ResourceLimits `json:"resourceLimits"`
}

// ResourceLimits defines CPU and memory limits
type ResourceLimits struct {
	// CPU limit (e.g., "500m", "2")
	// +kubebuilder:validation:Required
	// +kubebuilder:default="1"
	CPU string `json:"cpu"`

	// Memory limit (e.g., "512Mi", "2Gi")
	// +kubebuilder:validation:Required
	// +kubebuilder:default="1Gi"
	Memory string `json:"memory"`
}

// UpgradeSandboxStatus defines the observed state of UpgradeSandbox
type UpgradeSandboxStatus struct {
	// Phase represents the current lifecycle phase
	// +kubebuilder:validation:Enum=;Creating;Running;Idle;TearingDown;Failed;Deleted
	Phase string `json:"phase"`

	// Health indicates the health status of the sandbox
	// +kubebuilder:validation:Enum=Healthy;Unhealthy;Unknown
	Health string `json:"health"`

	// SandboxRef is a reference to the actual sandbox resource
	SandboxRef string `json:"sandboxRef,omitempty"`

	// ValidationCompleted indicates if validation has completed
	ValidationCompleted bool `json:"validationCompleted,omitempty"`

	// CertificateRef is a reference to the created certificate
	CertificateRef string `json:"certificateRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=upgradesandboxes,shortName=us,scope=Namespaced

// UpgradeSandbox is a resource that manages an isolated operator upgrade sandbox.
type UpgradeSandbox struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UpgradeSandboxSpec   `json:"spec"`
	Status UpgradeSandboxStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UpgradeSandboxList contains a list of UpgradeSandbox
type UpgradeSandboxList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UpgradeSandbox `json:"items"`
}
