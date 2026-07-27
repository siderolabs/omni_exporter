// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package watch maintains an in-memory view of a single COSI resource type, fed by a watch,
// and renders Prometheus metrics from it.
//
// It is the generic machinery behind every collector of the parent package: what a resource type
// contributes is its metric descriptors and a render function, everything else lives here.
// Nothing in this package knows about Omni.
package watch

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// eventChannelBuffer absorbs event bursts (e.g. the bootstrap of a large resource type),
// keeping backpressure on the watch stream rare.
const eventChannelBuffer = 128

// Timings groups the watch loop delays, injectable for tests.
type Timings struct {
	EstablishTimeout time.Duration
	BackoffMin       time.Duration
	BackoffMax       time.Duration
}

// Config groups the dependencies shared by all watch collectors.
type Config struct {
	Logger   *zap.Logger
	Events   *prometheus.CounterVec
	Attempts *prometheus.CounterVec
	Timings  Timings
}

// MetricSink accumulates rendered metrics.
type MetricSink func(prometheus.Metric)

// Status reports the state of the in-memory view of a single collector at collection time.
type Status struct {
	// LastSync is the time of the last completed sync or applied event, zero before the first sync.
	LastSync time.Time

	// Cached is the number of resources in the view.
	Cached int

	// Success reports whether the object metrics were exposed for this collection.
	// It is deliberately the zero value, so that any path returning early fails closed.
	Success bool
}

// Collector maintains an in-memory view of a single resource type, fed by a COSI watch,
// and renders metrics from it.
//
// Once the view has completed its initial sync it keeps serving across watch failures, going
// stale instead of going absent. A COSI stream is never silently lossy: a resume from a bookmark
// is either exact or refused outright, and a refusal arrives here as an error that drives a full
// re-bootstrap. So an interrupted view converges back to correct on its own. The re-bootstrap
// fills a staging map that is atomically swapped in on the Bootstrapped event, so the previous
// view keeps serving throughout instead of flapping to absent.
//
// How long that staleness can last is not bounded here. The caller decides what to do about it.
//
// The initial sync is the one case that must fail closed. A half-filled view renders as
// "these objects do not exist", which is a wrong answer, not an old one.
//
//nolint:govet // field grouping is preferred over alignment here
type Collector[T resource.Resource] struct {
	collectorName string
	kind          resource.Metadata
	descs         []*prometheus.Desc
	render        func(items []T, add MetricSink)

	logger   *zap.Logger
	timings  Timings
	events   *prometheus.CounterVec
	attempts prometheus.Counter

	mu     sync.Mutex
	active map[resource.ID]T

	// bootstrapped reports whether the initial sync has ever completed. It stays set across
	// watch failures, when the view is merely old, and is cleared when the run ends.
	bootstrapped bool
	lastSync     time.Time
}

// New creates a collector for the resource type of the given prototype.
//
// The descriptors are what the collector describes to Prometheus, and render turns a snapshot
// of the view into the matching metrics.
func New[T resource.Resource](
	config Config,
	name string,
	prototype T,
	descs []*prometheus.Desc,
	render func(items []T, add MetricSink),
) *Collector[T] {
	// pre-initialize the event counter series, so they exist at 0 from the first scrape
	for _, eventType := range []state.EventType{state.Created, state.Updated, state.Destroyed} {
		config.Events.WithLabelValues(name, eventLabel(eventType))
	}

	return &Collector[T]{
		collectorName: name,
		kind:          prototype.Metadata().Copy(),
		descs:         descs,
		render:        render,
		active:        map[resource.ID]T{},
		logger:        config.Logger,
		timings:       config.Timings,
		events:        config.Events,
		attempts:      config.Attempts.WithLabelValues(name),
	}
}

// Name returns the collector name used as a metric label.
func (w *Collector[T]) Name() string {
	return w.collectorName
}

// Describe sends the descriptors of the rendered metrics.
func (w *Collector[T]) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range w.descs {
		ch <- desc
	}
}

// Run implements the retry loop: establish the watch, consume it until it fails,
// back off, repeat. It returns only on ctx cancelation: all failures, including panics
// in the event handling, degrade this collector instead of crashing the process.
func (w *Collector[T]) Run(ctx context.Context, st state.CoreState) error {
	// The view is only maintained while this loop runs. Staying "bootstrapped" across watch
	// failures is the point of the design, but staying so across the end of the loop is not:
	// nothing is left to recover it, so the drift becomes unbounded. This also stops a second
	// Run from serving the previous run's view before it has bootstrapped its own.
	defer w.clearBootstrapped()

	backoffDelay := w.timings.BackoffMin

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		w.attempts.Inc()

		bootstrapped, err := w.watchOnceSafe(ctx, st)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // ordinary shutdown
		}

		// an attempt that completed its bootstrap starts the backoff over, however long it then ran
		if bootstrapped {
			backoffDelay = w.timings.BackoffMin
		}

		w.logger.Warn("watch failed, retrying",
			zap.String("collector", w.collectorName), zap.Duration("backoff", backoffDelay), zap.Error(err))

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoffDelay + rand.N(backoffDelay/4+1)): // jittered, so that exporters do not retry in lockstep
		}

		backoffDelay = min(backoffDelay*2, w.timings.BackoffMax)
	}
}

// watchOnceSafe converts a panic in the event handling into an ordinary attempt failure.
//
// A panic leaves the reported flag false even when the attempt had already bootstrapped, so the
// caller does not reset its backoff. That is deliberate: a panicking event handler is a bug, and
// backing off keeps it from re-bootstrapping in a tight loop for as long as the bug lasts.
func (w *Collector[T]) watchOnceSafe(ctx context.Context, st state.CoreState) (bootstrapped bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("watch panicked: %v", recovered)
		}
	}()

	return w.watchOnce(ctx, st)
}

// watchOnce runs a single watch attempt: establish, bootstrap into a staging map,
// swap it in on Bootstrapped, then apply live events until the watch fails. It reports
// whether the attempt got as far as completing its bootstrap.
//
// Attempts run strictly sequentially: the previous attempt has fully returned (and its
// staging map is abandoned with it) before the next one starts, so a stale attempt
// can never mutate the served view.
func (w *Collector[T]) watchOnce(ctx context.Context, st state.CoreState) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eventCh := make(chan state.Event, eventChannelBuffer)

	// The protobuf transport has no deadline on the watch establishment handshake,
	// so a peer that accepts the connection but never completes the handshake would
	// pin this attempt forever without the watchdog here.
	establishErrCh := make(chan error, 1)

	go func() {
		establishErrCh <- st.WatchKind(ctx, w.kind, eventCh, state.WithBootstrapContents(true))
	}()

	establishTimer := time.NewTimer(w.timings.EstablishTimeout)
	defer establishTimer.Stop()

	select {
	case err := <-establishErrCh:
		if err != nil {
			return false, fmt.Errorf("failed to establish watch: %w", err)
		}
	case <-establishTimer.C:
		return false, errors.New("timed out establishing watch")
	case <-ctx.Done():
		return false, ctx.Err()
	}

	staging := map[resource.ID]T{}
	bootstrapped := false

	for {
		var event state.Event

		select {
		case <-ctx.Done():
			return bootstrapped, ctx.Err()
		case event = <-eventCh:
		}

		// switch on the event type before touching the resource: Bootstrapped and Noop
		// events carry a tombstone resource of a different type
		switch event.Type {
		case state.Errored:
			if event.Error == nil {
				return bootstrapped, errors.New("watch errored without an error")
			}

			return bootstrapped, fmt.Errorf("watch errored: %w", event.Error)
		case state.Bootstrapped:
			// out of contract for all known backends: fail the attempt and re-bootstrap
			// instead of risking the served view
			if bootstrapped {
				return bootstrapped, errors.New("protocol error: repeated Bootstrapped event")
			}

			w.swap(staging)

			staging = nil
			bootstrapped = true
		case state.Noop:
		case state.Created, state.Updated, state.Destroyed:
			res, ok := event.Resource.(T)
			if !ok {
				return bootstrapped, fmt.Errorf("unexpected resource type %T in the %q watch", event.Resource, w.collectorName)
			}

			if bootstrapped {
				w.applyLive(event.Type, res)
			} else {
				applyToMap(staging, event.Type, res)
			}
		default:
			return bootstrapped, fmt.Errorf("protocol error: unexpected event type %s", event.Type)
		}
	}
}

// clearBootstrapped takes the view out of service until it is bootstrapped again.
func (w *Collector[T]) clearBootstrapped() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.bootstrapped = false
}

// swap atomically replaces the served view with the freshly bootstrapped one.
func (w *Collector[T]) swap(staging map[resource.ID]T) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.active = staging
	w.bootstrapped = true
	w.lastSync = time.Now()
}

// applyLive applies a post-bootstrap event to the served view.
//
// The counter is bumped outside the lock, so that a slow or panicking metric path cannot
// hold up the view.
func (w *Collector[T]) applyLive(eventType state.EventType, res T) {
	w.applyLocked(eventType, res)

	w.events.WithLabelValues(w.collectorName, eventLabel(eventType)).Inc()
}

// applyLocked mutates the served view under the lock.
//
// It is a separate function purely so that the lock is released by defer. A panic in here is
// recovered by the enclosing watch attempt, and a lock leaked on that path would deadlock every
// later scrape and every later attempt.
func (w *Collector[T]) applyLocked(eventType state.EventType, res T) {
	w.mu.Lock()
	defer w.mu.Unlock()

	applyToMap(w.active, eventType, res)
	w.lastSync = time.Now()
}

func applyToMap[T resource.Resource](items map[resource.ID]T, eventType state.EventType, res T) {
	if eventType == state.Destroyed {
		delete(items, res.Metadata().ID())

		return
	}

	items[res.Metadata().ID()] = res
}

func eventLabel(eventType state.EventType) string {
	switch eventType { //nolint:exhaustive
	case state.Created:
		return "created"
	case state.Updated:
		return "updated"
	case state.Destroyed:
		return "destroyed"
	default:
		return "unknown"
	}
}

// Collect renders metrics from the in-memory view.
//
// Object metrics are suppressed while the view would be an outright wrong answer: Omni is
// unreachable, or the initial sync has not completed yet. An old view is not such a case, so a
// watch failure after the initial sync suppresses nothing and the last view keeps being served
// until a resume catches it up or a re-bootstrap swaps in a fresh one. Suppressed series get
// staleness markers from Prometheus right away, exactly as if the objects were read per scrape.
//
// The view is snapshotted under the lock and rendered outside of it, so a slow scrape
// can never backpressure the event loop. Rendering runs inside a recovery boundary into a
// temporary slice, so a rendering bug degrades this collector instead of crashing the process
// or emitting a partial metric family.
func (w *Collector[T]) Collect(ch chan<- prometheus.Metric, reachable bool) Status {
	items, bootstrapped, status := w.snapshot()

	if !reachable || !bootstrapped {
		return status
	}

	metrics, renderOK := w.renderSafe(items)
	for _, metric := range metrics {
		ch <- metric
	}

	status.Success = renderOK

	return status
}

// snapshot copies the served view out from under the lock, so that the rendering that follows
// cannot backpressure the event loop.
func (w *Collector[T]) snapshot() ([]T, bool, Status) {
	w.mu.Lock()
	defer w.mu.Unlock()

	items := make([]T, 0, len(w.active))
	for _, item := range w.active {
		items = append(items, item)
	}

	return items, w.bootstrapped, Status{LastSync: w.lastSync, Cached: len(w.active)}
}

func (w *Collector[T]) renderSafe(items []T) (metrics []prometheus.Metric, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			w.logger.Error("collector rendering panicked",
				zap.String("collector", w.collectorName), zap.Any("panic", recovered))

			metrics, ok = nil, false
		}
	}()

	metrics = make([]prometheus.Metric, 0, len(items)*8)

	w.render(items, func(metric prometheus.Metric) {
		metrics = append(metrics, metric)
	})

	return metrics, true
}
