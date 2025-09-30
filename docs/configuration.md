# Configuration

Cattery uses YAML configuration files and searches for the file named config.yaml first in the working directory and then in the /etc/cattery directory.
Cattery will use the first config file found.

### Example:

```yaml
server:
  listenAddress: "0.0.0.0:5137"
  advertiseUrl: https://example.org

database:
  uri: mongodb://localhost:27017/
  database: cattery

github:
  - name: my-org
    appId: 123456
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
    config:
      image: cattery-runner-tiny:latest
      namePrefix: cattery

  - name: cattery-gce-prod
    provider: gce-prod
    shutdown: true
    githubOrg: my-org
    runnerGroupId: 3
    maxTrays: 10
    config:
      zones:
        - europe-west1-c
        - europe-west1-d
      machineType: e2-standard-4
      instanceTemplate: global/instanceTemplates/cattery-default
```

### Config sections

#### server

| Key           | Type     | Required | Description                                                     |
|---------------|----------|----------|-----------------------------------------------------------------|
| listenAddress | string   | yes      | Host:port for the HTTP server to bind (e.g., 0.0.0.0:5137).     |
| advertiseUrl  | string   | no       | Public base URL where the server is reachable. Passed to agents |

#### database

| Key      | Type   | Required | Description                                                   |
|----------|--------|----------|---------------------------------------------------------------|
| uri      | string | yes      | MongoDB connection string (e.g., mongodb://localhost:27017/). |
| database | string | yes      | Database name (e.g., cattery).                                |

#### github
A list of GitHub organizations/accounts the server manages via a GitHub App.

| Key            | Type   | Required | Description                                             |
|----------------|--------|----------|---------------------------------------------------------|
| name           | string | yes      | Name of the github organization                         |
| appId          | int    | yes      | GitHub App ID                                           |
| installationId | int    | yes      | Installation ID of that App in the organization/account |
| privateKeyPath | string | yes      | Path to the App private key PEM on disk                 |

#### providers
Providers define how trays (runner machines) are provisioned. At least one provider is required.

Common fields for all providers:

| Key  | Type   | Required | Description                                         |
|------|--------|----------|-----------------------------------------------------|
| name | string | yes      | Provider name to reference from trayTypes.          |
| type | enum   | yes      | Provider type. Built-ins: docker, google (GCE).     |

Provider-specific fields:

- docker

  The docker provider has no extra fields

- google (GCE)
  
  | Key             | Type   | Required | Description                                  |
  |-----------------|--------|----------|----------------------------------------------|
  | project         | string | yes      | GCP project ID                               |
  | credentialsFile | string | no       | Path to GCP service account JSON credentials |

#### trayTypes
Defines one or more tray "profiles" that the Tray Manager can maintain.

| Key           | Type               | Required | Description                                                          |
|---------------|--------------------|----------|----------------------------------------------------------------------|
| name          | string             | yes      | Unique name for the tray type                                        |
| provider      | string             | yes      | Name of a provider defined in `providers`                            |
| shutdown      | bool               | no       | Whether instances should self-terminate when complete                |
| runnerGroupId | int                | yes      | GitHub Runner Group ID to register runners into                      |
| githubOrg     | string             | yes      | The GitHub org key, matching one of the entries under `github`       |
| maxTrays      | int                | yes      | Maximum number of concurrent trays of this type                      |
| config        | provider-dependent | yes      | Provider-specific configuration for how to create a tray (see below) |

Provider-specific config under trayType.config:

- docker config
  
  | Key        | Type   | Required | Description                                                                 |
  |------------|--------|----------|-----------------------------------------------------------------------------|
  | image      | string | yes      | Docker image to run for the agent/runner (e.g., cattery-runner-tiny:latest) |
  | namePrefix | string | no       | Prefix for container names                                                  |

- google (GCE) config
  
  | Key              | Type     | Required | Description                                                                     |
  |------------------|----------|----------|---------------------------------------------------------------------------------|
  | zones            | []string | yes      | List of zones to create instances in (e.g. `europe-west1-c`)                    |
  | machineType      | string   | yes      | Instance machine type (e.g. `e2-standard-4`)                                    |
  | instanceTemplate | string   | yes      | Template to base instances on (e.g. `global/instanceTemplates/cattery-default`) |
  | namePrefix       | string   | no       | Prefix for VM's name                                                            |


Notes:
- Ensure runnerGroupId corresponds to an existing Runner Group in your GitHub org and that your GitHub App has permission to register runners.
  To find the runner group id go to org Settings -> Actions -> RunnerGroups -> <Your runner group>, the id will be in the page url: https://github.com/organizations/<org_name>/settings/actions/runner-groups/<group_id>
- Ensure that the repository/workflow has access to the runner group (runner group repository access)

Where configuration is read:
- Structs and parsing are defined in src/lib/config/config.go. The server reads a config file on startup (see src/cmd/server.go).
