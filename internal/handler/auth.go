package handler

import (
	"net/http"
	"strings"
	"time"

	"claude2api/internal/middleware"

	"github.com/gin-gonic/gin"
)

// LoginLimiter 限制后台登录失败次数：同一来源 10 分钟内最多失败 5 次。
var LoginLimiter = middleware.NewLoginLimiter(5, 10*time.Minute)

func AdminLogin(c *gin.Context) {
	var body struct {
		Credential string `json:"credential"`
	}
	_ = c.ShouldBindJSON(&body)
	credential := strings.TrimSpace(body.Credential)
	role, ok := middleware.CredentialRole(credential)
	if !ok {
		LoginLimiter.Fail(c.ClientIP())
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密码或密钥错误"})
		return
	}
	LoginLimiter.Reset(c.ClientIP())
	middleware.SetAuthCookie(c, credential)
	c.JSON(http.StatusOK, gin.H{"role": role})
}

func AdminLogout(c *gin.Context) {
	middleware.ClearAuthCookie(c)
	c.Status(http.StatusNoContent)
}

// AdminSession 返回当前凭据对应的角色，供前端刷新页面后靠 HttpOnly Cookie 恢复登录态。
func AdminSession(c *gin.Context) {
	role, ok := middleware.CredentialRole(middleware.RequestCredential(c.Request))
	if !ok {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录或密码错误"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"role": role})
}
