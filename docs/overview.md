# Overview

Cattery is a scheduler and lifecycle manager for GitHub self-hosted runners. It provisions and manages ephemeral runner "trays" across different infrastructure providers (such as Docker or Google Compute Engine) and integrates with the GitHub Actions Runner Scale Set SDK to receive job assignments and scale runners.

Key capabilities:
- Scale runners on-demand using the GitHub Actions Runner Scale Set SDK (long-polling for job assignments).
- Support multiple providers via a pluggable interface (Docker, GCE, and extensible to others).
- Coordinate runner lifecycle: create, monitor, restart (if enabled), and shutdown.
- Persist state in MongoDB for durability and coordination.
- Expose an HTTP server with agent registration/control endpoints and Prometheus metrics.
- Provide a lightweight agent that runs on provisioned machines to manage the runner process.

Who is it for:
- Teams using GitHub Actions that need to scale self-hosted runners across different environments.
- CI platforms that want simple, configurable provisioning with clear separation between control-plane (server) and data-plane (agents/providers).

High-level components:
- Server: control-plane coordinating trays, scaling, and GitHub integration.
- Scale Set Poller: polls GitHub via the scale set SDK for job demand and lifecycle events (job started, job completed).
- Scale Set Client: manages sessions and communication with the GitHub Actions Runner Scale Set API.
- Agent: runs on provisioned machines/instances to manage the runner process and support commands like shutdown.
- Providers: implement infrastructure-specific create/delete/query for trays (e.g., Docker, GCE).
- Tray Manager: ensures the desired number of trays per type are up and in the expected state, scaling based on demand from the poller.
- Restarter: handles workflow restart logic.
- Repositories: MongoDB-backed persistence for trays and restarter state.

See also:
- [Architecture](./architecture.md)
- [Configuration](./configuration.md)
