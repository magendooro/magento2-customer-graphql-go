package middleware

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/magendooro/magento2-customer-graphql-go/internal/jwt"
)

const CustomerIDKey contextKey = "customer_id"
const BearerTokenKey contextKey = "bearer_token"

// TokenResolver validates bearer tokens. It tries JWT first, then falls back to oauth_token.
type TokenResolver struct {
	db         *sql.DB
	jwtManager *jwt.Manager
}

func NewTokenResolver(db *sql.DB, jwtManager *jwt.Manager) *TokenResolver {
	return &TokenResolver{
		db:         db,
		jwtManager: jwtManager,
	}
}

// Resolve returns the customer_id for a given Bearer token.
func (tr *TokenResolver) Resolve(token string) (int, error) {
	// Try JWT validation first (Magento 2.4+ default)
	if tr.jwtManager != nil {
		customerID, err := tr.jwtManager.Validate(token)
		if err == nil {
			// Check revocation in jwt_auth_revoked
			revoked, err := tr.isJWTRevoked(customerID, token)
			if err != nil {
				log.Debug().Err(err).Msg("jwt revocation check failed")
			}
			if !revoked {
				return customerID, nil
			}
			log.Debug().Int("customer_id", customerID).Msg("jwt token revoked")
			return 0, fmt.Errorf("token has been revoked")
		}
		log.Debug().Err(err).Msg("jwt validation failed, trying oauth_token")
	}

	// Fallback: oauth_token table lookup (legacy opaque tokens)
	var customerID int
	err := tr.db.QueryRow(
		`SELECT customer_id FROM oauth_token
		 WHERE token = ? AND revoked = 0 AND customer_id IS NOT NULL`,
		token,
	).Scan(&customerID)
	if err != nil {
		return 0, err
	}
	return customerID, nil
}

// isJWTRevoked checks the jwt_auth_revoked table.
func (tr *TokenResolver) isJWTRevoked(customerID int, tokenString string) (bool, error) {
	var revokeBefore int64
	err := tr.db.QueryRow(
		"SELECT revoke_before FROM jwt_auth_revoked WHERE user_type_id = ? AND user_id = ?",
		jwt.CustomerUserType, customerID,
	).Scan(&revokeBefore)
	if err == sql.ErrNoRows {
		return false, nil // Not revoked
	}
	if err != nil {
		return false, err
	}

	// Get token's issued-at time
	iat, err := tr.jwtManager.GetIssuedAt(tokenString)
	if err != nil {
		return true, nil // Can't parse iat — treat as revoked
	}

	return iat.Unix() <= revokeBefore, nil
}

// AuthMiddleware extracts the Bearer token from the Authorization header
// and resolves it to a customer_id.
func AuthMiddleware(resolver *TokenResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var customerID int

			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token := strings.TrimPrefix(authHeader, "Bearer ")
				id, err := resolver.Resolve(token)
				if err != nil {
					log.Debug().Err(err).Msg("token resolution failed")
				} else {
					customerID = id
				}
			}

			ctx := context.WithValue(r.Context(), CustomerIDKey, customerID)
			if strings.HasPrefix(authHeader, "Bearer ") {
				ctx = context.WithValue(ctx, BearerTokenKey, strings.TrimPrefix(authHeader, "Bearer "))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetCustomerID returns the authenticated customer ID from context, or 0.
func GetCustomerID(ctx context.Context) int {
	if id, ok := ctx.Value(CustomerIDKey).(int); ok {
		return id
	}
	return 0
}

// GetBearerToken returns the raw Bearer token from context, or empty string.
func GetBearerToken(ctx context.Context) string {
	if t, ok := ctx.Value(BearerTokenKey).(string); ok {
		return t
	}
	return ""
}

// RevokeJWT writes a revocation record to jwt_auth_revoked.
func (tr *TokenResolver) RevokeJWT(customerID int) error {
	_, err := tr.db.Exec(
		`INSERT INTO jwt_auth_revoked (user_type_id, user_id, revoke_before)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE revoke_before = VALUES(revoke_before)`,
		jwt.CustomerUserType, customerID, time.Now().Unix(),
	)
	return err
}
