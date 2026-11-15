package handler

import (
	"net/http"
	"strings"
	"testing"
)

func TestArtifactTicketDoesNotExposeCredential(t *testing.T) {
	token, err := issueArtifactTicket("admin-secret", "user@example.com")
	if err != nil || strings.Contains(token, "admin-secret") {
		t.Fatalf("invalid ticket: %q %v", token, err)
	}
	credential, email, path, ok := artifactAuth(authPrefix + token + "/artifact")
	if !ok || credential != "admin-secret" || email != "user@example.com" || path != "/artifact" {
		t.Fatalf("ticket lookup failed: %q %q %q %v", credential, email, path, ok)
	}
	if cookies := mergeProxyCookies("claude2api_auth=admin-secret; browser=1", "sessionKey=2"); strings.Contains(cookies, "admin-secret") {
		t.Fatalf("auth cookie leaked upstream: %s", cookies)
	}
}

// 主站不允许被第三方页面嵌入，artifact 沙箱只允许被主站嵌入。
func TestApplyFrameGuards(t *testing.T) {
	main := http.Header{}
	applyFrameGuards(main, &proxyCtx{onMain: true})
	if main.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("主站应设置 SAMEORIGIN，得到 %q", main.Get("X-Frame-Options"))
	}
	if !strings.Contains(main.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("主站 CSP 缺失: %q", main.Get("Content-Security-Policy"))
	}

	uc := http.Header{}
	applyFrameGuards(uc, &proxyCtx{onMain: false, mainOrig: "http://127.0.0.1:8787"})
	if uc.Get("X-Frame-Options") != "" {
		t.Fatalf("artifact 沙箱不能带 X-Frame-Options，否则无法被 iframe 加载: %q", uc.Get("X-Frame-Options"))
	}
	if got := uc.Get("Content-Security-Policy"); got != "frame-ancestors http://127.0.0.1:8787" {
		t.Fatalf("artifact CSP 错误: %q", got)
	}

	empty := http.Header{}
	applyFrameGuards(empty, &proxyCtx{onMain: false})
	if got := empty.Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Fatalf("缺少主站来源时应拒绝嵌入，得到 %q", got)
	}
}

func TestSanitizeOrigin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:8787", "http://127.0.0.1:8787"},
		{"https://example.com/path?q=1", "https://example.com"},
		{"http://user@evil.com/x", "http://evil.com"},
		{"javascript:alert(1)", ""},
		{"", ""},
		{"http://a.com; script-src *", ""},
	}
	for _, tc := range cases {
		got := sanitizeOrigin(tc.in)
		if got != tc.want {
			t.Fatalf("sanitizeOrigin(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, ";") {
			t.Fatalf("不允许向 CSP 注入额外指令: %q", got)
		}
	}
}
