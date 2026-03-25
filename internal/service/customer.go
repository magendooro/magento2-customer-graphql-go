package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/rs/zerolog/log"

	custerr "github.com/magendooro/magento2-customer-graphql-go/internal/errors"
	"github.com/magendooro/magento2-go-common/mgerrors"

	"github.com/magendooro/magento2-customer-graphql-go/graph/model"
	"github.com/magendooro/magento2-go-common/middleware"
	"github.com/magendooro/magento2-go-common/config"
	"github.com/magendooro/magento2-customer-graphql-go/internal/repository"
)

// Default lockout and password config (matching Magento defaults)
const (
	defaultLockoutFailures  = 10
	defaultLockoutThreshold = 10 // minutes
	defaultMinPasswordLen   = 8
	defaultRequiredClasses  = 3
)

type CustomerService struct {
	customerRepo   *repository.CustomerRepository
	addressRepo    *repository.AddressRepository
	tokenRepo      *repository.TokenRepository
	newsletterRepo *repository.NewsletterRepository
	storeRepo      *repository.StoreRepository
	groupRepo      *repository.GroupRepository
	eavRepo        *repository.EAVAttributeRepository
	cp             *config.ConfigProvider
}

func NewCustomerService(
	customerRepo *repository.CustomerRepository,
	addressRepo *repository.AddressRepository,
	tokenRepo *repository.TokenRepository,
	newsletterRepo *repository.NewsletterRepository,
	storeRepo *repository.StoreRepository,
	groupRepo *repository.GroupRepository,
	eavRepo *repository.EAVAttributeRepository,
	cp *config.ConfigProvider,
) *CustomerService {
	return &CustomerService{
		customerRepo:   customerRepo,
		addressRepo:    addressRepo,
		tokenRepo:      tokenRepo,
		newsletterRepo: newsletterRepo,
		storeRepo:      storeRepo,
		groupRepo:      groupRepo,
		eavRepo:        eavRepo,
		cp:             cp,
	}
}

// GetCustomer returns the authenticated customer's data.
func (s *CustomerService) GetCustomer(ctx context.Context) (*model.Customer, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

	data, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("customer not found: %w", err)
	}

	customer := s.mapCustomer(data)

	// Load addresses
	addresses, err := s.addressRepo.GetByCustomerID(ctx, customerID)
	if err != nil {
		log.Warn().Err(err).Int("customer_id", customerID).Msg("failed to load addresses")
	} else {
		customer.Addresses = s.mapAddresses(addresses, data.DefaultBilling, data.DefaultShipping)
	}

	// Populate addressesV2 with default pagination (#11)
	if len(customer.Addresses) > 0 {
		totalCount := len(customer.Addresses)
		currentPage := 1
		pageSize := 20
		totalPages := int(math.Ceil(float64(totalCount) / float64(pageSize)))
		customer.AddressesV2 = &model.CustomerAddresses{
			Items:      customer.Addresses,
			TotalCount: &totalCount,
			PageInfo: &model.SearchResultPageInfo{
				CurrentPage: &currentPage,
				PageSize:    &pageSize,
				TotalPages:  &totalPages,
			},
		}
	}

	// Check newsletter subscription
	subscribed, err := s.newsletterRepo.IsSubscribed(ctx, customerID)
	if err != nil {
		log.Warn().Err(err).Int("customer_id", customerID).Msg("failed to check newsletter")
	}
	customer.IsSubscribed = &subscribed

	return customer, nil
}

// GenerateToken authenticates a customer and returns a JWT token.
func (s *CustomerService) GenerateToken(ctx context.Context, email, password string) (*model.CustomerToken, error) {
	authErr := custerr.ErrAuthFailed

	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)

	data, err := s.customerRepo.GetByEmail(ctx, email, websiteID)
	if err != nil {
		return nil, authErr
	}

	if data.IsActive != 1 {
		return nil, authErr
	}

	// Check account lockout (#12)
	if err := s.checkAccountLockout(data); err != nil {
		return nil, err
	}

	if !repository.VerifyPassword(data.PasswordHash, password) {
		s.recordLoginFailure(ctx, data.EntityID)
		return nil, authErr
	}

	// Success — reset failure counters
	s.resetLoginFailures(ctx, data.EntityID)

	token, err := s.tokenRepo.Create(ctx, data.EntityID)
	if err != nil {
		return nil, fmt.Errorf("token generation failed: %w", err)
	}

	return &model.CustomerToken{Token: &token}, nil
}

// RevokeToken revokes the current customer's token.
func (s *CustomerService) RevokeToken(ctx context.Context) (*model.RevokeCustomerTokenOutput, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

	err := s.tokenRepo.RevokeAllForCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("token revocation failed: %w", err)
	}

	result := true
	return &model.RevokeCustomerTokenOutput{Result: result}, nil
}

// IsEmailAvailable checks if an email can be used for registration.
// Respects Magento's guest_checkout/login config — when disabled (default in 2.4.6+),
// always returns true to prevent email enumeration.
func (s *CustomerService) IsEmailAvailable(ctx context.Context, email string) (*model.IsEmailAvailableOutput, error) {
	// Check if email availability check is enabled (Magento default: disabled)
	storeID := middleware.GetStoreID(ctx)
	if !s.cp.GetBool("customer/account/login/email_availability_check", storeID) {
		// Config disabled — always return true (matches Magento 2.4.6+ default behavior)
		available := true
		return &model.IsEmailAvailableOutput{IsEmailAvailable: &available}, nil
	}

	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)

	exists, err := s.customerRepo.EmailExists(ctx, email, websiteID)
	if err != nil {
		return nil, err
	}

	available := !exists
	return &model.IsEmailAvailableOutput{IsEmailAvailable: &available}, nil
}

// CreateCustomer registers a new customer account.
func (s *CustomerService) CreateCustomer(ctx context.Context, input model.CustomerCreateInput) (*model.CustomerOutput, error) {
	// Validate password strength (#13)
	if err := s.validatePassword(input.Password); err != nil {
		return nil, err
	}

	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)

	exists, err := s.customerRepo.EmailExists(ctx, input.Email, websiteID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, custerr.ErrEmailAlreadyExists
	}

	passwordHash, err := repository.HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	data := &repository.CustomerData{
		WebsiteID:    websiteID,
		Email:        input.Email,
		GroupID:      1, // General
		StoreID:      storeID,
		Firstname:    &input.Firstname,
		Lastname:     &input.Lastname,
		Prefix:       input.Prefix,
		Middlename:   input.Middlename,
		Suffix:       input.Suffix,
		Dob:          input.DateOfBirth,
		Taxvat:       input.Taxvat,
		Gender:       input.Gender,
		PasswordHash: passwordHash,
	}

	id, err := s.customerRepo.Create(ctx, data)
	if err != nil {
		return nil, err
	}

	// Handle newsletter subscription
	if input.IsSubscribed != nil && *input.IsSubscribed {
		if err := s.newsletterRepo.Subscribe(ctx, id, storeID, input.Email); err != nil {
			log.Warn().Err(err).Int("customer_id", id).Msg("newsletter subscribe failed")
		}
	}

	customer, err := s.customerRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	result := s.mapCustomer(customer)
	return &model.CustomerOutput{Customer: result}, nil
}

// UpdateCustomer updates the authenticated customer's profile.
func (s *CustomerService) UpdateCustomer(ctx context.Context, input model.CustomerUpdateInput) (*model.CustomerOutput, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

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
	if input.DateOfBirth != nil {
		fields["dob"] = *input.DateOfBirth
	}
	if input.Taxvat != nil {
		fields["taxvat"] = *input.Taxvat
	}
	if input.Gender != nil {
		fields["gender"] = *input.Gender
	}

	if err := s.customerRepo.Update(ctx, customerID, fields); err != nil {
		return nil, err
	}

	// Handle newsletter
	if input.IsSubscribed != nil {
		storeID := middleware.GetStoreID(ctx)
		customer, _ := s.customerRepo.GetByID(ctx, customerID)
		if *input.IsSubscribed {
			s.newsletterRepo.Subscribe(ctx, customerID, storeID, customer.Email)
		} else {
			s.newsletterRepo.Unsubscribe(ctx, customerID)
		}
	}

	customer, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	result := s.mapCustomer(customer)
	return &model.CustomerOutput{Customer: result}, nil
}

// ChangePassword changes the authenticated customer's password.
func (s *CustomerService) ChangePassword(ctx context.Context, currentPassword, newPassword string) (*model.Customer, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

	data, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	if !repository.VerifyPassword(data.PasswordHash, currentPassword) {
		return nil, custerr.ErrPasswordMismatch
	}

	if err := s.validatePassword(newPassword); err != nil {
		return nil, err
	}

	hash, err := repository.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}

	if err := s.customerRepo.Update(ctx, customerID, map[string]interface{}{
		"password_hash": hash,
	}); err != nil {
		return nil, err
	}

	// Revoke existing tokens for security
	s.tokenRepo.RevokeAllForCustomer(ctx, customerID)

	updated, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}
	return s.mapCustomer(updated), nil
}

// UpdateEmail changes the authenticated customer's email (requires password verification).
func (s *CustomerService) UpdateEmail(ctx context.Context, email, password string) (*model.CustomerOutput, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

	data, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	if !repository.VerifyPassword(data.PasswordHash, password) {
		return nil, custerr.ErrPasswordMismatch
	}

	// Check email uniqueness within the same website
	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)
	exists, err := s.customerRepo.EmailExists(ctx, email, websiteID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, custerr.ErrEmailAlreadyExists
	}

	if err := s.customerRepo.Update(ctx, customerID, map[string]interface{}{
		"email": email,
	}); err != nil {
		return nil, err
	}

	updated, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}
	result := s.mapCustomer(updated)
	return &model.CustomerOutput{Customer: result}, nil
}

// CreateAddress creates a new address for the authenticated customer.
func (s *CustomerService) CreateAddress(ctx context.Context, input model.CustomerAddressInput) (*model.CustomerAddress, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

	data := s.mapAddressInput(input)
	data.ParentID = customerID

	id, err := s.addressRepo.Create(ctx, data)
	if err != nil {
		return nil, err
	}

	// Set as default if requested
	if input.DefaultBilling != nil && *input.DefaultBilling {
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"default_billing": id})
	}
	if input.DefaultShipping != nil && *input.DefaultShipping {
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"default_shipping": id})
	}

	created, err := s.addressRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	customer, _ := s.customerRepo.GetByID(ctx, customerID)
	return s.mapAddress(created, customer.DefaultBilling, customer.DefaultShipping), nil
}

// UpdateAddress updates an existing address.
func (s *CustomerService) UpdateAddress(ctx context.Context, addressID int, input model.CustomerAddressInput) (*model.CustomerAddress, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, mgerrors.ErrUnauthorized
	}

	// Verify ownership
	existing, err := s.addressRepo.GetByID(ctx, addressID)
	if err != nil {
		return nil, fmt.Errorf("address not found: %w", err)
	}
	if existing.ParentID != customerID {
		return nil, custerr.ErrAddressNotOwned
	}

	fields := s.mapAddressFields(input)
	if err := s.addressRepo.Update(ctx, addressID, fields); err != nil {
		return nil, err
	}

	// Handle default flags
	if input.DefaultBilling != nil && *input.DefaultBilling {
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"default_billing": addressID})
	}
	if input.DefaultShipping != nil && *input.DefaultShipping {
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"default_shipping": addressID})
	}

	updated, err := s.addressRepo.GetByID(ctx, addressID)
	if err != nil {
		return nil, err
	}

	customer, _ := s.customerRepo.GetByID(ctx, customerID)
	return s.mapAddress(updated, customer.DefaultBilling, customer.DefaultShipping), nil
}

// DeleteAddress removes an address.
func (s *CustomerService) DeleteAddress(ctx context.Context, addressID int) (bool, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return false, mgerrors.ErrUnauthorized
	}

	existing, err := s.addressRepo.GetByID(ctx, addressID)
	if err != nil {
		return false, fmt.Errorf("address not found: %w", err)
	}
	if existing.ParentID != customerID {
		return false, custerr.ErrAddressNotOwned
	}

	// Clear default references if needed
	customer, _ := s.customerRepo.GetByID(ctx, customerID)
	if customer.DefaultBilling != nil && *customer.DefaultBilling == addressID {
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"default_billing": nil})
	}
	if customer.DefaultShipping != nil && *customer.DefaultShipping == addressID {
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"default_shipping": nil})
	}

	if err := s.addressRepo.Delete(ctx, addressID); err != nil {
		return false, err
	}
	return true, nil
}

// DeleteCustomer deletes the authenticated customer's account.
func (s *CustomerService) DeleteCustomer(ctx context.Context) (bool, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return false, mgerrors.ErrUnauthorized
	}

	s.tokenRepo.RevokeAllForCustomer(ctx, customerID)

	if err := s.customerRepo.Delete(ctx, customerID); err != nil {
		return false, err
	}
	return true, nil
}

// RequestPasswordResetEmail generates a reset token and stores it.
// Returns true regardless of whether the email exists (prevents enumeration).
func (s *CustomerService) RequestPasswordResetEmail(ctx context.Context, email string) (bool, error) {
	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)

	data, err := s.customerRepo.GetByEmail(ctx, email, websiteID)
	if err != nil {
		// Don't reveal whether the email exists
		return true, nil
	}

	// Generate a random reset token
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)
	rpToken := hex.EncodeToString(tokenBytes)

	err = s.customerRepo.Update(ctx, data.EntityID, map[string]interface{}{
		"rp_token":            rpToken,
		"rp_token_created_at": time.Now().UTC().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return false, fmt.Errorf("failed to store reset token: %w", err)
	}

	log.Info().Str("email", email).Msg("password reset token generated (email sending not implemented)")
	return true, nil
}

// ResetPassword validates the reset token and updates the password.
func (s *CustomerService) ResetPassword(ctx context.Context, email, resetPasswordToken, newPassword string) (bool, error) {
	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)

	data, err := s.customerRepo.GetByEmail(ctx, email, websiteID)
	if err != nil {
		return false, custerr.ErrNoSuchEmail(email)
	}

	if data.RPToken == nil || *data.RPToken != resetPasswordToken {
		return false, custerr.ErrPasswordTokenBad
	}

	// Check token expiry (default: 2 hours)
	if data.RPTokenCreatedAt != nil {
		created, err := time.Parse("2006-01-02 15:04:05", *data.RPTokenCreatedAt)
		if err == nil && time.Since(created) > 2*time.Hour {
			return false, custerr.ErrPasswordResetExpiry
		}
	}

	if err := s.validatePassword(newPassword); err != nil {
		return false, err
	}

	hash, err := repository.HashPassword(newPassword)
	if err != nil {
		return false, err
	}

	err = s.customerRepo.Update(ctx, data.EntityID, map[string]interface{}{
		"password_hash":       hash,
		"rp_token":            nil,
		"rp_token_created_at": nil,
	})
	if err != nil {
		return false, err
	}

	s.tokenRepo.RevokeAllForCustomer(ctx, data.EntityID)
	return true, nil
}

// ConfirmEmail confirms a customer's email using a confirmation key.
func (s *CustomerService) ConfirmEmail(ctx context.Context, input model.ConfirmEmailInput) (*model.CustomerOutput, error) {
	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)

	data, err := s.customerRepo.GetByEmail(ctx, input.Email, websiteID)
	if err != nil {
		return nil, custerr.ErrNoSuchEmail(input.Email)
	}

	if data.Confirmation == nil || *data.Confirmation != input.ConfirmationKey {
		return nil, custerr.ErrConfirmationTokenInvalid
	}

	err = s.customerRepo.Update(ctx, data.EntityID, map[string]interface{}{
		"confirmation": nil,
	})
	if err != nil {
		return nil, err
	}

	updated, _ := s.customerRepo.GetByID(ctx, data.EntityID)
	return &model.CustomerOutput{Customer: s.mapCustomer(updated)}, nil
}

// ResendConfirmationEmail is a no-op (email sending not implemented).
func (s *CustomerService) ResendConfirmationEmail(ctx context.Context, email string) (bool, error) {
	log.Info().Str("email", email).Msg("resend confirmation email requested (email sending not implemented)")
	return true, nil
}

// GetCustomerGroup returns the logged-in customer's group.
func (s *CustomerService) GetCustomerGroup(ctx context.Context) (*model.CustomerGroup, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		// Return NOT LOGGED IN group
		return s.mapGroup(0)
	}
	data, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}
	return s.mapGroup(data.GroupID)
}

// GetAddressesPaginated returns paginated addresses for the authenticated customer.
func (s *CustomerService) GetAddressesPaginated(ctx context.Context, customerID int, currentPage, pageSize int) (*model.CustomerAddresses, error) {
	customer, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	allAddrs, err := s.addressRepo.GetByCustomerID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	totalCount := len(allAddrs)
	totalPages := int(math.Ceil(float64(totalCount) / float64(pageSize)))

	start := (currentPage - 1) * pageSize
	if start > totalCount {
		start = totalCount
	}
	end := start + pageSize
	if end > totalCount {
		end = totalCount
	}

	pageAddrs := allAddrs[start:end]
	items := s.mapAddresses(pageAddrs, customer.DefaultBilling, customer.DefaultShipping)

	return &model.CustomerAddresses{
		Items:      items,
		TotalCount: &totalCount,
		PageInfo: &model.SearchResultPageInfo{
			CurrentPage: &currentPage,
			PageSize:    &pageSize,
			TotalPages:  &totalPages,
		},
	}, nil
}

// GetCustomAttributes loads EAV custom attributes for a customer or address entity.
// entityType is "customer" or "customer_address". attributeCodes filters results if non-empty.
func (s *CustomerService) GetCustomAttributes(ctx context.Context, entityType string, entityID int, attributeCodes []string) ([]model.AttributeValueInterface, error) {
	values, err := s.eavRepo.GetValuesForEntity(ctx, entityType, entityID)
	if err != nil {
		log.Warn().Err(err).Str("entity_type", entityType).Int("entity_id", entityID).Msg("failed to load custom attributes")
		return []model.AttributeValueInterface{}, nil
	}
	if len(values) == 0 {
		return []model.AttributeValueInterface{}, nil
	}

	// Build filter set
	filterSet := make(map[string]bool)
	for _, code := range attributeCodes {
		filterSet[code] = true
	}

	storeID := middleware.GetStoreID(ctx)
	var result []model.AttributeValueInterface

	for _, v := range values {
		// Apply attributeCodes filter if provided
		if len(filterSet) > 0 && !filterSet[v.AttributeCode] {
			continue
		}

		// For select/multiselect, resolve option labels
		if v.FrontendInput == "select" || v.FrontendInput == "multiselect" {
			options, err := s.resolveSelectOptions(ctx, entityType, v, storeID)
			if err == nil && len(options) > 0 {
				result = append(result, &model.AttributeSelectedOptions{
					Code:            v.AttributeCode,
					SelectedOptions: options,
				})
				continue
			}
		}

		// Default: return as simple string value
		result = append(result, &model.AttributeValue{
			Code:  v.AttributeCode,
			Value: v.Value,
		})
	}

	return result, nil
}

