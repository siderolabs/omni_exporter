// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector

import (
	"time"

	"github.com/cosi-project/runtime/pkg/state"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/siderolabs/omni_exporter/pkg/collector/internal/watch"
)

// Hooks for the external test package.
var (
	ToSnakeCase   = toSnakeCase
	NewEnumValues = newEnumValues
)

// EnumValues exposes the enum expansion to the external test package.
type EnumValues = enumValues

// Send emits one 0/1 series per known enum value, marking the current value with 1.
func (e *enumValues) Send(add watch.MetricSink, desc *prometheus.Desc, current int32, labelValues ...string) {
	e.send(add, desc, current, labelValues...)
}

// NewWithTimings creates a [Collector] with custom watch loop delays, for testing.
func NewWithTimings(st state.State, options Options, establishTimeout, backoffMin, backoffMax time.Duration) *Collector {
	return newCollector(st, options, timings{
		watch: watch.Timings{
			EstablishTimeout: establishTimeout,
			BackoffMin:       backoffMin,
			BackoffMax:       backoffMax,
		},
		probeTimeout: time.Second,
	})
}
