package onion

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

const promPushGwAddr = "http://localhost:9091"

var (
	labels = []string{"layer"}

	responseSuccessMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "success"),
		Help: "Successful CID responses observed for a layer",
	}, labels)
	responseMismatchMetric = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName("onion", "response_code", "mismatch"),
		Help: "Response mismatches observed for a layer for a given CID",
	}, labels)
)

func pushMetric(run int, co prometheus.Collector) error {
	return push.New(promPushGwAddr, "onion").
		Collector(co).
		Grouping("run", strconv.Itoa(run)).
		Push()
}

func init() {
	prometheus.MustRegister(responseSuccessMetric)
	prometheus.MustRegister(responseMismatchMetric)
}
