# Repository: Add order.go data access layer

## Description

Create `internal/repository/order.go` with all SQL access for Magento sales tables. This is a read-only repository that queries `sales_order` and related tables to retrieve a customer's order history with all sub-resources.

## Acceptance Criteria

- [ ] `OrderData` struct mapping `sales_order` columns
- [ ] `OrderItemData` struct mapping `sales_order_item` columns
- [ ] `OrderAddressData` struct mapping `sales_order_address` columns
- [ ] `OrderPaymentData` struct mapping `sales_order_payment` columns
- [ ] `InvoiceData` / `InvoiceItemData` structs for `sales_invoice` / `sales_invoice_item`
- [ ] `ShipmentData` / `ShipmentItemData` / `ShipmentTrackData` structs
- [ ] `CreditMemoData` / `CreditMemoItemData` structs for `sales_creditmemo` / `sales_creditmemo_item`
- [ ] `OrderCommentData` struct for `sales_order_status_history`
- [ ] `OrderRepository` with `NewOrderRepository(db *sql.DB) *OrderRepository`
- [ ] `FindByCustomerID` — filters (number, order_date range, status, grand_total range), sort (order_date/number/grand_total ASC/DESC), pagination (LIMIT/OFFSET), returns `([]*OrderData, int, error)` with total count
- [ ] Batch loaders: `GetItems`, `GetAddresses`, `GetPayments`, `GetInvoices`, `GetInvoiceItems`, `GetShipments`, `GetShipmentItems`, `GetShipmentTracks`, `GetCreditMemos`, `GetCreditMemoItems`, `GetComments` — all take `[]int` order/invoice/shipment IDs and return `map[int][]*XData`
- [ ] All SQL uses `?` positional placeholders compatible with `go-sql-driver/mysql`
- [ ] IN clause expansion handled with correct placeholder generation

## Solution Approach

Create `internal/repository/order.go`. Follow the same pattern as `customer.go` — struct definitions, then repository struct, then methods. For the `FindByCustomerID` filter, build WHERE clause dynamically with a `strings.Builder` + args slice, similar to the catalog service's `FindProducts`. Batch loaders use `strings.Repeat("?,", n)` to build IN placeholders.

## Tables

- `sales_order` — main order record
- `sales_order_item` — line items
- `sales_order_address` — billing/shipping addresses
- `sales_order_payment` — payment method + additional_information (JSON)
- `sales_invoice` / `sales_invoice_item`
- `sales_shipment` / `sales_shipment_item` / `sales_shipment_track`
- `sales_creditmemo` / `sales_creditmemo_item`
- `sales_order_status_history` — comments/status history

## Labels

`enhancement`, `repository`, `sql`
