package metrics

import (
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/rendau/pulse_agent/internal/constant"
)

var Enabled bool
var Registry *prometheus.Registry
var Factory promauto.Factory

func init() {
	Registry = prometheus.NewRegistry()

	Enabled = strings.ToLower(os.Getenv("WITH_METRICS")) == "true"
	metricsNamespace := os.Getenv("METRICS_NAMESPACE")

	if metricsNamespace == "" {
		metricsNamespace = "company"
	}

	if !Enabled {
		return
	}

	Factory = promauto.With(
		prometheus.WrapRegistererWithPrefix(
			strings.Join([]string{
				metricsNamespace,
				constant.ServiceName,
			}, "_")+"_",
			Registry,
		),
	)
}
