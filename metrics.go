package onion

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

const promPushGwAddr = "http://localhost:9091"

var (
	labels = []string{"layer"}

	responseCodeSuccessMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "success"),
		Help: "Successful CID response codes observed for a layer",
	}, labels)
	responseCodeMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "mismatch"),
		Help: "Response code mismatches for a given CID observed for a layer",
	}, labels)
	responseSizeMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_size", "mismatch"),
		Help: "Response size mismatches for a given CID observed for a layer",
	}, labels)

	metrics = []prometheus.Collector{
		responseCodeMismatchMetric,
		responseCodeSuccessMetric,
		responseSizeMismatchMetric,
	}
)

func pushMetric(run int, co prometheus.Collector) error {
	return push.New(promPushGwAddr, "onion").
		Collector(co).
		Grouping("run", strconv.Itoa(run)).
		Push()
}

func init() {
	for _, m := range metrics {
		prometheus.MustRegister(m)
	}
}
