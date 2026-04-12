# Cattery

Cattery is a scheduler and lifecycle manager for GitHub Actions self-hosted runners. It dynamically provisions ephemeral runner instances across multiple infrastructure providers in response to workflow job demand using GitHub's [runner scale sets](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/deploying-runner-scale-sets-with-actions-runner-controller) API.

## How it works

Cattery follows a **server/agent** architecture:

```
GitHub Actions                Cattery Server               Infrastructure
 (Scale Set API)                  |                         (Docker / GCE)
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

Stale trays (no heartbeat for 2+ minutes) are automatically cleaned up.

## Features

- **Multi-provider** — Ships with Docker and Google Compute Engine providers, new providers can be added
- **Scale-to-zero** — Runners are only provisioned when jobs are queued
- **Ephemeral runners** — Each job gets a fresh, isolated environment via JIT configuration
- **Multi-org support** — Manage runners across multiple GitHub organizations from a single server
- **Automatic failed job restart** — Agents can request reruns of failed workflow jobs
- **Monitoring** — Built-in status page (`/status`) and Prometheus metrics (`/metrics`)

## Prerequisites

- Go 1.25+
- A [GitHub App](https://docs.github.com/en/apps/creating-github-apps) with Actions read/write permissions, installed on your organization
- Docker and/or GCP credentials depending on your provider choice
- A database: **SQLite** (no external dependencies) or **MongoDB**

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
  type: sqlite
  path: /var/lib/cattery/cattery.db

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

## Configuration

See [docs/configuration.md](docs/configuration.md) for the full configuration reference covering server, database, GitHub App, providers, and tray types.

## License

[MIT](LICENSE)