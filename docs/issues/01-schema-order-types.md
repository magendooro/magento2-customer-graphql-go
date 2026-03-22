# Schema: Add CustomerOrder types to GraphQL schema

## Description

Add customer order history support to the GraphQL schema, following the current Adobe Commerce GraphQL spec.
Orders are exposed as a nested field on the `Customer` type (`customer.orders`), not as a standalone `customerOrders` query (deprecated).

## Acceptance Criteria

- [ ] `Customer.orders(filter: CustomerOrdersFilterInput, currentPage: Int = 1, pageSize: Int = 20, sort: CustomerOrderSortInput): CustomerOrders` field added
- [ ] `CustomerOrders` type with `items`, `page_info`, `total_count`
- [ ] `CustomerOrder` type with all spec-required fields: `id`, `number`, `order_date`, `status`, `carrier`, `shipping_method`, `shipping_address`, `billing_address`, `payment_methods`, `items`, `total`, `invoices`, `shipments`, `credit_memos`, `comments`
- [ ] `OrderTotal`, `TaxItem`, `Discount`, `ShippingHandling` types
- [ ] `OrderAddress` type
- [ ] `OrderPaymentMethod` and `KeyValue` types
- [ ] `OrderItemInterface` interface + `OrderItem implements OrderItemInterface` type
- [ ] `Invoice`, `InvoiceItemInterface`, `InvoiceItem` types
- [ ] `OrderShipment`, `ShipmentItemInterface`, `ShipmentItem`, `ShipmentTracking` types
- [ ] `CreditMemo`, `CreditMemoItemInterface`, `CreditMemoItem` types
- [ ] `SalesCommentItem` type
- [ ] `Money` type with `value` and `currency`
- [ ] `CurrencyEnum` enum
- [ ] Filter input types: `CustomerOrdersFilterInput`, `FilterStringTypeInput`, `FilterRangeTypeInput`, `FilterEqualTypeInput`
- [ ] Sort input: `CustomerOrderSortInput` with `SortEnum`
- [ ] Schema compiles and gqlgen regeneration succeeds

## Solution Approach

Edit `graph/schema.graphqls` to add all new types. After editing, run:
```bash
GOTOOLCHAIN=auto go run github.com/99designs/gqlgen generate
```

Also update `gqlgen.yml` to add `Customer.orders.resolver: true` so gqlgen generates a proper `CustomerResolver` interface (rather than resolving `orders` directly from the struct field, which would silently ignore filter/sort/pagination arguments — same issue as `addressesV2`).

## Labels

`enhancement`, `schema`, `graphql`
