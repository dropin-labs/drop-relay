package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// IngestedTotal tracks the total number of messages received via HTTP or SMTP.
	IngestedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "relay",
			Name:      "messages_ingested_total",
			Help:      "Total count of inbound message ingestion attempts.",
		},
		[]string{"channel", "status"},
	)

	// DeliveredTotal tracks messages successfully dispatched to remote or upstream MTA.
	DeliveredTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "relay",
			Name:      "messages_delivered_total",
			Help:      "Total count of messages dispatched downstream.",
		},
		[]string{"status"},
	)

	// DeliveryFailuresTotal tracks failed delivery attempts by reason.
	DeliveryFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "relay",
			Name:      "messages_failed_total",
			Help:      "Total count of message delivery failures.",
		},
		[]string{"reason"},
	)

	// QueueDepth tracks current buffer depth.
	QueueDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "relay",
			Name:      "queue_depth",
			Help:      "Current count of messages in the spool buffer.",
		},
	)

	// DeliveryDuration tracks latency of SMTP dispatches.
	DeliveryDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "relay",
			Name:      "delivery_latency_seconds",
			Help:      "Time spent transmitting email to downstream MTA in seconds.",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
	)
)
