package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	lucidv1alpha1 "github.com/lucid-project/lucid/api/v1alpha1"
	"github.com/lucid-project/lucid/internal/controller"
	"github.com/lucid-project/lucid/internal/sandbox"
	"github.com/lucid-project/lucid/internal/snapshotter"
	"github.com/lucid-project/lucid/internal/validator"
	"github.com/lucid-project/lucid/internal/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lucidv1alpha1.AddToScheme(scheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var certDir string
	var schemaDir string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&certDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs", "Directory containing webhook certificates")
	flag.StringVar(&schemaDir, "schema-dir", "/etc/lucid/schemas", "Directory containing CRD schemas for validation")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "lucid-operator",
		CertDir:                certDir,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Initialize components
	vclusterManager := sandbox.NewVClusterManager(mgr.GetClient())
	crSnapshotter := snapshotter.NewCRSnapshotter(mgr.GetClient())
	schemaValidator := validator.NewSchemaValidator()

	// Load CRD schemas if directory is provided
	if schemaDir != "" {
		if err := schemaValidator.LoadCRDSchemas(ctrl.SetupSignalHandler(), schemaDir); err != nil {
			setupLog.Error(err, "failed to load CRD schemas", "directory", schemaDir)
			os.Exit(1)
		}
	}

	// Setup controllers
	if err = controller.NewUpgradeSandboxReconciler(mgr, vclusterManager, crSnapshotter, schemaValidator).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UpgradeSandbox")
		os.Exit(1)
	}

	if err = controller.NewCertificateController(mgr).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Certificate")
		os.Exit(1)
	}

	// Setup webhook
	setupLog.Info("Setting up webhook server")

	upgradeGatingWebhook := &webhook.UpgradeGatingWebhook{
		Client:          mgr.GetClient(),
		VClusterManager: vclusterManager,
		CRSnapshotter:   crSnapshotter,
		SchemaValidator: schemaValidator,
	}

	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&operatorsv1alpha1.Subscription{}).
		WithValidator(upgradeGatingWebhook).
		Complete(upgradeGatingWebhook); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "UpgradeGating")
		os.Exit(1)
	}

	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&operatorsv1alpha1.InstallPlan{}).
		WithValidator(upgradeGatingWebhook).
		Complete(upgradeGatingWebhook); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "InstallPlan")
		os.Exit(1)
	}

	// Setup health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	fmt.Println("LUCID Operator started successfully")
}