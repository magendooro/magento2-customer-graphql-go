// Package errors provides centralized, Magento-compatible error messages.
// All user-facing error strings must match Magento PHP exactly.
package errors

import "fmt"

// Auth errors
var (
	ErrUnauthorized = fmt.Errorf("The current customer isn't authorized.")
	ErrAuthFailed   = fmt.Errorf("The account sign-in was incorrect or your account is disabled temporarily. Please wait and try again later.")
)

// Password errors
var (
	ErrPasswordTooShort = func(minLen int) error {
		return fmt.Errorf("the password needs at least %d characters. Create a new password and try again", minLen)
	}
	ErrPasswordTooLong      = fmt.Errorf("please enter a valid password with at most 256 characters")
	ErrPasswordClassesShort = func(required int) error {
		return fmt.Errorf("minimum of different classes of characters in password is %d. Classes of characters: Lower Case, Upper Case, Digits, Special Characters", required)
	}
	ErrPasswordMismatch    = fmt.Errorf("The password doesn't match this account. Verify the password and try again.")
	ErrPasswordTokenBad    = fmt.Errorf("the password token is mismatched. Reset and try again")
	ErrPasswordResetExpiry = fmt.Errorf("your password reset link has expired")
)

// Customer errors
var (
	ErrEmailAlreadyExists = fmt.Errorf("a customer with the same email address already exists in an associated website")
	ErrNoSuchEmail        = func(email string) error {
		return fmt.Errorf("no such entity with email = %s", email)
	}
	ErrConfirmationTokenInvalid = fmt.Errorf("the confirmation token is invalid. Verify the token and try again")
)

// Address errors
var ErrAddressNotOwned = fmt.Errorf("address doesn't belong to this customer")
