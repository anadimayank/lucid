package controller

import (
	"context"
	"testing"
	"time"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCertificateController_Reconcile_StateChange(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(lucidv1alpha1.GroupVersion, &lucidv1alpha1.OperatorUpgradeCertificate{}, &lucidv1alpha1.UpgradeSandbox{})
	scheme.AddKnownTypes(corev1.GroupVersion, &corev1.ConfigMap{})

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &CertificateController{
		Client: c,
		Scheme: scheme,
	}

	ctx := context.Background()
	ns := "default"
	op := "prometheus-operator"
	certName := "prometheus-operator-cert"

	cert := &lucidv1alpha1.OperatorUpgradeCertificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certName,
			Namespace: ns,
		},
		Spec: lucidv1alpha1.OperatorUpgradeCertificateSpec{
			OperatorRef: op,
		},
		Status: lucidv1alpha1.OperatorUpgradeCertificateStatus{
			Summary: "Certified",
		},
	}
	err := c.Create(ctx, cert)
	if err != nil {
		t.Fatalf("failed to create cert: %v", err)
	}

	sandbox := &lucidv1alpha1.UpgradeSandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sandbox-1",
			Namespace: ns,
			Labels:    map[string]string{"lucid.lucid-project.io/operator": op},
		},
	}
	err = c.Create(ctx, sandbox)
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}

	// First reconcile - should create the state ConfigMap and return no change
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: certName, Namespace: ns}}
	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if res.RequeueAfter != 10*time.Minute {
		t.Errorf("expected requeue after 10m, got %v", res.RequeueAfter)
	}

	// Modify the sandbox to trigger a state change (update ResourceVersion)
	sandbox.ResourceVersion = "2"
	err = c.Update(ctx, sandbox)
	if err != nil {
		t.Fatalf("failed to update sandbox: %v", err)
	}

	// Second reconcile - should detect change, mark cert as Stale, and trigger revalidation
	res, err = r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Verify certificate is now Stale
	updatedCert := &lucidv1alpha1.OperatorUpgradeCertificate{}
	err = c.Get(ctx, types.NamespacedName{Name: certName, Namespace: ns}, updatedCert)
	if err != nil {
		t.Fatalf("failed to get updated cert: %v", err)
	}
	if updatedCert.Status.Summary != "Stale" {
		t.Errorf("expected cert status Stale, got %s", updatedCert.Status.Summary)
	}

	// Verify ValidationRun was created
	var runs lucidv1alpha1.ValidationRunList
	err = c.List(ctx, &runs)
	if err != nil {
		t.Fatalf("failed to list validation runs: %v", err)
	}
	if len(runs.Items) == 0 {
		t.Error("expected a ValidationRun to be created for revalidation")
	}
}
