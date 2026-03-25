package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/magendooro/magento2-customer-graphql-go/graph/model"
	"github.com/magendooro/magento2-customer-graphql-go/internal/repository"
)

func (s *CustomerService) mapGroup(groupID int) (*model.CustomerGroup, error) {
	data, err := s.groupRepo.GetByID(context.Background(), groupID)
	if err != nil {
		return nil, err
	}
	uid := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(data.GroupID)))
	return &model.CustomerGroup{
		UID:  uid,
		Name: data.GroupCode,
	}, nil
}

func (s *CustomerService) mapCustomer(data *repository.CustomerData) *model.Customer {
	id := strconv.Itoa(data.EntityID)
	createdAt := formatDateTime(data.CreatedAt)

	var defaultBilling, defaultShipping *string
	if data.DefaultBilling != nil {
		v := strconv.Itoa(*data.DefaultBilling)
		defaultBilling = &v
	}
	if data.DefaultShipping != nil {
		v := strconv.Itoa(*data.DefaultShipping)
		defaultShipping = &v
	}

	// Magento: confirmation=NULL → ACCOUNT_CONFIRMATION_NOT_REQUIRED (default for most accounts).
	// confirmation=non-null with value → account needs confirmation (pending).
	// Magento GraphQL only has two enum values; it uses ACCOUNT_CONFIRMATION_NOT_REQUIRED
	// for both "confirmed" and "doesn't need confirmation" when the field is NULL.
	confirmStatus := model.ConfirmationStatusEnumAccountConfirmationNotRequired

	var dob *string
	if data.Dob != nil && *data.Dob != "" {
		d := formatDate(*data.Dob)
		dob = &d
	}

	// Resolve customer group
	var group *model.CustomerGroup
	if g, err := s.mapGroup(data.GroupID); err == nil {
		group = g
	}

	return &model.Customer{
		ID:                 id,
		Firstname:          data.Firstname,
		Lastname:           data.Lastname,
		Middlename:         data.Middlename,
		Prefix:             data.Prefix,
		Suffix:             data.Suffix,
		Email:              &data.Email,
		Dob:                dob,
		DateOfBirth:        dob,
		Taxvat:             data.Taxvat,
		Gender:             data.Gender,
		CreatedAt:          &createdAt,
		DefaultBilling:     defaultBilling,
		DefaultShipping:    defaultShipping,
		ConfirmationStatus: confirmStatus,
		GroupID:            &data.GroupID,
		Group:              group,
	}
}

func (s *CustomerService) mapAddresses(addrs []*repository.AddressData, defaultBilling, defaultShipping *int) []*model.CustomerAddress {
	result := make([]*model.CustomerAddress, len(addrs))
	for i, a := range addrs {
		result[i] = s.mapAddress(a, defaultBilling, defaultShipping)
	}
	return result
}

func (s *CustomerService) mapAddress(a *repository.AddressData, defaultBilling, defaultShipping *int) *model.CustomerAddress {
	uid := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(a.EntityID)))
	id := a.EntityID

	isDefaultBilling := defaultBilling != nil && *defaultBilling == a.EntityID
	isDefaultShipping := defaultShipping != nil && *defaultShipping == a.EntityID

	var region *model.CustomerAddressRegion
	if a.RegionID != nil || a.Region != nil {
		region = &model.CustomerAddressRegion{
			Region:   a.Region,
			RegionID: a.RegionID,
		}
		// Try to resolve region code
		if a.RegionID != nil && *a.RegionID > 0 {
			if code, _, err := s.addressRepo.GetRegion(context.Background(), *a.RegionID); err == nil {
				region.RegionCode = &code
			}
		}
	}

	var countryCode *model.CountryCodeEnum
	if a.CountryID != nil {
		cc := model.CountryCodeEnum(*a.CountryID)
		if cc.IsValid() {
			countryCode = &cc
		}
	}

	// Parse street (stored as newline-separated in Magento)
	var street []*string
	if a.Street != nil {
		lines := strings.Split(*a.Street, "\n")
		for _, l := range lines {
			line := l
			street = append(street, &line)
		}
	}

	return &model.CustomerAddress{
		ID:              &id,
		UID:             uid,
		Firstname:       a.Firstname,
		Lastname:        a.Lastname,
		Middlename:      a.Middlename,
		Prefix:          a.Prefix,
		Suffix:          a.Suffix,
		Company:         a.Company,
		Street:          street,
		City:            a.City,
		Region:          region,
		RegionID:        a.RegionID,
		Postcode:        a.Postcode,
		CountryCode:     countryCode,
		CountryID:       a.CountryID,
		Telephone:       a.Telephone,
		Fax:             a.Fax,
		VatID:           a.VatID,
		DefaultShipping: &isDefaultShipping,
		DefaultBilling:  &isDefaultBilling,
	}
}

func (s *CustomerService) mapAddressInput(input model.CustomerAddressInput) *repository.AddressData {
	a := &repository.AddressData{
		Firstname:  input.Firstname,
		Lastname:   input.Lastname,
		Middlename: input.Middlename,
		Prefix:     input.Prefix,
		Suffix:     input.Suffix,
		Company:    input.Company,
		City:       input.City,
		Postcode:   input.Postcode,
		Telephone:  input.Telephone,
		Fax:        input.Fax,
		VatID:      input.VatID,
	}

	if input.CountryCode != nil {
		cc := string(*input.CountryCode)
		a.CountryID = &cc
	}

	if input.Street != nil {
		lines := make([]string, len(input.Street))
		for i, s := range input.Street {
			if s != nil {
				lines[i] = *s
			}
		}
		street := strings.Join(lines, "\n")
		a.Street = &street
	}

	if input.Region != nil {
		a.Region = input.Region.Region
		a.RegionID = input.Region.RegionID
	}

	return a
}

func (s *CustomerService) mapAddressFields(input model.CustomerAddressInput) map[string]interface{} {
	fields := make(map[string]interface{})
	if input.Firstname != nil {
		fields["firstname"] = *input.Firstname
	}
	if input.Lastname != nil {
		fields["lastname"] = *input.Lastname
	}
	if input.Middlename != nil {
		fields["middlename"] = *input.Middlename
	}
	if input.Prefix != nil {
		fields["prefix"] = *input.Prefix
	}
	if input.Suffix != nil {
		fields["suffix"] = *input.Suffix
	}
	if input.Company != nil {
		fields["company"] = *input.Company
	}
	if input.City != nil {
		fields["city"] = *input.City
	}
	if input.Postcode != nil {
		fields["postcode"] = *input.Postcode
	}
	if input.Telephone != nil {
		fields["telephone"] = *input.Telephone
	}
	if input.Fax != nil {
		fields["fax"] = *input.Fax
	}
	if input.VatID != nil {
		fields["vat_id"] = *input.VatID
	}
	if input.CountryCode != nil {
		fields["country_id"] = string(*input.CountryCode)
	}
	if input.Street != nil {
		lines := make([]string, len(input.Street))
		for i, s := range input.Street {
			if s != nil {
				lines[i] = *s
			}
		}
		fields["street"] = strings.Join(lines, "\n")
	}
	if input.Region != nil {
		if input.Region.Region != nil {
			fields["region"] = *input.Region.Region
		}
		if input.Region.RegionID != nil {
			fields["region_id"] = *input.Region.RegionID
		}
	}
	return fields
}

// formatDate converts a datetime string to YYYY-MM-DD (Magento dob format).
func formatDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// formatDateTime converts a datetime string to "YYYY-MM-DD HH:MM:SS" (Magento created_at format).
func formatDateTime(s string) string {
	// MySQL parseTime gives "2006-01-02T15:04:05Z" or "2006-01-02 15:04:05"
	if len(s) >= 19 {
		// Replace T with space and strip timezone suffix
		result := strings.Replace(s[:19], "T", " ", 1)
		return result
	}
	return s
}

// resolveSelectOptions resolves option labels for select/multiselect EAV attributes.
func (s *CustomerService) resolveSelectOptions(ctx context.Context, entityType string, v *repository.EAVAttributeValue, storeID int) ([]*model.AttributeSelectedOption, error) {
	var attrs []*repository.EAVAttributeMeta
	var err error
	if entityType == "customer_address" {
		attrs, err = s.eavRepo.GetAddressAttributes(ctx)
	} else {
		attrs, err = s.eavRepo.GetCustomerAttributes(ctx)
	}
	if err != nil {
		return nil, err
	}

	var attrID int
	for _, a := range attrs {
		if a.AttributeCode == v.AttributeCode {
			attrID = a.AttributeID
			break
		}
	}
	if attrID == 0 {
		return nil, fmt.Errorf("attribute not found")
	}

	allOptions, err := s.eavRepo.GetOptionLabels(ctx, attrID, storeID)
	if err != nil {
		return nil, err
	}

	// Parse the selected option IDs
	selectedIDs := strings.Split(v.Value, ",")
	selectedSet := make(map[string]bool)
	for _, id := range selectedIDs {
		selectedSet[strings.TrimSpace(id)] = true
	}

	var selected []*model.AttributeSelectedOption
	for _, opt := range allOptions {
		if selectedSet[opt.Value] {
			uid := base64.StdEncoding.EncodeToString([]byte(opt.Value))
			selected = append(selected, &model.AttributeSelectedOption{
				UID:   uid,
				Label: opt.Label,
				Value: opt.Value,
			})
		}
	}
	return selected, nil
}
