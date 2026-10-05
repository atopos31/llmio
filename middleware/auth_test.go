package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates a temporary test database
func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	// Open in-memory SQLite database for testing
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	// Migrate the AuthKey table
	if err := db.AutoMigrate(&models.AuthKey{}); err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	// Set the global DB
	models.DB = db

	// Return cleanup function
	cleanup := func() {
		models.DB = nil
	}

	return db, cleanup
}

func TestCheckAuthKey_EmptyAdminToken(t *testing.T) {
	// Setup test database
	_, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Test with empty admin token
	checkAuthKey(c, "", "")

	// Should not abort and set AllowAllModel to true
	if c.IsAborted() {
		t.Error("expected request to not be aborted")
	}

	// Check context values
	ctx := c.Request.Context()
	allowAll := ctx.Value(consts.ContextKeyAllowAllModel)
	if allowAll == nil || allowAll != true {
		t.Error("expected AllowAllModel to be true")
	}

	t.Log("✓ Empty admin token allows access to all models")
}

func TestCheckAuthKey_AdminTokenMatch(t *testing.T) {
	// Setup test database
	_, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	adminToken := "secret-admin-token"

	// Test with matching admin token
	checkAuthKey(c, adminToken, adminToken)

	// Should not abort and set AllowAllModel to true
	if c.IsAborted() {
		t.Error("expected request to not be aborted")
	}

	// Check context values
	ctx := c.Request.Context()
	allowAll := ctx.Value(consts.ContextKeyAllowAllModel)
	if allowAll == nil || allowAll != true {
		t.Error("expected AllowAllModel to be true")
	}

	t.Log("✓ Matching admin token allows access to all models")
}

func TestCheckAuthKey_EmptyKey(t *testing.T) {
	// Setup test database
	_, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context with response recorder
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	adminToken := "admin-token"

	// Test with empty key but admin token is set
	checkAuthKey(c, "", adminToken)

	// Should abort with Unauthorized status
	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}

	// Check response status
	if c.Writer.Status() != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, c.Writer.Status())
	}

	t.Log("✓ Empty key is rejected when admin token is set")
}

func TestCheckAuthKey_ValidAuthKey_AllowAll(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create a valid auth key with AllowAll=true
	authKey := models.AuthKey{
		Name:     "Test Project",
		Key:      "test-key-456",
		Status:   new(true),
		IOLog:    new(true),
		AllowAll: new(true),
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with valid auth key
	checkAuthKey(c, "test-key-456", "admin-token")

	// Should not abort
	if c.IsAborted() {
		t.Error("expected request to not be aborted")
	}

	// Check context values
	ctx := c.Request.Context()

	authKeyID := ctx.Value(consts.ContextKeyAuthKeyID)
	if authKeyID != authKey.ID {
		t.Errorf("expected authKeyID %d, got %v", authKey.ID, authKeyID)
	}

	allowAll := ctx.Value(consts.ContextKeyAllowAllModel)
	if allowAll == nil || allowAll != true {
		t.Error("expected AllowAllModel to be true")
	}

	ioLog := ctx.Value(consts.ContextKeyAuthKeyIOLog)
	if ioLog == nil || ioLog != true {
		t.Error("expected AuthKeyIOLog to be true")
	}

	// Should not have AllowModels when AllowAll is true
	allowModels := ctx.Value(consts.ContextKeyAllowModels)
	if allowModels != nil {
		t.Error("expected AllowModels to be nil when AllowAll is true")
	}

	t.Log("✓ Valid auth key with AllowAll=true works correctly")
}

func TestCheckAuthKey_ValidAuthKey_RequireModel(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create a valid auth key with specific models
	allowedModels := []string{"gpt-3.5-turbo", "gpt-4"}
	authKey := models.AuthKey{
		Name:     "Test Project",
		Key:      "test-key-789",
		Status:   new(true),
		IOLog:    new(false),
		AllowAll: new(false),
		Models:   allowedModels,
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with valid auth key
	checkAuthKey(c, "test-key-789", "admin-token")

	// Should not abort
	if c.IsAborted() {
		t.Error("expected request to not be aborted")
	}

	// Check context values
	ctx := c.Request.Context()

	authKeyID := ctx.Value(consts.ContextKeyAuthKeyID)
	if authKeyID != authKey.ID {
		t.Errorf("expected authKeyID %d, got %v", authKey.ID, authKeyID)
	}

	allowAll := ctx.Value(consts.ContextKeyAllowAllModel)
	if allowAll == nil || allowAll != false {
		t.Error("expected AllowAllModel to be false")
	}

	allowModels := ctx.Value(consts.ContextKeyAllowModels)
	if allowModels == nil {
		t.Error("expected AllowModels to be set")
	}
	if models, ok := allowModels.([]string); !ok {
		t.Error("expected AllowModels to be []string")
	} else if len(models) != len(allowedModels) {
		t.Errorf("expected %d models, got %d", len(allowedModels), len(models))
	}

	ioLog := ctx.Value(consts.ContextKeyAuthKeyIOLog)
	if ioLog == nil || ioLog != false {
		t.Error("expected AuthKeyIOLog to be false")
	}

	t.Log("✓ Valid auth key with specific models works correctly")
}

func TestCheckAuthKey_InvalidKey(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context with response recorder
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create only a valid key
	authKey := models.AuthKey{
		Name:     "Test Project",
		Key:      "valid-key",
		Status:   new(true),
		AllowAll: new(true),
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with invalid key
	checkAuthKey(c, "invalid-key", "admin-token")

	// Should abort with Unauthorized status
	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}

	// Check response status
	if c.Writer.Status() != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, c.Writer.Status())
	}

	t.Log("✓ Invalid key is rejected")
}

func TestCheckAuthKey_DisabledKey(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context with response recorder
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create a disabled auth key
	authKey := models.AuthKey{
		Name:   "Disabled Project",
		Key:    "disabled-key",
		Status: new(false), // Disabled
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with disabled key
	checkAuthKey(c, "disabled-key", "admin-token")

	// Should abort with Unauthorized status
	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}

	// Check response status
	if c.Writer.Status() != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, c.Writer.Status())
	}

	t.Log("✓ Disabled key is rejected")
}

func TestCheckAuthKey_ExpiredKey(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context with response recorder
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create an expired auth key
	expiredTime := time.Now().Add(-24 * time.Hour) // 24 hours ago
	authKey := models.AuthKey{
		Name:      "Expired Project",
		Key:       "expired-key",
		Status:    new(true),
		AllowAll:  new(true),
		ExpiresAt: &expiredTime,
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with expired key
	checkAuthKey(c, "expired-key", "admin-token")

	// Should abort with Unauthorized status
	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}

	// Check response status
	if c.Writer.Status() != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, c.Writer.Status())
	}

	t.Log("✓ Expired key is rejected")
}

func TestCheckAuthKey_NotExpiredKey(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create a not expired auth key (expires in 24 hours)
	futureTime := time.Now().Add(24 * time.Hour)
	authKey := models.AuthKey{
		Name:      "Valid Project",
		Key:       "valid-key",
		Status:    new(true),
		AllowAll:  new(true),
		ExpiresAt: &futureTime,
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with not expired key
	checkAuthKey(c, "valid-key", "admin-token")

	// Should not abort
	if c.IsAborted() {
		t.Error("expected request to not be aborted")
	}

	ctx := c.Request.Context()
	allowAll := ctx.Value(consts.ContextKeyAllowAllModel)
	if allowAll == nil || allowAll != true {
		t.Error("expected AllowAllModel to be true")
	}

	t.Log("✓ Not expired key is accepted")
}

func TestCheckAuthKey_NilExpiry(t *testing.T) {
	// Setup test database
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create gin context
	r := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(r)

	// Setup request
	req := httptest.NewRequest("POST", "/", nil)
	c.Request = req

	// Create an auth key with nil expiry (never expires)
	authKey := models.AuthKey{
		Name:      "Never Expire Project",
		Key:       "never-expire-key",
		Status:    new(true),
		AllowAll:  new(true),
		ExpiresAt: nil, // Never expires
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	// Test with never expire key
	checkAuthKey(c, "never-expire-key", "admin-token")

	// Should not abort
	if c.IsAborted() {
		t.Error("expected request to not be aborted")
	}

	ctx := c.Request.Context()
	allowAll := ctx.Value(consts.ContextKeyAllowAllModel)
	if allowAll == nil || allowAll != true {
		t.Error("expected AllowAllModel to be true", allowAll)
	}

	t.Log("✓ Nil expiry (never expires) key is accepted")
}

// ── issue #38：Anthropic 侧要能收 Authorization: Bearer ──────────────────────

// 携带方式的矩阵。判据只有一条：能不能过；过了还要确认拿到了 admin token 的权限。
func TestAuthAnthropic_HeaderForms(t *testing.T) {
	const admin = "admin-token"
	cases := []struct {
		name    string
		headers map[string]string
		wantOK  bool
	}{
		{"x-api-key 单独给（既有行为，别回归）", map[string]string{"x-api-key": admin}, true},
		{"Authorization: Bearer 单独给（ANTHROPIC_AUTH_TOKEN）", map[string]string{"Authorization": "Bearer " + admin}, true},
		{"两个都给且一致", map[string]string{"x-api-key": admin, "Authorization": "Bearer " + admin}, true},
		{"两个都给但 x-api-key 是错的：以 x-api-key 为准，不放行", map[string]string{"x-api-key": "wrong", "Authorization": "Bearer " + admin}, false},
		{"非 Bearer 方案不认", map[string]string{"Authorization": "Basic " + admin}, false},
		{"两个都没给", nil, false},
	}

	gin.SetMode(gin.TestMode)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cleanup := setupTestDB(t)
			defer cleanup()

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			req := httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			c.Request = req

			AuthAnthropic(admin)(c)

			if got := !c.IsAborted(); got != tc.wantOK {
				t.Fatalf("放行=%v，期望 %v（响应：%s）", got, tc.wantOK, rec.Body.String())
			}
			if tc.wantOK {
				if v := c.Request.Context().Value(consts.ContextKeyAllowAllModel); v != true {
					t.Fatalf("用 admin token 时应当拿到 AllowAllModel=true，实际 %v", v)
				}
			}
		})
	}
}

// 走一遍签发出来的 AuthKey（sk-llmio-…）经 Bearer 进来的完整路径：
// 既要放行，也要把密钥 id 与 IO 开关写进 context——只断言"没被拒"会漏掉后半段。
func TestAuthAnthropic_BearerAuthKey(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	authKey := models.AuthKey{
		Name:     "Claude Code（ANTHROPIC_AUTH_TOKEN）",
		Key:      "sk-llmio-claude-code",
		Status:   new(true),
		IOLog:    new(true),
		AllowAll: new(true),
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("failed to create test auth key: %v", err)
	}

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
	req.Header.Set("Authorization", "Bearer sk-llmio-claude-code")
	c.Request = req

	AuthAnthropic("admin-token")(c)

	if c.IsAborted() {
		t.Fatalf("Authorization: Bearer 携带 AuthKey 时不该被拒：%s", rec.Body.String())
	}
	ctx := c.Request.Context()
	if got := ctx.Value(consts.ContextKeyAuthKeyID); got != authKey.ID {
		t.Fatalf("ContextKeyAuthKeyID=%v，期望 %d", got, authKey.ID)
	}
	if got := ctx.Value(consts.ContextKeyAuthKeyIOLog); got != true {
		t.Fatalf("ContextKeyAuthKeyIOLog=%v，期望 true", got)
	}
}

// bearerKey 是两处共用的解析：OpenAI 侧的行为必须一字不变（守门测试）。
func TestAuthOpenAI_BearerUnchanged(t *testing.T) {
	_, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	c.Request = req

	AuthOpenAI("admin-token")(c)

	if c.IsAborted() {
		t.Fatalf("OpenAI 侧的 Bearer 一直是认的，不该被拒：%s", rec.Body.String())
	}
	if v := c.Request.Context().Value(consts.ContextKeyAllowAllModel); v != true {
		t.Fatalf("AllowAllModel=%v，期望 true", v)
	}
}
