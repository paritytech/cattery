# Configuration

Cattery uses YAML configuration files and searches for the file named config.yaml first in the working directory and then in the /etc/cattery directory.
Cattery will use the first config file found.

### Example:

```yaml
server:
  listenAddress: "0.0.0.0:5137"
  statusListenAddress: "0.0.0.0:5138"
  advertiseUrl: https://example.org
  agentSecret: my-secret-token

database:
  uri: mongodb://localhost:27017/
  database: cattery

github:
  - name: my-org
    appId: 123456
    appClientId: Iv123abC
    installationId: 987654321
    privateKeyPath: my-app.private-key.pem

providers:
  - name: docker-local
    type: docker

  - name: gce-prod
    type: google
    project: my-gcp-project
    credentialsFile: my-gcp-creds.json

trayTypes:
  - name: cattery-docker-local
    provider: docker-local
    shutdown: false
    runnerGroupId: 1
    githubOrg: my-org
    maxTrays: 3
    maxParallelCreation: 5
    config:
      image: cattery-runner-tiny:latest
      namePrefix: cattery

  - name: cattery-gce-prod
    provider: gce-prod
    shutdown: true
    githubOrg: my-org
    runnerGroupId: 3
    maxTrays: 10
    extraMetadata:
      cattery-agent-version: 0.0.4
    config:
      project: my-gcp-project
      zones:
        - europe-west1-c
        - europe-west1-d
      machineType: e2-standard-4
      instanceTemplate: global/instanceTemplates/cattery-default
```

### Config sections

#### server

| Key                  | Type   | Required | Description                                                                                                              |
|----------------------|--------|----------|--------------------------------------------------------------------------------------------------------------------------|
| listenAddress        | string | yes      | Host:port for the HTTP server to bind (e.g., 0.0.0.0:5137).                                                             |
| statusListenAddress  | string | no       | Separate host:port for the /status and /metrics endpoints. If empty or equal to listenAddress, served on the agent port. |
| advertiseUrl         | string | yes      | Public base URL where the server is reachable. Passed to agents.                                                         |
| agentSecret          | string | no       | Bearer token that agents must present to register/unregister. If empty, agent auth is disabled.                          |

#### database

| Key      | Type   | Required | Description                                                   |
|----------|--------|----------|---------------------------------------------------------------|
| uri      | string | yes      | MongoDB connection string (e.g., mongodb://localhost:27017/). |
| database | string | yes      | Database name (e.g., cattery).                                |

#### github
A list of GitHub organizations/accounts the server manages via a GitHub App.

| Key            | Type   | Required | Description                                             |
|----------------|--------|----------|---------------------------------------------------------|
| name           | string | yes      | Name of the GitHub organization                         |
| appId          | int    | yes      | GitHub App ID                                           |
| appClientId    | string | yes      | GitHub App Client ID                                    |
| installationId | int    | yes      | Installation ID of that App in the organization/account |
| privateKeyPath | string | no       | Path to the App private key PEM on disk                 |

#### providers
Providers define how trays (runner machines) are provisioned. At least one provider is required.

Common fields for all providers:

| Key  | Type   | Required | Description                                         |
|------|--------|----------|-----------------------------------------------------|
| name | string | yes      | Provider name to reference from trayTypes.          |
| type | enum   | yes      | Provider type. Currently implemented: docker, google (GCE). |

Provider-specific fields:

- docker

  The docker provider has no extra fields.

- google (GCE)
  
  | Key             | Type   | Required | Description                                  |
  |-----------------|--------|----------|----------------------------------------------|
  | project         | string | yes      | GCP project ID                               |
  | credentialsFile | string | no       | Path to GCP service account JSON credentials. If omitted, uses Application Default Credentials. |

#### trayTypes
Defines one or more tray "profiles" that the Tray Manager can maintain.

| Key                 | Type               | Required | Description                                                                                                |
|---------------------|--------------------|----------|------------------------------------------------------------------------------------------------------------|
| name                | string             | yes      | Unique name for the tray type. Also used as the runner scale set name/label.                               |
| provider            | string             | yes      | Name of a provider defined in `providers`.                                                                 |
| runnerGroupId       | int                | yes      | GitHub Runner Group ID to register runners into.                                                           |
| githubOrg           | string             | yes      | The GitHub org key, matching one of the entries under `github`.                                            |
| shutdown            | bool               | no       | Whether instances should self-terminate when the job completes.                                            |
| maxTrays            | int                | no       | Maximum number of concurrent trays of this type.                                                           |
| maxParallelCreation | int                | no       | Maximum number of trays to create in parallel. Defaults to 10.                                             |
| runnerVersion       | string             | no       | Pin the GitHub Actions runner version the agent downloads. Empty -> latest from GH releases.               |
| bootstrap           | object             | no       | Provider-injected agent bootstrap (see below). Enabled by default; set `bootstrap.enabled: false` to opt out. |
| extraMetadata       | map[string]string  | no       | Extra key-value metadata passed to the provider (e.g., GCE instance metadata).                             |
| config              | provider-dependent | yes      | Provider-specific configuration for how to create a tray (see below).                                      |

#### bootstrap

When enabled, the provider injects a script into the spawned tray that
downloads the cattery agent binary from `<advertiseUrl>/agent/download` and
starts it. The agent in turn downloads the GitHub Actions runner if it is not
already present on disk.

This means a fresh VM image only needs the OS plus whatever heavy tooling the
user wants (Docker, language runtimes, security agents). Cattery handles
installing itself and the runner.

| Key          | Type   | Required | Description                                                                                                            |
|--------------|--------|----------|------------------------------------------------------------------------------------------------------------------------|
| enabled      | bool   | no       | Master switch. Defaults to `true`. Set `false` for legacy pre-baked images that already start the agent themselves.    |
| os           | string | no       | Selects the built-in script template. Default: `linux`.                                                                |
| agentFolder  | string | no       | Where to download the cattery binary on the tray. Default: `/opt/cattery`.                                             |
| runnerFolder | string | no       | Where to install the GH Actions runner. Default: `/opt/cattery/actions-runner`. Passed to the agent as `--runner-folder`. |
| user         | string | no       | OS user to run the agent as. Default: empty (script runs as whatever user the provider's delivery mechanism uses).     |
| script       | string | no       | Override the built-in template. Treated as a Go `text/template` with `{{.ServerURL}}`, `{{.AgentID}}`, `{{.AgentFolder}}`, `{{.RunnerFolder}}`, `{{.User}}` available. |

Provider delivery:

- **gce**: script is set as the `startup-script` instance metadata key.
- **docker**: script is piped to `/bin/sh -s` as the container's entrypoint stdin.

**Migration note**: If you previously relied on a pre-baked image with its own
systemd unit (e.g. `cattery.service` + `install-agent.sh`) starting the agent,
add `bootstrap: { enabled: false }` to those tray types after upgrading.
Otherwise the injected startup script will spawn a second agent.

Provider-specific config under trayType.config:

- docker config
  
  | Key        | Type   | Required | Description                                                                 |
  |------------|--------|----------|-----------------------------------------------------------------------------|
  | image      | string | yes      | Docker image to run for the agent/runner (e.g., cattery-runner-tiny:latest) |
  | namePrefix | string | no       | Prefix for container names                                                  |

- google (GCE) config
  
  | Key              | Type     | Required | Description                                                                     |
  |------------------|----------|----------|---------------------------------------------------------------------------------|
  | project          | string   | no       | GCP project ID (can also be set at provider level)                              |
  | zones            | []string | yes      | List of zones to create instances in (e.g. `europe-west1-c`)                    |
  | machineType      | string   | yes      | Instance machine type (e.g. `e2-standard-4`)                                    |
  | instanceTemplate | string   | yes      | Template to base instances on (e.g. `global/instanceTemplates/cattery-default`) |
  | namePrefix       | string   | no       | Prefix for VM names                                                             |


Notes:
- Ensure runnerGroupId corresponds to an existing Runner Group in your GitHub org and that your GitHub App has permission to register runners.
  To find the runner group id go to org Settings -> Actions -> Runner Groups -> your runner group, the id will be in the page URL: `https://github.com/organizations/<org_name>/settings/actions/runner-groups/<group_id>`
- Ensure that the repository/workflow has access to the runner group (runner group repository access).
