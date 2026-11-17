---
name: infra-map
description: Draw a Mermaid flowchart of services, networks, volumes, ports and external dependencies from docker-compose, Kubernetes and Terraform files.
---
# Infrastructure map

1. Read docker-compose files (all overrides), Kubernetes manifests / Helm templates and Terraform modules.
2. Write `.fleetpocket/out/infra.mmd` as a `flowchart LR`:
   - one node per service / deployment / managed resource (label: name and image or resource type);
   - `subgraph` per network, namespace or VPC;
   - volumes and persistent disks as cylinder nodes (`[( )]`);
   - published ports on the edges from the outside (`internet -->|443| proxy`);
   - external dependencies (SaaS APIs, managed databases, registries) as separate nodes;
   - `depends_on` and service-to-service traffic as edges.
3. One file per environment when they differ (`infra-dev.mmd`, `infra-prod.mmd`).
4. Use `confidence: "derived"`; sources are the files you read.

## Rules

- Stay read-only on the repository: write only under `.fleetpocket/out/`. Do not edit, create or delete repository files.
- Cite sources: list every file (and, for databases, every table) a diagram was built from in `sources` in `.fleetpocket/out/manifest.json`.
- Keep diagrams readable: at most 40 nodes per diagram. If there are more, split into several files (e.g. by schema, domain or service) and add an index entry per file to the manifest.
- Every diagram is a Mermaid file `<name>.mmd`. Extra sources use the same basename (`.dbml`, `.d2`, `.puml`, `.sql`).
- Manifest format: `{"artifacts":[{"file":"er.mmd","title":"…","summary":"…","kind":"mermaid","confidence":"exact|derived","sources":["…"]}]}`.
