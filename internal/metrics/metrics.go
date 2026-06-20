package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ProxyNodesTotal tracks the number of ProxyNodes by region, role, and phase
	ProxyNodesTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "singbox_proxy_nodes_total",
			Help: "Total number of ProxyNodes by region, role, and phase",
		},
		[]string{"region", "role", "phase"},
	)

	// ProxyUsersTotal tracks the number of ProxyUsers
	ProxyUsersTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "singbox_proxy_users_total",
			Help: "Total number of ProxyUsers",
		},
	)

	// ReconcileDurationSeconds tracks reconcile duration by controller and result
	ReconcileDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "singbox_reconcile_duration_seconds",
			Help:    "Duration of reconcile operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"controller", "result"},
	)

	// ReconcileErrorsTotal tracks reconcile errors by controller and error type
	ReconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "singbox_reconcile_errors_total",
			Help: "Total number of reconcile errors by controller and error type",
		},
		[]string{"controller", "error_type"},
	)

	// ConfigUpdatesTotal tracks config updates that trigger rolling updates
	ConfigUpdatesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "singbox_config_updates_total",
			Help: "Total number of sing-box config updates that trigger rolling updates",
		},
		[]string{"node_region", "trigger"},
	)
)

func init() {
	// Register all custom metrics with the controller-runtime metrics registry
	metrics.Registry.MustRegister(
		ProxyNodesTotal,
		ProxyUsersTotal,
		ReconcileDurationSeconds,
		ReconcileErrorsTotal,
		ConfigUpdatesTotal,
	)
}
