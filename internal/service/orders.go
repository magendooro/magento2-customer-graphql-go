package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/magendooro/magento2-customer-graphql-go/graph/model"
	"github.com/magendooro/magento2-customer-graphql-go/internal/config"
	"github.com/magendooro/magento2-customer-graphql-go/internal/repository"
)

type OrderService struct {
	orderRepo   *repository.OrderRepository
	cp          *config.ConfigProvider
	storeTZ     *time.Location
	storeTZOnce sync.Once
}

func NewOrderService(orderRepo *repository.OrderRepository, cp *config.ConfigProvider) *OrderService {
	return &OrderService{orderRepo: orderRepo, cp: cp}
}

// getStoreTimezone loads the Magento store timezone from ConfigProvider.
func (s *OrderService) getStoreTimezone() *time.Location {
	s.storeTZOnce.Do(func() {
		tz := s.cp.GetDefault("general/locale/timezone")
		if tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				s.storeTZ = loc
				return
			}
		}
		s.storeTZ = time.UTC
	})
	return s.storeTZ
}

// formatOrderDate converts a MySQL timestamp to the Magento store timezone.
// Magento: $this->timezone->date($createdAt)->format('Y-m-d H:i:s')
func (s *OrderService) formatOrderDate(mysqlTimestamp string) string {
	// MySQL parseTime with loc=UTC gives us time in UTC
	// But our DSN might use local time, so parse the raw string
	t, err := time.Parse("2006-01-02T15:04:05Z", mysqlTimestamp)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", mysqlTimestamp)
		if err != nil {
			return formatDateTime(mysqlTimestamp)
		}
	}
	// Convert to store timezone (matching Magento's TimezoneInterface::date())
	return t.In(s.getStoreTimezone()).Format("2006-01-02 15:04:05")
}

func (s *OrderService) GetOrders(
	ctx context.Context,
	customerID int,
	filter *model.CustomerOrdersFilterInput,
	sort *model.CustomerOrderSortInput,
	currentPage, pageSize int,
) (*model.CustomerOrders, error) {
	// 1. Auth check
	if customerID == 0 {
		return nil, errors.New("customer not authenticated")
	}

	// 2. Map filter/sort
	repoFilter := mapFilter(filter)
	repoSort := mapSort(sort)

	// 3. Fetch orders
	orders, totalCount, err := s.orderRepo.FindByCustomerID(ctx, customerID, repoFilter, repoSort, currentPage, pageSize)
	if err != nil {
		return nil, fmt.Errorf("fetch orders: %w", err)
	}

	if len(orders) == 0 {
		totalPages := 0
		pageInfo := &model.SearchResultPageInfo{
			CurrentPage: &currentPage,
			PageSize:    &pageSize,
			TotalPages:  &totalPages,
		}
		zero := 0
		return &model.CustomerOrders{
			Items:      []*model.CustomerOrder{},
			PageInfo:   pageInfo,
			TotalCount: &zero,
		}, nil
	}

	// Collect order IDs
	orderIDs := make([]int, len(orders))
	for i, o := range orders {
		orderIDs[i] = o.EntityID
	}

	// Batch load sub-resources using errgroup
	var (
		itemsMap        map[int][]*repository.OrderItemData
		addressesMap    map[int][]*repository.OrderAddressData
		paymentsMap     map[int][]*repository.OrderPaymentData
		invoicesMap     map[int][]*repository.InvoiceData
		shipmentsMap    map[int][]*repository.ShipmentData
		creditMemosMap  map[int][]*repository.CreditMemoData
		commentsMap     map[int][]*repository.OrderCommentData
	)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var e error
		itemsMap, e = s.orderRepo.GetItems(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("items: %w", e)
		}
		return nil
	})

	g.Go(func() error {
		var e error
		addressesMap, e = s.orderRepo.GetAddresses(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("addresses: %w", e)
		}
		return nil
	})

	g.Go(func() error {
		var e error
		paymentsMap, e = s.orderRepo.GetPayments(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("payments: %w", e)
		}
		return nil
	})

	g.Go(func() error {
		var e error
		invoicesMap, e = s.orderRepo.GetInvoices(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("invoices: %w", e)
		}
		return nil
	})

	g.Go(func() error {
		var e error
		shipmentsMap, e = s.orderRepo.GetShipments(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("shipments: %w", e)
		}
		return nil
	})

	g.Go(func() error {
		var e error
		creditMemosMap, e = s.orderRepo.GetCreditMemos(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("credit memos: %w", e)
		}
		return nil
	})

	g.Go(func() error {
		var e error
		commentsMap, e = s.orderRepo.GetComments(gctx, orderIDs)
		if e != nil {
			return fmt.Errorf("comments: %w", e)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Collect invoice IDs for invoice items
	var invoiceIDs []int
	for _, invList := range invoicesMap {
		for _, inv := range invList {
			invoiceIDs = append(invoiceIDs, inv.EntityID)
		}
	}

	// Collect shipment IDs for shipment items and tracks
	var shipmentIDs []int
	for _, shipList := range shipmentsMap {
		for _, ship := range shipList {
			shipmentIDs = append(shipmentIDs, ship.EntityID)
		}
	}

	// Collect credit memo IDs for credit memo items
	var creditMemoIDs []int
	for _, cmList := range creditMemosMap {
		for _, cm := range cmList {
			creditMemoIDs = append(creditMemoIDs, cm.EntityID)
		}
	}

	// Load items for sub-resources
	invoiceItemsMap := make(map[int][]*repository.InvoiceItemData)
	shipmentItemsMap := make(map[int][]*repository.ShipmentItemData)
	shipmentTracksMap := make(map[int][]*repository.ShipmentTrackData)
	creditMemoItemsMap := make(map[int][]*repository.CreditMemoItemData)

	if len(invoiceIDs) > 0 {
		items, err := s.orderRepo.GetInvoiceItems(ctx, invoiceIDs)
		if err != nil {
			return nil, fmt.Errorf("invoice items: %w", err)
		}
		invoiceItemsMap = items
	}

	if len(shipmentIDs) > 0 {
		items, err := s.orderRepo.GetShipmentItems(ctx, shipmentIDs)
		if err != nil {
			return nil, fmt.Errorf("shipment items: %w", err)
		}
		shipmentItemsMap = items

		tracks, err := s.orderRepo.GetShipmentTracks(ctx, shipmentIDs)
		if err != nil {
			return nil, fmt.Errorf("shipment tracks: %w", err)
		}
		shipmentTracksMap = tracks
	}

	if len(creditMemoIDs) > 0 {
		items, err := s.orderRepo.GetCreditMemoItems(ctx, creditMemoIDs)
		if err != nil {
			return nil, fmt.Errorf("credit memo items: %w", err)
		}
		creditMemoItemsMap = items
	}

	// Map orders
	var result []*model.CustomerOrder
	for _, o := range orders {
		mapped := s.mapOrder(
			o,
			itemsMap[o.EntityID],
			addressesMap[o.EntityID],
			paymentsMap[o.EntityID],
			invoicesMap[o.EntityID],
			invoiceItemsMap,
			shipmentsMap[o.EntityID],
			shipmentItemsMap,
			shipmentTracksMap,
			creditMemosMap[o.EntityID],
			creditMemoItemsMap,
			commentsMap[o.EntityID],
		)
		result = append(result, mapped)
	}

	// Pagination
	totalPages := (totalCount + pageSize - 1) / pageSize
	tc := totalCount
	pageInfo := &model.SearchResultPageInfo{
		CurrentPage: &currentPage,
		PageSize:    &pageSize,
		TotalPages:  &totalPages,
	}

	return &model.CustomerOrders{
		Items:      result,
		PageInfo:   pageInfo,
		TotalCount: &tc,
	}, nil
}

// mapFilter converts GraphQL filter input to repository filter
func mapFilter(f *model.CustomerOrdersFilterInput) *repository.OrderFilter {
	if f == nil {
		return nil
	}
	filter := &repository.OrderFilter{}
	if f.Number != nil {
		filter.NumberEq = f.Number.Eq
		filter.NumberMatch = f.Number.Match
		if f.Number.In != nil {
			for _, v := range f.Number.In {
				if v != nil {
					filter.NumberIn = append(filter.NumberIn, *v)
				}
			}
		}
	}
	if f.OrderDate != nil {
		filter.DateFrom = f.OrderDate.From
		filter.DateTo = f.OrderDate.To
	}
	if f.Status != nil {
		filter.StatusEq = f.Status.Eq
		if f.Status.In != nil {
			for _, v := range f.Status.In {
				if v != nil {
					filter.StatusIn = append(filter.StatusIn, *v)
				}
			}
		}
	}
	if f.GrandTotal != nil {
		filter.GrandTotalFrom = f.GrandTotal.From
		filter.GrandTotalTo = f.GrandTotal.To
	}
	return filter
}

// mapSort converts GraphQL sort input to repository sort
func mapSort(s *model.CustomerOrderSortInput) *repository.OrderSort {
	if s == nil {
		return nil
	}
	sort := &repository.OrderSort{}
	if s.OrderDate != nil {
		sort.Field = "order_date"
		sort.Direction = string(*s.OrderDate)
	} else if s.Number != nil {
		sort.Field = "number"
		sort.Direction = string(*s.Number)
	} else if s.GrandTotal != nil {
		sort.Field = "grand_total"
		sort.Direction = string(*s.GrandTotal)
	}
	return sort
}

// mapMoney creates a Money object from amount and currency code
func mapMoney(amount float64, currencyCode string) *model.Money {
	v := amount
	currency := parseCurrency(currencyCode)
	return &model.Money{Value: &v, Currency: currency}
}

// parseCurrency converts string currency code to CurrencyEnum
func parseCurrency(code string) *model.CurrencyEnum {
	c := model.CurrencyEnum(strings.ToUpper(code))
	if c.IsValid() {
		return &c
	}
	usd := model.CurrencyEnumUsd
	return &usd
}

// mapOrderAddress converts repository address data to GraphQL model
func mapOrderAddress(d *repository.OrderAddressData) *model.OrderAddress {
	if d == nil {
		return nil
	}
	addr := &model.OrderAddress{
		Firstname: d.Firstname,
		Lastname:  d.Lastname,
		City:      d.City,
	}
	// Street: newline-separated string → []*string
	if d.Street.Valid {
		for _, line := range strings.Split(d.Street.String, "\n") {
			l := strings.TrimSpace(line)
			if l != "" {
				addr.Street = append(addr.Street, &l)
			}
		}
	}
	if d.Middlename.Valid {
		addr.Middlename = &d.Middlename.String
	}
	if d.Prefix.Valid {
		addr.Prefix = &d.Prefix.String
	}
	if d.Suffix.Valid {
		addr.Suffix = &d.Suffix.String
	}
	if d.Company.Valid {
		addr.Company = &d.Company.String
	}
	if d.Region.Valid {
		addr.Region = &d.Region.String
	}
	if d.RegionID.Valid {
		rid := int(d.RegionID.Int64)
		addr.RegionID = &rid
	}
	if d.Postcode.Valid {
		addr.Postcode = &d.Postcode.String
	}
	if d.Telephone.Valid {
		addr.Telephone = &d.Telephone.String
	}
	if d.Fax.Valid {
		addr.Fax = &d.Fax.String
	}
	if d.VatID.Valid {
		addr.VatID = &d.VatID.String
	}
	if d.CountryID != "" {
		cc := model.CountryCodeEnum(d.CountryID)
		if cc.IsValid() {
			addr.CountryCode = &cc
		}
	}
	return addr
}

// mapOrderItem converts repository order item to GraphQL model


// mapOrderItemWithCurrency converts repository order item to GraphQL model with currency from order
func mapOrderItemWithCurrency(d *repository.OrderItemData, currencyCode string) model.OrderItemInterface {
	id := strconv.Itoa(d.ItemID)
	price := mapMoney(d.Price, currencyCode)
	item := model.OrderItem{
		ID:               id,
		ProductSku:       d.Sku,
		ProductSalePrice: price,
		QuantityOrdered:  &d.QtyOrdered,
		QuantityShipped:  &d.QtyShipped,
		QuantityInvoiced: &d.QtyInvoiced,
		QuantityRefunded: &d.QtyRefunded,
		QuantityCanceled: &d.QtyCanceled,
	}
	if d.Name.Valid {
		item.ProductName = &d.Name.String
	}
	if d.ProductType.Valid {
		item.ProductType = &d.ProductType.String
	}
	if d.Status.Valid {
		item.Status = &d.Status.String
	}
	return item
}

// mapPayment converts repository payment data to GraphQL model
func mapPayment(d *repository.OrderPaymentData) *model.OrderPaymentMethod {
	method := &model.OrderPaymentMethod{
		Name: d.Method,
		Type: d.Method,
	}
	return method
}

// mapComment converts repository comment data to GraphQL model
func mapComment(d *repository.OrderCommentData) *model.SalesCommentItem {
	if !d.Comment.Valid || d.Comment.String == "" {
		return nil
	}
	return &model.SalesCommentItem{
		Message:   d.Comment.String,
		Timestamp: d.CreatedAt,
	}
}

// mapInvoice converts repository invoice data to GraphQL model
func mapInvoice(d *repository.InvoiceData, items []*repository.InvoiceItemData, currencyCode string) *model.Invoice {
	id := strconv.Itoa(d.EntityID)
	inv := &model.Invoice{
		ID:     id,
		Number: d.IncrementID,
	}
	// Total
	inv.Total = &model.InvoiceTotal{
		GrandTotal:    mapMoney(d.GrandTotal, currencyCode),
		Subtotal:      mapMoney(d.Subtotal, currencyCode),
		TotalTax:      mapMoney(d.TaxAmount, currencyCode),
		TotalShipping: mapMoney(d.ShippingAmount, currencyCode),
	}
	// Items
	for _, item := range items {
		iid := strconv.Itoa(item.EntityID)
		price := mapMoney(item.Price, currencyCode)
		invItem := model.InvoiceItem{
			ID:               iid,
			ProductSku:       item.Sku,
			ProductSalePrice: price,
			QuantityInvoiced: item.Qty,
		}
		if item.Name.Valid {
			invItem.ProductName = &item.Name.String
		}
		inv.Items = append(inv.Items, invItem)
	}
	return inv
}

// mapShipment converts repository shipment data to GraphQL model
func mapShipment(d *repository.ShipmentData, items []*repository.ShipmentItemData, tracks []*repository.ShipmentTrackData) *model.OrderShipment {
	id := strconv.Itoa(d.EntityID)
	shipment := &model.OrderShipment{
		ID:     id,
		Number: d.IncrementID,
	}
	for _, track := range tracks {
		t := &model.ShipmentTracking{
			Carrier: track.CarrierCode,
		}
		if track.Title.Valid {
			t.Title = track.Title.String
		} else {
			t.Title = track.CarrierCode
		}
		if track.TrackNumber.Valid {
			t.Number = &track.TrackNumber.String
		}
		shipment.Tracking = append(shipment.Tracking, t)
	}
	for _, item := range items {
		iid := strconv.Itoa(item.EntityID)
		si := model.ShipmentItem{
			ID:              iid,
			QuantityShipped: item.Qty,
		}
		if item.Sku.Valid {
			si.ProductSku = item.Sku.String
		}
		if item.Name.Valid {
			si.ProductName = &item.Name.String
		}
		shipment.Items = append(shipment.Items, si)
	}
	return shipment
}

// mapCreditMemo converts repository credit memo data to GraphQL model
func mapCreditMemo(d *repository.CreditMemoData, items []*repository.CreditMemoItemData, currencyCode string) *model.CreditMemo {
	id := strconv.Itoa(d.EntityID)
	cm := &model.CreditMemo{
		ID:     id,
		Number: d.IncrementID,
	}
	cm.Total = &model.CreditMemoTotal{
		GrandTotal:    mapMoney(d.GrandTotal, currencyCode),
		Subtotal:      mapMoney(d.Subtotal, currencyCode),
		TotalTax:      mapMoney(d.TaxAmount, currencyCode),
		TotalShipping: mapMoney(d.ShippingAmount, currencyCode),
	}
	for _, item := range items {
		iid := strconv.Itoa(item.EntityID)
		price := mapMoney(item.Price, currencyCode)
		cmItem := model.CreditMemoItem{
			ID:               iid,
			ProductSku:       item.Sku,
			ProductSalePrice: price,
			QuantityRefunded: item.Qty,
		}
		if item.Name.Valid {
			cmItem.ProductName = &item.Name.String
		}
		cm.Items = append(cm.Items, cmItem)
	}
	return cm
}

// mapOrder converts repository order data and all sub-resources to GraphQL model
func (s *OrderService) mapOrder(
	d *repository.OrderData,
	items []*repository.OrderItemData,
	addresses []*repository.OrderAddressData,
	payments []*repository.OrderPaymentData,
	invoices []*repository.InvoiceData,
	invoiceItemsMap map[int][]*repository.InvoiceItemData,
	shipments []*repository.ShipmentData,
	shipmentItemsMap map[int][]*repository.ShipmentItemData,
	shipmentTracksMap map[int][]*repository.ShipmentTrackData,
	creditMemos []*repository.CreditMemoData,
	creditMemoItemsMap map[int][]*repository.CreditMemoItemData,
	comments []*repository.OrderCommentData,
) *model.CustomerOrder {
	id := strconv.Itoa(d.EntityID)
	currency := d.OrderCurrencyCode

	order := &model.CustomerOrder{
		ID:          id,
		OrderNumber: d.IncrementID,
		Number:      d.IncrementID,
		OrderDate:   s.formatOrderDate(d.CreatedAt),
		Status:      s.orderRepo.GetStatusLabel(d.Status),
	}

	if d.ShippingMethod.Valid {
		order.ShippingMethod = &d.ShippingMethod.String
	}

	// Addresses
	for _, addr := range addresses {
		mapped := mapOrderAddress(addr)
		if addr.AddressType == "shipping" {
			order.ShippingAddress = mapped
		} else if addr.AddressType == "billing" {
			order.BillingAddress = mapped
		}
	}

	// Payments
	for _, p := range payments {
		order.PaymentMethods = append(order.PaymentMethods, mapPayment(p))
	}
	if order.PaymentMethods == nil {
		order.PaymentMethods = []*model.OrderPaymentMethod{}
	}

	// Items
	for _, item := range items {
		order.Items = append(order.Items, mapOrderItemWithCurrency(item, currency))
	}
	if order.Items == nil {
		order.Items = []model.OrderItemInterface{}
	}

	// Total
	order.Total = &model.OrderTotal{
		GrandTotal:    mapMoney(d.GrandTotal, currency),
		Subtotal:      mapMoney(d.Subtotal, currency),
		TotalTax:      mapMoney(d.TaxAmount, currency),
		TotalShipping: mapMoney(d.ShippingAmount, currency),
	}

	// Invoices
	for _, inv := range invoices {
		invItems := invoiceItemsMap[inv.EntityID]
		order.Invoices = append(order.Invoices, mapInvoice(inv, invItems, currency))
	}
	if order.Invoices == nil {
		order.Invoices = []*model.Invoice{}
	}

	// Shipments
	for _, ship := range shipments {
		shipItems := shipmentItemsMap[ship.EntityID]
		shipTracks := shipmentTracksMap[ship.EntityID]
		order.Shipments = append(order.Shipments, mapShipment(ship, shipItems, shipTracks))
	}

	// Credit memos
	for _, cm := range creditMemos {
		cmItems := creditMemoItemsMap[cm.EntityID]
		order.CreditMemos = append(order.CreditMemos, mapCreditMemo(cm, cmItems, currency))
	}

	// Comments
	for _, c := range comments {
		if mapped := mapComment(c); mapped != nil {
			order.Comments = append(order.Comments, mapped)
		}
	}

	return order
}
