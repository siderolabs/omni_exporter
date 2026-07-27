// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/siderolabs/go-api-signature/pkg/pgp"
	"github.com/siderolabs/go-api-signature/pkg/serviceaccount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/siderolabs/omni_exporter/internal/app"
)

// TestRunSmoke runs the complete exporter against an unreachable Omni endpoint and asserts that
// the scrape still succeeds over HTTP, reporting the failure through the up metric.
func TestRunSmoke(t *testing.T) {
	key, err := pgp.GenerateKey("omni-exporter-test", "", "omni-exporter-test@example.org", time.Hour)
	require.NoError(t, err)

	serviceAccountKey, err := serviceaccount.Encode("omni-exporter-test", key)
	require.NoError(t, err)

	t.Setenv(serviceaccount.OmniServiceAccountKeyEnvVar, serviceAccountKey)

	address := freeListenAddress(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var eg errgroup.Group

	eg.Go(func() error {
		return app.Run(ctx, []string{
			"--omni.endpoint=https://" + address, // nothing listens here, the watches will keep failing
			"--web.listen-address=" + address,
			"--log.level=error",
		})
	})

	var metrics string

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		metrics = mustGet(collect, ctx, "http://"+address+"/metrics")
	}, 10*time.Second, 100*time.Millisecond)

	assert.Contains(t, metrics, "omni_exporter_up 0")
	assert.Contains(t, metrics, "omni_exporter_reachable 0")
	assert.Contains(t, metrics, `omni_exporter_collector_success{collector="machines"} 0`)
	assert.Contains(t, metrics, "omni_exporter_build_info")
	assert.Contains(t, metrics, "go_goroutines")

	// the watch loop must keep retrying, visible in the attempts counter
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Regexp(collect, `omni_exporter_collector_watch_attempts_total\{collector="machines"\} ([2-9]|\d{2,})`,
			mustGet(collect, ctx, "http://"+address+"/metrics"))
	}, 15*time.Second, 250*time.Millisecond)

	landing := mustGet(t, ctx, "http://"+address+"/")
	assert.Contains(t, landing, "Omni Exporter")

	cancel()

	require.NoError(t, eg.Wait())
}

func mustGet(t require.TestingT, ctx context.Context, url string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)

	defer response.Body.Close() //nolint:errcheck

	require.Equal(t, http.StatusOK, response.StatusCode)

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return string(body)
}

// freeListenAddress returns a localhost address with a free port.
func freeListenAddress(t *testing.T) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()

	require.NoError(t, listener.Close())

	return address
}
