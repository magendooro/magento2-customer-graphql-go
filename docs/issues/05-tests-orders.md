# Tests: Integration tests for customer order history

## Description

Add integration tests for the `customer.orders` field to `tests/integration_test.go`. Tests are HTTP-based via `httptest` and follow the existing test patterns in the file.

## Acceptance Criteria

- [ ] `TestCustomerOrders_Unauthenticated` — unauthenticated `customer { orders { total_count } }` must return an auth error
- [ ] `TestCustomerOrders_Authenticated` — generates token, queries `orders(pageSize: 5)` with full field selection (number, status, order_date, total, items), validates structure (skips if test customer not found)
- [ ] `TestCustomerOrders_Pagination` — queries page 1 vs page 2, validates they are distinct (skips if customer has <2 orders)
- [ ] `TestCustomerOrders_Filter_ByNumber` — filters `orders(filter: { number: { match: "XXXXXXX" } })` using an order number from page 1 results; validates exactly 1 result
- [ ] `TestCustomerOrders_Filter_ByStatus` — filters `orders(filter: { status: { eq: "complete" } })` and validates all returned orders have `status == "complete"`
- [ ] All tests use `t.Skipf` gracefully if required data is not in the test DB
- [ ] Tests do not import any internal packages — HTTP only via `testHandler`

## Solution Approach

Append test functions to `tests/integration_test.go`. Use a helper `generateTestToken(t)` to avoid repeating the token generation flow. Each test should be independent — don't share state between tests.

## Labels

`enhancement`, `tests`, `integration`
