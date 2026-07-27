// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/siderolabs/omni/client/api/omni/specs"
	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"

	"github.com/siderolabs/omni_exporter/pkg/collector/internal/watch"
)

// newTalosUpgradeCollector collects the per-cluster Talos upgrade metrics.
func newTalosUpgradeCollector(config watch.Config) resourceCollector {
	phaseDesc := prometheus.NewDesc(Prefix+"cluster_talos_upgrade_phase",
		"Whether the Talos upgrade of the cluster is in the given phase.", []string{"cluster_id", "phase"}, nil)

	phases := newEnumValues(specs.TalosUpgradeStatusSpec_Phase_name, "")

	return watch.New(config, "talos_upgrades", omni.NewTalosUpgradeStatus(""), []*prometheus.Desc{phaseDesc},
		func(items []*omni.TalosUpgradeStatus, add watch.MetricSink) {
			for _, status := range items {
				phases.send(add, phaseDesc, int32(status.TypedSpec().Value.Phase), status.Metadata().ID())
			}
		})
}

// newKubernetesUpgradeCollector collects the per-cluster Kubernetes upgrade metrics.
func newKubernetesUpgradeCollector(config watch.Config) resourceCollector {
	phaseDesc := prometheus.NewDesc(Prefix+"cluster_kubernetes_upgrade_phase",
		"Whether the Kubernetes upgrade of the cluster is in the given phase.", []string{"cluster_id", "phase"}, nil)

	phases := newEnumValues(specs.KubernetesUpgradeStatusSpec_Phase_name, "")

	return watch.New(config, "kubernetes_upgrades", omni.NewKubernetesUpgradeStatus(""), []*prometheus.Desc{phaseDesc},
		func(items []*omni.KubernetesUpgradeStatus, add watch.MetricSink) {
			for _, status := range items {
				phases.send(add, phaseDesc, int32(status.TypedSpec().Value.Phase), status.Metadata().ID())
			}
		})
}
