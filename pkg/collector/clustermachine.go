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

// newClusterMachineCollector collects metrics of the machines allocated to a cluster.
func newClusterMachineCollector(config watch.Config) resourceCollector {
	labels := []string{"cluster_id", "machine_set_id", "machine_id"}

	stageDesc := prometheus.NewDesc(Prefix+"cluster_machine_stage",
		"Whether the cluster machine is in the given stage.", slices.Concat(labels, []string{"stage"}), nil)
	readyDesc := prometheus.NewDesc(Prefix+"cluster_machine_ready",
		"Whether the cluster machine is ready.", labels, nil)

	stages := newEnumValues(specs.ClusterMachineStatusSpec_Stage_name, "")

	return watch.New(config, "cluster_machines", omni.NewClusterMachineStatus(""), []*prometheus.Desc{stageDesc, readyDesc},
		func(items []*omni.ClusterMachineStatus, add watch.MetricSink) {
			for _, status := range items {
				machineID := status.Metadata().ID()
				clusterID, _ := status.Metadata().Labels().Get(omni.LabelCluster)
				machineSetID, _ := status.Metadata().Labels().Get(omni.LabelMachineSet)
				spec := status.TypedSpec().Value

				stages.send(add, stageDesc, int32(spec.Stage), clusterID, machineSetID, machineID)

				add(prometheus.MustNewConstMetric(readyDesc, prometheus.GaugeValue, boolValue(spec.Ready), clusterID, machineSetID, machineID))
			}
		})
}
