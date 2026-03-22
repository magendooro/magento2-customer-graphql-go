# Service: Add OrderService business logic

## Description

Create `internal/service/orders.go` with `OrderService` that orchestrates the order repository, performs auth checks, batch-loads sub-resources in parallel, and maps DB structs to gqlgen model types.

## Acceptance Criteria

- [ ] `OrderService` struct with `orderRepo *repository.OrderRepository`
- [ ] `NewOrderService(orderRepo *repository.OrderRepository) *OrderService`
- [ ] `GetOrders(ctx, customerID, filter, sort, currentPage, pageSize) (*model.CustomerOrders, error)`
  - Returns auth error if `customerID == 0`
  - Calls `orderRepo.FindByCustomerID` with filter/sort/pagination
  - Batch-loads sub-resources using `errgroup.WithContext` in parallel
  - Maps to `*model.CustomerOrders` with correct pagination
- [ ] Mapping helpers: `mapOrder`, `mapMoney`, `mapOrderAddress`, `mapOrderItem`, `mapInvoice`, `mapShipment`, `mapCreditMemo`
- [ ] `OrderItemInterface` is satisfied by concrete `*model.OrderItem` (gqlgen-generated via `IsOrderItemInterface()` method)
- [ ] Pagination: `SearchResultPageInfo` correctly computed from total_count, page, pageSize

## Solution Approach

Create `internal/service/orders.go`. Use `errgroup.WithContext` (from `golang.org/x/sync/errgroup`) for parallel batch-loading — same pattern as the catalog service. Auth check: `customerID == 0` → return `errors.New("customer not authenticated")`. Map currency code string to `model.CurrencyEnum`.

## Labels

`enhancement`, `service`, `business-logic`
