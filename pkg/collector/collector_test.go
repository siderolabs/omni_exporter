// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	v1alpha1 "github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	protobufclient "github.com/cosi-project/runtime/pkg/state/protobuf/client"
	protobufserver "github.com/cosi-project/runtime/pkg/state/protobuf/server"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/siderolabs/omni_exporter/pkg/collector"
)

var updateGolden = flag.Bool("update", false, "update golden files")

const goldenPath = "testdata/collect.prom"

// startCollector builds a collector with test timings over the given state and runs it until the test ends.
func startCollector(t *testing.T, st state.State) *collector.Collector {
	t.Helper()

	c := collector.NewWithTimings(st, collector.Options{}, 2*time.Second, 10*time.Millisecond, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())

	var eg errgroup.Group

	eg.Go(func() error {
		return c.Run(ctx)
	})

	t.Cleanup(func() {
		cancel()

		require.NoError(t, eg.Wait())
	})

	return c
}

// requireEventuallyUp waits until the collector reports up=1.
func requireEventuallyUp(t *testing.T, c *collector.Collector) {
	t.Helper()

	expected := `
		# HELP omni_exporter_up Whether Omni is reachable and every collector has completed its initial sync and renders successfully.
		# TYPE omni_exporter_up gauge
		omni_exporter_up 1
	`

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.NoError(collect, testutil.CollectAndCompare(c, bytes.NewReader([]byte(expected)), "omni_exporter_up"))
	}, 10*time.Second, 20*time.Millisecond)
}

// TestCollectGolden asserts the complete metrics output for a fixed state against a golden file.
//
// Run with -update to regenerate the golden file.
func TestCollectGolden(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(newTestCoreState(ctx, t))
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	output := collectDeterministic(t, c)

	if *updateGolden {
		require.NoError(t, os.WriteFile(goldenPath, output, 0o644))
	}

	golden, err := os.ReadFile(goldenPath)
	require.NoError(t, err)

	require.Equal(t, string(golden), string(output))
}

// TestCollectLint runs the promlint checks over the complete metrics output.
func TestCollectLint(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(newTestCoreState(ctx, t))
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	problems, err := testutil.CollectAndLint(c)
	require.NoError(t, err)
	assert.Empty(t, problems)
}

// TestCollectEventDriven asserts that resource changes propagate into the metrics:
// updates are reflected, created resources appear, destroyed resources disappear.
func TestCollectEventDriven(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(newTestCoreState(ctx, t))
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	// update: machine-1 disconnects
	machineStatus, err := safe.StateGetByID[*omni.MachineStatus](ctx, st, "machine-1")
	require.NoError(t, err)

	machineStatus.TypedSpec().Value.Connected = false
	require.NoError(t, st.Update(ctx, machineStatus))

	requireEventuallyContains(t, c, `omni_exporter_machine_connected{machine_id="machine-1"} 0`)

	// create: a new machine appears
	machineStatus3 := omni.NewMachineStatus("machine-3")
	machineStatus3.TypedSpec().Value.Connected = true
	require.NoError(t, st.Create(ctx, machineStatus3))

	requireEventuallyContains(t, c, `omni_exporter_machine_connected{machine_id="machine-3"} 1`)

	// destroy: the machine disappears
	require.NoError(t, st.Destroy(ctx, machineStatus3.Metadata()))

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.NotContains(collect, gatherText(collect, c), `machine_id="machine-3"`)
	}, 10*time.Second, 20*time.Millisecond)
}

// TestCollectPartialSync asserts that one resource type failing to establish its watch
// degrades only that collector, while the others sync and serve.
func TestCollectPartialSync(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st := state.WrapCore(&hookedState{
		CoreState: newTestCoreState(ctx, t),
		onWatchKind: func(ctx context.Context, kind resource.Kind) error {
			if kind.Type() == omni.MachineStatusType { // never establishes
				<-ctx.Done()

				return ctx.Err()
			}

			return nil
		},
	})

	c := startCollector(t, st)

	expected := `
		# HELP omni_exporter_up Whether Omni is reachable and every collector has completed its initial sync and renders successfully.
		# TYPE omni_exporter_up gauge
		omni_exporter_up 0
		# HELP omni_exporter_cluster_info Cluster information.
		# TYPE omni_exporter_cluster_info gauge
		omni_exporter_cluster_info{cluster_id="talos-default",kubernetes_version="1.34.1",talos_version="1.11.2"} 1
	`

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.NoError(collect, testutil.CollectAndCompare(c, bytes.NewReader([]byte(expected)),
			"omni_exporter_up", "omni_exporter_cluster_info"))

		text := gatherText(collect, c)
		assert.Contains(collect, text, `omni_exporter_collector_success{collector="machines"} 0`)
		assert.Contains(collect, text, `omni_exporter_collector_success{collector="clusters"} 1`)
	}, 10*time.Second, 20*time.Millisecond)
}

// TestCollectUnreachableSuppression asserts that a failing reachability probe suppresses all
// object metrics for the scrape (they must not be served stale as if current), keeps the exporter
// self-metrics, and everything returns once Omni answers again.
func TestCollectUnreachableSuppression(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	var unreachable atomic.Bool

	st := state.WrapCore(&hookedState{
		CoreState: newTestCoreState(ctx, t),
		onGet: func(_ context.Context, _ resource.Pointer) error {
			if unreachable.Load() {
				return errors.New("induced unreachability")
			}

			return nil
		},
	})

	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	unreachable.Store(true)

	text := gatherText(t, c)
	assert.Contains(t, text, "omni_exporter_reachable 0")
	assert.Contains(t, text, "omni_exporter_up 0")
	assert.Contains(t, text, `omni_exporter_collector_success{collector="machines"} 0`)
	assert.NotContains(t, text, "omni_exporter_machine_connected", "object metrics must be suppressed")
	assert.Contains(t, text, `omni_exporter_collector_cached_resources{collector="machines"} 2`,
		"the cache self-metrics must stay")

	unreachable.Store(false)

	text = gatherText(t, c)
	assert.Contains(t, text, "omni_exporter_reachable 1")
	assert.Contains(t, text, "omni_exporter_up 1")
	assert.Contains(t, text, `omni_exporter_machine_connected{machine_id="machine-1"} 1`)
}

// TestCollectWatchRecovery asserts that a watch failing at establishment recovers by retrying,
// and the failure is visible in the attempts counter.
func TestCollectWatchRecovery(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	var failures atomic.Int32

	st := state.WrapCore(&hookedState{
		CoreState: newTestCoreState(ctx, t),
		onWatchKind: func(_ context.Context, kind resource.Kind) error {
			if kind.Type() == omni.MachineStatusType && failures.Add(1) <= 2 {
				return errors.New("induced establishment failure")
			}

			return nil
		},
	})

	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	text := gatherText(t, c)
	assert.Contains(t, text, `omni_exporter_collector_watch_attempts_total{collector="machines"} 3`)
	assert.Contains(t, text, `omni_exporter_collector_watch_attempts_total{collector="clusters"} 1`)
}

// erroredInjectingState forwards watch events, injecting a single Errored event after the bootstrap,
// simulating an asynchronous watch failure (e.g. an expired bookmark reported by the backend).
type erroredInjectingState struct {
	state.CoreState

	injected atomic.Bool
}

func (s *erroredInjectingState) WatchKind(ctx context.Context, kind resource.Kind, ch chan<- state.Event, opts ...state.WatchKindOption) error {
	if kind.Type() != omni.MachineStatusType || !s.injected.CompareAndSwap(false, true) {
		return s.CoreState.WatchKind(ctx, kind, ch, opts...)
	}

	innerCh := make(chan state.Event)

	if err := s.CoreState.WatchKind(ctx, kind, innerCh, opts...); err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-innerCh:
				select {
				case ch <- event:
				case <-ctx.Done():
					return
				}

				if event.Type == state.Bootstrapped {
					select {
					case ch <- state.Event{Type: state.Errored, Error: errors.New("induced asynchronous failure")}:
					case <-ctx.Done():
					}

					return
				}
			}
		}
	}()

	return nil
}

// TestCollectAsyncErroredRecovery asserts that an asynchronous Errored event after a completed
// sync triggers a re-bootstrap which is visible in the counter, while the view keeps serving
// throughout: a watch failure makes the metrics stale for a moment, never absent.
func TestCollectAsyncErroredRecovery(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(&erroredInjectingState{CoreState: newTestCoreState(ctx, t)})
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	// the re-bootstrap happens behind the scrapes; TestWatchStaysBootstrappedAcrossFailures is
	// what proves no scrape observes a gap, since a polling loop would retry straight past one
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		text := gatherText(collect, c)

		assert.Contains(collect, text, `omni_exporter_machine_connected{machine_id="machine-1"} 1`)
		assert.Contains(collect, text, `omni_exporter_collector_success{collector="machines"} 1`)
		assert.Contains(collect, text, `omni_exporter_collector_watch_attempts_total{collector="machines"} 2`)
	}, 10*time.Second, 20*time.Millisecond)
}

// TestCollectStoreNotReady asserts that a backup store configuration error is reported.
func TestCollectStoreNotReady(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(namespaced.NewState(inmem.Build))

	overallStatus := omni.NewEtcdBackupOverallStatus()
	overallStatus.TypedSpec().Value.ConfigurationName = "s3"
	overallStatus.TypedSpec().Value.ConfigurationError = "bucket is not accessible"
	require.NoError(t, st.Create(ctx, overallStatus))

	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	expected := `
		# HELP omni_exporter_etcd_backup_store_info Etcd backup store information. The configuration_name label reflects the configured store type, e.g. s3 or disabled.
		# TYPE omni_exporter_etcd_backup_store_info gauge
		omni_exporter_etcd_backup_store_info{configuration_name="s3"} 1
		# HELP omni_exporter_etcd_backup_store_ready Whether the etcd backup store configuration of the instance has no errors. It is 1 also when backups are disabled altogether.
		# TYPE omni_exporter_etcd_backup_store_ready gauge
		omni_exporter_etcd_backup_store_ready 0
	`

	require.NoError(t, testutil.CollectAndCompare(c, bytes.NewReader([]byte(expected)),
		"omni_exporter_etcd_backup_store_info", "omni_exporter_etcd_backup_store_ready"))
}

// TestCollectConcurrentStorm gathers concurrently while the state churns, verifying (under -race)
// that scrapes and the event loops are safe together.
func TestCollectConcurrentStorm(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(newTestCoreState(ctx, t))
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	registry := prometheus.NewRegistry()
	registry.MustRegister(c)

	var eg errgroup.Group

	stormCtx, stormCancel := context.WithCancel(ctx)

	eg.Go(func() error {
		for i := 0; ; i++ {
			select {
			case <-stormCtx.Done():
				return nil
			default:
			}

			machineStatus := omni.NewMachineStatus(fmt.Sprintf("storm-machine-%d", i%10))
			machineStatus.TypedSpec().Value.Connected = i%2 == 0

			if err := st.Create(stormCtx, machineStatus); err == nil {
				continue
			}

			if destroyErr := st.Destroy(stormCtx, machineStatus.Metadata()); destroyErr != nil && stormCtx.Err() == nil {
				return destroyErr
			}
		}
	})

	for range 4 {
		eg.Go(func() error {
			for range 50 {
				if _, err := registry.Gather(); err != nil {
					return err
				}
			}

			return nil
		})
	}

	time.Sleep(200 * time.Millisecond)
	stormCancel()

	require.NoError(t, eg.Wait())

	// the storm must have flowed through the watch as live events
	assert.Regexp(t, `omni_exporter_collector_events_total\{collector="machines",event="created"\} [1-9]`, gatherText(t, c))
}

// TestRunTwice asserts that concurrent Run calls are rejected.
func TestRunTwice(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := state.WrapCore(newTestCoreState(ctx, t))
	c := startCollector(t, st)

	// make sure the first Run is actually running, so the second call below cannot win the race
	requireEventuallyUp(t, c)

	require.ErrorContains(t, c.Run(ctx), "already running")
}

// TestCollectOverGRPC runs the collector against the same fixed state served over the real
// COSI gRPC protocol, asserting the output matches the same golden file as the direct state access.
func TestCollectOverGRPC(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	coreState := newTestCoreState(ctx, t)

	grpcServer := grpc.NewServer()
	v1alpha1.RegisterStateServer(grpcServer, protobufserver.NewState(coreState))

	listener := bufconn.Listen(1024 * 1024)

	var eg errgroup.Group

	eg.Go(func() error {
		return grpcServer.Serve(listener)
	})

	t.Cleanup(func() {
		grpcServer.Stop()

		require.NoError(t, eg.Wait())
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { conn.Close() }) //nolint:errcheck

	st := state.WrapCore(protobufclient.NewAdapter(v1alpha1.NewStateClient(conn)))
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	golden, err := os.ReadFile(goldenPath)
	require.NoError(t, err)

	require.Equal(t, string(golden), string(collectDeterministic(t, c)))
}

// TestCollectOverGRPCServerRestart asserts that the collector recovers when the gRPC server
// backing the watches goes away and comes back, and the view converges to the same data.
// The client is built exactly as the standalone exporter builds it, transparent watch retry
// included. Which of the two recovery paths runs is deliberately not asserted. Both servers here
// share one backing state, so the bookmark survives and the client resumes outright, while a real
// Omni restart invalidates it and the refusal drives a full re-bootstrap. The contract is that the
// view converges back either way, and that nothing is served while Omni is unreachable.
func TestCollectOverGRPCServerRestart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	coreState := newTestCoreState(ctx, t)

	var listener atomic.Pointer[bufconn.Listener]

	startServer := func() *grpc.Server {
		grpcServer := grpc.NewServer()
		v1alpha1.RegisterStateServer(grpcServer, protobufserver.NewState(coreState))

		lis := bufconn.Listen(1024 * 1024)
		listener.Store(lis)

		go grpcServer.Serve(lis) //nolint:errcheck

		return grpcServer
	}

	server := startServer()

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.Load().DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { conn.Close() }) //nolint:errcheck

	st := state.WrapCore(protobufclient.NewAdapter(v1alpha1.NewStateClient(conn)))
	c := startCollector(t, st)

	requireEventuallyUp(t, c)

	// kill the server, breaking all streams and the probe: the object metrics must be
	// suppressed while the outage is ongoing, not served stale
	server.Stop()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		text := gatherText(collect, c)
		assert.Contains(collect, text, "omni_exporter_up 0")
		assert.NotContains(collect, text, "omni_exporter_machine_connected")
	}, 10*time.Second, 20*time.Millisecond)

	// bring up a replacement: the collector must converge back to a live, correct view
	replacement := startServer()
	t.Cleanup(replacement.Stop)

	requireEventuallyUp(t, c)

	// Reachability alone brings the metrics back, served from the view held across the outage,
	// so asserting a pre-outage value here would pass even against a permanently dead watch.
	// Changing the state after the restart is what proves the stream is live again.
	backingState := state.WrapCore(coreState)

	machineStatus, err := safe.StateGetByID[*omni.MachineStatus](ctx, backingState, "machine-1")
	require.NoError(t, err)

	machineStatus.TypedSpec().Value.Connected = false
	require.NoError(t, backingState.Update(ctx, machineStatus))

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Contains(collect, gatherText(collect, c), `omni_exporter_machine_connected{machine_id="machine-1"} 0`)
	}, 10*time.Second, 20*time.Millisecond)
}

// collectDeterministic renders the complete collector output in the text format,
// dropping only the last-sync timestamps whose values are not deterministic.
func collectDeterministic(t *testing.T, c prometheus.Collector) []byte {
	t.Helper()

	registry := prometheus.NewRegistry()
	registry.MustRegister(c)

	families, err := registry.Gather()
	require.NoError(t, err)

	var buf bytes.Buffer

	encoder := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))

	for _, family := range families {
		if family.GetName() == collector.Prefix+"collector_last_sync_timestamp_seconds" {
			continue
		}

		require.NoError(t, encoder.Encode(family))
	}

	return buf.Bytes()
}

// gatherText renders the full collector output as text for substring assertions.
func gatherText(t require.TestingT, c prometheus.Collector) string {
	registry := prometheus.NewRegistry()
	registry.MustRegister(c)

	families, err := registry.Gather()
	require.NoError(t, err)

	var buf bytes.Buffer

	encoder := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))

	for _, family := range families {
		require.NoError(t, encoder.Encode(family))
	}

	return buf.String()
}

func requireEventuallyContains(t *testing.T, c prometheus.Collector, substring string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Contains(collect, gatherText(collect, c), substring)
	}, 10*time.Second, 20*time.Millisecond)
}
