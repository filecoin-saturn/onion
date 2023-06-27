package onion

import (
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

const promPushGwAddr = "http://localhost:9091"

// Note that the purpose of this module is to be useful for a constrained number of test runs
// Having all these unconstrained labels (i.e. CID and status code to some extent) will result in high cardinality
// Keep that in mind when using this in a real system
var (
	labels     = []string{"cid", "layer"}
	codeLabels = append(labels, "code")

	responseCodeMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "value"),
		Help: "Response codes for a given CID observed for a layer",
	}, codeLabels)
	responseCodeMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "mismatch"),
		Help: "Response code mismatches for a given CID observed for a layer",
	}, labels)
	responseSizeMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_size", "mismatch"),
		Help: "Response size mismatches for a given CID observed for a layer",
	}, labels)

	metrics = []prometheus.Collector{
		responseCodeMetric,
		responseCodeMismatchMetric,
		responseSizeMismatchMetric,
	}
)

func PushMetrics(runID uuid.UUID) error {
	pusher := push.New(promPushGwAddr, "onion")
	for _, co := range metrics {
		pusher.Collector(co)
	}
	return pusher.
		Grouping("run_id", runID.String()).
		Push()
}

func init() {
	for _, m := range metrics {
		prometheus.MustRegister(m)
	}
}
