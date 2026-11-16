package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"

	"github.com/gin-gonic/gin"
)

const AuthCookieName = "claude2api_auth"

// AdminAuth 保护管理 API，接受 Authorization 头或登录 Cookie。
// 只有 admin 角色（管理密码）能进管理接口；号池密钥只能访问 /pool 与 /api/session。
func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if p == "/api/login" || p == "/api/logout" || p == "/api/session" {
			c.Next()
			return
		}
		if role, ok := CredentialRole(RequestCredential(c.Request)); ok && role == "admin" {
			c.Next()
			return
		}
		if !strings.Contains(p, "/api/") {
			c.Redirect(http.StatusFound, "/")
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录或密码错误"})
	}
}

// PoolAuth 保护号池页面和接口。
func PoolAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		credential := RequestCredential(c.Request)
		if PoolCredentialValid(credential) {
			if bearerToken(c.GetHeader("Authorization")) != "" {
				SetAuthCookie(c, credential)
			}
			c.Next()
			return
		}
		if !strings.Contains(c.Request.URL.Path, "/api/") {
			c.Redirect(http.StatusFound, "/")
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing pool key"})
	}
}

// sessionMaxAge 返回登录 Cookie 的有效秒数。
func sessionMaxAge() int {
	days := config.Get().SessionDays
	if days <= 0 {
		days = config.DefaultSessionDays
	}
	return days * 86400
}

// SetAuthCookie 保存登录凭据。有效期为 session_days 天（默认 30），
// 避免每次访问都要重新输入密码。
func SetAuthCookie(c *gin.Context, credential string) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(AuthCookieName, credential, sessionMaxAge(), "/", "", secure, true)
}

func ClearAuthCookie(c *gin.Context) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(AuthCookieName, "", -1, "/", "", secure, true)
}

// SetAuthResponseCookie 同步 artifact 登录凭据。
func SetAuthResponseCookie(w http.ResponseWriter, r *http.Request, credential string) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name: AuthCookieName, Value: credential, Path: "/", MaxAge: sessionMaxAge(),
		Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// RequestCredential 读取 Bearer 或 Cookie。
func RequestCredential(r *http.Request) string {
	if credential := bearerToken(r.Header.Get("Authorization")); credential != "" {
		return credential
	}
	cookie, err := r.Cookie(AuthCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// CredentialRole 返回凭据对应的角色：admin（管理密码）或 pool（API 密钥）。
func CredentialRole(credential string) (string, bool) {
	if credential == "" {
		return "", false
	}
	if AdminCredentialValid(credential) {
		return "admin", true
	}
	if repository.ValidateAPIKey(credential) {
		return "pool", true
	}
	return "", false
}

// AdminCredentialValid 用恒定时间比较校验管理密码。
func AdminCredentialValid(credential string) bool {
	pw := config.Get().AdminPassword
	if pw == "" || credential == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(credential), []byte(pw)) == 1
}

func PoolCredentialValid(credential string) bool {
	_, ok := CredentialRole(credential)
	return ok
}

// APIKey 保护 2api：只接受数据库里的 API 密钥，或已登录后台的 HttpOnly Cookie。
// 管理密码不再作为请求头凭据使用，避免密码泄露后直接白拿全部模型额度。
func APIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if token == "" {
			token = strings.TrimSpace(c.GetHeader("x-api-key"))
		}
		if token != "" && repository.ValidateAPIKey(token) {
			c.Next()
			return
		}
		// 后台「对话测试」页面用登录 Cookie 直接调用 /v1，不再把管理密码当 API Key。
		if cookie, err := c.Request.Cookie(AuthCookieName); err == nil && AdminCredentialValid(cookie.Value) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "无效的 API 密钥", "type": "invalid_request_error"},
		})
	}
}

// bearerToken 解析 Bearer token。
func bearerToken(h string) string {
	h = strings.TrimSpace(h)
	if len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}

// LoginLimiter 限制单个来源的登录失败次数，缓解后台口令爆破。
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	limit    int
	window   time.Duration
}

type loginAttempt struct {
	count int
	first time.Time
}

// NewLoginLimiter 构造失败次数限制器。
func NewLoginLimiter(limit int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{attempts: map[string]loginAttempt{}, limit: limit, window: window}
}

// Allow 判断来源是否还能继续尝试。
func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, a := range l.attempts {
		if now.Sub(a.first) > l.window {
			delete(l.attempts, k)
		}
	}
	a, ok := l.attempts[key]
	if !ok {
		return true
	}
	return a.count < l.limit
}

// Fail 记录一次失败。
func (l *LoginLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.attempts[key]
	if !ok || now.Sub(a.first) > l.window {
		l.attempts[key] = loginAttempt{count: 1, first: now}
		return
	}
	a.count++
	l.attempts[key] = a
}

// Reset 清空某个来源的失败记录。
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// RateLimitLogin 对超限来源直接返回 429。
func RateLimitLogin(l *LoginLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.Allow(c.ClientIP()) {
			c.Header("Retry-After", "600")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "登录尝试过于频繁，请稍后再试"})
			return
		}
		c.Next()
	}
}
