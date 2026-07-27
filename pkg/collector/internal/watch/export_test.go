// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package watch

import (
	"context"
	"maps"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
)

// WatchOnceSafe runs a single watch attempt, which [Collector.Run] otherwise only does inside
// its retry loop. Driving one attempt at a time is what lets the event handling be asserted
// synchronously, with no sleeps or timing assumptions.
func (w *Collector[T]) WatchOnceSafe(ctx context.Context, st state.CoreState) (bool, error) {
	return w.watchOnceSafe(ctx, st)
}

// Snapshot returns a copy of the served view and whether its initial sync has completed.
func (w *Collector[T]) Snapshot() (map[resource.ID]T, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	items := make(map[resource.ID]T, len(w.active))
	maps.Copy(items, w.active)

	return items, w.bootstrapped
}

// BreakEventCounter drops the event counter, so that applying a live event panics.
// It exists to exercise the recovery boundary around the event handling.
func (w *Collector[T]) BreakEventCounter() {
	w.events = nil
}
