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

