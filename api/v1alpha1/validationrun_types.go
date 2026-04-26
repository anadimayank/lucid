package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ValidationRunSpec defines the desired state of ValidationRun
type ValidationRunSpec struct {
	// SandboxRef is a reference to the sandbox being validated
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SandboxRef string `json:"sandboxRef"`

	// Procedure specifies the validation procedure to execute
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=full-validation;schema-validation;behavioral-validation;quick-check
	Procedure string `json:"procedure"`
}

// ValidationRunStatus defines the observed state of ValidationRun
type ValidationRunStatus struct {
	// Result is the validation outcome
	// +kubebuilder:validation:Enum=Success;Failure;Running;Pending
	Result string `json:"result"`

	// Logs contains validation execution logs
	Logs string `json:"logs,omitempty"`

	// Artifacts lists generated validation artifacts
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Artifact represents a validation artifact
type Artifact struct {
	// Name is the artifact name
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Path is the artifact storage location
	// +kubebuilder:validation:Required
	Path string `json:"path"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=validationruns,shortName=vr,scope=Namespaced

// ValidationRun captures the execution details of a certification procedure.
type ValidationRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ValidationRunSpec   `json:"spec"`
	Status ValidationRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ValidationRunList contains a list of ValidationRun
type ValidationRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ValidationRun `json:"items"`
}
