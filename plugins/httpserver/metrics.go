package httpserver

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// SecurityBlockedTotal tracks all security blocks by reason (waf, rate_limit, csrf, ip, geo)
	SecurityBlockedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_security_blocked_total",
		Help: "The total number of requests blocked by security layers.",
	}, []string{"reason", "app"})

	// WAFRuleHits tracks specific rule violations if available
	WAFRuleHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_waf_rule_hits_total",
		Help: "The total number of WAF rule hits.",
	}, []string{"rule_id", "app"})

	// RequestDuration tracks latency of requests (standard but good to have)
	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Histogram of request durations.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status", "app"})
)

// RecordSecurityBlock is a helper to record a block event
func RecordSecurityBlock(app, reason string) {
	SecurityBlockedTotal.WithLabelValues(reason, app).Inc()
}

// RecordWAFHit is a helper to record a WAF rule hit
func RecordWAFHit(app, ruleID string) {
	WAFRuleHits.WithLabelValues(ruleID, app).Inc()
}
