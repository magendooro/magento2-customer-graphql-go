# magento2-customer-graphql-go

High-performance Go drop-in replacement for Magento 2's customer-related GraphQL queries and mutations. Reads from and writes to Magento's MySQL database, delivering identical results with significantly faster response times.

## Quick Start

```bash
git clone https://github.com/magendooro/magento2-customer-graphql-go.git
cd magento2-customer-graphql-go
GOTOOLCHAIN=auto go build -o server ./cmd/server/

DB_HOST=localhost DB_NAME=magento ./server
```

Endpoints: GraphQL at `/graphql`, Playground at `/`, Health at `/health`.

Default port: **8082** (to avoid conflict with Magento on 8080).

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_HOST` | `localhost` | MySQL host |
| `DB_PORT` | `3306` | MySQL port |
| `DB_USER` | `root` | MySQL user |
| `DB_PASSWORD` | `""` | MySQL password |
| `DB_NAME` | `magento` | Magento database name |
| `REDIS_HOST` | `127.0.0.1` | Redis host (empty to disable) |
| `SERVER_PORT` | `8082` | HTTP listen port |
| `MAGENTO_CRYPT_KEY` | `""` | Magento's `crypt/key` from env.php (required for JWT) |
| `JWT_TTL_MINUTES` | `60` | JWT token lifetime in minutes |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Features

### Queries
- **`customer`** — Returns the authenticated customer's profile, addresses, newsletter status
- **`isEmailAvailable`** — Checks if an email is available for registration

### Mutations
- **`generateCustomerToken`** — Authenticate and receive a Bearer token
- **`revokeCustomerToken`** — Revoke the current token
- **`createCustomerV2`** — Register a new customer account
- **`updateCustomerV2`** — Update customer profile
- **`changeCustomerPassword`** — Change password (requires current password)
- **`updateCustomerEmail`** — Change email (requires password verification)
- **`createCustomerAddress`** — Add a new address
- **`updateCustomerAddress`** — Update an existing address
- **`deleteCustomerAddress`** — Delete an address

### Infrastructure
- **Magento-compatible JWT authentication** (HS256 signed with Magento's `crypt/key`)
- **Store-scoped multi-tenancy** via `Store` HTTP header
- **Redis response caching** (optional, skips authenticated requests)
- **Magento-compatible password verification** (Argon2id, SHA256, bcrypt)

## Magento Compatibility

- **Magento 2.4+ Enterprise Edition**
- **Same database** as Magento — reads and writes to the same MySQL instance
- Customer entity uses `entity_id` (not `row_id`)

## Usage

```bash
# Generate a customer token
curl -s -H 'Content-Type: application/json' \
  -d '{"query":"mutation { generateCustomerToken(email: \"user@example.com\", password: \"password123\") { token } }"}' \
  http://localhost:8082/graphql

# Query customer data (with token)
curl -s -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -d '{"query":"{ customer { id firstname lastname email addresses { city country_code } } }"}' \
  http://localhost:8082/graphql
```

## License

MIT
