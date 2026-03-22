// Package jwt handles Magento-compatible JWT token creation and validation.
//
// Magento 2.4+ uses HS256-signed JWTs for customer authentication.
// The signing key is derived from Magento's crypt/key in env.php.
// Token format: {"kid":"1","alg":"HS256"}.{"uid":<id>,"utypid":3,"iat":<ts>,"exp":<ts>}.<sig>
package jwt

import (
	"fmt"
	"strings"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

const (
	// CustomerUserType matches Magento's UserContextInterface::USER_TYPE_CUSTOMER
	CustomerUserType = 3
	// DefaultTTLMinutes is the default token TTL matching Magento's default.
	DefaultTTLMinutes = 60
	// KeyPadLength matches Magento's str_pad to 2048 chars.
	KeyPadLength = 2048
	// KeyPadChar is the padding character Magento uses.
	KeyPadChar = '&'
	// DefaultKID is the key ID Magento uses (index "1").
	DefaultKID = "1"
)

// MagentoClaims represents the JWT claims Magento uses for customer tokens.
type MagentoClaims struct {
	UID    int `json:"uid"`
	UTypID int `json:"utypid"`
	jwtlib.RegisteredClaims
}

// Manager handles JWT token creation and validation using Magento's key format.
type Manager struct {
	signingKey []byte
	ttl        time.Duration
}

// NewManager creates a JWT manager from a Magento crypt key.
// The cryptKey should be the raw value from env.php's crypt/key.
func NewManager(cryptKey string, ttlMinutes int) *Manager {
	if ttlMinutes <= 0 {
		ttlMinutes = DefaultTTLMinutes
	}

	key := deriveSigningKey(cryptKey)

	return &Manager{
		signingKey: key,
		ttl:        time.Duration(ttlMinutes) * time.Minute,
	}
}

// Create generates a Magento-compatible JWT for a customer.
func (m *Manager) Create(customerID int) (string, error) {
	now := time.Now().UTC()
	claims := MagentoClaims{
		UID:    customerID,
		UTypID: CustomerUserType,
		RegisteredClaims: jwtlib.RegisteredClaims{
			IssuedAt:  jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(now.Add(m.ttl)),
		},
	}

	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	token.Header["kid"] = DefaultKID

	return token.SignedString(m.signingKey)
}

// Validate parses and validates a JWT, returning the customer ID.
// Returns 0 and an error if the token is invalid or expired.
func (m *Manager) Validate(tokenString string) (int, error) {
	token, err := jwtlib.ParseWithClaims(tokenString, &MagentoClaims{}, func(token *jwtlib.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.signingKey, nil
	})
	if err != nil {
		return 0, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*MagentoClaims)
	if !ok || !token.Valid {
		return 0, fmt.Errorf("invalid token claims")
	}

	if claims.UID <= 0 {
		return 0, fmt.Errorf("token missing uid claim")
	}
	if claims.UTypID != CustomerUserType {
		return 0, fmt.Errorf("token is not a customer token (utypid=%d)", claims.UTypID)
	}

	return claims.UID, nil
}

// GetIssuedAt extracts the iat claim from a token without full validation.
// Used for revocation checks.
func (m *Manager) GetIssuedAt(tokenString string) (time.Time, error) {
	token, _, err := jwtlib.NewParser().ParseUnverified(tokenString, &MagentoClaims{})
	if err != nil {
		return time.Time{}, err
	}
	claims, ok := token.Claims.(*MagentoClaims)
	if !ok || claims.IssuedAt == nil {
		return time.Time{}, fmt.Errorf("missing iat claim")
	}
	return claims.IssuedAt.Time, nil
}

// deriveSigningKey replicates Magento's key derivation from SecretBasedJwksFactory:
//  1. Take the last space-separated value from crypt/key
//  2. Pad to 2048 chars with '&' using STR_PAD_BOTH logic
func deriveSigningKey(cryptKey string) []byte {
	// Take the last space-separated key (Magento supports multiple keys)
	parts := strings.Fields(cryptKey)
	if len(parts) == 0 {
		return []byte(strings.Repeat(string(KeyPadChar), KeyPadLength))
	}
	key := parts[len(parts)-1]

	// PHP's str_pad($key, 2048, '&', STR_PAD_BOTH)
	if len(key) < KeyPadLength {
		padTotal := KeyPadLength - len(key)
		padLeft := padTotal / 2
		padRight := padTotal - padLeft
		key = strings.Repeat(string(KeyPadChar), padLeft) + key + strings.Repeat(string(KeyPadChar), padRight)
	}

	return []byte(key)
}
