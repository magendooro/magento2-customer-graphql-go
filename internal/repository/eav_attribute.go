package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// EAVAttributeMeta holds metadata for a customer or customer_address EAV attribute.
type EAVAttributeMeta struct {
	AttributeID   int
	AttributeCode string
	BackendType   string // varchar, int, text, decimal, datetime, static
	FrontendInput string // text, select, multiselect, date, boolean, etc.
	FrontendLabel string
	IsUserDefined bool
	IsVisible     bool
}

// EAVAttributeValue holds a single EAV attribute value for an entity.
type EAVAttributeValue struct {
	EntityID      int
	AttributeID   int
	AttributeCode string
	Value         string
	FrontendInput string
}

// EAVOptionValue holds an option for select/multiselect attributes.
type EAVOptionValue struct {
	OptionID int
	Value    string
	Label    string
}

// EAVAttributeRepository loads customer and customer_address EAV attributes and values.
type EAVAttributeRepository struct {
	db               *sql.DB
	customerAttrs    []*EAVAttributeMeta
	addrAttrs        []*EAVAttributeMeta
	customerLoaded   bool
	addrLoaded       bool
	mu               sync.RWMutex
}

func NewEAVAttributeRepository(db *sql.DB) *EAVAttributeRepository {
	return &EAVAttributeRepository{db: db}
}

// GetCustomerAttributes returns all user-defined, visible customer EAV attributes.
func (r *EAVAttributeRepository) GetCustomerAttributes(ctx context.Context) ([]*EAVAttributeMeta, error) {
	r.mu.RLock()
	if r.customerLoaded {
		defer r.mu.RUnlock()
		return r.customerAttrs, nil
	}
	r.mu.RUnlock()

	attrs, err := r.loadAttributes(ctx, "customer")
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.customerAttrs = attrs
	r.customerLoaded = true
	r.mu.Unlock()

	return attrs, nil
}

// GetAddressAttributes returns all user-defined, visible customer_address EAV attributes.
func (r *EAVAttributeRepository) GetAddressAttributes(ctx context.Context) ([]*EAVAttributeMeta, error) {
	r.mu.RLock()
	if r.addrLoaded {
		defer r.mu.RUnlock()
		return r.addrAttrs, nil
	}
	r.mu.RUnlock()

	attrs, err := r.loadAttributes(ctx, "customer_address")
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.addrAttrs = attrs
	r.addrLoaded = true
	r.mu.Unlock()

	return attrs, nil
}

func (r *EAVAttributeRepository) loadAttributes(ctx context.Context, entityTypeCode string) ([]*EAVAttributeMeta, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ea.attribute_id, ea.attribute_code, ea.backend_type, ea.frontend_input,
		       COALESCE(ea.frontend_label, ''), ea.is_user_defined
		FROM eav_attribute ea
		JOIN eav_entity_type eet ON ea.entity_type_id = eet.entity_type_id
		LEFT JOIN customer_eav_attribute cea ON ea.attribute_id = cea.attribute_id
		WHERE eet.entity_type_code = ?
		  AND ea.is_user_defined = 1
		  AND ea.backend_type != 'static'
		  AND COALESCE(cea.is_visible, 1) = 1
		ORDER BY ea.attribute_code`,
		entityTypeCode,
	)
	if err != nil {
		return nil, fmt.Errorf("load %s EAV attributes: %w", entityTypeCode, err)
	}
	defer rows.Close()

	var attrs []*EAVAttributeMeta
	for rows.Next() {
		var a EAVAttributeMeta
		var isUserDefined int
		err := rows.Scan(&a.AttributeID, &a.AttributeCode, &a.BackendType, &a.FrontendInput,
			&a.FrontendLabel, &isUserDefined)
		if err != nil {
			return nil, fmt.Errorf("scan EAV attribute: %w", err)
		}
		a.IsUserDefined = isUserDefined == 1
		a.IsVisible = true
		attrs = append(attrs, &a)
	}
	return attrs, rows.Err()
}

// GetValuesForEntity loads EAV values for a single entity (customer or address).
func (r *EAVAttributeRepository) GetValuesForEntity(ctx context.Context, entityTypeCode string, entityID int) ([]*EAVAttributeValue, error) {
	attrs, err := r.getAttrsForType(ctx, entityTypeCode)
	if err != nil {
		return nil, err
	}
	if len(attrs) == 0 {
		return nil, nil
	}

	tablePrefix := "customer_entity"
	if entityTypeCode == "customer_address" {
		tablePrefix = "customer_address_entity"
	}

	// Group attributes by backend_type to minimize queries
	byType := make(map[string][]*EAVAttributeMeta)
	for _, a := range attrs {
		byType[a.BackendType] = append(byType[a.BackendType], a)
	}

	// Valid backend types (whitelist to prevent SQL injection via table name)
	validBackendTypes := map[string]bool{
		"varchar": true, "int": true, "text": true, "decimal": true, "datetime": true,
	}

	var values []*EAVAttributeValue
	for backendType, typeAttrs := range byType {
		if !validBackendTypes[backendType] {
			continue // skip unknown backend types
		}
		table := tablePrefix + "_" + backendType
		attrIDs := make([]interface{}, len(typeAttrs))
		placeholders := inPlaceholders(len(typeAttrs))
		attrMap := make(map[int]*EAVAttributeMeta)
		for i, a := range typeAttrs {
			attrIDs[i] = a.AttributeID
			attrMap[a.AttributeID] = a
		}

		args := append([]interface{}{entityID}, attrIDs...)
		rows, err := r.db.QueryContext(ctx,
			fmt.Sprintf(`SELECT attribute_id, value FROM %s WHERE entity_id = ? AND attribute_id IN (%s)`,
				table, placeholders),
			args...,
		)
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", table, err)
		}

		for rows.Next() {
			var attrID int
			var val string
			if err := rows.Scan(&attrID, &val); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan %s value: %w", table, err)
			}
			meta := attrMap[attrID]
			values = append(values, &EAVAttributeValue{
				EntityID:      entityID,
				AttributeID:   attrID,
				AttributeCode: meta.AttributeCode,
				Value:         val,
				FrontendInput: meta.FrontendInput,
			})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return values, nil
}

// GetOptionLabels loads option labels for select/multiselect attributes.
func (r *EAVAttributeRepository) GetOptionLabels(ctx context.Context, attributeID int, storeID int) ([]*EAVOptionValue, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT eao.option_id,
		       COALESCE(eaov_store.value, eaov_default.value) as label
		FROM eav_attribute_option eao
		LEFT JOIN eav_attribute_option_value eaov_default
		    ON eao.option_id = eaov_default.option_id AND eaov_default.store_id = 0
		LEFT JOIN eav_attribute_option_value eaov_store
		    ON eao.option_id = eaov_store.option_id AND eaov_store.store_id = ?
		WHERE eao.attribute_id = ?
		ORDER BY eao.sort_order`,
		storeID, attributeID,
	)
	if err != nil {
		return nil, fmt.Errorf("get option labels for attribute %d: %w", attributeID, err)
	}
	defer rows.Close()

	var options []*EAVOptionValue
	for rows.Next() {
		var opt EAVOptionValue
		if err := rows.Scan(&opt.OptionID, &opt.Label); err != nil {
			return nil, err
		}
		opt.Value = fmt.Sprintf("%d", opt.OptionID)
		options = append(options, &opt)
	}
	return options, rows.Err()
}

func (r *EAVAttributeRepository) getAttrsForType(ctx context.Context, entityTypeCode string) ([]*EAVAttributeMeta, error) {
	if entityTypeCode == "customer" {
		return r.GetCustomerAttributes(ctx)
	}
	return r.GetAddressAttributes(ctx)
}
