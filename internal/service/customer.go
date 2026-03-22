package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"

	"github.com/magendooro/magento2-customer-graphql-go/graph/model"
	"github.com/magendooro/magento2-customer-graphql-go/internal/middleware"
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
	db             *sql.DB
}

func NewCustomerService(
	customerRepo *repository.CustomerRepository,
	addressRepo *repository.AddressRepository,
	tokenRepo *repository.TokenRepository,
	newsletterRepo *repository.NewsletterRepository,
	storeRepo *repository.StoreRepository,
	groupRepo *repository.GroupRepository,
	eavRepo *repository.EAVAttributeRepository,
	db *sql.DB,
) *CustomerService {
	return &CustomerService{
		customerRepo:   customerRepo,
		addressRepo:    addressRepo,
		tokenRepo:      tokenRepo,
		newsletterRepo: newsletterRepo,
		storeRepo:      storeRepo,
		groupRepo:      groupRepo,
		eavRepo:        eavRepo,
		db:             db,
	}
}

// validatePassword checks password strength against Magento rules.
func (s *CustomerService) validatePassword(password string) error {
	minLen := defaultMinPasswordLen
	requiredClasses := defaultRequiredClasses

	// Try to read from core_config_data
	s.db.QueryRow("SELECT value FROM core_config_data WHERE path = 'customer/password/minimum_password_length' AND scope = 'default'").Scan(&minLen)
	s.db.QueryRow("SELECT value FROM core_config_data WHERE path = 'customer/password/required_character_classes_number' AND scope = 'default'").Scan(&requiredClasses)

	if len(password) < minLen {
		return fmt.Errorf("the password needs at least %d characters. Create a new password and try again", minLen)
	}
	if len(password) > 256 {
		return fmt.Errorf("please enter a valid password with at most 256 characters")
	}

	// Count character classes: lowercase, uppercase, digits, special
	var hasLower, hasUpper, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	classCount := 0
	if hasLower {
		classCount++
	}
	if hasUpper {
		classCount++
	}
	if hasDigit {
		classCount++
	}
	if hasSpecial {
		classCount++
	}

	if classCount < requiredClasses {
		return fmt.Errorf("minimum of different classes of characters in password is %d. Classes of characters: Lower Case, Upper Case, Digits, Special Characters", requiredClasses)
	}
	return nil
}

// checkAccountLockout returns an error if the customer account is locked.
func (s *CustomerService) checkAccountLockout(data *repository.CustomerData) error {
	if data.LockExpires != nil && *data.LockExpires != "" {
		lockExpires, err := time.Parse("2006-01-02 15:04:05", *data.LockExpires)
		if err == nil && time.Now().UTC().Before(lockExpires) {
			return fmt.Errorf("the account sign-in was incorrect or your account is disabled temporarily. Please wait and try again later")
		}
	}
	return nil
}

// recordLoginFailure increments the failure counter and potentially locks the account.
func (s *CustomerService) recordLoginFailure(ctx context.Context, customerID int) {
	maxFailures := defaultLockoutFailures
	lockoutMinutes := defaultLockoutThreshold
	s.db.QueryRow("SELECT value FROM core_config_data WHERE path = 'customer/password/lockout_failures' AND scope = 'default'").Scan(&maxFailures)
	s.db.QueryRow("SELECT value FROM core_config_data WHERE path = 'customer/password/lockout_threshold' AND scope = 'default'").Scan(&lockoutMinutes)

	// Use raw SQL for atomic increment (map-based update doesn't support expressions)
	s.db.ExecContext(ctx,
		"UPDATE customer_entity SET failures_num = COALESCE(failures_num, 0) + 1, first_failure = COALESCE(first_failure, NOW()) WHERE entity_id = ?",
		customerID,
	)

	// Check if we should lock
	var failuresNum int
	s.db.QueryRowContext(ctx, "SELECT COALESCE(failures_num, 0) FROM customer_entity WHERE entity_id = ?", customerID).Scan(&failuresNum)
	if failuresNum >= maxFailures {
		lockExpires := time.Now().UTC().Add(time.Duration(lockoutMinutes) * time.Minute).Format("2006-01-02 15:04:05")
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"lock_expires": lockExpires})
	}
}

// resetLoginFailures clears the failure counter on successful login.
func (s *CustomerService) resetLoginFailures(ctx context.Context, customerID int) {
	s.db.ExecContext(ctx,
		"UPDATE customer_entity SET failures_num = 0, first_failure = NULL, lock_expires = NULL WHERE entity_id = ?",
		customerID,
	)
}

// GetCustomer returns the authenticated customer's data.
func (s *CustomerService) GetCustomer(ctx context.Context) (*model.Customer, error) {
	customerID := middleware.GetCustomerID(ctx)
	if customerID == 0 {
		return nil, fmt.Errorf("the current customer isn't authorized")
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
	authErr := fmt.Errorf("the account sign-in was incorrect or your account is disabled temporarily. Please wait and try again later")

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
		return nil, fmt.Errorf("the current customer isn't authorized")
	}

	err := s.tokenRepo.RevokeAllForCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("token revocation failed: %w", err)
	}

	result := true
	return &model.RevokeCustomerTokenOutput{Result: result}, nil
}

// IsEmailAvailable checks if an email can be used for registration.
func (s *CustomerService) IsEmailAvailable(ctx context.Context, email string) (*model.IsEmailAvailableOutput, error) {
	storeID := middleware.GetStoreID(ctx)
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
		return nil, fmt.Errorf("a customer with the same email address already exists in an associated website")
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
		return nil, fmt.Errorf("the current customer isn't authorized")
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
		return nil, fmt.Errorf("the current customer isn't authorized")
	}

	data, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	if !repository.VerifyPassword(data.PasswordHash, currentPassword) {
		return nil, fmt.Errorf("the password doesn't match this account. Verify the password and try again")
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
		return nil, fmt.Errorf("the current customer isn't authorized")
	}

	data, err := s.customerRepo.GetByID(ctx, customerID)
	if err != nil {
		return nil, err
	}

	if !repository.VerifyPassword(data.PasswordHash, password) {
		return nil, fmt.Errorf("the password doesn't match this account. Verify the password and try again")
	}

	// Check email uniqueness within the same website
	storeID := middleware.GetStoreID(ctx)
	websiteID, _ := s.storeRepo.GetWebsiteIDForStore(ctx, storeID)
	exists, err := s.customerRepo.EmailExists(ctx, email, websiteID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("a customer with the same email address already exists in an associated website")
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
		return nil, fmt.Errorf("the current customer isn't authorized")
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
		return nil, fmt.Errorf("the current customer isn't authorized")
	}

	// Verify ownership
	existing, err := s.addressRepo.GetByID(ctx, addressID)
	if err != nil {
		return nil, fmt.Errorf("address not found: %w", err)
	}
	if existing.ParentID != customerID {
		return nil, fmt.Errorf("address doesn't belong to this customer")
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
		return false, fmt.Errorf("the current customer isn't authorized")
	}

	existing, err := s.addressRepo.GetByID(ctx, addressID)
	if err != nil {
		return false, fmt.Errorf("address not found: %w", err)
	}
	if existing.ParentID != customerID {
		return false, fmt.Errorf("address doesn't belong to this customer")
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
		return false, fmt.Errorf("the current customer isn't authorized")
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
		return false, fmt.Errorf("no such entity with email = %s", email)
	}

	if data.RPToken == nil || *data.RPToken != resetPasswordToken {
		return false, fmt.Errorf("the password token is mismatched. Reset and try again")
	}

	// Check token expiry (default: 2 hours)
	if data.RPTokenCreatedAt != nil {
		created, err := time.Parse("2006-01-02 15:04:05", *data.RPTokenCreatedAt)
		if err == nil && time.Since(created) > 2*time.Hour {
			return false, fmt.Errorf("your password reset link has expired")
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
		return nil, fmt.Errorf("no such entity with email = %s", input.Email)
	}

	if data.Confirmation == nil || *data.Confirmation != input.ConfirmationKey {
		return nil, fmt.Errorf("the confirmation token is invalid. Verify the token and try again")
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

func (s *CustomerService) resolveSelectOptions(ctx context.Context, entityType string, v *repository.EAVAttributeValue, storeID int) ([]*model.AttributeSelectedOption, error) {
	// For select attributes, value is a single option_id
	// For multiselect, value is comma-separated option_ids
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

// ── Mapping helpers ──────────────────────────────────────────────────────────

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
