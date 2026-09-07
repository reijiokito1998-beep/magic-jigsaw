package metrics

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolCollector exposes pgxpool statistics. It is a custom collector (rather
// than a set of gauges updated on a ticker) because pool.Stat() is cheap and
// reading it at scrape time never reports stale numbers.
//
// Pool exhaustion is the most common cause of latency cliffs in this service:
// when acquired_conns sits at max_conns and empty_acquire_count climbs,
// requests are queueing on the database, not on CPU.
type PoolCollector struct {
	pool *pgxpool.Pool

	acquiredConns    *prometheus.Desc
	idleConns        *prometheus.Desc
	totalConns       *prometheus.Desc
	maxConns         *prometheus.Desc
	constructing     *prometheus.Desc
	acquireCount     *prometheus.Desc
	acquireDuration  *prometheus.Desc
	emptyAcquire     *prometheus.Desc
	canceledAcquire  *prometheus.Desc
	newConnsCount    *prometheus.Desc
	maxLifetimeClose *prometheus.Desc
	maxIdleClose     *prometheus.Desc
}

// NewPoolCollector builds a collector for the given pool.
func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(namespace+"_db_"+name, help, nil, nil)
	}
	return &PoolCollector{
		pool:             pool,
		acquiredConns:    d("connections_acquired", "Connections currently checked out of the pool."),
		idleConns:        d("connections_idle", "Idle connections in the pool."),
		totalConns:       d("connections_total", "Total connections in the pool (idle + acquired + constructing)."),
		maxConns:         d("connections_max", "Configured maximum pool size."),
		constructing:     d("connections_constructing", "Connections currently being established."),
		acquireCount:     d("acquires_total", "Cumulative successful connection acquisitions."),
		acquireDuration:  d("acquire_duration_seconds_total", "Cumulative time spent waiting to acquire a connection."),
		emptyAcquire:     d("acquires_empty_total", "Acquisitions that had to wait because the pool was empty."),
		canceledAcquire:  d("acquires_canceled_total", "Acquisitions canceled by context before completing."),
		newConnsCount:    d("connections_new_total", "Cumulative new connections opened."),
		maxLifetimeClose: d("connections_closed_max_lifetime_total", "Connections closed for exceeding max lifetime."),
		maxIdleClose:     d("connections_closed_max_idle_total", "Connections closed for exceeding max idle time."),
	}
}

// Describe implements prometheus.Collector.
func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquiredConns
	ch <- c.idleConns
	ch <- c.totalConns
	ch <- c.maxConns
	ch <- c.constructing
	ch <- c.acquireCount
	ch <- c.acquireDuration
	ch <- c.emptyAcquire
	ch <- c.canceledAcquire
	ch <- c.newConnsCount
	ch <- c.maxLifetimeClose
	ch <- c.maxIdleClose
}

// Collect implements prometheus.Collector.
func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()

	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}

	gauge(c.acquiredConns, float64(s.AcquiredConns()))
	gauge(c.idleConns, float64(s.IdleConns()))
	gauge(c.totalConns, float64(s.TotalConns()))
	gauge(c.maxConns, float64(s.MaxConns()))
	gauge(c.constructing, float64(s.ConstructingConns()))

	counter(c.acquireCount, float64(s.AcquireCount()))
	counter(c.acquireDuration, s.AcquireDuration().Seconds())
	counter(c.emptyAcquire, float64(s.EmptyAcquireCount()))
	counter(c.canceledAcquire, float64(s.CanceledAcquireCount()))
	counter(c.newConnsCount, float64(s.NewConnsCount()))
	counter(c.maxLifetimeClose, float64(s.MaxLifetimeDestroyCount()))
	counter(c.maxIdleClose, float64(s.MaxIdleDestroyCount()))
}
