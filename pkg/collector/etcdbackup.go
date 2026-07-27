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

// newEtcdBackupConfigCollector collects the per-cluster etcd backup configuration metrics
// from the cluster definitions.
//
// A backup status resource only exists after the first backup attempt of a cluster,
// so the enabled metric is collected from the cluster definitions instead:
// enabled=1 with no status series means backups are enabled but none was attempted yet.
func newEtcdBackupConfigCollector(config watch.Config) resourceCollector {
	enabledDesc := prometheus.NewDesc(Prefix+"cluster_etcd_backup_enabled",
		"Whether etcd backups are enabled for the cluster.", []string{"cluster_id"}, nil)

	return watch.New(config, "etcd_backup_configs", omni.NewCluster(""), []*prometheus.Desc{enabledDesc},
		func(items []*omni.Cluster, add watch.MetricSink) {
			for _, cluster := range items {
				enabled := cluster.TypedSpec().Value.GetBackupConfiguration().GetEnabled()

				add(prometheus.MustNewConstMetric(enabledDesc, prometheus.GaugeValue, boolValue(enabled), cluster.Metadata().ID()))
			}
		})
}

// newEtcdBackupCollector collects the per-cluster etcd backup status metrics.
func newEtcdBackupCollector(config watch.Config) resourceCollector {
	clusterLabels := []string{"cluster_id"}

	statusDesc := prometheus.NewDesc(Prefix+"cluster_etcd_backup_status",
		"Whether the last etcd backup of the cluster is in the given status.", []string{"cluster_id", "status"}, nil)
	lastSuccessDesc := prometheus.NewDesc(Prefix+"cluster_etcd_backup_last_success_timestamp_seconds",
		"Unix timestamp of the last successful etcd backup of the cluster.", clusterLabels, nil)
	lastAttemptDesc := prometheus.NewDesc(Prefix+"cluster_etcd_backup_last_attempt_timestamp_seconds",
		"Unix timestamp of the last etcd backup attempt of the cluster.", clusterLabels, nil)

	statuses := newEnumValues(specs.EtcdBackupStatusSpec_Status_name, "")

	descs := []*prometheus.Desc{statusDesc, lastSuccessDesc, lastAttemptDesc}

	return watch.New(config, "etcd_backups", omni.NewEtcdBackupStatus(""), descs,
		func(items []*omni.EtcdBackupStatus, add watch.MetricSink) {
			for _, status := range items {
				clusterID := status.Metadata().ID()
				spec := status.TypedSpec().Value

				statuses.send(add, statusDesc, int32(spec.Status), clusterID)

				if lastBackup := spec.GetLastBackupTime(); lastBackup != nil {
					add(prometheus.MustNewConstMetric(lastSuccessDesc, prometheus.GaugeValue, float64(lastBackup.AsTime().Unix()), clusterID))
				}

				if lastAttempt := spec.GetLastBackupAttempt(); lastAttempt != nil {
					add(prometheus.MustNewConstMetric(lastAttemptDesc, prometheus.GaugeValue, float64(lastAttempt.AsTime().Unix()), clusterID))
				}
			}
		})
}

// newEtcdBackupStoreCollector collects the instance-wide etcd backup store metrics.
func newEtcdBackupStoreCollector(config watch.Config) resourceCollector {
	infoDesc := prometheus.NewDesc(Prefix+"etcd_backup_store_info",
		"Etcd backup store information. The configuration_name label reflects the configured store type, e.g. s3 or disabled.",
		[]string{"configuration_name"}, nil)
	readyDesc := prometheus.NewDesc(Prefix+"etcd_backup_store_ready",
		"Whether the etcd backup store configuration of the instance has no errors. It is 1 also when backups are disabled altogether.",
		nil, nil)

	return watch.New(config, "etcd_backup_store", omni.NewEtcdBackupOverallStatus(), []*prometheus.Desc{infoDesc, readyDesc},
		func(items []*omni.EtcdBackupOverallStatus, add watch.MetricSink) {
			for _, overallStatus := range items {
				spec := overallStatus.TypedSpec().Value

				add(prometheus.MustNewConstMetric(infoDesc, prometheus.GaugeValue, 1, spec.GetConfigurationName()))
				add(prometheus.MustNewConstMetric(readyDesc, prometheus.GaugeValue, boolValue(spec.GetConfigurationError() == "")))
			}
		})
}
