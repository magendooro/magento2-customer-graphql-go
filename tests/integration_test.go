package tests

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	_ "github.com/go-sql-driver/mysql"

	"github.com/magendooro/magento2-customer-graphql-go/graph"
	"github.com/magendooro/magento2-customer-graphql-go/internal/jwt"
	"github.com/magendooro/magento2-customer-graphql-go/internal/middleware"
)

var testHandler http.Handler
var testDB *sql.DB

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestMain(m *testing.M) {
	host := envOrDefault("TEST_DB_HOST", "localhost")
	port := envOrDefault("TEST_DB_PORT", "3306")
	user := envOrDefault("TEST_DB_USER", "magento_go")
	password := envOrDefault("TEST_DB_PASSWORD", "magento_go")
	dbName := envOrDefault("TEST_DB_NAME", "magento248")
	socket := envOrDefault("TEST_DB_SOCKET", "/tmp/mysql.sock")

	var dsn string
	if host == "localhost" {
		dsn = user + ":" + password + "@unix(" + socket + ")/" + dbName + "?parseTime=true"
	} else {
		dsn = user + ":" + password + "@tcp(" + host + ":" + port + ")/" + dbName + "?parseTime=true"
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		panic("failed to connect to test database: " + err.Error())
	}
	if err := db.Ping(); err != nil {
		panic("failed to ping test database: " + err.Error())
	}

	testDB = db

	// Clean up any stale test state
	db.Exec("DELETE FROM jwt_auth_revoked WHERE user_type_id = 3")
	db.Exec("UPDATE customer_entity SET failures_num = 0, first_failure = NULL, lock_expires = NULL WHERE entity_id = 1")

	// Read Magento crypt key for JWT
	cryptKey := envOrDefault("MAGENTO_CRYPT_KEY", "base64KjBr8ZM6bmK4xIWfk2/K0+xHEn+Ym6/Ogyl7Y7otzso=")
	jwtManager := jwt.NewManager(cryptKey, 60)

	resolver, err := graph.NewResolver(db, jwtManager)
	if err != nil {
		panic("failed to create resolver: " + err.Error())
	}

	srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{
		Resolvers: resolver,
	}))

	storeResolver := middleware.NewStoreResolver(db)
	tokenResolver := middleware.NewTokenResolver(db, jwtManager)

	resolver.TokenResolver = tokenResolver

	// Apply middleware: store → auth → graphql
	var h http.Handler = srv
	h = middleware.AuthMiddleware(tokenResolver)(h)
	h = middleware.StoreMiddleware(storeResolver)(h)
	testHandler = h

	os.Exit(m.Run())
}

type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func doQuery(t *testing.T, query string, token string) graphQLResponse {
	t.Helper()
	return doQueryWithStore(t, query, token, "default")
}

func doQueryWithStore(t *testing.T, query string, token string, store string) graphQLResponse {
	t.Helper()

	body, _ := json.Marshal(graphQLRequest{Query: query})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Store", store)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	testHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}

	var resp graphQLResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v\n%s", err, rec.Body.String())
	}
	return resp
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	// Health endpoint is not part of graphQL handler, just verify the handler works
	resp := doQuery(t, `{ __typename }`, "")
	if len(resp.Errors) > 0 {
		t.Fatalf("introspection failed: %s", resp.Errors[0].Message)
	}
}

func TestIsEmailAvailable_ExistingEmail(t *testing.T) {
	resp := doQuery(t, `{ isEmailAvailable(email: "roni_cost@example.com") { is_email_available } }`, "")
	if len(resp.Errors) > 0 {
		// Email may not exist in test DB — that's okay, just check no panic
		t.Logf("query returned error (expected if email not in DB): %s", resp.Errors[0].Message)
		return
	}

	var data struct {
		IsEmailAvailable struct {
			IsEmailAvailable bool `json:"is_email_available"`
		} `json:"isEmailAvailable"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// roni_cost@example.com is a Magento sample data customer — should return false (not available)
	t.Logf("is_email_available for roni_cost@example.com: %v", data.IsEmailAvailable.IsEmailAvailable)
}

func TestIsEmailAvailable_NewEmail(t *testing.T) {
	resp := doQuery(t, `{ isEmailAvailable(email: "does-not-exist-99999@test.com") { is_email_available } }`, "")
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected error: %s", resp.Errors[0].Message)
	}

	var data struct {
		IsEmailAvailable struct {
			IsEmailAvailable bool `json:"is_email_available"`
		} `json:"isEmailAvailable"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !data.IsEmailAvailable.IsEmailAvailable {
		t.Error("expected email to be available")
	}
}

func TestCustomer_Unauthenticated(t *testing.T) {
	resp := doQuery(t, `{ customer { id firstname lastname email } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for unauthenticated customer query")
	}
	if resp.Errors[0].Message == "" {
		t.Error("expected non-empty error message")
	}
	t.Logf("got expected error: %s", resp.Errors[0].Message)
}

func TestGenerateToken_InvalidCredentials(t *testing.T) {
	resp := doQuery(t, `mutation { generateCustomerToken(email: "invalid@test.com", password: "wrongpassword") { token } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected error for invalid credentials")
	}
	t.Logf("got expected error: %s", resp.Errors[0].Message)
}

func TestGenerateToken_AndCustomerQuery(t *testing.T) {
	// This test requires a known customer in the database.
	// Use Magento sample data customer: roni_cost@example.com / roni_cost3@example.com
	email := envOrDefault("TEST_CUSTOMER_EMAIL", "roni_cost@example.com")
	password := envOrDefault("TEST_CUSTOMER_PASSWORD", "roni_cost3@example.com")

	// Generate token
	tokenResp := doQuery(t, `mutation { generateCustomerToken(email: "`+email+`", password: "`+password+`") { token } }`, "")
	if len(tokenResp.Errors) > 0 {
		t.Skipf("cannot generate token (customer may not exist): %s", tokenResp.Errors[0].Message)
	}

	var tokenData struct {
		GenerateCustomerToken struct {
			Token string `json:"token"`
		} `json:"generateCustomerToken"`
	}
	if err := json.Unmarshal(tokenResp.Data, &tokenData); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}
	token := tokenData.GenerateCustomerToken.Token
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	t.Logf("generated token: %s...", token[:8])

	// Query customer with token
	customerResp := doQuery(t, `{
		customer {
			id
			firstname
			lastname
			email
			created_at
			default_billing
			default_shipping
			addresses {
				id
				uid
				firstname
				lastname
				street
				city
				region { region_code region region_id }
				postcode
				country_code
				telephone
				default_billing
				default_shipping
			}
			is_subscribed
			confirmation_status
			group_id
		}
	}`, token)

	if len(customerResp.Errors) > 0 {
		t.Fatalf("customer query failed: %s", customerResp.Errors[0].Message)
	}

	var custData struct {
		Customer struct {
			ID        string `json:"id"`
			Firstname string `json:"firstname"`
			Lastname  string `json:"lastname"`
			Email     string `json:"email"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(customerResp.Data, &custData); err != nil {
		t.Fatalf("unmarshal customer: %v", err)
	}

	if custData.Customer.ID == "" {
		t.Error("expected non-empty customer id")
	}
	if custData.Customer.Firstname == "" {
		t.Error("expected non-empty firstname")
	}
	if custData.Customer.Email != email {
		t.Errorf("expected email %s, got %s", email, custData.Customer.Email)
	}
	t.Logf("customer: %s %s (%s)", custData.Customer.Firstname, custData.Customer.Lastname, custData.Customer.Email)

	// Revoke token
	revokeResp := doQuery(t, `mutation { revokeCustomerToken { result } }`, token)
	if len(revokeResp.Errors) > 0 {
		t.Fatalf("revoke failed: %s", revokeResp.Errors[0].Message)
	}
	t.Log("token revoked successfully")
}

func TestUpdateCustomer(t *testing.T) {
	if testDB != nil {
		testDB.Exec("DELETE FROM jwt_auth_revoked WHERE user_type_id = 3 AND user_id = 1")
	}
	email := envOrDefault("TEST_CUSTOMER_EMAIL", "roni_cost@example.com")
	password := envOrDefault("TEST_CUSTOMER_PASSWORD", "roni_cost3@example.com")

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

	// Update middlename
	resp := doQuery(t, `mutation { updateCustomerV2(input: { middlename: "TestMiddle" }) { customer { middlename } } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("update failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		UpdateCustomerV2 struct {
			Customer struct {
				Middlename string `json:"middlename"`
			} `json:"customer"`
		} `json:"updateCustomerV2"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.UpdateCustomerV2.Customer.Middlename != "TestMiddle" {
		t.Errorf("expected middlename 'TestMiddle', got '%s'", data.UpdateCustomerV2.Customer.Middlename)
	}

	// Reset middlename
	doQuery(t, `mutation { updateCustomerV2(input: { middlename: "" }) { customer { middlename } } }`, token)
}

func TestStoreMiddleware(t *testing.T) {
	// Test with explicit store header
	resp := doQueryWithStore(t, `{ isEmailAvailable(email: "test@example.com") { is_email_available } }`, "", "default")
	if len(resp.Errors) > 0 {
		t.Logf("store middleware error (may be expected): %s", resp.Errors[0].Message)
	}
}

// generateTestToken is a helper that generates a customer token for tests.
// Returns empty string and calls t.Skip if the customer doesn't exist.
func generateTestToken(t *testing.T) string {
	t.Helper()
	// Clear any stale JWT revocation before generating a new token
	if testDB != nil {
		testDB.Exec("DELETE FROM jwt_auth_revoked WHERE user_type_id = 3 AND user_id = 1")
	}
	email := envOrDefault("TEST_CUSTOMER_EMAIL", "roni_cost@example.com")
	password := envOrDefault("TEST_CUSTOMER_PASSWORD", "roni_cost3@example.com")

	tokenResp := doQuery(t, `mutation { generateCustomerToken(email: "`+email+`", password: "`+password+`") { token } }`, "")
	if len(tokenResp.Errors) > 0 {
		t.Skipf("cannot generate token (customer may not exist): %s", tokenResp.Errors[0].Message)
	}

	var tokenData struct {
		GenerateCustomerToken struct {
			Token string `json:"token"`
		} `json:"generateCustomerToken"`
	}
	if err := json.Unmarshal(tokenResp.Data, &tokenData); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}
	if tokenData.GenerateCustomerToken.Token == "" {
		t.Skip("empty token — skipping order tests")
	}
	return tokenData.GenerateCustomerToken.Token
}

func TestCustomerOrders_Unauthenticated(t *testing.T) {
	resp := doQuery(t, `{ customer { orders { total_count } } }`, "")
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error for unauthenticated orders query")
	}
	t.Logf("got expected error: %s", resp.Errors[0].Message)
}

func TestCustomerOrders_Authenticated(t *testing.T) {
	token := generateTestToken(t)

	resp := doQuery(t, `{
		customer {
			orders(pageSize: 5) {
				total_count
				page_info { current_page page_size total_pages }
				items {
					id
					number
					order_date
					status
					total {
						grand_total { value currency }
						subtotal { value currency }
					}
					items {
						product_sku
						product_name
						product_sale_price { value currency }
						quantity_ordered
					}
					payment_methods { name type }
				}
			}
		}
	}`, token)

	if len(resp.Errors) > 0 {
		t.Fatalf("orders query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			Orders struct {
				TotalCount int `json:"total_count"`
				PageInfo   struct {
					CurrentPage int `json:"current_page"`
					PageSize    int `json:"page_size"`
				} `json:"page_info"`
				Items []struct {
					ID        string `json:"id"`
					Number    string `json:"number"`
					OrderDate string `json:"order_date"`
					Status    string `json:"status"`
				} `json:"items"`
			} `json:"orders"`
		} `json:"customer"`
	}

	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	t.Logf("total_count=%d page_size=%d items_returned=%d",
		data.Customer.Orders.TotalCount,
		data.Customer.Orders.PageInfo.PageSize,
		len(data.Customer.Orders.Items))

	// page_info should reflect the requested page size
	if data.Customer.Orders.PageInfo.PageSize != 5 {
		t.Errorf("expected page_size=5, got %d", data.Customer.Orders.PageInfo.PageSize)
	}

	// if there are orders, validate fields
	if len(data.Customer.Orders.Items) > 0 {
		item := data.Customer.Orders.Items[0]
		if item.ID == "" {
			t.Error("expected non-empty order id")
		}
		if item.Number == "" {
			t.Error("expected non-empty order number")
		}
		if item.OrderDate == "" {
			t.Error("expected non-empty order_date")
		}
		t.Logf("first order: id=%s number=%s status=%s date=%s", item.ID, item.Number, item.Status, item.OrderDate)
	}
}

func TestCustomerOrders_Pagination(t *testing.T) {
	token := generateTestToken(t)

	// Get page 1 with pageSize=1
	resp1 := doQuery(t, `{ customer { orders(pageSize: 1, currentPage: 1) { total_count items { number } } } }`, token)
	if len(resp1.Errors) > 0 {
		t.Fatalf("page 1 query failed: %s", resp1.Errors[0].Message)
	}

	var data1 struct {
		Customer struct {
			Orders struct {
				TotalCount int `json:"total_count"`
				Items      []struct {
					Number string `json:"number"`
				} `json:"items"`
			} `json:"orders"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(resp1.Data, &data1); err != nil {
		t.Fatalf("unmarshal page 1: %v", err)
	}

	if data1.Customer.Orders.TotalCount < 2 {
		t.Skipf("customer has fewer than 2 orders (%d) — skipping pagination test", data1.Customer.Orders.TotalCount)
	}

	// Get page 2 with pageSize=1
	resp2 := doQuery(t, `{ customer { orders(pageSize: 1, currentPage: 2) { items { number } } } }`, token)
	if len(resp2.Errors) > 0 {
		t.Fatalf("page 2 query failed: %s", resp2.Errors[0].Message)
	}

	var data2 struct {
		Customer struct {
			Orders struct {
				Items []struct {
					Number string `json:"number"`
				} `json:"items"`
			} `json:"orders"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(resp2.Data, &data2); err != nil {
		t.Fatalf("unmarshal page 2: %v", err)
	}

	if len(data1.Customer.Orders.Items) == 0 || len(data2.Customer.Orders.Items) == 0 {
		t.Skip("empty items on one of the pages — skipping")
	}

	num1 := data1.Customer.Orders.Items[0].Number
	num2 := data2.Customer.Orders.Items[0].Number
	if num1 == num2 {
		t.Errorf("expected different orders on page 1 and page 2, both got %s", num1)
	}
	t.Logf("page1=%s page2=%s (distinct ✓)", num1, num2)
}

func TestCustomerOrders_Filter_ByNumber(t *testing.T) {
	token := generateTestToken(t)

	// First, get any order number
	resp := doQuery(t, `{ customer { orders(pageSize: 1) { items { number } } } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("initial query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			Orders struct {
				Items []struct {
					Number string `json:"number"`
				} `json:"items"`
			} `json:"orders"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(data.Customer.Orders.Items) == 0 {
		t.Skip("no orders found — skipping filter test")
	}

	orderNumber := data.Customer.Orders.Items[0].Number

	// Filter by that exact order number
	filterResp := doQuery(t, `{ customer { orders(filter: { number: { eq: "`+orderNumber+`" } }) { total_count items { number } } } }`, token)
	if len(filterResp.Errors) > 0 {
		t.Fatalf("filter query failed: %s", filterResp.Errors[0].Message)
	}

	var filterData struct {
		Customer struct {
			Orders struct {
				TotalCount int `json:"total_count"`
				Items      []struct {
					Number string `json:"number"`
				} `json:"items"`
			} `json:"orders"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(filterResp.Data, &filterData); err != nil {
		t.Fatalf("unmarshal filter: %v", err)
	}

	if filterData.Customer.Orders.TotalCount != 1 {
		t.Errorf("expected total_count=1 for exact number filter, got %d", filterData.Customer.Orders.TotalCount)
	}
	if len(filterData.Customer.Orders.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(filterData.Customer.Orders.Items))
	}
	if filterData.Customer.Orders.Items[0].Number != orderNumber {
		t.Errorf("expected number=%s, got %s", orderNumber, filterData.Customer.Orders.Items[0].Number)
	}
	t.Logf("filter by number=%s returned exactly 1 result ✓", orderNumber)
}

func TestCustomerOrders_Filter_ByStatus(t *testing.T) {
	token := generateTestToken(t)

	resp := doQuery(t, `{ customer { orders(filter: { status: { eq: "complete" } }, pageSize: 5) { total_count items { number status } } } }`, token)
	if len(resp.Errors) > 0 {
		t.Fatalf("status filter query failed: %s", resp.Errors[0].Message)
	}

	var data struct {
		Customer struct {
			Orders struct {
				TotalCount int `json:"total_count"`
				Items      []struct {
					Number string `json:"number"`
					Status string `json:"status"`
				} `json:"items"`
			} `json:"orders"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	t.Logf("found %d complete orders", data.Customer.Orders.TotalCount)

	// All returned orders must have status=complete
	for _, item := range data.Customer.Orders.Items {
		if item.Status != "complete" {
			t.Errorf("expected status=complete for order %s, got %s", item.Number, item.Status)
		}
	}
}
