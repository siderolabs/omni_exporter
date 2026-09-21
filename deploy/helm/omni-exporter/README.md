# omni-exporter

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat) ![AppVersion: v0.1.0](https://img.shields.io/badge/AppVersion-v0.1.0-informational?style=flat)

A Helm chart to deploy the Prometheus exporter for Sidero Omni

**Homepage:** <https://github.com/siderolabs/omni_exporter>

## Quick start

Create a read-only Omni service account (the `Reader` role is enough):

```sh
omnictl serviceaccount create --use-user-role=false --role=Reader omni-exporter
```

Store the printed key in a secret and install the chart against your Omni endpoint:

```sh
kubectl create secret generic omni-exporter-key \
  --from-literal=OMNI_SERVICE_ACCOUNT_KEY=<key>

helm install omni-exporter oci://ghcr.io/siderolabs/charts/omni-exporter \
  --set omni.endpoint=https://<account>.omni.siderolabs.io \
  --set omni.serviceAccountKey.existingSecret=omni-exporter-key \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.release=<prometheus-release>
```

A default kube-prometheus-stack installation only selects ServiceMonitors with a `release` label that matches its own release name.
Set the same label in `prometheusRule.labels` when enabling the alerts.

To scrape several Omni instances, deploy one release per instance.
The bundled dashboard selects them by their `job` label.

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Sidero Labs | <info@siderolabs.com> | <https://www.siderolabs.com> |

## Source Code

* <https://github.com/siderolabs/omni_exporter>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity. |
| dashboards.annotations | object | `{}` | Annotations of the dashboard ConfigMap (e.g., the folder annotation of the sidecar). |
| dashboards.enabled | bool | `false` | Create a ConfigMap with the Grafana dashboard, labeled for the Grafana sidecar to pick up. |
| dashboards.label | string | `"grafana_dashboard"` | Label that the Grafana sidecar watches for. |
| dashboards.labelValue | string | `"1"` | Value of the sidecar label. |
| dashboards.namespace | string | `""` | Namespace of the dashboard ConfigMap, for a sidecar that only watches its own namespace. Defaults to the release namespace. |
| extraArgs | list | `[]` |  |
| extraEnv | list | `[]` | Extra environment variables for the exporter container. |
| extraVolumeMounts | list | `[]` | Extra volume mounts for the exporter container. |
| extraVolumes | list | `[]` | Extra volumes for the pod. |
| fullnameOverride | string | `""` | Override the full name of the release resources. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/siderolabs/omni_exporter"` | Image repository. |
| image.tag | string | `""` | Image tag. Defaults to the chart appVersion. |
| imagePullSecrets | list | `[]` | Image pull secrets. |
| livenessProbe | object | `{"httpGet":{"path":"/","port":"metrics"},"periodSeconds":30}` | Liveness probe. It checks the landing page, not `/metrics`, so that it does not depend on Omni and does not collect the metrics. |
| nameOverride | string | `""` | Override the chart name. |
| nodeSelector | object | `{}` | Node selector. |
| omni.endpoint | string | `""` | Omni API endpoint (e.g., `https://<account>.omni.siderolabs.io`). Required. |
| omni.serviceAccountKey.existingSecret | string | `""` | Name of an existing Secret holding the key. The exporter reads the key only at startup, so restart it after rotating the key. |
| omni.serviceAccountKey.existingSecretKey | string | `"OMNI_SERVICE_ACCOUNT_KEY"` | Key of the service account key within the existing Secret. |
| omni.serviceAccountKey.value | string | `""` | The key itself, stored in a Secret created by the chart. Used only when `existingSecret` is empty. |
| podAnnotations | object | `{}` | Pod annotations. |
| podLabels | object | `{}` | Pod labels. |
| podSecurityContext | object | `{"fsGroup":65532,"runAsGroup":65532,"runAsNonRoot":true,"runAsUser":65532,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context. |
| priorityClassName | string | `""` | Priority class name. |
| prometheusRule.annotations | object | `{}` | PrometheusRule annotations. |
| prometheusRule.enabled | bool | `false` | Create a Prometheus Operator PrometheusRule with alerts on Omni reachability and on the exporter itself. The alerts select the exporter by its `job` and `namespace` labels. Therefore, it must be scraped through its Service with the default job name, like the ServiceMonitor does. |
| prometheusRule.labels | object | `{}` | PrometheusRule labels (e.g., to match the `ruleSelector` of Prometheus). It must be the Prometheus that scrapes the exporter. |
| prometheusRule.namespace | string | `""` | PrometheusRule namespace. Defaults to the release namespace. |
| readinessProbe | object | `{"httpGet":{"path":"/","port":"metrics"},"periodSeconds":15}` | Readiness probe. It checks the landing page, like the liveness probe. |
| replicaCount | int | `1` | Number of exporter replicas. Every replica watches the whole Omni instance and serves the same series, so more replicas duplicate the series instead of sharing the load. Sums and counts over them, including in the bundled dashboard, then count every object once per replica. |
| resources | object | `{"limits":{"memory":"256Mi"},"requests":{"cpu":"25m","memory":"64Mi"}}` | Container resources. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container security context. |
| service.annotations | object | `{}` | Service annotations. |
| service.port | int | `10048` | Service port. |
| service.type | string | `"ClusterIP"` | Service type. |
| serviceAccount.annotations | object | `{}` | ServiceAccount annotations. |
| serviceAccount.automountServiceAccountToken | bool | `false` | Mount the ServiceAccount token. The exporter does not talk to the Kubernetes API. |
| serviceAccount.create | bool | `true` | Create a ServiceAccount. |
| serviceAccount.name | string | `""` | ServiceAccount name. Defaults to the full name of the release. |
| serviceMonitor.annotations | object | `{}` | ServiceMonitor annotations. |
| serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor. |
| serviceMonitor.honorLabels | bool | `false` | Keep the labels of the scraped series when they collide with the target labels. |
| serviceMonitor.interval | string | `"30s"` | Scrape interval. |
| serviceMonitor.labels | object | `{}` | ServiceMonitor labels (e.g., to match the `serviceMonitorSelector` of Prometheus). |
| serviceMonitor.metricRelabelings | list | `[]` | Prometheus [MetricRelabelConfigs](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#metric_relabel_configs) to apply before ingestion. |
| serviceMonitor.namespace | string | `""` | ServiceMonitor namespace. Defaults to the release namespace. |
| serviceMonitor.relabelings | list | `[]` | Prometheus [RelabelConfigs](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#relabel_config) to apply before scraping. |
| serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout. |
| tolerations | list | `[]` | Tolerations. |
| topologySpreadConstraints | list | `[]` | Topology spread constraints. |
