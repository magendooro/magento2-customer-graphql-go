package service

import (
	"context"
	"time"

	custerr "github.com/magendooro/magento2-customer-graphql-go/internal/errors"
	"github.com/magendooro/magento2-customer-graphql-go/internal/repository"
)

// checkAccountLockout returns an error if the customer account is locked.
func (s *CustomerService) checkAccountLockout(data *repository.CustomerData) error {
	if data.LockExpires != nil && *data.LockExpires != "" {
		lockExpires, err := time.Parse("2006-01-02 15:04:05", *data.LockExpires)
		if err == nil && time.Now().UTC().Before(lockExpires) {
			return custerr.ErrAuthFailed
		}
	}
	return nil
}

// recordLoginFailure increments the failure counter and potentially locks the account.
func (s *CustomerService) recordLoginFailure(ctx context.Context, customerID int) {
	maxFailures := s.cp.GetInt("customer/password/lockout_failures", 0, defaultLockoutFailures)
	lockoutMinutes := s.cp.GetInt("customer/password/lockout_threshold", 0, defaultLockoutThreshold)

	s.customerRepo.IncrementLoginFailure(ctx, customerID)

	failuresNum := s.customerRepo.GetLoginFailures(ctx, customerID)
	if failuresNum >= maxFailures {
		lockExpires := time.Now().UTC().Add(time.Duration(lockoutMinutes) * time.Minute).Format("2006-01-02 15:04:05")
		s.customerRepo.Update(ctx, customerID, map[string]interface{}{"lock_expires": lockExpires})
	}
}

// resetLoginFailures clears the failure counter on successful login.
func (s *CustomerService) resetLoginFailures(ctx context.Context, customerID int) {
	s.customerRepo.ResetLoginFailures(ctx, customerID)
}
