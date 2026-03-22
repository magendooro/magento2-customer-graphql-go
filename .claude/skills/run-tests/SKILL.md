---
name: run-tests
description: Run integration and comparison tests against the Magento database. Use when verifying changes work correctly.
argument-hint: [test-pattern]
disable-model-invocation: true
---

Run the project test suites. If a test pattern is provided, run only matching tests.

## Environment

Tests require a MySQL connection to a Magento database. Default env vars:
- `TEST_DB_HOST` (localhost — uses Unix socket)
- `TEST_DB_PORT` (3306)
- `TEST_DB_USER` (fch)
- `TEST_DB_PASSWORD` ("")
- `TEST_DB_NAME` (magento248)
- `TEST_DB_SOCKET` (/tmp/mysql.sock)
- `MAGENTO_CRYPT_KEY` (defaults to local Magento's key for JWT tests)

## Steps

### 1. Build check

```bash
GOTOOLCHAIN=auto go build ./...
GOTOOLCHAIN=auto go vet ./...
```

### 2. Run tests

If `$ARGUMENTS` is provided, use it as the test pattern:

```bash
GOTOOLCHAIN=auto go test ./tests/ -run '$ARGUMENTS' -v -timeout 120s -count=1
```

If no argument, run ALL tests (72 total across 5 packages):

```bash
GOTOOLCHAIN=auto go test ./... -v -timeout 120s -count=1
```

### 3. Test categories

- **Unit tests** (23): JWT, password hashing, date formatting, UID decoding, helpers
- **Integration tests** (13): health, isEmailAvailable, auth, token lifecycle, CRUD, store, orders
- **Comparison tests** (31): field-by-field validation against Magento ground truth
- **Order tests** (5): authenticated, pagination, filter by number, filter by status

### 4. Live comparison (optional)

For a side-by-side comparison against Magento PHP, both services must be running:

```bash
# Start Go service
MAGENTO_CRYPT_KEY=<key> DB_USER=fch DB_NAME=magento248 GOTOOLCHAIN=auto go run ./cmd/server/ &

# Run comparison — same query to both :8080 (Magento) and :8082 (Go)
GOTOOLCHAIN=auto go test ./tests/ -run TestCompare -v -timeout 300s -count=1
```

### 5. Report results

Summarize pass/fail counts. For failures, show the test name and error message.
