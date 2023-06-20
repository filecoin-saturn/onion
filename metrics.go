package onion

import (
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

const promPushGwAddr = "http://localhost:9091"

var (
	codeLabels = []string{"layer", "code"}
	sizeLabels = []string{"layer"}

	responseCodeMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "success"),
		Help: "Response codes for a given CID observed for a layer",
	}, codeLabels)
	responseCodeMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "mismatch"),
		Help: "Response code mismatches for a given CID observed for a layer",
	}, codeLabels)
	responseSizeMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_size", "mismatch"),
		Help: "Response size mismatches for a given CID observed for a layer",
	}, sizeLabels)

	metrics = []prometheus.Collector{
		responseCodeMetric,
		responseCodeMismatchMetric,
		responseSizeMismatchMetric,
	}
)

func pushMetric(runID uuid.UUID, co prometheus.Collector) error {
	return push.New(promPushGwAddr, "onion").
		Collector(co).
		Grouping("run", runID.String()).
		Push()
}

func init() {
	for _, m := range metrics {
		prometheus.MustRegister(m)
	}
}
