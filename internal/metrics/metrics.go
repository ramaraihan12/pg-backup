// Package metrics exposes the state of the last backup and drill to Prometheus.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns its own registry, so the process does not export Go runtime
// metrics nobody asked for and tests can create as many as they like.
type Metrics struct {
	reg *prometheus.Registry

	lastSuccess    prometheus.Gauge
	lastDuration   prometheus.Gauge
	lastStored     prometheus.Gauge
	lastRaw        prometheus.Gauge
	lastVerifyOK   prometheus.Gauge
	lastVerifyTime prometheus.Gauge
	lastVerifyDur  prometheus.Gauge
	runs           *prometheus.CounterVec
	verifyRuns     *prometheus.CounterVec
}

// New registers all metrics.
func New() *Metrics {
	g := func(name, help string) prometheus.Gauge {
		return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	}
	m := &Metrics{
		reg:            prometheus.NewRegistry(),
		lastSuccess:    g("pg_backup_last_success_timestamp_seconds", "Unix time the last successful backup finished."),
		lastDuration:   g("pg_backup_last_duration_seconds", "Duration of the last successful backup."),
		lastStored:     g("pg_backup_last_size_bytes", "Stored (compressed and encrypted) size of the last successful backup."),
		lastRaw:        g("pg_backup_last_raw_size_bytes", "Size of the last successful backup before compression."),
		lastVerifyOK:   g("pg_backup_last_verify_success", "1 if the last restore drill passed, 0 if it failed."),
		lastVerifyTime: g("pg_backup_last_verify_timestamp_seconds", "Unix time the last restore drill finished."),
		lastVerifyDur:  g("pg_backup_last_verify_duration_seconds", "Duration of the last restore drill."),
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pg_backup_runs_total", Help: "Backup runs by result."}, []string{"result"}),
		verifyRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pg_backup_verify_runs_total", Help: "Restore drills by result."}, []string{"result"}),
	}
	m.reg.MustRegister(m.lastSuccess, m.lastDuration, m.lastStored, m.lastRaw,
		m.lastVerifyOK, m.lastVerifyTime, m.lastVerifyDur, m.runs, m.verifyRuns)
	// Make the counters appear at 0 so rate() and absent() alerts work from the start.
	for _, r := range []string{"success", "failure"} {
		m.runs.WithLabelValues(r)
		m.verifyRuns.WithLabelValues(r)
	}
	return m
}

// Handler serves the metrics in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// BackupSucceeded records a finished backup.
func (m *Metrics) BackupSucceeded(at time.Time, took time.Duration, storedBytes, rawBytes int64) {
	m.lastSuccess.Set(float64(at.Unix()))
	m.lastDuration.Set(took.Seconds())
	m.lastStored.Set(float64(storedBytes))
	m.lastRaw.Set(float64(rawBytes))
	m.runs.WithLabelValues("success").Inc()
}

// BackupFailed counts a failed backup. The last-success gauges keep their old values.
func (m *Metrics) BackupFailed() { m.runs.WithLabelValues("failure").Inc() }

// Verified records a restore drill.
func (m *Metrics) Verified(at time.Time, took time.Duration, ok bool) {
	v, result := 0.0, "failure"
	if ok {
		v, result = 1, "success"
	}
	m.lastVerifyOK.Set(v)
	m.lastVerifyTime.Set(float64(at.Unix()))
	m.lastVerifyDur.Set(took.Seconds())
	m.verifyRuns.WithLabelValues(result).Inc()
}
