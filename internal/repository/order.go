package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// OrderData holds a single order from sales_order table.
type OrderData struct {
	EntityID             int
	IncrementID          string
	CustomerID           int
	StoreID              int
	Status               string
	State                string
	CreatedAt            string
	GrandTotal           float64
	Subtotal             float64
	TaxAmount            float64
	ShippingAmount       float64
	ShippingMethod       sql.NullString
	ShippingDescription  sql.NullString
	OrderCurrencyCode    string
}

// OrderItemData holds a single item from sales_order_item table.
type OrderItemData struct {
	ItemID         int
	OrderID        int
	ProductType    sql.NullString
	Sku            string
	Name           sql.NullString
	Price          float64
	QtyOrdered     float64
	QtyShipped     float64
	QtyInvoiced    float64
	QtyRefunded    float64
	QtyCanceled    float64
	RowTotal       float64
	TaxAmount      float64
	DiscountAmount float64
	Status         sql.NullString
}

// OrderAddressData holds a single address from sales_order_address table.
type OrderAddressData struct {
	EntityID   int
	ParentID   int
	AddressType string // "billing" or "shipping"
	Firstname  string
	Lastname   string
	Street     sql.NullString // newline-separated
	City       string
	Region     sql.NullString
	RegionID   sql.NullInt64
	Postcode   sql.NullString
	CountryID  string
	Telephone  sql.NullString
	Company    sql.NullString
	Prefix     sql.NullString
	Suffix     sql.NullString
	Fax        sql.NullString
	VatID      sql.NullString
	Middlename sql.NullString
}

// OrderPaymentData holds a single payment record from sales_order_payment table.
type OrderPaymentData struct {
	EntityID              int
	ParentID              int
	Method                string
	AdditionalInformation sql.NullString // JSON
}

// InvoiceData holds a single invoice from sales_invoice table.
type InvoiceData struct {
	EntityID       int
	OrderID        int
	IncrementID    string
	GrandTotal     float64
	Subtotal       float64
	TaxAmount      float64
	ShippingAmount float64
	CreatedAt      string
}

// InvoiceItemData holds a single item from sales_invoice_item table.
type InvoiceItemData struct {
	EntityID    int
	ParentID    int
	OrderItemID sql.NullInt64
	Sku         string
	Name        sql.NullString
	Price       float64
	Qty         float64
	RowTotal    float64
	TaxAmount   float64
	DiscountAmount float64
}

// ShipmentData holds a single shipment from sales_shipment table.
type ShipmentData struct {
	EntityID    int
	OrderID     int
	IncrementID string
	CreatedAt   string
}

// ShipmentItemData holds a single item from sales_shipment_item table.
type ShipmentItemData struct {
	EntityID    int
	ParentID    int
	OrderItemID sql.NullInt64
	Sku         sql.NullString
	Name        sql.NullString
	Qty         float64
}

// ShipmentTrackData holds a single tracking record from sales_shipment_track table.
type ShipmentTrackData struct {
	EntityID    int
	ParentID    int
	TrackNumber sql.NullString
	Title       sql.NullString
	CarrierCode string
}

// CreditMemoData holds a single credit memo from sales_creditmemo table.
type CreditMemoData struct {
	EntityID       int
	OrderID        int
	IncrementID    string
	GrandTotal     float64
	Subtotal       float64
	TaxAmount      float64
	ShippingAmount float64
	CreatedAt      string
}

// CreditMemoItemData holds a single item from sales_creditmemo_item table.
type CreditMemoItemData struct {
	EntityID    int
	ParentID    int
	OrderItemID sql.NullInt64
	Sku         string
	Name        sql.NullString
	Price       float64
	Qty         float64
	RowTotal    float64
	TaxAmount   float64
	DiscountAmount float64
}

// OrderCommentData holds a single comment from sales_order_status_history table.
type OrderCommentData struct {
	EntityID         int
	ParentID         int
	Comment          sql.NullString
	CreatedAt        string
	IsVisibleOnFront int
}

// OrderFilter holds filtering options for FindByCustomerID.
type OrderFilter struct {
	NumberEq      *string
	NumberMatch   *string
	NumberIn      []string
	DateFrom      *string
	DateTo        *string
	StatusEq      *string
	StatusIn      []string
	GrandTotalFrom *string
	GrandTotalTo   *string
}

// OrderSort holds sorting options for FindByCustomerID.
type OrderSort struct {
	Field     string // "order_date", "number", "grand_total"
	Direction string // "ASC", "DESC"
}

// OrderRepository provides data access for Magento sales tables.
type OrderRepository struct {
	db           *sql.DB
	statusLabels map[string]string
	statusLoaded bool
}

// NewOrderRepository creates a new OrderRepository.
func NewOrderRepository(db *sql.DB) *OrderRepository {
	return &OrderRepository{db: db, statusLabels: make(map[string]string)}
}

// GetStatusLabel resolves a raw status code to its display label from sales_order_status.
func (r *OrderRepository) GetStatusLabel(status string) string {
	if !r.statusLoaded {
		r.loadStatusLabels()
	}
	if label, ok := r.statusLabels[status]; ok {
		return label
	}
	return status
}

func (r *OrderRepository) loadStatusLabels() {
	rows, err := r.db.Query("SELECT status, label FROM sales_order_status")
	if err != nil {
		r.statusLoaded = true
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status, label string
		if rows.Scan(&status, &label) == nil {
			r.statusLabels[status] = label
		}
	}
	r.statusLoaded = true
}

// FindByCustomerID finds orders for a customer with filtering, sorting, and pagination.
// Returns list of orders, total count, and error.
// Returns nil slice and nil error for empty results (not an error).
func (r *OrderRepository) FindByCustomerID(ctx context.Context, customerID int, filter *OrderFilter, sort *OrderSort, currentPage, pageSize int) ([]*OrderData, int, error) {
	// Build WHERE clause dynamically
	whereClause := "customer_id = ?"
	args := []interface{}{customerID}

	if filter != nil {
		if filter.NumberEq != nil {
			whereClause += " AND increment_id = ?"
			args = append(args, *filter.NumberEq)
		}
		if filter.NumberMatch != nil {
			whereClause += " AND increment_id LIKE ?"
			args = append(args, "%"+*filter.NumberMatch+"%")
		}
		if len(filter.NumberIn) > 0 {
			placeholders := inPlaceholders(len(filter.NumberIn))
			whereClause += fmt.Sprintf(" AND increment_id IN (%s)", placeholders)
			for _, num := range filter.NumberIn {
				args = append(args, num)
			}
		}
		if filter.DateFrom != nil {
			whereClause += " AND created_at >= ?"
			args = append(args, *filter.DateFrom)
		}
		if filter.DateTo != nil {
			whereClause += " AND created_at <= ?"
			args = append(args, *filter.DateTo)
		}
		if filter.StatusEq != nil {
			whereClause += " AND status = ?"
			args = append(args, *filter.StatusEq)
		}
		if len(filter.StatusIn) > 0 {
			placeholders := inPlaceholders(len(filter.StatusIn))
			whereClause += fmt.Sprintf(" AND status IN (%s)", placeholders)
			for _, s := range filter.StatusIn {
				args = append(args, s)
			}
		}
		if filter.GrandTotalFrom != nil {
			whereClause += " AND grand_total >= ?"
			args = append(args, *filter.GrandTotalFrom)
		}
		if filter.GrandTotalTo != nil {
			whereClause += " AND grand_total <= ?"
			args = append(args, *filter.GrandTotalTo)
		}
	}

	// Get total count with same WHERE clause
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	var totalCount int
	err := r.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM sales_order WHERE %s", whereClause),
		countArgs...,
	).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("count orders failed: %w", err)
	}

	// Determine sort field and direction (whitelist to prevent SQL injection)
	sortField := "created_at"
	sortDirection := "ASC"
	if sort != nil {
		switch strings.ToUpper(sort.Direction) {
		case "ASC":
			sortDirection = "ASC"
		case "DESC":
			sortDirection = "DESC"
		}
		switch sort.Field {
		case "number":
			sortField = "increment_id"
		case "order_date":
			sortField = "created_at"
		case "grand_total":
			sortField = "grand_total"
		}
	}

	// Calculate LIMIT and OFFSET
	limit := pageSize
	offset := (currentPage - 1) * pageSize

	// Fetch orders with pagination
	query := fmt.Sprintf(`
		SELECT entity_id, increment_id, customer_id, store_id, status, state,
		       created_at, grand_total, subtotal, tax_amount, shipping_amount,
		       shipping_method, shipping_description, order_currency_code
		FROM sales_order
		WHERE %s
		ORDER BY %s %s
		LIMIT ? OFFSET ?
	`, whereClause, sortField, sortDirection)

	queryArgs := append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query orders failed: %w", err)
	}
	defer rows.Close()

	var orders []*OrderData
	for rows.Next() {
		var o OrderData
		err := rows.Scan(
			&o.EntityID, &o.IncrementID, &o.CustomerID, &o.StoreID, &o.Status, &o.State,
			&o.CreatedAt, &o.GrandTotal, &o.Subtotal, &o.TaxAmount, &o.ShippingAmount,
			&o.ShippingMethod, &o.ShippingDescription, &o.OrderCurrencyCode,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan order row failed: %w", err)
		}
		orders = append(orders, &o)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate orders failed: %w", err)
	}

	if len(orders) == 0 {
		return nil, totalCount, nil
	}

	return orders, totalCount, nil
}

// GetItems loads order items for a batch of order IDs.
// Returns map[order_id][]*OrderItemData.
func (r *OrderRepository) GetItems(ctx context.Context, orderIDs []int) (map[int][]*OrderItemData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*OrderItemData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT item_id, order_id, product_type, sku, name, COALESCE(price, 0),
			       COALESCE(qty_ordered, 0), COALESCE(qty_shipped, 0), COALESCE(qty_invoiced, 0),
			       COALESCE(qty_refunded, 0), COALESCE(qty_canceled, 0),
			       COALESCE(row_total, 0), COALESCE(tax_amount, 0), COALESCE(discount_amount, 0)
			FROM sales_order_item
			WHERE order_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query order items failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*OrderItemData)
	for rows.Next() {
		var item OrderItemData
		err := rows.Scan(
			&item.ItemID, &item.OrderID, &item.ProductType, &item.Sku, &item.Name, &item.Price,
			&item.QtyOrdered, &item.QtyShipped, &item.QtyInvoiced, &item.QtyRefunded, &item.QtyCanceled,
			&item.RowTotal, &item.TaxAmount, &item.DiscountAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("scan order item row failed: %w", err)
		}
		result[item.OrderID] = append(result[item.OrderID], &item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items failed: %w", err)
	}

	return result, nil
}

// GetAddresses loads order addresses for a batch of order IDs.
// Returns map[order_id][]*OrderAddressData.
func (r *OrderRepository) GetAddresses(ctx context.Context, orderIDs []int) (map[int][]*OrderAddressData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*OrderAddressData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, address_type, firstname, lastname, street,
			       city, region, region_id, postcode, country_id, telephone, company,
			       prefix, suffix, fax, vat_id, middlename
			FROM sales_order_address
			WHERE parent_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query order addresses failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*OrderAddressData)
	for rows.Next() {
		var addr OrderAddressData
		err := rows.Scan(
			&addr.EntityID, &addr.ParentID, &addr.AddressType, &addr.Firstname, &addr.Lastname, &addr.Street,
			&addr.City, &addr.Region, &addr.RegionID, &addr.Postcode, &addr.CountryID, &addr.Telephone, &addr.Company,
			&addr.Prefix, &addr.Suffix, &addr.Fax, &addr.VatID, &addr.Middlename,
		)
		if err != nil {
			return nil, fmt.Errorf("scan order address row failed: %w", err)
		}
		result[addr.ParentID] = append(result[addr.ParentID], &addr)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order addresses failed: %w", err)
	}

	return result, nil
}

// GetPayments loads order payments for a batch of order IDs.
// Returns map[order_id][]*OrderPaymentData.
func (r *OrderRepository) GetPayments(ctx context.Context, orderIDs []int) (map[int][]*OrderPaymentData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*OrderPaymentData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, method, additional_information
			FROM sales_order_payment
			WHERE parent_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query order payments failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*OrderPaymentData)
	for rows.Next() {
		var payment OrderPaymentData
		err := rows.Scan(
			&payment.EntityID, &payment.ParentID, &payment.Method, &payment.AdditionalInformation,
		)
		if err != nil {
			return nil, fmt.Errorf("scan order payment row failed: %w", err)
		}
		result[payment.ParentID] = append(result[payment.ParentID], &payment)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order payments failed: %w", err)
	}

	return result, nil
}

// GetInvoices loads invoices for a batch of order IDs.
// Returns map[order_id][]*InvoiceData.
func (r *OrderRepository) GetInvoices(ctx context.Context, orderIDs []int) (map[int][]*InvoiceData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*InvoiceData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, order_id, increment_id, grand_total, subtotal,
			       tax_amount, shipping_amount, created_at
			FROM sales_invoice
			WHERE order_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query invoices failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*InvoiceData)
	for rows.Next() {
		var invoice InvoiceData
		err := rows.Scan(
			&invoice.EntityID, &invoice.OrderID, &invoice.IncrementID, &invoice.GrandTotal, &invoice.Subtotal,
			&invoice.TaxAmount, &invoice.ShippingAmount, &invoice.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan invoice row failed: %w", err)
		}
		result[invoice.OrderID] = append(result[invoice.OrderID], &invoice)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invoices failed: %w", err)
	}

	return result, nil
}

// GetInvoiceItems loads invoice items for a batch of invoice IDs.
// Returns map[invoice_id][]*InvoiceItemData.
func (r *OrderRepository) GetInvoiceItems(ctx context.Context, invoiceIDs []int) (map[int][]*InvoiceItemData, error) {
	if len(invoiceIDs) == 0 {
		return map[int][]*InvoiceItemData{}, nil
	}

	placeholders := inPlaceholders(len(invoiceIDs))
	args := intsToAny(invoiceIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, order_item_id, sku, name,
			       COALESCE(price, 0), qty,
			       COALESCE(row_total, 0), COALESCE(tax_amount, 0), COALESCE(discount_amount, 0)
			FROM sales_invoice_item
			WHERE parent_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query invoice items failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*InvoiceItemData)
	for rows.Next() {
		var item InvoiceItemData
		err := rows.Scan(
			&item.EntityID, &item.ParentID, &item.OrderItemID, &item.Sku, &item.Name, &item.Price, &item.Qty,
			&item.RowTotal, &item.TaxAmount, &item.DiscountAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("scan invoice item row failed: %w", err)
		}
		result[item.ParentID] = append(result[item.ParentID], &item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invoice items failed: %w", err)
	}

	return result, nil
}

// GetShipments loads shipments for a batch of order IDs.
// Returns map[order_id][]*ShipmentData.
func (r *OrderRepository) GetShipments(ctx context.Context, orderIDs []int) (map[int][]*ShipmentData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*ShipmentData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, order_id, increment_id, created_at
			FROM sales_shipment
			WHERE order_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query shipments failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*ShipmentData)
	for rows.Next() {
		var shipment ShipmentData
		err := rows.Scan(
			&shipment.EntityID, &shipment.OrderID, &shipment.IncrementID, &shipment.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan shipment row failed: %w", err)
		}
		result[shipment.OrderID] = append(result[shipment.OrderID], &shipment)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shipments failed: %w", err)
	}

	return result, nil
}

// GetShipmentItems loads shipment items for a batch of shipment IDs.
// Returns map[shipment_id][]*ShipmentItemData.
func (r *OrderRepository) GetShipmentItems(ctx context.Context, shipmentIDs []int) (map[int][]*ShipmentItemData, error) {
	if len(shipmentIDs) == 0 {
		return map[int][]*ShipmentItemData{}, nil
	}

	placeholders := inPlaceholders(len(shipmentIDs))
	args := intsToAny(shipmentIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, order_item_id, sku, name, qty
			FROM sales_shipment_item
			WHERE parent_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query shipment items failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*ShipmentItemData)
	for rows.Next() {
		var item ShipmentItemData
		err := rows.Scan(
			&item.EntityID, &item.ParentID, &item.OrderItemID, &item.Sku, &item.Name, &item.Qty,
		)
		if err != nil {
			return nil, fmt.Errorf("scan shipment item row failed: %w", err)
		}
		result[item.ParentID] = append(result[item.ParentID], &item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shipment items failed: %w", err)
	}

	return result, nil
}

// GetShipmentTracks loads shipment tracking records for a batch of shipment IDs.
// Returns map[shipment_id][]*ShipmentTrackData.
func (r *OrderRepository) GetShipmentTracks(ctx context.Context, shipmentIDs []int) (map[int][]*ShipmentTrackData, error) {
	if len(shipmentIDs) == 0 {
		return map[int][]*ShipmentTrackData{}, nil
	}

	placeholders := inPlaceholders(len(shipmentIDs))
	args := intsToAny(shipmentIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, track_number, title, carrier_code
			FROM sales_shipment_track
			WHERE parent_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query shipment tracks failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*ShipmentTrackData)
	for rows.Next() {
		var track ShipmentTrackData
		err := rows.Scan(
			&track.EntityID, &track.ParentID, &track.TrackNumber, &track.Title, &track.CarrierCode,
		)
		if err != nil {
			return nil, fmt.Errorf("scan shipment track row failed: %w", err)
		}
		result[track.ParentID] = append(result[track.ParentID], &track)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shipment tracks failed: %w", err)
	}

	return result, nil
}

// GetCreditMemos loads credit memos for a batch of order IDs.
// Returns map[order_id][]*CreditMemoData.
func (r *OrderRepository) GetCreditMemos(ctx context.Context, orderIDs []int) (map[int][]*CreditMemoData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*CreditMemoData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, order_id, increment_id, grand_total, subtotal,
			       tax_amount, shipping_amount, created_at
			FROM sales_creditmemo
			WHERE order_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query credit memos failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*CreditMemoData)
	for rows.Next() {
		var memo CreditMemoData
		err := rows.Scan(
			&memo.EntityID, &memo.OrderID, &memo.IncrementID, &memo.GrandTotal, &memo.Subtotal,
			&memo.TaxAmount, &memo.ShippingAmount, &memo.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan credit memo row failed: %w", err)
		}
		result[memo.OrderID] = append(result[memo.OrderID], &memo)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credit memos failed: %w", err)
	}

	return result, nil
}

// GetCreditMemoItems loads credit memo items for a batch of credit memo IDs.
// Returns map[memo_id][]*CreditMemoItemData.
func (r *OrderRepository) GetCreditMemoItems(ctx context.Context, memoIDs []int) (map[int][]*CreditMemoItemData, error) {
	if len(memoIDs) == 0 {
		return map[int][]*CreditMemoItemData{}, nil
	}

	placeholders := inPlaceholders(len(memoIDs))
	args := intsToAny(memoIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, order_item_id, sku, name,
			       COALESCE(price, 0), qty,
			       COALESCE(row_total, 0), COALESCE(tax_amount, 0), COALESCE(discount_amount, 0)
			FROM sales_creditmemo_item
			WHERE parent_id IN (%s)
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query credit memo items failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*CreditMemoItemData)
	for rows.Next() {
		var item CreditMemoItemData
		err := rows.Scan(
			&item.EntityID, &item.ParentID, &item.OrderItemID, &item.Sku, &item.Name, &item.Price, &item.Qty,
			&item.RowTotal, &item.TaxAmount, &item.DiscountAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("scan credit memo item row failed: %w", err)
		}
		result[item.ParentID] = append(result[item.ParentID], &item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credit memo items failed: %w", err)
	}

	return result, nil
}

// GetComments loads visible comments for a batch of order IDs.
// Returns map[order_id][]*OrderCommentData.
func (r *OrderRepository) GetComments(ctx context.Context, orderIDs []int) (map[int][]*OrderCommentData, error) {
	if len(orderIDs) == 0 {
		return map[int][]*OrderCommentData{}, nil
	}

	placeholders := inPlaceholders(len(orderIDs))
	args := intsToAny(orderIDs)

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT entity_id, parent_id, comment, created_at, is_visible_on_front
			FROM sales_order_status_history
			WHERE parent_id IN (%s) AND is_visible_on_front = 1
			ORDER BY created_at ASC
		`, placeholders),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("query order comments failed: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]*OrderCommentData)
	for rows.Next() {
		var comment OrderCommentData
		err := rows.Scan(
			&comment.EntityID, &comment.ParentID, &comment.Comment, &comment.CreatedAt, &comment.IsVisibleOnFront,
		)
		if err != nil {
			return nil, fmt.Errorf("scan order comment row failed: %w", err)
		}
		result[comment.ParentID] = append(result[comment.ParentID], &comment)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order comments failed: %w", err)
	}

	return result, nil
}

// inPlaceholders builds a comma-separated list of ? placeholders.
func inPlaceholders(n int) string {
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// intsToAny converts []int to []any for variadic SQL arguments.
func intsToAny(ids []int) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}
