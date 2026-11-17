---
name: db-schema
description: Draw the database schema as a Mermaid ER diagram and DBML, from the live database when DATABASE_URL is set, otherwise from migrations and ORM models.
---
# Database schema diagram

## With a database (`DATABASE_URL` is set)

1. Detect the project's migrations and apply them to `$DATABASE_URL`:
   - goose (`-- +goose Up`, `goose` in go.mod): `goose -dir <dir> postgres "$DATABASE_URL" up`, or apply the Up sections in order with `psql`.
   - golang-migrate (`*.up.sql`): apply the `.up.sql` files in order with `psql`.
   - Prisma (`prisma/schema.prisma`): `npx prisma migrate deploy`.
   - Django (`manage.py`): `python manage.py migrate`.
   - Rails (`db/migrate`): `bin/rails db:migrate`, or load `db/structure.sql` / `db/schema.rb`.
   - Alembic (`alembic.ini`): `alembic upgrade head`.
   - Knex / TypeORM: `npx knex migrate:latest` / `npx typeorm migration:run`.
   - Plain SQL (`schema.sql`, `migrations/*.sql`): apply in filename order with `psql -v ON_ERROR_STOP=1`.
   If a step needs dependencies that are not installed and cannot be installed quickly, fall back to "Without a database".
2. Introspect with `psql "$DATABASE_URL"` using `information_schema` and `pg_catalog`: schemas, tables, columns, data types, nullability, defaults, primary keys, foreign keys (with ON DELETE), unique constraints, indexes (`pg_indexes`) and enums (`pg_type` + `pg_enum`). Skip `pg_catalog`, `information_schema` and migration bookkeeping tables (`goose_db_version`, `schema_migrations`, `_prisma_migrations`, `django_migrations`, `alembic_version`, `knex_migrations`).
3. Write `.fleetpocket/out/er.mmd` (`erDiagram`: entities with typed columns marked PK/FK/UK, relationships with cardinality from FKs and nullability) and `.fleetpocket/out/schema.dbml` (tables, columns, `pk`, `unique`, `not null`, `ref`, indexes, enums). Use `confidence: "exact"`; sources are the table names plus the migration directory.

## Without a database

Derive the same two files from the migrations and ORM models (later migrations win; follow renames and drops). Use `confidence: "derived"`; sources are the files you read.

## Grouping

Group by schema: one `er-<schema>.mmd` per schema when there is more than one, with `er.mmd` as the overview of the main schema.

## Rules

- Stay read-only on the repository: write only under `.fleetpocket/out/`. Do not edit, create or delete repository files.
- Cite sources: list every file (and, for databases, every table) a diagram was built from in `sources` in `.fleetpocket/out/manifest.json`.
- Keep diagrams readable: at most 40 nodes per diagram. If there are more, split into several files (e.g. by schema, domain or service) and add an index entry per file to the manifest.
- Every diagram is a Mermaid file `<name>.mmd`. Extra sources use the same basename (`.dbml`, `.d2`, `.puml`, `.sql`).
- Manifest format: `{"artifacts":[{"file":"er.mmd","title":"…","summary":"…","kind":"mermaid","confidence":"exact|derived","sources":["…"]}]}`.
