package validator

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSchemaValidator_Validate(t *testing.T) {
	ctx := context.Background()
	v := NewSchemaValidator()

	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	// Define a simple JSON schema: requires "replicas" as an integer
	schemaJSON := `{
		"type": "object",
		"properties": {
			"replicas": { "type": "integer" }
		},
		"required": ["replicas"]
	}`

	v.crdSchemas[gvk] = &jsonschema{
		Schema: json.RawMessage(schemaJSON),
	}

	tests := []struct {
		name     string
		objects  []unstructured.Unstructured
		wantTotal int
		wantFail  int
	}{
		{
			name: "Valid object",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "test.example.com/v1",
						"kind":       "TestResource",
						"metadata": map[string]interface{}{
							"name": "valid-res",
						},
						"spec": map[string]interface{}{
							"replicas": 3,
						},
					},
				},
			},
			wantTotal: 1,
			wantFail:  0,
		},
		{
			name: "Invalid object - missing replicas",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "test.example.com/v1",
						"kind":       "TestResource",
						"metadata": map[string]interface{}{
							"name": "invalid-res-1",
						},
						"spec": map[string]interface{}{
							"somethingElse": "value",
						},
					},
				},
			},
			wantTotal: 1,
			wantFail:  1,
		},
		{
			name: "Invalid object - wrong type for replicas",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "test.example.com/v1",
						"kind":       "TestResource",
						"metadata": map[string]interface{}{
							"name": "invalid-res-2",
						},
						"spec": map[string]interface{}{
							"replicas": "three",
						},
					},
				},
			},
			wantTotal: 1,
			wantFail:  1,
		},
		{
			name: "Object without spec",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "test.example.com/v1",
						"kind":       "TestResource",
						"metadata": map[string]interface{}{
							"name": "no-spec",
						},
					},
				},
			},
			wantTotal: 1,
			wantFail:  0, // Based on current implementation, no spec means success
		},
		{
			name: "Object without name",
			objects: []unstructured.Unstructured{
				{
					Object: map[string]interface{}{
						"apiVersion": "test.example.com/v1",
						"kind":       "TestResource",
						"spec": map[string]interface{}{
							"replicas": 3,
						},
					},
				},
			},
			wantTotal: 1,
			wantFail:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, failures, err := v.Validate(ctx, "v1", tt.objects)
			if err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
			if total != tt.wantTotal {
				t.Errorf("Validate() total = %v, want %v", total, tt.wantTotal)
			}
			if failures != tt.wantFail {
				t.Errorf("Validate() failures = %v, want %v", failures, tt.wantFail)
			}
		})
	}
}
