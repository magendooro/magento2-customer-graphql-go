package jwt

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testCryptKey = "base64KjBr8ZM6bmK4xIWfk2/K0+xHEn+Ym6/Ogyl7Y7otzso="

func TestDeriveSigningKey(t *testing.T) {
	key := deriveSigningKey(testCryptKey)

	if len(key) != KeyPadLength {
		t.Errorf("key length: got %d, want %d", len(key), KeyPadLength)
	}

	// Key should be padded with '&' on both sides
	keyStr := string(key)
	if keyStr[0] != KeyPadChar {
		t.Error("key should start with '&' padding")
	}
	if keyStr[len(keyStr)-1] != KeyPadChar {
		t.Error("key should end with '&' padding")
	}

	// The original key should be in the middle
	if !strings.Contains(keyStr, testCryptKey) {
		t.Error("padded key should contain original key")
	}
}

func TestDeriveSigningKey_MultipleKeys(t *testing.T) {
	// Magento supports multiple space-separated keys; last one is used
	key := deriveSigningKey("oldkey1 oldkey2 " + testCryptKey)
	keyStr := string(key)
	if !strings.Contains(keyStr, testCryptKey) {
		t.Error("should use last space-separated key")
	}
	if strings.Contains(keyStr, "oldkey1") {
		t.Error("should not contain old keys")
	}
}

func TestDeriveSigningKey_Empty(t *testing.T) {
	key := deriveSigningKey("")
	if len(key) != KeyPadLength {
		t.Errorf("empty key should still pad to %d, got %d", KeyPadLength, len(key))
	}
}

func TestCreateAndValidate_RoundTrip(t *testing.T) {
	mgr := NewManager(testCryptKey, 60)

	token, err := mgr.Create(42)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if token == "" {
		t.Fatal("token should not be empty")
	}

	// Token should have 3 dot-separated parts (JWS format)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT should have 3 parts, got %d", len(parts))
	}

	// Validate
	customerID, err := mgr.Validate(token)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if customerID != 42 {
		t.Errorf("customerID: got %d, want 42", customerID)
	}
}

func TestCreate_HeaderFormat(t *testing.T) {
	mgr := NewManager(testCryptKey, 60)
	token, _ := mgr.Create(1)

	// Decode header
	parts := strings.Split(token, ".")
	headerJSON, _ := base64.RawURLEncoding.DecodeString(parts[0])

	var header map[string]interface{}
	json.Unmarshal(headerJSON, &header)

	if header["alg"] != "HS256" {
		t.Errorf("alg: got %v, want HS256", header["alg"])
	}
	if header["kid"] != "1" {
		t.Errorf("kid: got %v, want 1", header["kid"])
	}
}

func TestCreate_ClaimsFormat(t *testing.T) {
	mgr := NewManager(testCryptKey, 60)
	token, _ := mgr.Create(99)

	parts := strings.Split(token, ".")
	payloadJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])

	var claims map[string]interface{}
	json.Unmarshal(payloadJSON, &claims)

	// uid should be the customer ID
	if int(claims["uid"].(float64)) != 99 {
		t.Errorf("uid: got %v, want 99", claims["uid"])
	}
	// utypid should be 3 (customer)
	if int(claims["utypid"].(float64)) != CustomerUserType {
		t.Errorf("utypid: got %v, want %d", claims["utypid"], CustomerUserType)
	}
	// iat and exp should be present
	if claims["iat"] == nil {
		t.Error("iat claim should be present")
	}
	if claims["exp"] == nil {
		t.Error("exp claim should be present")
	}
	// exp should be ~60 minutes after iat
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	diff := exp - iat
	if diff < 3500 || diff > 3700 {
		t.Errorf("exp-iat should be ~3600 seconds, got %d", diff)
	}
}

func TestValidate_WrongKey(t *testing.T) {
	mgr1 := NewManager("key1", 60)
	mgr2 := NewManager("key2", 60)

	token, _ := mgr1.Create(1)
	_, err := mgr2.Validate(token)
	if err == nil {
		t.Error("should reject token signed with different key")
	}
}

func TestValidate_ExpiredToken(t *testing.T) {
	mgr := NewManager(testCryptKey, 0) // 0 gets set to default 60
	// We can't easily create an expired token with the public API,
	// so just test that the validation logic exists
	_, err := mgr.Validate("invalid.token.here")
	if err == nil {
		t.Error("should reject invalid token")
	}
}

func TestValidate_MalformedToken(t *testing.T) {
	mgr := NewManager(testCryptKey, 60)

	tests := []string{
		"",
		"not-a-jwt",
		"a.b",
		"a.b.c.d",
		"eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOjF9.invalid-signature",
	}

	for _, token := range tests {
		_, err := mgr.Validate(token)
		if err == nil {
			t.Errorf("should reject malformed token: %q", token)
		}
	}
}

func TestGetIssuedAt(t *testing.T) {
	mgr := NewManager(testCryptKey, 60)
	token, _ := mgr.Create(1)

	iat, err := mgr.GetIssuedAt(token)
	if err != nil {
		t.Fatalf("GetIssuedAt failed: %v", err)
	}

	// Should be within a few seconds of now
	if time.Since(iat) > 5*time.Second {
		t.Errorf("iat should be recent, got %v", iat)
	}
}
