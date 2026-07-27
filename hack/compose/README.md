# Local dev stack

Prometheus and Grafana for developing the exporter against a real Omni instance.
The exporter itself runs on the host, Prometheus in the stack scrapes it on port 10048.

## Run

Start the exporter on the host, pointed at your Omni instance:

```bash
OMNI_ENDPOINT=https://<omni> OMNI_SERVICE_ACCOUNT_KEY=<key> go run ./cmd/omni_exporter --log.format=text
```

Start the stack:

```bash
docker compose -f hack/compose/docker-compose.yaml up -d
```

Then open:

- Grafana: <http://localhost:3000> (anonymous admin, no login), with the Omni Exporter dashboard and the Prometheus datasource provisioned
- Prometheus: <http://localhost:9090>
- Exporter metrics: <http://localhost:10048/metrics>

Stop and wipe the throwaway state (Prometheus/Grafana volumes):

```bash
docker compose -f hack/compose/docker-compose.yaml down -v
```
