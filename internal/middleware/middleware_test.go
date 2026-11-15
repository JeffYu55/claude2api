package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"

	"github.com/gin-gonic/gin"
)

const testPassword = "s3cret-admin"

func setupPassword(t *testing.T) {
	t.Helper()
	config.Update(map[string]any{"admin_password": testPassword})
	t.Cleanup(func() { config.Update(map[string]any{"admin_password": testPassword}) })
}

func TestAdminCredentialValidUsesExactMatch(t *testing.T) {
	setupPassword(t)
	for _, tc := range []struct {
		credential string
		want       bool
	}{
		{testPassword, true},
		{testPassword + " ", false},
		{testPassword[:len(testPassword)-1], false},
		{"", false},
	} {
		if got := AdminCredentialValid(tc.credential); got != tc.want {
			t.Fatalf("AdminCredentialValid(%q)=%v，期望 %v", tc.credential, got, tc.want)
		}
	}
}

func TestCredentialRole(t *testing.T) {
	setupPassword(t)
	if role, ok := CredentialRole(testPassword); !ok || role != "admin" {
		t.Fatalf("管理密码应为 admin，得到 %q %v", role, ok)
	}
	if _, ok := CredentialRole("wrong"); ok {
		t.Fatal("错误凭据不应通过")
	}
	if _, ok := CredentialRole(""); ok {
		t.Fatal("空凭据不应通过")
	}
}

// 管理密码不再能当 API Key 用：请求头里带明文口令必须被拒绝。
func TestAPIKeyRejectsAdminPasswordHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPassword(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("Authorization", "Bearer "+testPassword)
	APIKey()(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("管理密码不应作为 API Key，status=%d", w.Code)
	}
}

// 后台「对话测试」改用登录 Cookie 调用 /v1。
func TestAPIKeyAcceptsAdminCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPassword(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.AddCookie(&http.Cookie{Name: AuthCookieName, Value: testPassword})
	APIKey()(c)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("登录 Cookie 应可通过 /v1 鉴权")
	}
}

func TestAdminAuthAcceptsCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPassword(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	c.Request.AddCookie(&http.Cookie{Name: AuthCookieName, Value: testPassword})
	AdminAuth()(c)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("登录 Cookie 应可访问管理接口")
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	AdminAuth()(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("缺少凭据应返回 401，得到 %d", w.Code)
	}
}

func TestLoginLimiter(t *testing.T) {
	limiter := NewLoginLimiter(2, time.Minute)
	if !limiter.Allow("1.2.3.4") {
		t.Fatal("首次应放行")
	}
	limiter.Fail("1.2.3.4")
	limiter.Fail("1.2.3.4")
	if limiter.Allow("1.2.3.4") {
		t.Fatal("达到上限后应拦截")
	}
	if !limiter.Allow("5.6.7.8") {
		t.Fatal("其他来源不应被牵连")
	}
	limiter.Reset("1.2.3.4")
	if !limiter.Allow("1.2.3.4") {
		t.Fatal("重置后应放行")
	}
}

// 号池 API Key 属于 pool 角色，不能进管理接口，否则任何密钥都能改账号和配置。
func TestAdminAuthRejectsPoolKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	config.Load()
	if err := repository.InitDB(); err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = repository.CloseDB() })
	setupPassword(t)

	key, err := repository.CreateAPIKey("test", "sk-pool-only")
	if err != nil {
		t.Fatalf("创建测试密钥失败: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	c.Request.Header.Set("Authorization", "Bearer "+key.Key)
	AdminAuth()(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("号池密钥不应进入管理接口，status=%d", w.Code)
	}

	// 同一密钥仍可用于号池接口。
	if role, ok := CredentialRole(key.Key); !ok || role != "pool" {
		t.Fatalf("号池密钥角色应为 pool，得到 %q %v", role, ok)
	}
}
