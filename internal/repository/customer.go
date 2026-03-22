package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// CustomerData holds the flat customer_entity row.
type CustomerData struct {
	EntityID        int
	WebsiteID       int
	Email           string
	GroupID         int
	StoreID         int
	CreatedAt       string
	UpdatedAt       string
	IsActive        int
	Prefix          *string
	Firstname       *string
	Middlename      *string
	Lastname        *string
	Suffix          *string
	Dob             *string
	PasswordHash    string
	DefaultBilling  *int
	DefaultShipping *int
	Taxvat            *string
	Confirmation      *string
	Gender            *int
	RPToken           *string
	RPTokenCreatedAt  *string
	FailuresNum       int
	LockExpires       *string
}

type CustomerRepository struct {
	db *sql.DB
}

func NewCustomerRepository(db *sql.DB) *CustomerRepository {
	return &CustomerRepository{db: db}
}

// GetByID loads a customer by entity_id.
func (r *CustomerRepository) GetByID(ctx context.Context, id int) (*CustomerData, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT entity_id, website_id, email, group_id, store_id,
		       created_at, updated_at, is_active,
		       prefix, firstname, middlename, lastname, suffix,
		       dob, COALESCE(password_hash, ''), default_billing, default_shipping,
		       taxvat, confirmation, gender, rp_token, rp_token_created_at,
		       COALESCE(failures_num, 0), lock_expires
		FROM customer_entity
		WHERE entity_id = ?`,
		id,
	)

	var c CustomerData
	err := row.Scan(
		&c.EntityID, &c.WebsiteID, &c.Email, &c.GroupID, &c.StoreID,
		&c.CreatedAt, &c.UpdatedAt, &c.IsActive,
		&c.Prefix, &c.Firstname, &c.Middlename, &c.Lastname, &c.Suffix,
		&c.Dob, &c.PasswordHash, &c.DefaultBilling, &c.DefaultShipping,
		&c.Taxvat, &c.Confirmation, &c.Gender, &c.RPToken, &c.RPTokenCreatedAt,
		&c.FailuresNum, &c.LockExpires,
	)
	if err != nil {
		return nil, fmt.Errorf("customer %d not found: %w", id, err)
	}
	return &c, nil
}

// GetByEmail loads a customer by email (and optional website_id).
func (r *CustomerRepository) GetByEmail(ctx context.Context, email string, websiteID int) (*CustomerData, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT entity_id, website_id, email, group_id, store_id,
		       created_at, updated_at, is_active,
		       prefix, firstname, middlename, lastname, suffix,
		       dob, COALESCE(password_hash, ''), default_billing, default_shipping,
		       taxvat, confirmation, gender, rp_token, rp_token_created_at,
		       COALESCE(failures_num, 0), lock_expires
		FROM customer_entity
		WHERE email = ? AND website_id = ?`,
		email, websiteID,
	)

	var c CustomerData
	err := row.Scan(
		&c.EntityID, &c.WebsiteID, &c.Email, &c.GroupID, &c.StoreID,
		&c.CreatedAt, &c.UpdatedAt, &c.IsActive,
		&c.Prefix, &c.Firstname, &c.Middlename, &c.Lastname, &c.Suffix,
		&c.Dob, &c.PasswordHash, &c.DefaultBilling, &c.DefaultShipping,
		&c.Taxvat, &c.Confirmation, &c.Gender, &c.RPToken, &c.RPTokenCreatedAt,
		&c.FailuresNum, &c.LockExpires,
	)
	if err != nil {
		return nil, fmt.Errorf("customer %s not found: %w", email, err)
	}
	return &c, nil
}

// Delete removes a customer entity.
func (r *CustomerRepository) Delete(ctx context.Context, id int) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM customer_entity WHERE entity_id = ?", id)
	if err != nil {
		return fmt.Errorf("delete customer %d failed: %w", id, err)
	}
	return nil
}

// EmailExists checks if an email is already registered for the given website.
func (r *CustomerRepository) EmailExists(ctx context.Context, email string, websiteID int) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM customer_entity WHERE email = ? AND website_id = ?",
		email, websiteID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("email check failed: %w", err)
	}
	return count > 0, nil
}

// Create inserts a new customer_entity record.
func (r *CustomerRepository) Create(ctx context.Context, c *CustomerData) (int, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO customer_entity
			(website_id, email, group_id, store_id, prefix, firstname, middlename,
			 lastname, suffix, dob, password_hash, taxvat, gender, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, NOW(), NOW())`,
		c.WebsiteID, c.Email, c.GroupID, c.StoreID,
		c.Prefix, c.Firstname, c.Middlename, c.Lastname, c.Suffix,
		c.Dob, c.PasswordHash, c.Taxvat, c.Gender,
	)
	if err != nil {
		return 0, fmt.Errorf("create customer failed: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get insert id failed: %w", err)
	}
	return int(id), nil
}

// Update modifies an existing customer_entity record. Only non-nil fields are updated.
func (r *CustomerRepository) Update(ctx context.Context, id int, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}

	sets := make([]string, 0, len(fields)+1)
	args := make([]interface{}, 0, len(fields)+1)
	for col, val := range fields {
		sets = append(sets, col+" = ?")
		args = append(args, val)
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE customer_entity SET %s WHERE entity_id = ?", strings.Join(sets, ", "))
	_, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update customer %d failed: %w", id, err)
	}
	return nil
}

// VerifyPassword checks a plaintext password against the Magento password hash.
// Magento format: hash:salt:version
//   - version "1" = SHA256(salt + password)
//   - version "2" = bcrypt
//   - version "3_32_2_67108864" = Argon2id (Magento 2.4+ default)
func VerifyPassword(passwordHash, password string) bool {
	parts := strings.Split(passwordHash, ":")
	if len(parts) < 2 {
		return false
	}

	hash := parts[0]
	salt := parts[1]
	version := "1"
	if len(parts) >= 3 {
		version = parts[2]
	}

	// Argon2id: version starts with "3_" (e.g., "3_32_2_67108864")
	if strings.HasPrefix(version, "3") {
		return verifyArgon2id(hash, salt, password, version)
	}

	switch version {
	case "0":
		// MD5 (legacy) — not supported
		return false
	case "1":
		// SHA256(salt + password)
		computed := mageSHA256Hash(salt, password)
		return computed == hash
	case "2":
		// bcrypt
		err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
		return err == nil
	default:
		return false
	}
}

// verifyArgon2id verifies a Magento Argon2id password hash.
// Version format: "3_keyLen_opsLimit_memLimit"
// Uses sodium-compatible Argon2id with parallelism=1.
func verifyArgon2id(hash, salt, password, version string) bool {
	// Parse version parameters: 3_keyLen_opsLimit_memLimit
	vparts := strings.Split(version, "_")
	keyLen := uint32(32)
	opsLimit := uint32(2)
	memLimit := uint32(65536) // 64MB in KiB

	if len(vparts) >= 2 {
		if v, err := strconv.Atoi(vparts[1]); err == nil {
			keyLen = uint32(v)
		}
	}
	if len(vparts) >= 3 {
		if v, err := strconv.Atoi(vparts[2]); err == nil {
			opsLimit = uint32(v)
		}
	}
	if len(vparts) >= 4 {
		if v, err := strconv.Atoi(vparts[3]); err == nil {
			// Magento stores memLimit in bytes; argon2.IDKey expects KiB
			memLimit = uint32(v / 1024)
		}
	}

	// sodium_crypto_pwhash requires exactly 16-byte salt (SODIUM_CRYPTO_PWHASH_SALTBYTES).
	// Magento stores a longer salt string but sodium truncates to 16 bytes.
	saltBytes := []byte(salt)
	if len(saltBytes) > 16 {
		saltBytes = saltBytes[:16]
	}

	// sodium_crypto_pwhash uses parallelism=1
	computed := argon2.IDKey([]byte(password), saltBytes, opsLimit, memLimit, 1, keyLen)
	return hex.EncodeToString(computed) == hash
}

// HashPassword creates a Magento 2.4-compatible Argon2id password hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt failed: %w", err)
	}
	saltStr := base64Encode(salt)

	// Match Magento 2.4 defaults: Argon2id, keyLen=32, ops=2, mem=64MB
	keyLen := uint32(32)
	opsLimit := uint32(2)
	memLimitKiB := uint32(65536) // 64MB
	memLimitBytes := 67108864

	// sodium_crypto_pwhash uses only the first 16 bytes of the salt
	saltForHash := []byte(saltStr)
	if len(saltForHash) > 16 {
		saltForHash = saltForHash[:16]
	}

	key := argon2.IDKey([]byte(password), saltForHash, opsLimit, memLimitKiB, 1, keyLen)
	hashHex := hex.EncodeToString(key)

	version := fmt.Sprintf("3_%d_%d_%d", keyLen, opsLimit, memLimitBytes)
	return hashHex + ":" + saltStr + ":" + version, nil
}

// mageSHA256Hash computes Magento's SHA256 hash: hex(sha256(salt + password)).
func mageSHA256Hash(salt, password string) string {
	h := sha256.New()
	h.Write([]byte(salt + password))
	return hex.EncodeToString(h.Sum(nil))
}
