package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tvplayer/backend/internal/middleware"
	"github.com/tvplayer/backend/internal/models"
	"github.com/tvplayer/backend/internal/services"
)

func setupTestDB(t *testing.T) *services.ClientService {
	t.Helper()
	db, err := services.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return services.NewClientService(db)
}

func setupRouter(clientSvc *services.ClientService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// 初始化 JWT
	InitSecret("test-secret-key-for-unit-tests", "testpassword", 24)

	// 公开路由（无需认证）
	r.POST("/api/v1/admin/login", func(c *gin.Context) {
		h := &Handler{}
		h.AdminLogin(c)
	})
	r.POST("/api/v1/client/register", func(c *gin.Context) {
		ch := NewClientHandler(clientSvc)
		ch.Register(c)
	})
	r.GET("/api/v1/client/verify", func(c *gin.Context) {
		ch := NewClientHandler(clientSvc)
		ch.Verify(c)
	})

	// 需要认证的路由
	auth := r.Group("/api/v1")
	auth.Use(middleware.AuthMiddleware("test-secret-key-for-unit-tests", nil))
	{
		auth.GET("/client/me", func(c *gin.Context) {
			ch := NewClientHandler(clientSvc)
			ch.Me(c)
		})
	}

	return r
}

func TestAdminLogin_Success(t *testing.T) {
	r := setupRouter(nil)

	body, _ := json.Marshal(map[string]string{"password": "testpassword"})
	req := httptest.NewRequest("POST", "/api/v1/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
}

func TestAdminLogin_WrongPassword(t *testing.T) {
	r := setupRouter(nil)

	body, _ := json.Marshal(map[string]string{"password": "wrongpassword"})
	req := httptest.NewRequest("POST", "/api/v1/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAdminLogin_MissingPassword(t *testing.T) {
	r := setupRouter(nil)

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest("POST", "/api/v1/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestClientRegister_Success(t *testing.T) {
	clientSvc := setupTestDB(t)
	r := setupRouter(clientSvc)

	body, _ := json.Marshal(map[string]string{
		"name":      "Test Device",
		"device_id": "test-device-001",
	})
	req := httptest.NewRequest("POST", "/api/v1/client/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// 默认不自动审批，应返回 202 (pending)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", resp.Data)
	}
	if data["status"] != "pending" {
		t.Fatalf("expected status pending, got %v", data["status"])
	}
}

func TestClientRegister_MissingFields(t *testing.T) {
	clientSvc := setupTestDB(t)
	r := setupRouter(clientSvc)

	body, _ := json.Marshal(map[string]string{
		"name": "Test Device",
		// 缺少 device_id
	})
	req := httptest.NewRequest("POST", "/api/v1/client/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthRequired_GetProtectedEndpoint(t *testing.T) {
	clientSvc := setupTestDB(t)
	r := setupRouter(clientSvc)

	// 无 token 访问 /client/me — 应该被拒绝（非公开只读路径）
	req := httptest.NewRequest("GET", "/api/v1/client/me", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthRequired_WriteWithoutToken(t *testing.T) {
	clientSvc := setupTestDB(t)
	r := setupRouter(clientSvc)

	body, _ := json.Marshal(map[string]string{
		"name": "Test Group",
	})
	req := httptest.NewRequest("POST", "/api/v1/groups", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminTokenAccess(t *testing.T) {
	clientSvc := setupTestDB(t)
	r := setupRouter(clientSvc)

	// 先登录获取 token
	body, _ := json.Marshal(map[string]string{"password": "testpassword"})
	loginReq := httptest.NewRequest("POST", "/api/v1/admin/login", bytes.NewReader(body))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	r.ServeHTTP(loginW, loginReq)

	var loginResp models.APIResponse
	json.Unmarshal(loginW.Body.Bytes(), &loginResp)
	data := loginResp.Data.(map[string]interface{})
	token := data["token"].(string)

	// 用 token 访问受保护接口
	req := httptest.NewRequest("GET", "/api/v1/client/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Admin token 访问 /client/me — 应该能通过认证层
	// （可能因为没有 client context 返回其他错误，但不应该是 401）
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("admin token should pass auth, got 401")
	}
}

func TestVersion(t *testing.T) {
	// 测试版本号变量
	if Version != "dev" {
		t.Logf("Version: %s", Version)
	}
}

func TestMain(m *testing.M) {
	// 设置测试环境
	os.Exit(m.Run())
}
