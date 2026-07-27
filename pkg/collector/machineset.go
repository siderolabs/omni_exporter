// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/siderolabs/omni/client/api/omni/specs"
	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"

	"github.com/siderolabs/omni_exporter/pkg/collector/internal/watch"
)

// newMachineSetCollector collects per-machine-set metrics from the machine set statuses.
func newMachineSetCollector(config watch.Config) resourceCollector {
	labels := []string{"cluster_id", "machine_set_id"}

	phaseDesc := prometheus.NewDesc(Prefix+"machine_set_phase",
		"Whether the machine set is in the given phase.", slices.Concat(labels, []string{"phase"}), nil)
	readyDesc := prometheus.NewDesc(Prefix+"machine_set_ready",
		"Whether the machine set is ready.", labels, nil)
	machinesDesc := prometheus.NewDesc(Prefix+"machine_set_machines",
		"Number of machines in the machine set.", labels, nil)
	machinesHealthyDesc := prometheus.NewDesc(Prefix+"machine_set_machines_healthy",
		"Number of healthy machines in the machine set.", labels, nil)
	machinesConnectedDesc := prometheus.NewDesc(Prefix+"machine_set_machines_connected",
		"Number of machines in the machine set connected to Omni.", labels, nil)
	machinesRequestedDesc := prometheus.NewDesc(Prefix+"machine_set_machines_requested",
		"Number of machines requested for the machine set. It can differ from the number of machines when machine classes are used.",
		labels, nil)

	phases := newEnumValues(specs.MachineSetPhase_name, "")

	descs := []*prometheus.Desc{
		phaseDesc, readyDesc, machinesDesc, machinesHealthyDesc, machinesConnectedDesc, machinesRequestedDesc,
	}

	return watch.New(config, "machine_sets", omni.NewMachineSetStatus(""), descs,
		func(items []*omni.MachineSetStatus, add watch.MetricSink) {
			for _, status := range items {
				machineSetID := status.Metadata().ID()
				clusterID, _ := status.Metadata().Labels().Get(omni.LabelCluster)
				spec := status.TypedSpec().Value

				phases.send(add, phaseDesc, int32(spec.Phase), clusterID, machineSetID)

				machines := spec.GetMachines()

				add(prometheus.MustNewConstMetric(readyDesc, prometheus.GaugeValue, boolValue(spec.Ready), clusterID, machineSetID))
				add(prometheus.MustNewConstMetric(machinesDesc, prometheus.GaugeValue, float64(machines.GetTotal()), clusterID, machineSetID))
				add(prometheus.MustNewConstMetric(machinesHealthyDesc, prometheus.GaugeValue, float64(machines.GetHealthy()), clusterID, machineSetID))
				add(prometheus.MustNewConstMetric(machinesConnectedDesc, prometheus.GaugeValue, float64(machines.GetConnected()), clusterID, machineSetID))
				add(prometheus.MustNewConstMetric(machinesRequestedDesc, prometheus.GaugeValue, float64(machines.GetRequested()), clusterID, machineSetID))
			}
		})
}
