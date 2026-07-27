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

// newMachineCollector collects per-machine metrics from the machine statuses.
//
// State metrics carry only the machine_id label. Human-readable and mutable machine attributes
// are exposed on the info metric instead, to be joined in queries when needed, so that e.g.
// a hostname change does not churn the state series.
func newMachineCollector(config watch.Config) resourceCollector {
	connectedDesc := prometheus.NewDesc(Prefix+"machine_connected",
		"Whether the machine is connected to Omni.", []string{"machine_id"}, nil)
	powerStateDesc := prometheus.NewDesc(Prefix+"machine_power_state",
		"Whether the machine is in the given power state.", []string{"machine_id", "state"}, nil)
	infoDesc := prometheus.NewDesc(Prefix+"machine_info",
		"Machine information. The cluster_id label is empty for machines not allocated to a cluster.",
		[]string{"machine_id", "cluster_id", "role", "hostname", "management_address", "talos_version", "platform", "arch"}, nil)

	powerStates := newEnumValues(specs.MachineStatusSpec_PowerState_name, "POWER_STATE_")

	descs := []*prometheus.Desc{connectedDesc, powerStateDesc, infoDesc}

	return watch.New(config, "machines", omni.NewMachineStatus(""), descs,
		func(items []*omni.MachineStatus, add watch.MetricSink) {
			for _, status := range items {
				machineID := status.Metadata().ID()
				spec := status.TypedSpec().Value

				add(prometheus.MustNewConstMetric(connectedDesc, prometheus.GaugeValue, boolValue(spec.Connected), machineID))

				powerStates.send(add, powerStateDesc, int32(spec.PowerState), machineID)

				hostname := spec.GetNetwork().GetHostname()
				if hostname == "" {
					// same fallback order Omni itself uses when matching machines by hostname
					hostname = spec.GetPlatformMetadata().GetHostname()
				}

				add(prometheus.MustNewConstMetric(
					infoDesc, prometheus.GaugeValue, 1,
					machineID,
					spec.Cluster,
					toSnakeCase(spec.Role.String()),
					hostname,
					spec.ManagementAddress,
					spec.TalosVersion,
					spec.GetPlatformMetadata().GetPlatform(),
					spec.GetHardware().GetArch(),
				))
			}
		})
}
