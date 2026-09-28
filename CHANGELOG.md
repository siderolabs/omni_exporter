## [omni_exporter 0.2.0](https://github.com/siderolabs/omni_exporter/releases/tag/v0.2.0) (2026-09-28)

Welcome to the v0.2.0 release of omni_exporter!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/omni_exporter/issues.

### Helm Chart

The exporter now has a Helm chart, released together with it as `oci://ghcr.io/siderolabs/charts/omni-exporter`:

```
helm install omni-exporter oci://ghcr.io/siderolabs/charts/omni-exporter \
  --set omni.endpoint=https://<account>.omni.siderolabs.io \
  --set omni.serviceAccountKey.existingSecret=omni-exporter-key
```

The chart can optionally create a `ServiceMonitor`, a ConfigMap with the Grafana dashboard for the Grafana sidecar, and a `PrometheusRule` with alerts on Omni reachability and on the exporter itself.
The dashboard has a `datasource` and a `job` variable, so several exporters (one per Omni instance) can share the same Prometheus.


### Contributors

* Olivier Gintrand
* Utku Ozdemir

### Changes
<details><summary>2 commits</summary>
<p>

* [`d3f123c`](https://github.com/siderolabs/omni_exporter/commit/d3f123c3c9b2e92865e4af177eeb318651794e55) chore: bump deps, rekres
* [`c25c892`](https://github.com/siderolabs/omni_exporter/commit/c25c89254846b7815e98a1f3901949a88ea4636b) feat: add a helm chart with the grafana dashboard and alerts
</p>
</details>

### Dependency Changes

* **github.com/cosi-project/runtime**         v1.16.2 -> v1.16.3
* **github.com/prometheus/client_golang**     v1.23.2 -> v1.24.1
* **github.com/prometheus/client_model**      v0.6.2 -> v0.6.3
* **github.com/prometheus/common**            v0.70.0 -> v0.72.0
* **github.com/prometheus/exporter-toolkit**  v0.17.1 -> v0.20.0
* **github.com/siderolabs/omni/client**       d34ecf49816b -> v1.12.2
* **github.com/stretchr/testify**             v1.11.1 -> v1.12.1
* **golang.org/x/sync**                       v0.22.0 -> v0.23.0
* **google.golang.org/grpc**                  v1.82.1 -> v1.84.0
* **google.golang.org/protobuf**              f2248ac996af -> v1.36.12

Previous release can be found at [v0.1.0](https://github.com/siderolabs/omni_exporter/releases/tag/v0.1.0)

## [omni_exporter 0.1.0](https://github.com/siderolabs/omni_exporter/releases/tag/v0.1.0) (2026-08-04)

Welcome to the v0.1.0 release of omni_exporter!



Please try out the release binaries and report any issues at
https://github.com/siderolabs/omni_exporter/issues.

### Omni Exporter

`omni_exporter` exposes the state of an Omni instance as per-object Prometheus metrics: one series per cluster, machine, machine set, upgrade and etcd backup.
It is kube-state-metrics for Omni, and it works against both self-hosted and SaaS instances.

Omni exposes its own metrics under the `omni_` prefix, but those are instance-level aggregates on an internal endpoint.
This exporter is the per-object complement, and all of its metrics use the `omni_exporter_` prefix, so the two never collide.


### Getting Started

Create a read-only service account on the Omni instance and run the exporter with the key it prints:

```
omnictl serviceaccount create --use-user-role=false --role=Reader omni-exporter

docker run -d -p 10048:10048 \
  -e OMNI_ENDPOINT=https://<account>.omni.siderolabs.io \
  -e OMNI_SERVICE_ACCOUNT_KEY=<key> \
  ghcr.io/siderolabs/omni_exporter:v0.1.0
```

Metrics are served on port 10048, which is registered in the Prometheus default port allocations.
The `Reader` role is enough for everything the exporter reads, and it never writes to Omni.


### Design

The exporter maintains a watch per resource type and renders each scrape from an in-memory view, so its cost is driven by the state churn of the instance rather than by the scrape frequency.
Object metrics are suppressed only when serving them would be an outright wrong answer, meaning Omni is unreachable or a resource type has not finished its initial sync.
Gate alerts on `omni_exporter_up == 1`.

Aggregation is left to PromQL, and enum-valued state is emitted as one 0/1 series per possible value so that alerting expressions stay simple equality matches.
The collectors also ship as an importable library in `pkg/collector`, which takes a COSI state and returns a `prometheus.Collector`.


### Contributors

* Utku Ozdemir

### Changes
<details><summary>3 commits</summary>
<p>

* [`e4ff7cd`](https://github.com/siderolabs/omni_exporter/commit/e4ff7cdeeff3f55b35869d41dea13cfa19780a64) feat: implement the exporter
* [`4f97108`](https://github.com/siderolabs/omni_exporter/commit/4f97108aacfa16fdb86446f9b7b84548c725d562) chore: bootstrap the project
* [`46394b1`](https://github.com/siderolabs/omni_exporter/commit/46394b14df12aab88b1cb23bda6375f376020a55) chore: add README
</p>
</details>

### Dependency Changes

This release has no dependency changes

