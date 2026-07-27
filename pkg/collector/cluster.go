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

// newClusterCollector collects per-cluster metrics from the cluster statuses.
func newClusterCollector(config watch.Config) resourceCollector {
	clusterLabels := []string{"cluster_id"}

	phaseDesc := prometheus.NewDesc(Prefix+"cluster_phase",
		"Whether the cluster is in the given phase.", []string{"cluster_id", "phase"}, nil)
	readyDesc := prometheus.NewDesc(Prefix+"cluster_ready",
		"Whether the cluster is ready.", clusterLabels, nil)
	availableDesc := prometheus.NewDesc(Prefix+"cluster_available",
		"Whether the cluster is available.", clusterLabels, nil)
	kubernetesAPIReadyDesc := prometheus.NewDesc(Prefix+"cluster_kubernetes_api_ready",
		"Whether the Kubernetes API of the cluster is ready.", clusterLabels, nil)
	controlPlaneReadyDesc := prometheus.NewDesc(Prefix+"cluster_control_plane_ready",
		"Whether the control plane of the cluster is ready.", clusterLabels, nil)
	machinesDesc := prometheus.NewDesc(Prefix+"cluster_machines",
		"Number of machines in the cluster.", clusterLabels, nil)
	machinesHealthyDesc := prometheus.NewDesc(Prefix+"cluster_machines_healthy",
		"Number of healthy machines in the cluster.", clusterLabels, nil)
	machinesConnectedDesc := prometheus.NewDesc(Prefix+"cluster_machines_connected",
		"Number of machines in the cluster connected to Omni.", clusterLabels, nil)
	machinesRequestedDesc := prometheus.NewDesc(Prefix+"cluster_machines_requested",
		"Number of machines requested for the cluster. It can differ from the number of machines when machine classes are used.",
		clusterLabels, nil)
	infoDesc := prometheus.NewDesc(Prefix+"cluster_info",
		"Cluster information.", []string{"cluster_id", "talos_version", "kubernetes_version"}, nil)

	phases := newEnumValues(specs.ClusterStatusSpec_Phase_name, "")

	descs := []*prometheus.Desc{
		phaseDesc, readyDesc, availableDesc, kubernetesAPIReadyDesc, controlPlaneReadyDesc,
		machinesDesc, machinesHealthyDesc, machinesConnectedDesc, machinesRequestedDesc, infoDesc,
	}

	return watch.New(config, "clusters", omni.NewClusterStatus(""), descs,
		func(items []*omni.ClusterStatus, add watch.MetricSink) {
			for _, status := range items {
				clusterID := status.Metadata().ID()
				spec := status.TypedSpec().Value

				phases.send(add, phaseDesc, int32(spec.Phase), clusterID)

				machines := spec.GetMachines()

				add(prometheus.MustNewConstMetric(readyDesc, prometheus.GaugeValue, boolValue(spec.Ready), clusterID))
				add(prometheus.MustNewConstMetric(availableDesc, prometheus.GaugeValue, boolValue(spec.Available), clusterID))
				add(prometheus.MustNewConstMetric(kubernetesAPIReadyDesc, prometheus.GaugeValue, boolValue(spec.KubernetesAPIReady), clusterID))
				add(prometheus.MustNewConstMetric(controlPlaneReadyDesc, prometheus.GaugeValue, boolValue(spec.ControlplaneReady), clusterID))
				add(prometheus.MustNewConstMetric(machinesDesc, prometheus.GaugeValue, float64(machines.GetTotal()), clusterID))
				add(prometheus.MustNewConstMetric(machinesHealthyDesc, prometheus.GaugeValue, float64(machines.GetHealthy()), clusterID))
				add(prometheus.MustNewConstMetric(machinesConnectedDesc, prometheus.GaugeValue, float64(machines.GetConnected()), clusterID))
				add(prometheus.MustNewConstMetric(machinesRequestedDesc, prometheus.GaugeValue, float64(machines.GetRequested()), clusterID))
				add(prometheus.MustNewConstMetric(infoDesc, prometheus.GaugeValue, 1, clusterID, spec.TalosVersion, spec.KubernetesVersion))
			}
		})
}
