/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	proxyv1alpha1 "github.com/shlande/singbox-operator/api/v1alpha1"
	"github.com/shlande/singbox-operator/internal/apiserver"
	"github.com/shlande/singbox-operator/internal/controller"
	"github.com/shlande/singbox-operator/internal/usagecollector"
	proxywebhook "github.com/shlande/singbox-operator/internal/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(proxyv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var apiBindAddress string
	var clientConfigTemplate string
	var defaultTLSSecret string
	var singboxImage string
	var nodePortRangeMin int
	var nodePortRangeMax int
	var usageCollectEnabled bool
	var usageV2RayAPIListenAddr string
	var usagePollInterval time.Duration
	var usageNodeTimeout time.Duration
	var usageESEndpoint string
	var usageESAPIKey string
	var usageESDataStream string
	var usageCheckpointPath string
	var usageMaxBufferSize int
	var usageShutdownTimeout time.Duration
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&apiBindAddress, "api-bind-address", ":8082",
		"The address the client config API endpoint binds to.")
	flag.StringVar(&clientConfigTemplate, "client-config-template", "",
		"ConfigMap reference for client config template in namespace/name format.")
	flag.StringVar(&defaultTLSSecret, "default-tls-secret", "sing-box-tls",
		"Name of the default kubernetes.io/tls Secret used for TLS-requiring protocols (e.g. hysteria2). Can be overridden per SingBoxNode via spec.tlsSecretName.")
	flag.StringVar(&singboxImage, "singbox-image", "ghcr.io/sagernet/sing-box:latest",
		"Container image used for sing-box pods. Override to use a build with v2ray_api support.")
	flag.IntVar(&nodePortRangeMin, "nodeport-range-min", 30000,
		"Lower bound of the Kubernetes NodePort range. hostPort values in [nodeport-range-min, nodeport-range-max] are rejected.")
	flag.IntVar(&nodePortRangeMax, "nodeport-range-max", 32767,
		"Upper bound of the Kubernetes NodePort range. hostPort values in [nodeport-range-min, nodeport-range-max] are rejected.")
	flag.BoolVar(&usageCollectEnabled, "usage-collect-enabled", false,
		"Enable the usage collector that polls sing-box node traffic stats and writes to Elasticsearch.")
	flag.StringVar(&usageV2RayAPIListenAddr, "usage-v2rayapi-listen", "127.0.0.1:10085",
		"Listen address injected into sing-box config for v2ray_api stats. Only used when usage-collect-enabled=true.")
	flag.DurationVar(&usagePollInterval, "usage-poll-interval", 30*time.Second,
		"Interval between usage collection poll cycles.")
	flag.DurationVar(&usageNodeTimeout, "usage-node-timeout", 10*time.Second,
		"Timeout for gRPC queries to individual sing-box nodes.")
	flag.StringVar(&usageESEndpoint, "usage-es-endpoint", "",
		"Elasticsearch endpoint URL for the usage collector sink.")
	flag.StringVar(&usageESAPIKey, "usage-es-api-key", "",
		"Elasticsearch API key for the usage collector sink.")
	flag.StringVar(&usageESDataStream, "usage-es-data-stream", "usage-traffic",
		"Elasticsearch data stream name for usage records.")
	flag.StringVar(&usageCheckpointPath, "usage-checkpoint-path", "/tmp/usage-collector-checkpoint.json",
		"Filesystem path for the usage collector checkpoint file.")
	flag.IntVar(&usageMaxBufferSize, "usage-max-buffer-size", 10000,
		"Maximum number of usage records to buffer before flushing.")
	flag.DurationVar(&usageShutdownTimeout, "usage-shutdown-timeout", 30*time.Second,
		"Maximum time to wait for in-flight usage data flush during shutdown.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "7d42b374.singboxoperator.shlande.top",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// Register field index for spec.nodeRef to enable efficient Node→SingBoxNode lookups.
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &proxyv1alpha1.SingBoxNode{}, "spec.nodeRef", func(rawObj client.Object) []string {
		sbn := rawObj.(*proxyv1alpha1.SingBoxNode)
		if sbn.Spec.NodeRef == "" {
			return nil
		}
		return []string{sbn.Spec.NodeRef}
	}); err != nil {
		setupLog.Error(err, "Failed to set up field index for spec.nodeRef")
		os.Exit(1)
	}

	if err := (&controller.SingBoxNodeReconciler{
		Client:                 mgr.GetClient(),
		Scheme:                 mgr.GetScheme(),
		DefaultTLSSecret:       defaultTLSSecret,
		SingBoxImage:           singboxImage,
		UsageCollectionEnabled: usageCollectEnabled,
		V2RayAPIListenAddr:     usageV2RayAPIListenAddr,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "singboxnode")
		os.Exit(1)
	}
	if err := (&controller.UserReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "user")
		os.Exit(1)
	}
	if err := (&controller.CustomRouteReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "customroute")
		os.Exit(1)
	}
	if err := proxywebhook.SetupSingBoxNodeWebhookWithManager(mgr, int32(nodePortRangeMin), int32(nodePortRangeMax)); err != nil {
		setupLog.Error(err, "Failed to create webhook", "webhook", "SingBoxNode")
		os.Exit(1)
	}
	if err := proxywebhook.SetupUserWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create webhook", "webhook", "User")
		os.Exit(1)
	}
	if err := proxywebhook.SetupCustomRouteWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create webhook", "webhook", "CustomRoute")
		os.Exit(1)
	}
	if err := mgr.Add(&apiserver.Server{
		BindAddress: apiBindAddress,
		TemplateRef: clientConfigTemplate,
		Client:      mgr.GetClient(),
	}); err != nil {
		setupLog.Error(err, "Failed to register API server")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if usageCollectEnabled {
		usageCfg := usagecollector.CollectorConfig{
			Enabled:         true,
			PollInterval:    usagePollInterval,
			NodeTimeout:     usageNodeTimeout,
			ESEndpoint:      usageESEndpoint,
			ESAPIKey:        usageESAPIKey,
			ESDataStream:    usageESDataStream,
			CheckpointPath:  usageCheckpointPath,
			MaxBufferSize:   usageMaxBufferSize,
			ShutdownTimeout: usageShutdownTimeout,
		}
		if err := usageCfg.Validate(); err != nil {
			setupLog.Error(err, "Invalid usage collector configuration")
			os.Exit(1)
		}

		watchNamespace := os.Getenv("WATCH_NAMESPACE")
		discoverer := usagecollector.NewK8sDiscoverer(mgr.GetClient(), watchNamespace)
		statsClient := usagecollector.NewGRPCStatsClient(usageCfg.NodeTimeout)
		esSink, err := usagecollector.NewElasticsearchSink(usageCfg)
		if err != nil {
			setupLog.Error(err, "Failed to create Elasticsearch sink")
			os.Exit(1)
		}
		collector := usagecollector.NewCollector(usageCfg, discoverer, statsClient, esSink)

		if err := mgr.Add(&usagecollector.CollectorRunnable{Collector: collector}); err != nil {
			setupLog.Error(err, "Failed to register usage collector")
			os.Exit(1)
		}
		setupLog.Info("Usage collector enabled and registered with leader election")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
