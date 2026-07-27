// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/siderolabs/omni_exporter/pkg/collector"
)

func TestToSnakeCase(t *testing.T) {
	t.Parallel()

	for input, expected := range map[string]string{
		"UNKNOWN":                   "unknown",
		"SCALING_UP":                "scaling_up",
		"ScalingUp":                 "scaling_up",
		"Ok":                        "ok",
		"Running":                   "running",
		"UpdatingMachineSchematics": "updating_machine_schematics",
		"ON":                        "on",
	} {
		assert.Equal(t, expected, collector.ToSnakeCase(input), "input: %s", input)
	}
}

// TestEnumValuesSend asserts that all known enum values are materialized as 0/1 series and that
// a value unknown to this build is emitted with its number instead of being dropped.
func TestEnumValuesSend(t *testing.T) {
	t.Parallel()

	enum := collector.NewEnumValues(map[int32]string{
		0: "STATE_UNKNOWN",
		1: "STATE_OK",
		2: "STATE_ERROR",
	}, "STATE_")

	desc := prometheus.NewDesc("test_metric", "Test metric.", []string{"id", "state"}, nil)

	collected := collectEnum(t, enum, desc, 1)
	assert.Equal(t, map[string]float64{"unknown": 0, "ok": 1, "error": 0}, collected)

	collected = collectEnum(t, enum, desc, 5)
	assert.Equal(t, map[string]float64{"unknown": 0, "ok": 0, "error": 0, "5": 1}, collected)
}

func collectEnum(t *testing.T, enum *collector.EnumValues, desc *prometheus.Desc, current int32) map[string]float64 {
	t.Helper()

	ch := make(chan prometheus.Metric, 16)

	enum.Send(func(metric prometheus.Metric) { ch <- metric }, desc, current, "some-id")
	close(ch)

	collected := map[string]float64{}

	for metric := range ch {
		var m dto.Metric

		require.NoError(t, metric.Write(&m))

		require.Len(t, m.GetLabel(), 2)
		assert.Equal(t, "some-id", m.GetLabel()[0].GetValue())

		collected[m.GetLabel()[1].GetValue()] = m.GetGauge().GetValue()
	}

	return collected
}
