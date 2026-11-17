---
name: sequence-flow
description: Draw a Mermaid sequence diagram for one endpoint, command or job named in the task, following the real call path through the code.
---
# Sequence flow

1. Take the endpoint (e.g. `POST /tasks`), CLI command or background job from the task prompt. If none is named, ask with AskUserQuestion; do not pick one yourself.
2. Find its entry (router registration, command definition, scheduler) and follow the real call path: handlers, services, repositories, external calls (HTTP, queues, database queries, caches), and the responses and errors that come back. Read the code; do not guess from names.
3. Write `.fleetpocket/out/sequence-<name>.mmd` as a `sequenceDiagram`: participants in call order (client, handler, services, database, external systems), messages named after the real functions or queries, `alt`/`opt` blocks for the main error and branch paths, `par` for concurrent work.
4. Use `confidence: "derived"`; sources are the files (with functions) along the path.

## Rules

- Stay read-only on the repository: write only under `.fleetpocket/out/`. Do not edit, create or delete repository files.
- Cite sources: list every file (and, for databases, every table) a diagram was built from in `sources` in `.fleetpocket/out/manifest.json`.
- Keep diagrams readable: at most 40 nodes per diagram. If there are more, split into several files (e.g. by schema, domain or service) and add an index entry per file to the manifest.
- Every diagram is a Mermaid file `<name>.mmd`. Extra sources use the same basename (`.dbml`, `.d2`, `.puml`, `.sql`).
- Manifest format: `{"artifacts":[{"file":"er.mmd","title":"…","summary":"…","kind":"mermaid","confidence":"exact|derived","sources":["…"]}]}`.
