# Cattery

Cattery is a scheduler and lifecycle manager for GitHub Actions self-hosted runners. It dynamically provisions ephemeral runner instances across multiple infrastructure providers in response to workflow job demand using GitHub's [runner scale sets](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/deploying-runner-scale-sets-with-actions-runner-controller) API.

## How it works

Cattery follows a **server/agent** architecture:

```
GitHub Actions                Cattery Server               Infrastructure
 (Scale Set API)                  |              (Docker / GCE / Nomad / Kubernetes)
       |                          |                              |
       |-- job demand ----------->|                              |
       |                          |-- provision tray ----------->|
       |                          |                              |
       |                          |<-- agent registers ---------|
       |<-- JIT runner config ----|                              |
       |                          |                              |
       |-- job started ---------->|-- assign job to tray ------->|
       |-- job completed -------->|-- delete tray -------------->|
```

**Server** polls GitHub for pending jobs via the scale set API, provisions runner instances ("trays") through configured providers, and manages their full lifecycle. Each tray is an ephemeral runner — created on demand, used for a single job, then destroyed.

**Agent** runs inside each provisioned instance. It registers with the server to receive a JIT runner configuration, starts the GitHub Actions runner, monitors the job, and reports back when done.

### Tray lifecycle

A tray progresses through: **Creating** -> **Registering** -> **Registered** -> **Running** -> **Deleting**

A tray that sits in any non-running status for longer than a configurable threshold is treated as stale and deleted (defaults: 5 minutes for Creating and Registering, 15 minutes for Registered and Deleting; see the `stale` section in [Configuration](docs/configuration.md)). An idle Registered tray is also told to shut itself down after 15 minutes.

## Features

- **Multi-provider** — Ships with Docker, Google Compute Engine, Nomad and Kubernetes providers, new providers can be added
- **Scale-to-zero** — Runners are only provisioned when jobs are queued
- **Ephemeral runners** — Each job gets a fresh, isolated environment via JIT configuration
- **Multi-org support** — Manage runners across multiple GitHub organizations from a single server
- **Preemption recovery** — Jobs lost to a preempted or terminated VM are re-run once their workflow run completes (skipped for merge queue runs, closed pull requests, and runs cancelled by a person or a concurrency group)
- **High availability** — Multiple server replicas with leader election backed by MongoDB or Kubernetes Leases
- **Monitoring** — Built-in status page (`/status`) and Prometheus metrics (`/metrics`)

## Prerequisites

- Go 1.26+
- MongoDB
- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps) installed on your organization with these organization permissions: Self-hosted runners (read and write, for the runner scale set and JIT runner configuration), Actions (read and write, to re-run failed jobs), Checks (read) and Pull requests (read) for the preemption recovery checks
- Docker, GCP credentials, a Nomad cluster or a Kubernetes cluster depending on your provider choice

## Quick start

### Build

```bash
cd src
go build -o cattery
```

Or with Docker:

```bash
docker build --build-arg CATTERY_VERSION=0.1.0 -t cattery .
```

### Configure

Create a `config.yaml` (see [Configuration](docs/configuration.md) for full reference):

```yaml
server:
  listenAddress: "0.0.0.0:5137"
  advertiseUrl: https://cattery.example.com

database:
  uri: mongodb://localhost:27017/
  database: cattery

github:
  - name: my-org
    appId: 123456
    appClientId: Iv123abC
    installationId: 987654321
    privateKeyPath: /path/to/private-key.pem

providers:
  - name: docker-local
    type: docker

trayTypes:
  - name: my-runner
    provider: docker-local
    githubOrg: my-org
    runnerGroupId: 1
    maxTrays: 5
    config:
      image: my-runner-image:latest
```

`maxTrays` must be set: with the default of 0 no trays are ever created. A complete walkthrough with a local docker runner is in [examples/example.md](examples/example.md).

### Run

**Server:**
```bash
./cattery server -c /path/to/config.yaml
```

**Agent** (runs inside provisioned instances):
```bash
./cattery agent \
  --runner-folder /path/to/actions-runner \
  --server-url http://cattery-server:5137 \
  --agent-id <tray-id>
```

## Install with Helm

The chart is published to a Helm repository hosted on GitHub Pages:

```bash
helm repo add cattery https://paritytech.github.io/cattery
helm repo update
helm install cattery cattery/cattery
```

Override defaults with your own values:

```bash
helm install cattery cattery/cattery -f my-values.yaml
```

See [charts/cattery/values.yaml](charts/cattery/values.yaml) for the available values.

## Configuration

See [docs/configuration.md](docs/configuration.md) for the full configuration reference covering server, database, stale tray cleanup, leader election, GitHub App, providers, and tray types.

## License

[MIT](LICENSE)