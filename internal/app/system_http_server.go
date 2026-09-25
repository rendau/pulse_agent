package app

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mechta-market/pulse_agent/internal/infra/metrics"
)

// SystemHttpServerCreate builds the system HTTP server that exposes
// service endpoints: /healthcheck, /docs/*, /metrics and the pulse manifest
// (/.well-known/pulse, /.well-known/pulse/status, /diag/*).
func SystemHttpServerCreate(port string, register func(mux *http.ServeMux)) *http.Server {
	mux := http.NewServeMux()
	register(mux)

	// healthcheck
	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// docs
	docFS := http.StripPrefix("/docs/", http.FileServer(http.Dir("./docs")))
	mux.Handle("/docs/", docFS)

	// metrics (uses metrics.Registry instead of the default promhttp registry)
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       time.Minute,
		MaxHeaderBytes:    300 * 1024,
	}
}
