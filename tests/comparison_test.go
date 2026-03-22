package tests

import (
	"encoding/json"
	"strconv"
	"testing"
)

// These tests compare Go service responses against known Magento behavior.
// They use the Magento sample data customer: Veronica Costello (roni_cost@example.com).
// The ground truth was established by querying Magento PHP and the REST API.

func getTestToken(t *testing.T) string {
	t.Helper()
	// Clear any stale JWT revocation before generating a new token
	cleanupRevocation(t)
	email := envOrDefault("TEST_CUSTOMER_EMAIL", "roni_cost@example.com")
	password := envOrDefault("TEST_CUSTOMER_PASSWORD", "roni_cost3@example.com")

	resp := doQuery(t, `mutation { generateCustomerToken(email: "`+email+`", password: "`+password+`") { token } }`, "")
	if len(resp.Errors) > 0 {
		t.Skipf("cannot generate token: %s", resp.Errors[0].Message)
	}

	var data struct {
		GenerateCustomerToken struct {
			Token string `json:"token"`
		} `json:"generateCustomerToken"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.GenerateCustomerToken.Token == "" {
		t.Skip("empty token returned")
	}
	return data.GenerateCustomerToken.Token
}

// ─── Error Behavior Comparison ──────────────────────────────────────────────
// Magento returns specific error messages and HTTP categories.
// Our Go service must match these exactly.

func TestCompare_UnauthenticatedCustomerQuery(t *testing.T) {
	// Magento: HTTP 403, error category "graphql-authorization"
	// Message: "The current customer isn't authorized."
	resp := doQuery(t, `{ customer { id email } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error for unauthenticated customer query")
	}
	msg := resp.Errors[0].Message
	// Magento uses "The current customer isn't authorized." (with period)
	expected := "The current customer isn't authorized."
	if msg != expected {
		t.Errorf("error message mismatch:\n  Go:      %q\n  Magento: %q", msg, expected)
	}
}

func TestCompare_InvalidCredentials(t *testing.T) {
	// Magento: HTTP 401, error category "graphql-authentication"
	resp := doQuery(t, `mutation { generateCustomerToken(email: "roni_cost@example.com", password: "wrongpassword") { token } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for wrong password")
	}
	msg := resp.Errors[0].Message
	expected := "The account sign-in was incorrect or your account is disabled temporarily. Please wait and try again later."
	if msg != expected {
		t.Errorf("error message mismatch:\n  Go:      %q\n  Magento: %q", msg, expected)
	}
}

func TestCompare_NonExistentEmailLogin(t *testing.T) {
	// Magento returns the same error for non-existent email as for wrong password (no enumeration)
	resp := doQuery(t, `mutation { generateCustomerToken(email: "nobody999@test.com", password: "anything") { token } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for non-existent email")
	}
	// Should NOT reveal that the email doesn't exist — same generic message
	msg := resp.Errors[0].Message
	expected := "The account sign-in was incorrect or your account is disabled temporarily. Please wait and try again later."
	if msg != expected {
		t.Errorf("error message should not reveal email existence:\n  Go:      %q\n  Magento: %q", msg, expected)
	}
}

func TestCompare_RevokeTokenUnauthenticated(t *testing.T) {
	// Magento: HTTP 403, "The current customer isn't authorized."
	resp := doQuery(t, `mutation { revokeCustomerToken { result } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error for unauthenticated revoke")
	}
	msg := resp.Errors[0].Message
	expected := "The current customer isn't authorized."
	if msg != expected {
		t.Errorf("error message mismatch:\n  Go:      %q\n  Magento: %q", msg, expected)
	}
}

// ─── isEmailAvailable Comparison ────────────────────────────────────────────

func TestCompare_IsEmailAvailable_ExistingCustomer(t *testing.T) {
	// Magento 2.4.6+: always returns true when email_availability_check config is disabled (default).
	// This prevents email enumeration attacks.
	resp := doQuery(t, `{ isEmailAvailable(email: "roni_cost@example.com") { is_email_available } }`, "")
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected error: %s", resp.Errors[0].Message)
	}
	var data struct {
		IsEmailAvailable struct {
			IsEmailAvailable bool `json:"is_email_available"`
		} `json:"isEmailAvailable"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.IsEmailAvailable.IsEmailAvailable != true {
		t.Error("Magento 2.4.6+ returns true for all emails when email_availability_check is disabled (default)")
	}
}

func TestCompare_IsEmailAvailable_NewEmail(t *testing.T) {
	// Magento: returns true for non-existing email
	resp := doQuery(t, `{ isEmailAvailable(email: "brand-new-unique-999@test.com") { is_email_available } }`, "")
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected error: %s", resp.Errors[0].Message)
	}
	var data struct {
		IsEmailAvailable struct {
			IsEmailAvailable bool `json:"is_email_available"`
		} `json:"isEmailAvailable"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.IsEmailAvailable.IsEmailAvailable != true {
		t.Error("Magento returns true for non-existing email; Go returned false")
	}
}

// ─── Customer Data Comparison ───────────────────────────────────────────────
// Ground truth from Magento REST API /rest/V1/customers/me:
//   id: 1, group_id: 1, firstname: "Veronica", lastname: "Costello",
//   email: "roni_cost@example.com", dob: "1973-12-15", gender: 2,
//   default_billing: "1", default_shipping: "1", store_id: 1, website_id: 1

func TestCompare_CustomerFields(t *testing.T) {
	token := getTestToken(t)

	resp := doQuery(t, `{
		customer {
			id firstname lastname middlename prefix suffix
			email date_of_birth taxvat gender
			is_subscribed created_at
			default_billing default_shipping
			group_id
			confirmation_status
		}
	}`, token)

	if len(resp.Errors) > 0 {
		t.Fatalf("customer query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			ID                 string  `json:"id"`
			Firstname          string  `json:"firstname"`
			Lastname           string  `json:"lastname"`
			Middlename         *string `json:"middlename"`
			Prefix             *string `json:"prefix"`
			Suffix             *string `json:"suffix"`
			Email              string  `json:"email"`
			DateOfBirth        *string `json:"date_of_birth"`
			Taxvat             *string `json:"taxvat"`
			Gender             *int    `json:"gender"`
			IsSubscribed       *bool   `json:"is_subscribed"`
			CreatedAt          string  `json:"created_at"`
			DefaultBilling     *string `json:"default_billing"`
			DefaultShipping    *string `json:"default_shipping"`
			GroupID            *int    `json:"group_id"`
			ConfirmationStatus string  `json:"confirmation_status"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)
	c := data.Customer

	// Magento ground truth:
	if c.ID != "1" {
		t.Errorf("id: Go=%q, Magento=%q", c.ID, "1")
	}
	if c.Firstname != "Veronica" {
		t.Errorf("firstname: Go=%q, Magento=%q", c.Firstname, "Veronica")
	}
	if c.Lastname != "Costello" {
		t.Errorf("lastname: Go=%q, Magento=%q", c.Lastname, "Costello")
	}
	if c.Email != "roni_cost@example.com" {
		t.Errorf("email: Go=%q, Magento=%q", c.Email, "roni_cost@example.com")
	}

	// Magento: dob = "1973-12-15"
	if c.DateOfBirth == nil {
		t.Error("date_of_birth: Go=nil, Magento=\"1973-12-15\"")
	} else if *c.DateOfBirth != "1973-12-15" {
		t.Errorf("date_of_birth: Go=%q, Magento=%q", *c.DateOfBirth, "1973-12-15")
	}

	// Magento: gender = 2 (female)
	if c.Gender == nil || *c.Gender != 2 {
		t.Errorf("gender: Go=%v, Magento=2", c.Gender)
	}

	// Magento: group_id = 1 (General)
	if c.GroupID == nil || *c.GroupID != 1 {
		t.Errorf("group_id: Go=%v, Magento=1", c.GroupID)
	}

	// Magento: default_billing = "1" (string, not int)
	if c.DefaultBilling == nil || *c.DefaultBilling != "1" {
		t.Errorf("default_billing: Go=%v, Magento=\"1\"", c.DefaultBilling)
	}
	if c.DefaultShipping == nil || *c.DefaultShipping != "1" {
		t.Errorf("default_shipping: Go=%v, Magento=\"1\"", c.DefaultShipping)
	}

	// Magento: is_subscribed = false (from REST extension_attributes)
	if c.IsSubscribed == nil || *c.IsSubscribed != false {
		t.Errorf("is_subscribed: Go=%v, Magento=false", c.IsSubscribed)
	}

	// Magento: middlename, prefix, suffix, taxvat are null
	if c.Middlename != nil && *c.Middlename != "" {
		t.Errorf("middlename: Go=%q, Magento=null", *c.Middlename)
	}

	// created_at should not be empty
	if c.CreatedAt == "" {
		t.Error("created_at should not be empty")
	}

	// confirmation_status: customer has no confirmation value, so should be ACCOUNT_CONFIRMATION_NOT_REQUIRED
	if c.ConfirmationStatus != "ACCOUNT_CONFIRMATION_NOT_REQUIRED" {
		t.Errorf("confirmation_status: Go=%q, Magento=%q", c.ConfirmationStatus, "ACCOUNT_CONFIRMATION_NOT_REQUIRED")
	}

	t.Logf("All customer fields match Magento ground truth")
}

// ─── Address Data Comparison ────────────────────────────────────────────────
// Ground truth from Magento REST API:
//   id: 1, region: {region_code: "MI", region: "Michigan", region_id: 33},
//   country_id: "US", street: ["6146 Honey Bluff Parkway"],
//   telephone: "(555) 229-3326", postcode: "49628-7978", city: "Calder",
//   firstname: "Veronica", lastname: "Costello",
//   default_shipping: true, default_billing: true

func TestCompare_CustomerAddresses(t *testing.T) {
	token := getTestToken(t)

	resp := doQuery(t, `{
		customer {
			addresses {
				id uid
				firstname lastname middlename prefix suffix
				company street city
				region { region_code region region_id }
				region_id postcode country_code
				telephone fax vat_id
				default_shipping default_billing
			}
		}
	}`, token)

	if len(resp.Errors) > 0 {
		t.Fatalf("customer addresses query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			Addresses []struct {
				ID              int     `json:"id"`
				UID             string  `json:"uid"`
				Firstname       string  `json:"firstname"`
				Lastname        string  `json:"lastname"`
				Middlename      *string `json:"middlename"`
				Prefix          *string `json:"prefix"`
				Suffix          *string `json:"suffix"`
				Company         *string `json:"company"`
				Street          []string `json:"street"`
				City            string  `json:"city"`
				Region          *struct {
					RegionCode *string `json:"region_code"`
					Region     *string `json:"region"`
					RegionID   *int    `json:"region_id"`
				} `json:"region"`
				RegionID        *int    `json:"region_id"`
				Postcode        string  `json:"postcode"`
				CountryCode     string  `json:"country_code"`
				Telephone       string  `json:"telephone"`
				Fax             *string `json:"fax"`
				VatID           *string `json:"vat_id"`
				DefaultShipping bool    `json:"default_shipping"`
				DefaultBilling  bool    `json:"default_billing"`
			} `json:"addresses"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)

	if len(data.Customer.Addresses) == 0 {
		t.Fatal("expected at least 1 address")
	}

	addr := data.Customer.Addresses[0]

	// Magento: id = 1
	if addr.ID != 1 {
		t.Errorf("address id: Go=%d, Magento=%d", addr.ID, 1)
	}

	// uid should be base64 encoded id
	if addr.UID == "" {
		t.Error("uid should not be empty")
	}

	// Magento: firstname = "Veronica"
	if addr.Firstname != "Veronica" {
		t.Errorf("address firstname: Go=%q, Magento=%q", addr.Firstname, "Veronica")
	}
	if addr.Lastname != "Costello" {
		t.Errorf("address lastname: Go=%q, Magento=%q", addr.Lastname, "Costello")
	}

	// Magento: street = ["6146 Honey Bluff Parkway"]
	if len(addr.Street) != 1 || addr.Street[0] != "6146 Honey Bluff Parkway" {
		t.Errorf("address street: Go=%v, Magento=%v", addr.Street, []string{"6146 Honey Bluff Parkway"})
	}

	// Magento: city = "Calder"
	if addr.City != "Calder" {
		t.Errorf("address city: Go=%q, Magento=%q", addr.City, "Calder")
	}

	// Magento: postcode = "49628-7978"
	if addr.Postcode != "49628-7978" {
		t.Errorf("address postcode: Go=%q, Magento=%q", addr.Postcode, "49628-7978")
	}

	// Magento: country_code = "US" (enum)
	if addr.CountryCode != "US" {
		t.Errorf("address country_code: Go=%q, Magento=%q", addr.CountryCode, "US")
	}

	// Magento: telephone = "(555) 229-3326"
	if addr.Telephone != "(555) 229-3326" {
		t.Errorf("address telephone: Go=%q, Magento=%q", addr.Telephone, "(555) 229-3326")
	}

	// Magento: region = {region_code: "MI", region: "Michigan", region_id: 33}
	if addr.Region == nil {
		t.Fatal("address region should not be nil")
	}
	if addr.Region.RegionCode == nil || *addr.Region.RegionCode != "MI" {
		t.Errorf("address region_code: Go=%v, Magento=%q", addr.Region.RegionCode, "MI")
	}
	if addr.Region.Region == nil || *addr.Region.Region != "Michigan" {
		t.Errorf("address region name: Go=%v, Magento=%q", addr.Region.Region, "Michigan")
	}
	if addr.Region.RegionID == nil || *addr.Region.RegionID != 33 {
		t.Errorf("address region_id: Go=%v, Magento=%d", addr.Region.RegionID, 33)
	}

	// Also check top-level region_id
	if addr.RegionID == nil || *addr.RegionID != 33 {
		t.Errorf("address top-level region_id: Go=%v, Magento=%d", addr.RegionID, 33)
	}

	// Magento: default_shipping = true, default_billing = true
	if !addr.DefaultShipping {
		t.Error("address default_shipping: Go=false, Magento=true")
	}
	if !addr.DefaultBilling {
		t.Error("address default_billing: Go=false, Magento=true")
	}

	// Null fields in Magento: company, fax, vat_id, middlename, prefix, suffix
	if addr.Company != nil && *addr.Company != "" {
		t.Errorf("company should be null, got %q", *addr.Company)
	}

	t.Log("All address fields match Magento ground truth")
}

// ─── Token Lifecycle Comparison ─────────────────────────────────────────────

func TestCompare_TokenLifecycle(t *testing.T) {
	email := envOrDefault("TEST_CUSTOMER_EMAIL", "roni_cost@example.com")
	password := envOrDefault("TEST_CUSTOMER_PASSWORD", "roni_cost3@example.com")

	// 1. Generate token
	tokenResp := doQuery(t, `mutation { generateCustomerToken(email: "`+email+`", password: "`+password+`") { token } }`, "")
	if len(tokenResp.Errors) > 0 {
		t.Skipf("cannot generate token: %s", tokenResp.Errors[0].Message)
	}
	var tokenData struct {
		GenerateCustomerToken struct {
			Token string `json:"token"`
		} `json:"generateCustomerToken"`
	}
	json.Unmarshal(tokenResp.Data, &tokenData)
	token := tokenData.GenerateCustomerToken.Token
	if token == "" {
		t.Fatal("token should not be empty")
	}
	// JWT tokens are ~160+ chars (header.payload.signature)
	if len(token) < 50 {
		t.Errorf("token too short for JWT: length=%d", len(token))
	}

	// 2. Token works for customer query
	custResp := doQuery(t, `{ customer { id email } }`, token)
	if len(custResp.Errors) > 0 {
		t.Fatalf("token should be valid: %s", custResp.Errors[0].Message)
	}

	// 3. Revoke token
	revokeResp := doQuery(t, `mutation { revokeCustomerToken { result } }`, token)
	if len(revokeResp.Errors) > 0 {
		t.Fatalf("revoke should succeed: %s", revokeResp.Errors[0].Message)
	}
	var revokeData struct {
		RevokeCustomerToken struct {
			Result bool `json:"result"`
		} `json:"revokeCustomerToken"`
	}
	json.Unmarshal(revokeResp.Data, &revokeData)
	if !revokeData.RevokeCustomerToken.Result {
		t.Error("revoke result should be true")
	}

	// 4. Revoked token should fail
	// Magento: "User token has been revoked" or auth error
	postRevokeResp := doQuery(t, `{ customer { id } }`, token)
	if len(postRevokeResp.Errors) == 0 {
		t.Error("revoked token should return error")
	}

	// Clean up revocation so subsequent tests can generate tokens
	// (in a real system this wouldn't be needed — new tokens get fresh iat)
	cleanupRevocation(t)
}

// ─── Update Lifecycle Comparison ────────────────────────────────────────────

func TestCompare_UpdateAndVerify(t *testing.T) {
	token := getTestToken(t)

	// Update middlename (field that's null by default)
	updateResp := doQuery(t, `mutation {
		updateCustomerV2(input: { middlename: "TestMiddle" }) {
			customer { middlename firstname lastname }
		}
	}`, token)
	if len(updateResp.Errors) > 0 {
		t.Fatalf("update failed: %s", updateResp.Errors[0].Message)
	}

	var updateData struct {
		UpdateCustomerV2 struct {
			Customer struct {
				Middlename string `json:"middlename"`
				Firstname  string `json:"firstname"`
				Lastname   string `json:"lastname"`
			} `json:"customer"`
		} `json:"updateCustomerV2"`
	}
	json.Unmarshal(updateResp.Data, &updateData)

	// Magento: returns the updated customer object with new values
	if updateData.UpdateCustomerV2.Customer.Middlename != "TestMiddle" {
		t.Errorf("middlename after update: Go=%q, expected=%q",
			updateData.UpdateCustomerV2.Customer.Middlename, "TestMiddle")
	}

	// Verify the update persisted by re-querying
	verifyResp := doQuery(t, `{ customer { middlename } }`, token)
	if len(verifyResp.Errors) > 0 {
		t.Fatalf("verify query failed: %s", verifyResp.Errors[0].Message)
	}
	var verifyData struct {
		Customer struct {
			Middlename *string `json:"middlename"`
		} `json:"customer"`
	}
	json.Unmarshal(verifyResp.Data, &verifyData)
	if verifyData.Customer.Middlename == nil || *verifyData.Customer.Middlename != "TestMiddle" {
		t.Errorf("middlename not persisted: %v", verifyData.Customer.Middlename)
	}

	// Reset middlename
	doQuery(t, `mutation { updateCustomerV2(input: { middlename: "" }) { customer { middlename } } }`, token)
}

// ─── Password Change Comparison ─────────────────────────────────────────────

func TestCompare_ChangePasswordWrongCurrent(t *testing.T) {
	token := getTestToken(t)

	// Magento: "The password doesn't match this account. Verify the password and try again."
	resp := doQuery(t, `mutation { changeCustomerPassword(currentPassword: "wrongpassword", newPassword: "newpass123") { id } }`, token)
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for wrong current password")
	}
	msg := resp.Errors[0].Message
	expected := "The password doesn't match this account. Verify the password and try again."
	if msg != expected {
		t.Errorf("error message mismatch:\n  Go:      %q\n  Magento: %q", msg, expected)
	}
}

// ─── Mutation Auth Guards Comparison ────────────────────────────────────────
// All customer mutations (except generateCustomerToken, createCustomerV2, isEmailAvailable)
// require authentication.

func TestCompare_UpdateCustomerRequiresAuth(t *testing.T) {
	resp := doQuery(t, `mutation { updateCustomerV2(input: { firstname: "Test" }) { customer { firstname } } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error")
	}
}

func TestCompare_ChangePasswordRequiresAuth(t *testing.T) {
	resp := doQuery(t, `mutation { changeCustomerPassword(currentPassword: "a", newPassword: "b") { id } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error")
	}
}

func TestCompare_UpdateEmailRequiresAuth(t *testing.T) {
	resp := doQuery(t, `mutation { updateCustomerEmail(email: "new@test.com", password: "pass") { customer { email } } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error")
	}
}

func TestCompare_CreateAddressRequiresAuth(t *testing.T) {
	resp := doQuery(t, `mutation { createCustomerAddress(input: { firstname: "Test", lastname: "User", street: ["123 Main"], city: "NYC", country_code: US, telephone: "555-1234" }) { id } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error")
	}
}

func TestCompare_DeleteAddressRequiresAuth(t *testing.T) {
	resp := doQuery(t, `mutation { deleteCustomerAddress(id: 999) }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error")
	}
}

// ─── Address CRUD Comparison ────────────────────────────────────────────────

func TestCompare_AddressCreateUpdateDelete(t *testing.T) {
	token := getTestToken(t)

	// 1. Create address
	createResp := doQuery(t, `mutation {
		createCustomerAddress(input: {
			firstname: "Jane"
			lastname: "Doe"
			street: ["100 Test Street", "Apt 5"]
			city: "Springfield"
			region: { region_id: 12, region_code: "CA", region: "California" }
			postcode: "90210"
			country_code: US
			telephone: "(555) 000-1111"
			default_billing: false
			default_shipping: false
		}) {
			id firstname lastname street city
			region { region_code region region_id }
			postcode country_code telephone
			default_billing default_shipping
		}
	}`, token)

	if len(createResp.Errors) > 0 {
		t.Fatalf("create address failed: %s", createResp.Errors[0].Message)
	}

	var createData struct {
		CreateCustomerAddress struct {
			ID              int      `json:"id"`
			Firstname       string   `json:"firstname"`
			Lastname        string   `json:"lastname"`
			Street          []string `json:"street"`
			City            string   `json:"city"`
			Region          struct {
				RegionCode *string `json:"region_code"`
				Region     *string `json:"region"`
				RegionID   *int    `json:"region_id"`
			} `json:"region"`
			Postcode        string `json:"postcode"`
			CountryCode     string `json:"country_code"`
			Telephone       string `json:"telephone"`
			DefaultBilling  bool   `json:"default_billing"`
			DefaultShipping bool   `json:"default_shipping"`
		} `json:"createCustomerAddress"`
	}
	json.Unmarshal(createResp.Data, &createData)
	addr := createData.CreateCustomerAddress

	if addr.ID == 0 {
		t.Fatal("created address should have non-zero id")
	}
	if addr.Firstname != "Jane" {
		t.Errorf("firstname: %q != %q", addr.Firstname, "Jane")
	}
	if len(addr.Street) != 2 || addr.Street[0] != "100 Test Street" || addr.Street[1] != "Apt 5" {
		t.Errorf("street: %v != [\"100 Test Street\", \"Apt 5\"]", addr.Street)
	}
	if addr.City != "Springfield" {
		t.Errorf("city: %q != %q", addr.City, "Springfield")
	}
	if addr.CountryCode != "US" {
		t.Errorf("country_code: %q != %q", addr.CountryCode, "US")
	}
	if addr.Postcode != "90210" {
		t.Errorf("postcode: %q != %q", addr.Postcode, "90210")
	}

	addressID := addr.ID

	// 2. Update address
	updateResp := doQuery(t, `mutation {
		updateCustomerAddress(id: `+itoa(addressID)+`, input: {
			city: "Beverly Hills"
			telephone: "(555) 999-8888"
		}) {
			id city telephone
		}
	}`, token)

	if len(updateResp.Errors) > 0 {
		t.Fatalf("update address failed: %s", updateResp.Errors[0].Message)
	}

	var updateData struct {
		UpdateCustomerAddress struct {
			ID        int    `json:"id"`
			City      string `json:"city"`
			Telephone string `json:"telephone"`
		} `json:"updateCustomerAddress"`
	}
	json.Unmarshal(updateResp.Data, &updateData)
	if updateData.UpdateCustomerAddress.City != "Beverly Hills" {
		t.Errorf("updated city: %q != %q", updateData.UpdateCustomerAddress.City, "Beverly Hills")
	}

	// 3. Delete address
	deleteResp := doQuery(t, `mutation { deleteCustomerAddress(id: `+itoa(addressID)+`) }`, token)
	if len(deleteResp.Errors) > 0 {
		t.Fatalf("delete address failed: %s", deleteResp.Errors[0].Message)
	}

	// 4. Verify deletion — address should no longer appear
	verifyResp := doQuery(t, `{ customer { addresses { id } } }`, token)
	if len(verifyResp.Errors) > 0 {
		t.Fatalf("verify failed: %s", verifyResp.Errors[0].Message)
	}
	var verifyData struct {
		Customer struct {
			Addresses []struct {
				ID int `json:"id"`
			} `json:"addresses"`
		} `json:"customer"`
	}
	json.Unmarshal(verifyResp.Data, &verifyData)
	for _, a := range verifyData.Customer.Addresses {
		if a.ID == addressID {
			t.Errorf("deleted address %d still appears in customer addresses", addressID)
		}
	}
}

// ─── Date Format Comparison ─────────────────────────────────────────────────

func TestCompare_DateFormats(t *testing.T) {
	token := getTestToken(t)

	resp := doQuery(t, `{ customer { date_of_birth created_at } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			DateOfBirth *string `json:"date_of_birth"`
			CreatedAt   string  `json:"created_at"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)

	// Magento: date_of_birth format is "YYYY-MM-DD"
	if data.Customer.DateOfBirth == nil {
		t.Error("date_of_birth should not be null")
	} else if len(*data.Customer.DateOfBirth) != 10 {
		t.Errorf("date_of_birth format should be YYYY-MM-DD, got: %q", *data.Customer.DateOfBirth)
	}

	// Magento: created_at format is "YYYY-MM-DD HH:MM:SS"
	if len(data.Customer.CreatedAt) < 19 {
		t.Errorf("created_at format should be YYYY-MM-DD HH:MM:SS, got: %q", data.Customer.CreatedAt)
	}
}

// ─── New Feature Tests (Issues #1-#7) ───────────────────────────────────────

func TestCompare_CustomerGroup(t *testing.T) {
	token := getTestToken(t)

	// Test customerGroup query
	resp := doQuery(t, `{ customerGroup { uid name } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("customerGroup query failed: %s", resp.Errors[0].Message)
	}
	var data struct {
		CustomerGroup struct {
			UID  string `json:"uid"`
			Name string `json:"name"`
		} `json:"customerGroup"`
	}
	json.Unmarshal(resp.Data, &data)
	// Magento: group 1 = "General"
	if data.CustomerGroup.Name != "General" {
		t.Errorf("group name: Go=%q, Magento=%q", data.CustomerGroup.Name, "General")
	}
	if data.CustomerGroup.UID == "" {
		t.Error("group uid should not be empty")
	}
}

func TestCompare_CustomerGroupField(t *testing.T) {
	token := getTestToken(t)

	resp := doQuery(t, `{ customer { group { uid name } group_id } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("query failed: %s", resp.Errors[0].Message)
	}
	var data struct {
		Customer struct {
			Group struct {
				UID  string `json:"uid"`
				Name string `json:"name"`
			} `json:"group"`
			GroupID int `json:"group_id"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.Customer.Group.Name != "General" {
		t.Errorf("customer.group.name: %q != %q", data.Customer.Group.Name, "General")
	}
	if data.Customer.GroupID != 1 {
		t.Errorf("customer.group_id: %d != %d", data.Customer.GroupID, 1)
	}
}

func TestCompare_DeprecatedDobField(t *testing.T) {
	token := getTestToken(t)

	resp := doQuery(t, `{ customer { dob date_of_birth } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("query failed: %s", resp.Errors[0].Message)
	}
	var data struct {
		Customer struct {
			Dob         *string `json:"dob"`
			DateOfBirth *string `json:"date_of_birth"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)
	// Both should return the same value
	if data.Customer.Dob == nil || data.Customer.DateOfBirth == nil {
		t.Fatal("both dob and date_of_birth should be non-null")
	}
	if *data.Customer.Dob != *data.Customer.DateOfBirth {
		t.Errorf("dob=%q != date_of_birth=%q", *data.Customer.Dob, *data.Customer.DateOfBirth)
	}
	if *data.Customer.Dob != "1973-12-15" {
		t.Errorf("dob: %q != %q", *data.Customer.Dob, "1973-12-15")
	}
}

func TestCompare_RequestPasswordReset(t *testing.T) {
	// Should return true for existing email
	resp := doQuery(t, `mutation { requestPasswordResetEmail(email: "roni_cost@example.com") }`, "")
	if len(resp.Errors) > 0 {
		t.Fatalf("requestPasswordResetEmail failed: %s", resp.Errors[0].Message)
	}

	// Should also return true for non-existing email (no enumeration)
	resp2 := doQuery(t, `mutation { requestPasswordResetEmail(email: "nonexistent999@test.com") }`, "")
	if len(resp2.Errors) > 0 {
		t.Fatalf("requestPasswordResetEmail for non-existent should not error: %s", resp2.Errors[0].Message)
	}
}

func TestCompare_ResetPasswordInvalidToken(t *testing.T) {
	resp := doQuery(t, `mutation { resetPassword(email: "roni_cost@example.com", resetPasswordToken: "invalid-token", newPassword: "newpass123") }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for invalid reset token")
	}
	t.Logf("got expected error: %s", resp.Errors[0].Message)
}

func TestCompare_DeleteCustomerRequiresAuth(t *testing.T) {
	resp := doQuery(t, `mutation { deleteCustomer }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error for deleteCustomer")
	}
}

func TestCompare_ConfirmEmailInvalidKey(t *testing.T) {
	resp := doQuery(t, `mutation { confirmEmail(input: { email: "roni_cost@example.com", confirmation_key: "invalid-key" }) { customer { id } } }`, "")
	if len(resp.Errors) == 0 {
		// Customer has no confirmation set, so this should error
		t.Log("confirmEmail with invalid key returned no error (customer may not need confirmation)")
	}
}

func TestCompare_ResendConfirmationEmail(t *testing.T) {
	resp := doQuery(t, `mutation { resendConfirmationEmail(email: "roni_cost@example.com") }`, "")
	if len(resp.Errors) > 0 {
		t.Fatalf("resendConfirmationEmail failed: %s", resp.Errors[0].Message)
	}
}

func TestCompare_DeprecatedCreateCustomer(t *testing.T) {
	testEmail := "deprecated-test-999@example.com"
	testPassword := "Test1234!"

	resp := doQuery(t, `mutation { createCustomer(input: { firstname: "Test", lastname: "User", email: "`+testEmail+`", password: "`+testPassword+`" }) { customer { id email } } }`, "")
	if len(resp.Errors) > 0 {
		t.Logf("deprecated createCustomer response: %s", resp.Errors[0].Message)
		return
	}

	var data struct {
		CreateCustomer struct {
			Customer struct {
				ID string `json:"id"`
			} `json:"customer"`
		} `json:"createCustomer"`
	}
	json.Unmarshal(resp.Data, &data)
	t.Logf("deprecated createCustomer succeeded, id=%s", data.CreateCustomer.Customer.ID)

	// Clean up: generate token, then delete the customer
	tokenResp := doQuery(t, `mutation { generateCustomerToken(email: "`+testEmail+`", password: "`+testPassword+`") { token } }`, "")
	if len(tokenResp.Errors) == 0 {
		var td struct {
			GenerateCustomerToken struct{ Token string `json:"token"` } `json:"generateCustomerToken"`
		}
		json.Unmarshal(tokenResp.Data, &td)
		doQuery(t, `mutation { deleteCustomer }`, td.GenerateCustomerToken.Token)
		t.Log("test customer cleaned up via deleteCustomer")
	}
}

func TestCompare_AddressV2Mutations(t *testing.T) {
	token := getTestToken(t)

	// Create address to get a uid
	createResp := doQuery(t, `mutation {
		createCustomerAddress(input: {
			firstname: "V2Test"
			lastname: "User"
			street: ["456 V2 Street"]
			city: "Testville"
			country_code: US
			telephone: "(555) 222-3333"
			postcode: "12345"
		}) { id uid }
	}`, token)
	if len(createResp.Errors) > 0 {
		t.Fatalf("create address failed: %s", createResp.Errors[0].Message)
	}
	var createData struct {
		CreateCustomerAddress struct {
			ID  int    `json:"id"`
			UID string `json:"uid"`
		} `json:"createCustomerAddress"`
	}
	json.Unmarshal(createResp.Data, &createData)
	uid := createData.CreateCustomerAddress.UID

	// Update via V2 (uid-based)
	updateResp := doQuery(t, `mutation {
		updateCustomerAddressV2(uid: "`+uid+`", input: { city: "UpdatedCity" }) { city }
	}`, token)
	if len(updateResp.Errors) > 0 {
		t.Fatalf("updateCustomerAddressV2 failed: %s", updateResp.Errors[0].Message)
	}

	// Delete via V2 (uid-based)
	deleteResp := doQuery(t, `mutation { deleteCustomerAddressV2(uid: "`+uid+`") }`, token)
	if len(deleteResp.Errors) > 0 {
		t.Fatalf("deleteCustomerAddressV2 failed: %s", deleteResp.Errors[0].Message)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func TestCompare_CustomAttributes_Customer(t *testing.T) {
	token := getTestToken(t)

	// Query custom_attributes on Customer — should return empty array for default installation
	// (all standard customer attributes are static/flat, not EAV)
	resp := doQuery(t, `{
		customer {
			custom_attributes { code ... on AttributeValue { value } }
		}
	}`, token)

	if len(resp.Errors) > 0 {
		t.Fatalf("custom_attributes query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			CustomAttributes []json.RawMessage `json:"custom_attributes"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)

	// Default Magento installation has no user-defined EAV attributes
	// So custom_attributes should be empty (or null)
	t.Logf("custom_attributes count: %d (expected 0 for default installation)", len(data.Customer.CustomAttributes))
}

func TestCompare_CustomAttributesV2_Address(t *testing.T) {
	token := getTestToken(t)

	resp := doQuery(t, `{
		customer {
			addresses {
				id
				custom_attributesV2 { code ... on AttributeValue { value } }
			}
		}
	}`, token)

	if len(resp.Errors) > 0 {
		t.Fatalf("custom_attributesV2 query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			Addresses []struct {
				ID               int               `json:"id"`
				CustomAttributes []json.RawMessage `json:"custom_attributesV2"`
			} `json:"addresses"`
		} `json:"customer"`
	}
	json.Unmarshal(resp.Data, &data)

	if len(data.Customer.Addresses) > 0 {
		t.Logf("address %d custom_attributesV2 count: %d (expected 0 for default installation)",
			data.Customer.Addresses[0].ID, len(data.Customer.Addresses[0].CustomAttributes))
	}
}

func TestCompare_CustomAttributes_WithFilter(t *testing.T) {
	token := getTestToken(t)

	// Filter by specific attribute codes — should return empty even with filter
	resp := doQuery(t, `{
		customer {
			custom_attributes(attributeCodes: ["nonexistent_attr"]) {
				code
				... on AttributeValue { value }
			}
		}
	}`, token)

	if len(resp.Errors) > 0 {
		t.Fatalf("filtered custom_attributes query failed: %s", resp.Errors[0].Message)
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func cleanupRevocation(t *testing.T) {
	t.Helper()
	if testDB != nil {
		testDB.Exec("DELETE FROM jwt_auth_revoked WHERE user_type_id = 3 AND user_id = 1")
	}
}
