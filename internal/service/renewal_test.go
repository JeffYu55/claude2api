package service

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tlsclient "github.com/bogdanfinn/tls-client"
)

// LatestSessionKey 应从 jar 里读出上游换发后的新 sessionKey。
// 上游确实会在 Set-Cookie 里轮换 sessionKey，这就是续期机制的钩子。
func TestLatestSessionKeyReadsFromJar(t *testing.T) {
	client := NewClaudeAI("old-key", "", "user@example.com")

	// 初始状态：jar 里就是初始 key，无变化
	latest, changed := client.LatestSessionKey("old-key")
	if latest != "old-key" || changed {
		t.Fatalf("初始状态错误: latest=%q changed=%v", latest, changed)
	}

	// 模拟上游换发：往 jar 里塞新 sessionKey
	u, _ := url.Parse(claudeAIBaseURL)
	client.client.SetCookies(u, []*fhttp.Cookie{{Name: "sessionKey", Value: "new-key", Domain: "claude.ai"}})

	latest, changed = client.LatestSessionKey("old-key")
	if latest != "new-key" || !changed {
		t.Fatalf("换发后应读到新 key: latest=%q changed=%v", latest, changed)
	}

	// jar 里清空后应回退初始值
	client.client.SetCookies(u, []*fhttp.Cookie{{Name: "sessionKey", Value: "", Domain: "claude.ai"}})
	// 空值 cookie 被跳过，回退
	latest, _ = client.LatestSessionKey("old-key")
	if latest != "old-key" {
		t.Fatalf("空值应回退初始值: %q", latest)
	}
}

// 只需验证 jar 读写；不发起真实网络请求。
func TestJarSetGetRoundtrip(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), tlsclient.WithCookieJar(jar))
	if err != nil {
		t.Skipf("构造 tls client 失败: %v", err)
	}
	u, _ := url.Parse("https://claude.ai")
	client.SetCookies(u, []*fhttp.Cookie{{Name: "sessionKey", Value: "rotated", Domain: "claude.ai"}})
	var found bool
	for _, c := range client.GetCookieJar().Cookies(u) {
		if c.Name == "sessionKey" && c.Value == "rotated" {
			found = true
		}
	}
	if !found {
		t.Fatal("SetCookies 后应能从 GetCookieJar 读回")
	}
	_ = http.MethodGet
	_ = httptest.NewRecorder
}
