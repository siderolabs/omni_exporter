// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/siderolabs/omni_exporter/pkg/collector/internal/watch"
)

// enumValues maps a protobuf enum to normalized metric label values.
//
// All known enum values are materialized as separate series with a 0/1 value on every collection
// (the kube-state-metrics pattern), so that state transitions never make series appear or disappear
// and alerting can rely on simple "== 1" expressions.
type enumValues struct {
	byNumber map[int32]string
	numbers  []int32
}

// newEnumValues builds enumValues from a protobuf enum name map (the generated <Enum>_name map).
//
// The trimPrefix is stripped from the protobuf enum value names before normalization,
// e.g. "POWER_STATE_ON" with trimPrefix "POWER_STATE_" becomes "on".
func newEnumValues(names map[int32]string, trimPrefix string) *enumValues {
	e := &enumValues{
		byNumber: make(map[int32]string, len(names)),
		numbers:  make([]int32, 0, len(names)),
	}

	for number, name := range names {
		e.byNumber[number] = toSnakeCase(strings.TrimPrefix(name, trimPrefix))
		e.numbers = append(e.numbers, number)
	}

	slices.Sort(e.numbers)

	return e
}

// send emits one 0/1 series per known enum value, marking the current value with 1.
//
// A current value unknown to this build (e.g., sent by a newer Omni) is emitted as an extra series
// with the numeric value as the label, instead of being silently dropped.
func (e *enumValues) send(add watch.MetricSink, desc *prometheus.Desc, current int32, labelValues ...string) {
	known := false

	for _, number := range e.numbers {
		value := 0.0
		if number == current {
			value = 1.0
			known = true
		}

		add(prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, slices.Concat(labelValues, []string{e.byNumber[number]})...))
	}

	if !known {
		add(prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1.0, slices.Concat(labelValues, []string{strconv.FormatInt(int64(current), 10)})...))
	}
}

// toSnakeCase normalizes protobuf enum value names of any style ("SCALING_UP", "ScalingUp", "Ok")
// to lowercase snake case.
func toSnakeCase(s string) string {
	if s == strings.ToUpper(s) {
		return strings.ToLower(s)
	}

	var b strings.Builder

	b.Grow(len(s) + 5)

	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}

			b.WriteRune(unicode.ToLower(r))

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}
