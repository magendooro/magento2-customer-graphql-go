---
name: add-attribute
description: Add a new EAV customer attribute to the GraphQL schema. Use when adding custom Magento customer attributes to the API.
argument-hint: <attribute_code>
---

Add the Magento customer EAV attribute `$ARGUMENTS` to the GraphQL API. Follow these steps exactly:

## 1. Schema

Add the field to `Customer` type in `graph/schema.graphqls`. Use the appropriate GraphQL type:
- `varchar` / `text` backend → `String`
- `int` backend → `Int`
- `decimal` backend → `Float`
- `datetime` backend → `String`

## 2. Regenerate

```bash
GOTOOLCHAIN=auto go run github.com/99designs/gqlgen generate
```

## 3. Repository

In `internal/repository/customer.go`:
- Add the field to the `CustomerData` struct
- Add the column or EAV JOIN in `GetByID()` and `GetByEmail()`
- For flat columns: just add to SELECT and Scan
- For EAV attributes: JOIN `customer_entity_<backend_type>` using `entity_id` with `COALESCE(store_value, default_value)` for store scoping

Note: Standard Magento customer attributes are all `static` (flat table). Only user-defined custom attributes use EAV value tables. For custom EAV attributes, the `custom_attributes` field already handles them automatically via `EAVAttributeRepository`.

## 4. Service mapping

In `internal/service/customer.go`, in `mapCustomer()`, map the new field from `CustomerData` to the generated model type.

## 5. Verify

```bash
GOTOOLCHAIN=auto go build ./...
GOTOOLCHAIN=auto go vet ./...
GOTOOLCHAIN=auto go test ./... -count=1 -timeout 120s
```

## Important

- Customer entity uses `entity_id` (NOT `row_id` — that's catalog only)
- The attribute must exist in Magento's `eav_attribute` table with `entity_type_code = 'customer'`
- Keep SQL column count and Scan parameter count in sync in BOTH `GetByID()` and `GetByEmail()`
