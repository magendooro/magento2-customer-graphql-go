package service

import (
	"unicode"

	custerr "github.com/magendooro/magento2-customer-graphql-go/internal/errors"
)

// validatePassword checks password strength against Magento rules.
func (s *CustomerService) validatePassword(password string) error {
	minLen := s.cp.GetInt("customer/password/minimum_password_length", 0, defaultMinPasswordLen)
	requiredClasses := s.cp.GetInt("customer/password/required_character_classes_number", 0, defaultRequiredClasses)

	if len(password) < minLen {
		return custerr.ErrPasswordTooShort(minLen)
	}
	if len(password) > 256 {
		return custerr.ErrPasswordTooLong
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
		return custerr.ErrPasswordClassesShort(requiredClasses)
	}
	return nil
}
