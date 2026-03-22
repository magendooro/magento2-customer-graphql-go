# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project: magento2-customer-graphql-go

High-performance Go drop-in replacement for Magento 2's customer-related GraphQL queries and mutations using gqlgen.

## Architecture

- **Schema-first GraphQL** via gqlgen — edit `graph/schema.graphqls`, then `GOTOOLCHAIN=auto go run github.com/99designs/gqlgen generate`
- **Never edit** `graph/generated.go` or `graph/model/models_gen.go` — they are auto-generated
- **Magento Enterprise Edition** — customer entity uses `entity_id` (NOT `row_id` — that's catalog only)
- **Read AND write** — this service handles both queries and mutations (unlike the catalog service which is read-only)
- **Authentication** — Magento-compatible HS256 JWT tokens (primary), with `oauth_token` table fallback. Requires `MAGENTO_CRYPT_KEY` env var.

## Project Structure

```
cmd/server/           Entry point
graph/                GraphQL schema, resolvers, generated code
internal/
  app/                HTTP server bootstrap
  cache/              Redis client
  config/             Config loader (Viper: env vars > YAML > defaults)
  database/           MySQL connection (DSN, pooling, UTC timezone)
  middleware/         CORS, auth, caching, logging, panic recovery, store resolution
  repository/         Data access layer — one file per domain (SQL queries)
  service/            Business logic — customer operations, type mapping
tests/                Integration tests (HTTP-based, no internal imports)
```

## Build & Test

```bash
GOTOOLCHAIN=auto go build -o server ./cmd/server/   # build
GOTOOLCHAIN=auto go vet ./...                        # lint
GOTOOLCHAIN=auto go run github.com/99designs/gqlgen generate  # regenerate after schema changes

# integration tests (needs MySQL with Magento DB)
GOTOOLCHAIN=auto go test ./tests/ -v -timeout 60s -count=1

# single test
GOTOOLCHAIN=auto go test ./tests/ -run TestGenerateToken -v -timeout 60s -count=1

# run server (port 8082 — Magento is on 8080)
DB_HOST=localhost DB_NAME=magento GOTOOLCHAIN=auto ./server
```

Test env vars: `TEST_DB_HOST`, `TEST_DB_PORT`, `TEST_DB_USER`, `TEST_DB_PASSWORD`, `TEST_DB_NAME`, `TEST_CUSTOMER_EMAIL`, `TEST_CUSTOMER_PASSWORD`.

## Key Conventions

- **Go 1.25** (via toolchain directive) — use `GOTOOLCHAIN=auto` for all go commands
- **Error handling**: wrap with `fmt.Errorf("context: %w", err)`, use `errors.Is`/`errors.As`
- **Naming**: `CamelCase` exported, `camelCase` unexported, no stutter
- **Config**: all settings via env vars (`DB_HOST`, `DB_PORT`, etc.) with sensible defaults
- **Logging**: zerolog structured JSON logging
- **Context**: always first parameter `ctx context.Context`
- **Authentication**: middleware injects customer_id into context; use `middleware.GetCustomerID(ctx)` — returns 0 if unauthenticated
- **Store scoping**: middleware injects store_id into context; use `middleware.GetStoreID(ctx)`
- **Password hashing**: Magento format `hash:salt:version` — version 1=SHA256, 2=bcrypt, 3=Argon2id (Magento 2.4+ default)

## Magento Database Tables

- `customer_entity` — main customer table (flat, most fields are columns)
- `customer_address_entity` — addresses (flat, street is newline-separated)
- `oauth_token` — Bearer tokens (customer_id, token, revoked)
- `newsletter_subscriber` — newsletter status (subscriber_status: 1=subscribed)
- `store` — store resolution (code → store_id → website_id)
- `directory_country_region` — region code/name lookup

## Common Patterns

### Adding a customer attribute
1. If it's a flat column on `customer_entity`: add to `CustomerData` struct, update `GetByID()`/`GetByEmail()` SQL, add to `mapCustomer()`
2. If it's EAV: JOIN `customer_entity_<backend_type>` using `entity_id`
3. Add field to `Customer` type in `graph/schema.graphqls` → regenerate

### Adding a mutation
1. Add to `Mutation` type in schema → regenerate
2. Implement in `internal/service/customer.go`
3. Wire resolver stub in `graph/schema.resolvers.go`
