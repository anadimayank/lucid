package snapshotter

import (
	"context"
	"testing"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCRSnapshotter_CloneForSandbox(t *testing.T) {
	// Initialize fake client and snapshotter
	scheme := runtime.NewScheme()
	// Register types if necessary, though unstructured doesn't strictly require it for basic tests
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	s := NewCRSnapshotter(c)

	orig := unstructured.Unstructured{}
	orig.SetGroupVersionKind(unstructured.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	})
	orig.SetName("test-res")
	orig.SetNamespace("prod-ns")
	orig.SetLabels(map[string]string{"env": "prod"})
	orig.SetAnnotations(map[string]string{"managed-by": "operator"})
	orig.SetResourceVersion("12345")
	orig.SetUID("abc-123")
	orig.Object["status"] = map[string]interface{}{"phase": "Running"}

	sandboxName := "sandbox-1"
	sandboxNS := "lucid-sandbox-sandbox-1"

	cloned := s.cloneForSandbox(orig, sandboxNS, sandboxName)

	if cloned.GetNamespace() != sandboxNS {
		t.Errorf("expected namespace %s, got %s", sandboxNS, cloned.GetNamespace())
	}

	if cloned.GetLabels()["lucid.lucid-project.io/sandbox"] != sandboxName {
		t.Errorf("expected sandbox label %s, got %s", sandboxName, cloned.GetLabels()["lucid.lucid-project.io/sandbox"])
	}

	if cloned.GetAnnotations()["lucid.lucid-project.io/source-namespace"] != "prod-ns" {
		t.Errorf("expected source-namespace annotation prod-ns, got %s", cloned.GetAnnotations()["lucid.lucid-project.io/source-namespace"])
	}

	if _, exists := cloned.Object["status"]; exists {
		t.Errorf("expected status field to be removed")
	}

	if cloned.GetResourceVersion() != "" {
		t.Errorf("expected resource version to be cleared, got %s", cloned.GetResourceVersion())
	}

	if cloned.GetUID() != "" {
		t.Errorf("expected UID to be cleared, got %s", cloned.GetUID())
	}
}

func TestCRSnapshotter_ApplySnapshot(t *testing.T) {
	scheme := runtime.NewScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	s := NewCRSnapshotter(c)

	objs := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"apiVersion": "test.example.com/v1",
				"kind":       "TestResource",
				"metadata": map[string]interface{}{
					"name":      "res-1",
					"namespace": "sandbox-ns",
				},
			},
		},
		{
			Object: map[string]interface{}{
				"apiVersion": "test.example.com/v1",
				"kind":       "TestResource",
				"metadata": map[string]interface{}{
					"name":      "res-2",
					"namespace": "sandbox-ns",
				},
			},
		},
	}

	err := s.ApplySnapshot(context.Background(), objs)
	if err != nil {
		t.Fatalf("ApplySnapshot failed: %v", err)
	}

	// Verify objects were created
	for _, obj := range objs {
		found := &unstructured.Unstructured{}
		found.SetGroupVersionKind(obj.GroupVersionKind())
		err := c.Get(context.Background(), client.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}, found)
		if err != nil {
			t.Errorf("failed to find created object %s: %v", obj.GetName(), err)
		}
	}
}
