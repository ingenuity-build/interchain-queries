package prommetrics

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	Requests        prometheus.CounterVec
	RequestsLatency prometheus.HistogramVec
	HistoricQueries prometheus.GaugeVec
	SendQueue       prometheus.GaugeVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Requests: *prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "icq",
			Name:      "requests",
			Help:      "number of host requests",
		}, []string{"name"}),
		RequestsLatency: *prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "icq",
			Name:      "request_duration_seconds",
			Help:      "Latency of requests",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15),
		}, []string{"name"}),
		HistoricQueries: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "icq",
			Name:      "historic_queries",
			Help:      "historic queue size",
		}, []string{"name"}),
		SendQueue: *prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "icq",
			Name:      "send_queue",
			Help:      "send queue size",
		}, []string{"name"}),
	}
	reg.MustRegister(m.Requests, m.RequestsLatency, m.HistoricQueries, m.SendQueue)
	return m
}
