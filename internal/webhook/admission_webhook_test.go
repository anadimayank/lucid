package webhook

import (
	"context"
	"testing"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestUpgradeGatingWebhook_ExtractVersionFromCSV(t *testing.T) {
	w := &UpgradeGatingWebhook{}

	tests := []struct {
		csv      string
		expected string
	}{
		{"prometheus-operator.v2.45.0", "2.45.0"},
		{"mysql-operator.v1.2.3-beta", "1.2.3-beta"},
		{"invalid-csv", ""},
		{"", ""},
		{"operator.v", ""},
		{"operator.v1", ""}, // isVersionLike requires at least 2 parts (X.Y)
	}

	for _, tt := range tests {
		t.Run(tt.csv, func(t *testing.T) {
			got := w.extractVersionFromCSV(tt.csv)
			if got != tt.expected {
				t.Errorf("extractVersionFromCSV(%q) = %q; want %q", tt.csv, got, tt.expected)
			}
		})
	}
}

func TestUpgradeGatingWebhook_ParseCSVName(t *testing.T) {
	w := &UpgradeGatingWebhook{}

	tests := []struct {
		csv      string
		wantOp   string
		wantVer  string
	}{
		{"prometheus-operator.v2.45.0", "prometheus-operator", "2.45.0"},
		{"mysql-operator.v1.2.3", "mysql-operator", "1.2.3"},
		{"invalid-csv", "", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.csv, func(t *testing.T) {
			op, ver := w.parseCSVName(tt.csv)
			if op != tt.wantOp || ver != tt.wantVer {
				t.Errorf("parseCSVName(%q) = (%q, %q); want (%q, %q)", tt.csv, op, ver, tt.wantOp, tt.wantVer)
			}
		})
	}
}

func TestUpgradeGatingWebhook_ValidateUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(lucidv1alpha1.GroupVersion, &lucidv1alpha1.OperatorUpgradeCertificate{})
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	w := &UpgradeGatingWebhook{Client: c}

	ctx := context.Background()
	ns := "default"
	op := "prometheus-operator"
	ver := "2.45.0"
	certName := "prometheus-operator-2-45-0-cert"

	tests := []struct {
		name           string
		certStatus    string
		certOutcome   string
		wantAllowed    bool
		wantMessage    string
	}{
		{
			name:        "Certified",
			certStatus:  "Certified",
			certOutcome: "All checks passed",
			wantAllowed: true,
		},
		{
			name:        "CertifiedWithWarnings",
			certStatus:  "CertifiedWithWarnings",
			certOutcome: "Minor policy deviation",
			wantAllowed: true,
		},
		{
			name:        "Rejected",
			certStatus:  "Rejected",
			certOutcome: "Security vulnerability found",
			wantAllowed: false,
		},
		{
			name:        "Stale",
			certStatus:  "Stale",
			certOutcome: "State drift detected",
			wantAllowed: false,
		},
		{
			name:        "Pending",
			certStatus:  "Pending",
			certOutcome: "Validating",
			wantAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert := &lucidv1alpha1.OperatorUpgradeCertificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: ns,
				},
				Status: lucidv1alpha1.OperatorUpgradeCertificateStatus{
					Summary:       tt.certStatus,
					PolicyOutcome: tt.certOutcome,
				},
			}
			err := c.Create(ctx, cert)
			if err != nil {
				t.Fatalf("failed to create cert: %v", err)
			}

			resp := w.validateUpgrade(ctx, op, ver, ns)
			if resp.Allowed != tt.wantAllowed {
				t.Errorf("validateUpgrade() Allowed = %v, want %v", resp.Allowed, tt.wantAllowed)
			}
		})
	}
}
