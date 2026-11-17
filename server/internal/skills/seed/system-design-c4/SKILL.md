---
name: system-design-c4
description: Draw the system's architecture as C4 Context and Container diagrams (plus Component for the main service) in Mermaid, derived from the code and deployment files.
---
# System design (C4)

1. Find what the system is made of: entrypoints (`main` packages, `cmd/`, `package.json` scripts, `manage.py`, Procfiles), deployment files (docker-compose, Kubernetes manifests, Helm charts, Terraform), environment variables (`.env.example`, config loaders), HTTP clients and base URLs, queues and brokers, database drivers, caches, third-party SDKs.
2. Write:
   - `.fleetpocket/out/c4-context.mmd` — `C4Context`: people (users, operators), the system, external systems it talks to.
   - `.fleetpocket/out/c4-container.mmd` — `C4Container`: deployable units (services, web apps, workers, databases, queues) with technology and the protocol on each relationship.
   - `.fleetpocket/out/c4-component.mmd` — `C4Component` for the main service only: its main packages/modules and how they call each other and the containers around them.
3. Every element and relationship must be backed by something you read; leave out what you cannot point to. Use `confidence: "derived"` and list the files as sources.

## Rules

- Stay read-only on the repository: write only under `.fleetpocket/out/`. Do not edit, create or delete repository files.
- Cite sources: list every file (and, for databases, every table) a diagram was built from in `sources` in `.fleetpocket/out/manifest.json`.
- Keep diagrams readable: at most 40 nodes per diagram. If there are more, split into several files (e.g. by schema, domain or service) and add an index entry per file to the manifest.
- Every diagram is a Mermaid file `<name>.mmd`. Extra sources use the same basename (`.dbml`, `.d2`, `.puml`, `.sql`).
- Manifest format: `{"artifacts":[{"file":"er.mmd","title":"…","summary":"…","kind":"mermaid","confidence":"exact|derived","sources":["…"]}]}`.
