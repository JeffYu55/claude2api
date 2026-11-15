package adapter

import (
	"context"
	"testing"
)

func TestImageInput(t *testing.T) {
	if raw, err := decodeImageBase64("aGk"); err != nil || string(raw) != "hi" {
		t.Fatalf("无填充 Base64 解码失败: %q %v", raw, err)
	}
	if _, err := publicImageURL("http://127.0.0.1/image.png"); err == nil {
		t.Fatal("不应允许内网图片 URL")
	}
}

func TestPublicImageURLRejectsUnsafeTargets(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/image.png",
		"http://10.0.0.5/image.png",
		"http://169.254.169.254/latest/meta-data",
		"http://example.com:8080/image.png",
		"http://example.com:6379/image.png",
		"http://user:pass@example.com/image.png",
		"file:///etc/passwd",
		"gopher://example.com/",
	} {
		if _, err := publicImageURL(raw); err == nil {
			t.Fatalf("应拒绝图片地址 %s", raw)
		}
	}
}

func TestSafeDialContextRejectsPrivateIP(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:80", "10.1.2.3:443", "169.254.169.254:80"} {
		if conn, err := safeDialContext(context.Background(), "tcp", addr); err == nil {
			conn.Close()
			t.Fatalf("不应连到内网地址 %s", addr)
		}
	}
}
