package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/magendooro/magento2-customer-graphql-go/internal/jwt"
)

type TokenRepository struct {
	db         *sql.DB
	jwtManager *jwt.Manager
}

func NewTokenRepository(db *sql.DB, jwtManager *jwt.Manager) *TokenRepository {
	return &TokenRepository{db: db, jwtManager: jwtManager}
}

// Create generates a JWT token for a customer.
func (r *TokenRepository) Create(ctx context.Context, customerID int) (string, error) {
	if r.jwtManager == nil {
		return "", fmt.Errorf("JWT manager not configured — set MAGENTO_CRYPT_KEY")
	}
	return r.jwtManager.Create(customerID)
}

// RevokeAllForCustomer writes a revocation record to jwt_auth_revoked,
// invalidating all tokens issued before now.
func (r *TokenRepository) RevokeAllForCustomer(ctx context.Context, customerID int) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO jwt_auth_revoked (user_type_id, user_id, revoke_before)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE revoke_before = VALUES(revoke_before)`,
		jwt.CustomerUserType, customerID, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("revoke tokens for customer %d: %w", customerID, err)
	}

	// Also revoke legacy oauth_token entries for backward compatibility
	r.db.ExecContext(ctx,
		"UPDATE oauth_token SET revoked = 1 WHERE customer_id = ? AND revoked = 0",
		customerID,
	)

	return nil
}

