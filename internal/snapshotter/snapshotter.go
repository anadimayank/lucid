package snapshotter

import (
	"context"
	"fmt"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CRSnapshotter struct {
	client client.Client
	mapper meta.RESTMapper
}

func NewCRSnapshotter(c client.Client) *CRSnapshotter {
	return &CRSnapshotter{client: c}
}

// Snapshot captures relevant CRs from production and clones them for the sandbox
func (s *CRSnapshotter) Snapshot(ctx context.Context, sandbox *lucidv1alpha1.UpgradeSandbox) ([]unstructured.Unstructured, error) {
	l := log.FromContext(ctx)
	var mirroredObjects []unstructured.Unstructured

	sandboxNamespace := fmt.Sprintf("lucid-sandbox-%s", sandbox.Name)

	for _, group := range sandbox.Spec.Scope.CrdGroups {
		l.Info("Snapshotting CRD group", "group", group)

		// Get all resource kinds for this group
		gvrList, err := s.discoverResourcesForGroup(ctx, group)
		if err != nil {
			return nil, fmt.Errorf("failed to discover resources for group %s: %w", group, err)
		}

		for _, gvr := range gvrList {
			l.Info("Snapshotting resource", "gvr", gvr.String())

			// List resources with label selector
			listOpts := []client.ListOption{}
			if len(sandbox.Spec.Scope.Labels) > 0 {
				listOpts = append(listOpts, client.MatchingLabels(sandbox.Spec.Scope.Labels))
			}

			// Filter by namespaces if specified
			if len(sandbox.Spec.Scope.Namespaces) > 0 {
				for _, ns := range sandbox.Spec.Scope.Namespaces {
					nsListOpts := append([]client.ListOption(nil), listOpts...)
					nsListOpts = append(nsListOpts, client.InNamespace(ns))
					objects, err := s.listResources(ctx, gvr, nsListOpts...)
					if err != nil {
						l.Error(err, "failed to list resources", "gvr", gvr.String(), "namespace", ns)
						continue
					}

					for _, obj := range objects {
						cloned := s.cloneForSandbox(obj, sandboxNamespace, sandbox.Name)
						mirroredObjects = append(mirroredObjects, cloned)
					}
				}
			} else {
				// List across all namespaces
				objects, err := s.listResources(ctx, gvr, listOpts...)
				if err != nil {
					l.Error(err, "failed to list resources", "gvr", gvr.String())
					continue
				}

				for _, obj := range objects {
					cloned := s.cloneForSandbox(obj, sandboxNamespace, sandbox.Name)
					mirroredObjects = append(mirroredObjects, cloned)
				}
			}
		}
	}

	l.Info("Snapshot complete", "totalObjects", len(mirroredObjects))
	return mirroredObjects, nil
}

// discoverResourcesForGroup discovers all resources for a given API group
func (s *CRSnapshotter) discoverResourcesForGroup(ctx context.Context, group string) ([]schema.GroupVersionResource, error) {
	var gvrs []schema.GroupVersionResource

	// Get all resource types from the discovery client
	resourceList, err := s.client.Discovery().ServerPreferredResources()
	if err != nil {
		return nil, err
	}

	for _, rl := range resourceList {
		gv, err := schema.ParseGroupVersion(rl.GroupVersion)
		if err != nil {
			continue
		}

		if gv.Group == group {
			for _, resource := range rl.APIResources {
				if !resource.Namespaced {
					continue
				}
				gvrs = append(gvrs, gv.WithResource(resource.Name))
			}
		}
	}

	return gvrs, nil
}

// listResources lists resources for a given GVR
func (s *CRSnapshotter) listResources(ctx context.Context, gvr schema.GroupVersionResource, opts ...client.ListOption) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvr.GroupVersion().WithKind("List"))

	if err := s.client.List(ctx, list, opts...); err != nil {
		return nil, err
	}

	return list.Items, nil
}

// cloneForSandbox creates a sandbox-ready copy of a resource
func (s *CRSnapshotter) cloneForSandbox(obj unstructured.Unstructured, sandboxNamespace, sandboxName string) unstructured.Unstructured {
	cloned := obj.DeepCopy()

	// Translate namespace to sandbox-specific namespace
	cloned.SetNamespace(sandboxNamespace)

	// Add sandbox label and annotation for tracking
	labels := cloned.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["lucid.lucid-project.io/sandbox"] = sandboxName
	cloned.SetLabels(labels)

	annotations := cloned.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["lucid.lucid-project.io/source-namespace"] = obj.GetNamespace()
	cloned.SetAnnotations(annotations)

	// Remove status to avoid conflicts
	unstructured.RemoveNestedField(cloned.Object, "status")

	// Remove managed fields
	cloned.SetManagedFields(nil)

	// Clear resource version to allow creation
	cloned.SetResourceVersion("")

	// Generate new UID
	cloned.SetUID("")

	return *cloned
}

// ApplySnapshot applies the snapshot to the target cluster with rollback support
func (s *CRSnapshotter) ApplySnapshot(ctx context.Context, objects []unstructured.Unstructured) error {
	l := log.FromContext(ctx)

	createdObjects := []unstructured.Unstructured{}

	for i, obj := range objects {
		objCopy := obj
		l.Info("Applying resource", "index", i, "kind", objCopy.GetKind(), "name", objCopy.GetName(), "namespace", objCopy.GetNamespace())

		if err := s.client.Create(ctx, &objCopy); err != nil {
			l.Error(err, "failed to create resource", "kind", objCopy.GetKind(), "name", objCopy.GetName())
			// Rollback: delete all previously created objects
			l.Info("Rolling back previously created objects", "count", len(createdObjects))
			for _, created := range createdObjects {
				createdCopy := created
				if deleteErr := s.client.Delete(ctx, &createdCopy); deleteErr != nil {
					l.Error(deleteErr, "failed to rollback resource during cleanup", "kind", createdCopy.GetKind(), "name", createdCopy.GetName())
				}
			}
			return fmt.Errorf("failed to create %s/%s: %w", objCopy.GetKind(), objCopy.GetName(), err)
		}
		createdObjects = append(createdObjects, objCopy)
	}

	l.Info("ApplySnapshot completed successfully", "totalObjects", len(objects))
	return nil
}

// CleanupSnapshot removes all resources from a sandbox
func (s *CRSnapshotter) CleanupSnapshot(ctx context.Context, sandboxName, sandboxNamespace string) error {
	l := log.FromContext(ctx)

	// Delete all resources with the sandbox label
	deleteOpts := []client.DeleteAllOfOption{
		client.InNamespace(sandboxNamespace),
		client.MatchingLabels(map[string]string{
			"lucid.lucid-project.io/sandbox": sandboxName,
		}),
	}

	list := &unstructured.UnstructuredList{}
	if err := s.client.List(ctx, list, deleteOpts...); err != nil {
		l.Error(err, "failed to list resources for cleanup", "namespace", sandboxNamespace, "sandbox", sandboxName)
		return fmt.Errorf("failed to list resources for cleanup: %w", err)
	}

	for _, obj := range list.Items {
		objCopy := obj
		l.Info("Deleting sandbox resource", "kind", objCopy.GetKind(), "name", objCopy.GetName(), "namespace", objCopy.GetNamespace())
		if err := s.client.Delete(ctx, &objCopy); err != nil {
			l.Error(err, "failed to delete resource", "kind", objCopy.GetKind(), "name", objCopy.GetName())
			return fmt.Errorf("failed to delete %s/%s: %w", objCopy.GetKind(), objCopy.GetName(), err)
		}
	}

	l.Info("Cleanup snapshot completed", "namespace", sandboxNamespace, "sandbox", sandboxName)
	return nil
}
