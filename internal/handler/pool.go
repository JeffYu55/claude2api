package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"claude2api/internal/repository"
	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

func PoolList(c *gin.Context) {
	current, _ := c.Cookie("pool_acct")
	items := []gin.H{}
	for _, account := range repository.LoadAccounts() {
		if !service.AccountUsable(&account) {
			continue
		}
		status := account.Status
		if status == "" {
			status = "unknown"
		}
		items = append(items, gin.H{"email": account.Email, "status": status, "subscription_plan": account.SubscriptionPlan, "usage": account.Usage, "usage_updated_at": account.UsageUpdatedAt})
	}
	c.JSON(http.StatusOK, gin.H{"accounts": items, "current": current})
}

func PoolSelect(c *gin.Context) {
	email := c.Query("email")
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	if service.AccountByEmail(email) != nil {
		c.SetCookie("pool_acct", email, 31536000, "/", "", secure, true)
		c.SetCookie("mirror", "claude", 31536000, "/", "", secure, true)
		slog.Info("[号池] 切换当前账号", "email", email)
	} else {
		c.SetCookie("pool_acct", "", -1, "/", "", secure, true)
		slog.Info("[号池] 切换为自动轮询")
	}
	c.Redirect(http.StatusFound, "/new")
}
