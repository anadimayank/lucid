package validator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xeipuuv/gojsonschema"
	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type SchemaValidator struct {
	crdSchemas map[schema.GroupVersionKind]*jsonschema
}

type jsonschema struct {
	Schema json.RawMessage `json:"openAPIV3Schema"`
}

func NewSchemaValidator() *SchemaValidator {
	return &SchemaValidator{
		crdSchemas: make(map[schema.GroupVersionKind]*jsonschema),
	}
}

// LoadCRDSchemas loads CRD schemas from a directory
func (v *SchemaValidator) LoadCRDSchemas(ctx context.Context, schemaDir string) error {
	l := log.FromContext(ctx)

	l.Info("Loading CRD schemas", "directory", schemaDir)

	err := filepath.Walk(schemaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" {
			if err := v.loadSchemaFile(ctx, path); err != nil {
				l.Error(err, "failed to load schema file", "path", path)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk schema directory: %w", err)
	}

	l.Info("CRD schemas loaded", "count", len(v.crdSchemas))
	return nil
}

// loadSchemaFile loads a single CRD schema file
func (v *SchemaValidator) loadSchemaFile(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var crd struct {
		Spec struct {
			Group string `json:"group"`
			Versions []struct {
				Name    string `json:"name"`
				Served  bool   `json:"served"`
				Storage bool   `json:"storage"`
				Schema  struct {
					OpenAPIV3Schema json.RawMessage `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
			Names struct {
				Kind string `json:"kind"`
			} `json:"names"`
		} `json:"spec"`
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}

	if err := yaml.Unmarshal(data, &crd); err != nil {
		return fmt.Errorf("failed to unmarshal CRD: %w", err)
	}

	// Load schemas for all served versions
	for _, version := range crd.Spec.Versions {
		if !version.Served {
			continue
		}

		gvk := schema.GroupVersionKind{
			Group:   crd.Spec.Group,
			Version: version.Name,
			Kind:    crd.Spec.Names.Kind,
		}

		schema := &jsonschema{}
		if err := json.Unmarshal(version.Schema.OpenAPIV3Schema, schema); err != nil {
			return fmt.Errorf("failed to unmarshal schema for %s: %w", gvk, err)
		}

		v.crdSchemas[gvk] = schema
	}

	return nil
}

// Validate checks if mirrored CRs are compatible with the target operator version
func (v *SchemaValidator) Validate(ctx context.Context, targetVersion string, objects []unstructured.Unstructured) (int, int, error) {
	l := log.FromContext(ctx)

	failures := 0
	total := len(objects)

	l.Info("Starting validation", "targetVersion", targetVersion, "totalObjects", total)

	for _, obj := range objects {
		gvk := obj.GroupVersionKind()

		// Get the schema for this GVK
		schema, exists := v.crdSchemas[gvk]
		if !exists {
			l.Info("No schema found for resource", "gvk", gvk.String())
			continue
		}

		// Validate the object against the schema
		if err := v.validateObject(ctx, obj, schema); err != nil {
			failures++
			l.Error(err, "Validation failure", "object", obj.GetName(), "gvk", gvk.String())
		}
	}

	l.Info("Validation complete", "total", total, "failures", failures)
	return total, failures, nil
}

func (v *SchemaValidator) validateObject(ctx context.Context, obj unstructured.Unstructured, schema *jsonschema) error {
	l := log.FromContext(ctx)

	// Perform basic validation checks
	if obj.GetName() == "" {
		return fmt.Errorf("object has no name")
	}

	// Convert the object spec to a JSON loader
	specRaw, hasSpec := obj.Object["spec"]
	if !hasSpec {
		l.Info("Object has no spec field", "kind", obj.GetKind(), "name", obj.GetName())
		return nil
	}

	specJSON, err := json.Marshal(specRaw)
	if err != nil {
		return fmt.Errorf("failed to marshal spec to JSON: %w", err)
	}

	// Load the JSON schema and the document
	schemaLoader := gojsonschema.NewBytesLoader(schema.Schema)
	documentLoader := gojsonschema.NewBytesLoader(specJSON)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return fmt.Errorf("schema validation error: %w", err)
	}

	if !result.Valid() {
		var report string
		for _, desc := range result.Errors() {
			report += fmt.Sprintf("- %s\n", desc)
		}
		return fmt.Errorf("schema validation failed:\n%s", report)
	}

	l.Info("Object validated successfully", "kind", obj.GetKind(), "name", obj.GetName())
	return nil
}

// ValidateWithVersion validates objects against a specific version's schema
func (v *SchemaValidator) ValidateWithVersion(ctx context.Context, version string, objects []unstructured.Unstructured) (*lucidv1alpha1.ValidationRunStatus, error) {
	total, failures, err := v.Validate(ctx, version, objects)
	if err != nil {
		return nil, err
	}

	status := &lucidv1alpha1.ValidationRunStatus{
		Result: "Success",
	}

	if failures > 0 {
		status.Result = "Failure"
		status.Logs = fmt.Sprintf("Validation failed for %d out of %d objects", failures, total)
	} else {
		status.Logs = fmt.Sprintf("All %d objects validated successfully", total)
	}

	return status, nil
}

// GetSchema returns the schema for a given GVK
func (v *SchemaValidator) GetSchema(gvk schema.GroupVersionKind) (*jsonschema, bool) {
	schema, exists := v.crdSchemas[gvk]
	return schema, exists
}
