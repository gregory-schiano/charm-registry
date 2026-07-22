package api

import (
	"net/http"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "charm_registry_build_info",
			Help: "Build information for the charm-registry service.",
		},
		[]string{"version"},
	)
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "charm_registry_http_requests_total",
			Help: "Total HTTP requests processed by the registry.",
		},
		[]string{"method", "path", "status"},
	)
	goGoroutines = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "charm_registry_go_goroutines",
			Help: "Current number of goroutines.",
		},
		func() float64 { return float64(runtime.NumGoroutine()) },
	)
)

func init() {
	prometheus.MustRegister(buildInfo, requestsTotal, goGoroutines)
	buildInfo.WithLabelValues("dev").Set(1)
}

// metricsHandler returns an http.Handler that serves Prometheus metrics.
func metricsHandler() http.Handler {
	return promhttp.Handler()
}
