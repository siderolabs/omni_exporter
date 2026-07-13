# omni_exporter

A Prometheus exporter for [Omni](https://github.com/siderolabs/omni).

It connects to the Omni API using a service account and exposes the state of the Omni instance (clusters, machines, etcd backups and more) as Prometheus metrics, so that it can be scraped by the users' own observability stack.

Work in progress.
