// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package collector implements Prometheus collectors that expose the state of an Omni instance
// (clusters, machines, machine sets, etcd backups and more) as per-object metrics.
//
// The package is designed to be usable both by the standalone omni_exporter binary and as a library,
// e.g., embedded into Omni itself. It only depends on the Omni client SDK and reads all data
// through a COSI [state.State], so any state implementation (an Omni API client state,
// an in-memory test state) can back it.
//
// The state is consumed via watches: [Collector.Run] maintains one watch per resource type feeding
// an in-memory view, and scrapes render from that view. The only Omni access on the scrape path
// is a cheap reachability probe guarding against serving a stale view as current.
package collector

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/siderolabs/omni/client/pkg/omni/resources/system"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/siderolabs/omni_exporter/pkg/collector/internal/watch"
)

// Prefix is the prefix of all metrics exposed by this package.
//
// It is deliberately distinct from the "omni_" prefix used by the metrics Omni exposes natively,
// so that the two sets can never collide, e.g., when both are scraped into the same Prometheus
// or when these collectors are embedded into Omni itself.
const Prefix = "omni_exporter_"

// Collector collects Omni state metrics from a COSI state.
//
// It implements [prometheus.Collector]. [Collector.Run] must be running for the metrics to be
// populated and up to date: without it, scrapes serve up=0 and no object metrics.
//
//nolint:govet // field grouping is preferred over alignment here
type Collector struct {
	state  state.State
	logger *zap.Logger

	upDesc         *prometheus.Desc
	reachableDesc  *prometheus.Desc
	successDesc    *prometheus.Desc
	cachedDesc     *prometheus.Desc
	lastSyncDesc   *prometheus.Desc
	events         *prometheus.CounterVec
	watchAttempts  *prometheus.CounterVec
	collectors     []resourceCollector
	timings        timings
	reachableState atomic.Int32
	running        atomic.Bool
}

// resourceCollector is the type-erased view of a [watch.Collector].
type resourceCollector interface {
	Name() string
	Describe(ch chan<- *prometheus.Desc)

	// Run consumes the watch of the resource type until ctx is canceled, retrying failures forever.
	Run(ctx context.Context, st state.CoreState) error

	// Collect renders the metrics from the in-memory view in one consistent snapshot,
	// and reports the state of that view. Object metrics are exposed only when Omni is
	// reachable, the initial sync has completed and the rendering succeeds.
	Collect(ch chan<- prometheus.Metric, reachable bool) watch.Status
}

// timings groups the tunable delays, injectable for tests.
type timings struct {
	watch        watch.Timings
	probeTimeout time.Duration
}

func defaultTimings() timings {
	return timings{
		watch: watch.Timings{
			EstablishTimeout: 30 * time.Second,
			BackoffMin:       time.Second,
			BackoffMax:       time.Minute,
		},
		probeTimeout: 3 * time.Second,
	}
}

// reachability is the last observed reachability of Omni, tracked so that only genuine
// transitions are logged.
type reachability int32

const (
	reachabilityUnknown reachability = iota
	reachabilityReachable
	reachabilityUnreachable
)

// Options configures a [Collector].
type Options struct {
	// Logger reports watch, probe and rendering failures. Defaults to a no-op logger.
	Logger *zap.Logger
}

// New creates a new [Collector] reading from the given state.
func New(st state.State, options Options) *Collector {
	return newCollector(st, options, defaultTimings())
}

func newCollector(st state.State, options Options, t timings) *Collector {
	logger := options.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	c := &Collector{
		state:   st,
		logger:  logger,
		timings: t,
		upDesc: prometheus.NewDesc(Prefix+"up",
			"Whether Omni is reachable and every collector has completed its initial sync and renders successfully.", nil, nil),
		reachableDesc: prometheus.NewDesc(Prefix+"reachable",
			"Whether Omni answered the reachability probe of the current scrape.", nil, nil),
		successDesc: prometheus.NewDesc(Prefix+"collector_success",
			"Whether the object metrics of a single collector are exposed: Omni is reachable, the initial sync of the "+
				"in-memory view has completed and the rendering succeeded.",
			[]string{"collector"}, nil),
		cachedDesc: prometheus.NewDesc(Prefix+"collector_cached_resources",
			"Number of resources in the in-memory view of a single collector.", []string{"collector"}, nil),
		lastSyncDesc: prometheus.NewDesc(Prefix+"collector_last_sync_timestamp_seconds",
			"Unix timestamp of the last completed sync or applied event of a single collector. "+
				"Resource types without changes legitimately keep an old timestamp, it is not a liveness signal on its own.",
			[]string{"collector"}, nil),
		events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: Prefix + "collector_events_total",
			Help: "Number of watch events applied to the in-memory view of a single collector, excluding the initial sync.",
		}, []string{"collector", "event"}),
		watchAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: Prefix + "collector_watch_attempts_total",
			Help: "Number of watch establishment attempts of a single collector, counted before the attempt is made. " +
				"Each one that succeeds resynchronizes the in-memory view from scratch. A value above one means a previous " +
				"attempt had failed. Stream interruptions the client resumes from a bookmark are not counted here.",
		}, []string{"collector"}),
	}

	config := watch.Config{
		Logger:   logger,
		Timings:  t.watch,
		Events:   c.events,
		Attempts: c.watchAttempts,
	}

	c.collectors = []resourceCollector{
		newClusterCollector(config),
		newMachineCollector(config),
		newClusterMachineCollector(config),
		newMachineSetCollector(config),
		newTalosUpgradeCollector(config),
		newKubernetesUpgradeCollector(config),
		newEtcdBackupConfigCollector(config),
		newEtcdBackupCollector(config),
		newEtcdBackupStoreCollector(config),
	}

	return c
}

// Run establishes and maintains the watches feeding the in-memory views, blocking until ctx
// is canceled. All upstream failures are retried forever with backoff, so the exporter keeps
// serving its last known view instead of terminating. A watch failure after the initial sync
// deliberately does not show up in the up metric. Watch trouble is tracked through
// collector_watch_attempts_total and the client's retry log instead.
//
// It returns nil on ctx cancelation. Calling Run concurrently is an error.
func (c *Collector) Run(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("already running")
	}

	defer c.running.Store(false)

	var eg errgroup.Group

	for _, collector := range c.collectors {
		eg.Go(func() error {
			return collector.Run(ctx, c.state)
		})
	}

	return eg.Wait()
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{
		c.upDesc, c.reachableDesc, c.successDesc, c.cachedDesc, c.lastSyncDesc,
	} {
		ch <- desc
	}

	c.events.Describe(ch)
	c.watchAttempts.Describe(ch)

	for _, collector := range c.collectors {
		collector.Describe(ch)
	}
}

// Collect implements prometheus.Collector.
//
// Every scrape probes the reachability of Omni with a cheap, deadline-bounded read.
// When the probe fails, the object metrics of all collectors are suppressed for the scrape:
// an unreachable Omni puts no bound on how far the view has drifted, so serving it would
// present arbitrarily old data as current. The exporter self-metrics keep being served,
// with up and reachable at 0.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	reachable := c.probeReachable()

	up := boolValue(reachable)

	for _, collector := range c.collectors {
		status := collector.Collect(ch, reachable)
		if !status.Success {
			up = 0
		}

		sendMetrics(
			ch,
			prometheus.MustNewConstMetric(c.successDesc, prometheus.GaugeValue, boolValue(status.Success), collector.Name()),
			prometheus.MustNewConstMetric(c.cachedDesc, prometheus.GaugeValue, float64(status.Cached), collector.Name()),
		)

		if !status.LastSync.IsZero() {
			ch <- prometheus.MustNewConstMetric(
				c.lastSyncDesc, prometheus.GaugeValue, float64(status.LastSync.Unix()), collector.Name(),
			)
		}
	}

	sendMetrics(
		ch,
		prometheus.MustNewConstMetric(c.reachableDesc, prometheus.GaugeValue, boolValue(reachable)),
		prometheus.MustNewConstMetric(c.upDesc, prometheus.GaugeValue, up),
	)

	c.events.Collect(ch)
	c.watchAttempts.Collect(ch)
}

// probeReachable checks whether Omni currently answers a cheap read.
//
// The probed resource is the Omni version: it is tiny, always present and readable by any
// authenticated identity, and a NotFound answer still proves Omni is reachable.
// Concurrent scrapes each probe for themselves, which is bounded by the in-flight scrape limit
// and keeps every scrape's answer its own rather than one shared with an older scrape.
func (c *Collector) probeReachable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timings.probeTimeout)
	defer cancel()

	_, err := safe.StateGetByID[*system.SysVersion](ctx, c.state, system.SysVersionID)

	reachable := err == nil || state.IsNotFoundError(err)

	newState := reachabilityUnreachable
	if reachable {
		newState = reachabilityReachable
	}

	// log genuine transitions only, including an Omni that is unreachable from the very first probe
	if oldState := reachability(c.reachableState.Swap(int32(newState))); oldState != newState {
		switch {
		case !reachable:
			c.logger.Warn("omni is unreachable, suppressing the object metrics", zap.Error(err))
		case oldState != reachabilityUnknown:
			c.logger.Info("omni is reachable again")
		}
	}

	return reachable
}

// boolValue converts a bool to a Prometheus gauge value.
func boolValue(b bool) float64 {
	if b {
		return 1
	}

	return 0
}

// sendMetrics sends the given metrics to the channel.
func sendMetrics(ch chan<- prometheus.Metric, metrics ...prometheus.Metric) {
	for _, metric := range metrics {
		ch <- metric
	}
}
