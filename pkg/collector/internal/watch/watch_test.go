// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package watch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/conformance"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/siderolabs/omni_exporter/pkg/collector/internal/watch"
)

// scriptedState delivers a fixed event sequence on WatchKind, so that the event handling
// can be tested deterministically, without sleeps or timing assumptions: the sequences end
// with a terminal event (usually Errored), the attempt returns, and the resulting view
// is asserted synchronously.
type scriptedState struct {
	state.CoreState // nil: only WatchKind is expected to be called

	events         []state.Event
	blockEstablish bool
}

func (s *scriptedState) WatchKind(ctx context.Context, _ resource.Kind, ch chan<- state.Event, _ ...state.WatchKindOption) error {
	if s.blockEstablish {
		<-ctx.Done()

		return ctx.Err()
	}

	for _, event := range s.events {
		ch <- event // the channel buffer is larger than any script here
	}

	return nil
}

// newTestWatch builds a collector over a resource type of the COSI runtime itself, so that
// single attempts can be driven directly. The machinery under test knows nothing about the
// resource type beyond its metadata, so a stand-in serves as well as a real Omni resource.
func newTestWatch() *watch.Collector[*conformance.PathResource] {
	config := watch.Config{
		Logger: zap.NewNop(),
		Timings: watch.Timings{
			EstablishTimeout: 50 * time.Millisecond,
			BackoffMin:       time.Millisecond,
			BackoffMax:       10 * time.Millisecond,
		},
		Events: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_events_total", Help: "Test events.",
		}, []string{"collector", "event"}),
		Attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "test_attempts_total", Help: "Test attempts.",
		}, []string{"collector"}),
	}

	desc := prometheus.NewDesc("test_path", "Test path.", []string{"path"}, nil)

	return watch.New(config, "paths", conformance.NewPathResourceWithDefaultNS(""), []*prometheus.Desc{desc},
		func(items []*conformance.PathResource, add watch.MetricSink) {
			for _, item := range items {
				add(prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1, item.Metadata().ID()))
			}
		})
}

// pathEvent builds an event for the given path. The resource version distinguishes the
// revisions of one path, so that an applied update is observable in the view.
func pathEvent(eventType state.EventType, path string, version resource.Version) state.Event {
	res := conformance.NewPathResourceWithDefaultNS(path)
	res.Metadata().SetVersion(version)

	return state.Event{Type: eventType, Resource: res}
}

func terminalErrored() state.Event {
	return state.Event{Type: state.Errored, Error: errors.New("end of script")}
}

// runScript runs a single watch attempt over the given event script and returns its error.
func runScript(t *testing.T, w *watch.Collector[*conformance.PathResource], script ...state.Event) error {
	t.Helper()

	_, err := w.WatchOnceSafe(t.Context(), &scriptedState{events: script})

	return err
}

// TestWatchInterleavedBootstrap asserts that updates and deletes interleaved with the
// bootstrap contents land in the staging view before it is swapped in.
func TestWatchInterleavedBootstrap(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	err := runScript(
		t, w,
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
		pathEvent(state.Created, "path-2", resource.VersionUndefined),
		pathEvent(state.Updated, "path-1", resource.VersionUndefined.Next()),
		pathEvent(state.Destroyed, "path-2", resource.VersionUndefined),
		state.Event{Type: state.Bootstrapped},
		terminalErrored(),
	)
	require.ErrorContains(t, err, "end of script")

	items, bootstrapped := w.Snapshot()
	require.True(t, bootstrapped)
	require.Len(t, items, 1)
	assert.Equal(t, resource.VersionUndefined.Next(), items["path-1"].Metadata().Version())
}

// TestWatchEmptyBootstrap asserts that a bootstrap of an empty resource type syncs an empty view.
func TestWatchEmptyBootstrap(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	err := runScript(
		t, w,
		state.Event{Type: state.Bootstrapped},
		terminalErrored(),
	)
	require.ErrorContains(t, err, "end of script")

	items, bootstrapped := w.Snapshot()
	require.True(t, bootstrapped)
	require.Empty(t, items)
}

// TestWatchDuplicateBootstrapped asserts that an out-of-contract repeated Bootstrapped event
// fails the attempt without wiping or corrupting the served view.
func TestWatchDuplicateBootstrapped(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	err := runScript(
		t, w,
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
		state.Event{Type: state.Bootstrapped},
		pathEvent(state.Created, "path-2", resource.VersionUndefined),
		state.Event{Type: state.Bootstrapped},
		pathEvent(state.Created, "path-3", resource.VersionUndefined), // must not be reached, and especially must not panic
		terminalErrored(),
	)
	require.ErrorContains(t, err, "repeated Bootstrapped event")

	items, _ := w.Snapshot()
	require.Len(t, items, 2, "the served view must survive the protocol error")
}

// TestWatchTombstoneEvents asserts that Bootstrapped and Noop events never touch the view,
// even though they carry no usable resource.
func TestWatchTombstoneEvents(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	err := runScript(
		t, w,
		state.Event{Type: state.Noop},
		state.Event{Type: state.Bootstrapped},
		state.Event{Type: state.Noop},
		terminalErrored(),
	)
	require.ErrorContains(t, err, "end of script")

	items, _ := w.Snapshot()
	require.Empty(t, items)
}

// TestWatchErroredDuringBootstrap asserts that a failure before the sync completes abandons
// the staging view and keeps serving the previous one.
func TestWatchErroredDuringBootstrap(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	// a previous attempt synced a view with path-1
	require.ErrorContains(t, runScript(
		t, w,
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
		state.Event{Type: state.Bootstrapped},
		terminalErrored(),
	), "end of script")

	// the next attempt fails mid-bootstrap
	require.ErrorContains(t, runScript(
		t, w,
		pathEvent(state.Created, "path-2", resource.VersionUndefined),
		terminalErrored(),
	), "end of script")

	items, _ := w.Snapshot()
	require.Len(t, items, 1, "the previous view must keep serving")
	require.Contains(t, items, resource.ID("path-1"))
}

// TestWatchUnexpectedResourceType asserts that a resource of a wrong type fails the attempt.
func TestWatchUnexpectedResourceType(t *testing.T) {
	t.Parallel()

	err := runScript(
		t, newTestWatch(),
		state.Event{
			Type:     state.Created,
			Resource: resource.NewTombstone(resource.NewMetadata("default", "os/other", "nope", resource.VersionUndefined)),
		},
	)
	require.ErrorContains(t, err, "unexpected resource type")
}

// TestWatchUnknownEventType asserts that an unknown event type fails the attempt (fail closed).
func TestWatchUnknownEventType(t *testing.T) {
	t.Parallel()

	err := runScript(
		t, newTestWatch(),
		state.Event{Type: state.EventType(99)},
	)
	require.ErrorContains(t, err, "unexpected event type")
}

// TestWatchErroredWithoutError asserts that a malformed Errored event is handled gracefully.
func TestWatchErroredWithoutError(t *testing.T) {
	t.Parallel()

	err := runScript(
		t, newTestWatch(),
		state.Event{Type: state.Errored},
	)
	require.ErrorContains(t, err, "watch errored without an error")
}

// TestWatchEstablishmentTimeout asserts that a watch establishment which never completes
// its handshake is bounded by the watchdog.
func TestWatchEstablishmentTimeout(t *testing.T) {
	t.Parallel()

	_, err := newTestWatch().WatchOnceSafe(t.Context(), &scriptedState{blockEstablish: true})
	require.ErrorContains(t, err, "timed out establishing watch")
}

// TestWatchPanicRecovery asserts that a panic in the event application path degrades the
// attempt into an error instead of crashing the process.
func TestWatchPanicRecovery(t *testing.T) {
	t.Parallel()

	w := newTestWatch()
	w.BreakEventCounter() // provokes a panic on the first live event

	err := runScript(
		t, w,
		state.Event{Type: state.Bootstrapped},
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
	)
	require.ErrorContains(t, err, "watch panicked")
}

// TestWatchAttemptReportsBootstrap asserts the backoff reset signal: an attempt reports whether
// it got as far as completing its bootstrap, independently of how it then failed.
func TestWatchAttemptReportsBootstrap(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	bootstrapped, err := w.WatchOnceSafe(t.Context(), &scriptedState{events: []state.Event{
		{Type: state.Bootstrapped},
		terminalErrored(),
	}})
	require.ErrorContains(t, err, "end of script")
	assert.True(t, bootstrapped, "an attempt that bootstrapped must reset the backoff")

	bootstrapped, err = w.WatchOnceSafe(t.Context(), &scriptedState{events: []state.Event{
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
		terminalErrored(),
	}})
	require.ErrorContains(t, err, "end of script")
	assert.False(t, bootstrapped, "an attempt that failed mid-bootstrap must not reset the backoff")
}

// TestWatchStopsServingWhenRunReturns asserts the boundary of the staleness contract. Staying
// bootstrapped covers watch failures within a run. It does not cover the end of the run itself,
// after which nothing is left to recover the view and it would drift without limit.
func TestWatchStopsServingWhenRunReturns(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	// the script bootstraps and then goes quiet, so the attempt sits on the event channel
	st := &scriptedState{events: []state.Event{
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
		{Type: state.Bootstrapped},
	}}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, st) }()

	require.Eventually(t, func() bool {
		_, bootstrapped := w.Snapshot()

		return bootstrapped
	}, 10*time.Second, 5*time.Millisecond)

	cancel()
	require.NoError(t, <-done)

	items, bootstrapped := w.Snapshot()
	assert.False(t, bootstrapped, "a stopped collector must not keep serving its last view")
	assert.Len(t, items, 1, "the view itself is kept, only taken out of service")
}

// TestWatchStaysBootstrappedAcrossFailures asserts the core staleness contract: once the initial
// sync has completed, a later watch failure leaves the view serving (stale) rather than absent.
func TestWatchStaysBootstrappedAcrossFailures(t *testing.T) {
	t.Parallel()

	w := newTestWatch()

	require.ErrorContains(t, runScript(
		t, w,
		pathEvent(state.Created, "path-1", resource.VersionUndefined),
		state.Event{Type: state.Bootstrapped},
		terminalErrored(),
	), "end of script")

	// a later attempt that never reaches its own Bootstrapped event
	require.ErrorContains(t, runScript(
		t, w,
		pathEvent(state.Created, "path-2", resource.VersionUndefined),
		terminalErrored(),
	), "end of script")

	items, bootstrapped := w.Snapshot()
	assert.True(t, bootstrapped, "a failed resync must not take the view out of service")
	assert.Len(t, items, 1, "the previously synced view must keep serving")
}
