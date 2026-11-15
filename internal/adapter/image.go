package adapter

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"claude2api/internal/config"
)

const maxImageBytes = 20 << 20

var fetchImage = downloadImage

func normalizeImageSource(source, mediaType string) string {
	source = strings.TrimSpace(source)
	if source == "" || strings.HasPrefix(strings.ToLower(source), "data:") || strings.HasPrefix(strings.ToLower(source), "http://") || strings.HasPrefix(strings.ToLower(source), "https://") {
		return source
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return "data:" + mediaType + ";base64," + source
}

func prepareImages(sources []string) ([]string, error) {
	images := make([]string, 0, len(sources))
	for _, source := range sources {
		raw, err := imageBytes(source)
		if err != nil {
			return nil, err
		}
		images = append(images, base64.StdEncoding.EncodeToString(raw))
	}
	return images, nil
}

func imageBytes(source string) ([]byte, error) {
	lower := strings.ToLower(source)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return fetchImage(source)
	}
	if strings.HasPrefix(lower, "data:") {
		header, payload, ok := strings.Cut(source, ",")
		if !ok || !strings.Contains(strings.ToLower(header), ";base64") {
			return nil, fmt.Errorf("图片 Data URI 格式错误")
		}
		source = payload
	}
	raw, err := decodeImageBase64(source)
	if err != nil {
		return nil, fmt.Errorf("图片 Base64 无效: %w", err)
	}
	return validateImage(raw)
}

func downloadImage(source string) ([]byte, error) {
	target, err := publicImageURL(source)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		// 建连时按解析结果逐个校验并直连固定 IP，避免「先校验域名、再解析一次」的 TOCTOU。
		DialContext:           safeDialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	if proxyURL := config.Get().Proxy; proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		// 走出口代理时由代理解析域名，publicImageURL 的本地解析校验仍是第一道闸门。
		transport.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("图片重定向次数过多")
		}
		_, err := publicImageURL(req.URL.String())
		return err
	}}
	resp, err := client.Get(target.String())
	if err != nil {
		return nil, fmt.Errorf("下载图片失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载图片 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %w", err)
	}
	return validateImage(raw)
}

var imageDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

// safeDialContext 在真正建立连接时再校验一次目标 IP，只允许连到校验通过的地址。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("解析图片域名失败: %w", err)
	}
	var lastErr error
	for _, addr := range addrs {
		if !publicImageIP(addr.IP) {
			lastErr = fmt.Errorf("图片 URL 不允许访问内网地址")
			continue
		}
		conn, err := imageDialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("解析图片域名失败")
	}
	return nil, lastErr
}

// publicImageIP 只放行全球可路由的单播地址。
func publicImageIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

func publicImageURL(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("图片 URL 无效")
	}
	target.Scheme = strings.ToLower(target.Scheme)
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("图片 URL 无效")
	}
	if target.Hostname() == "" || target.User != nil {
		return nil, fmt.Errorf("图片 URL 无效")
	}
	// 只允许标准 Web 端口，避免借图片下载去打内网非 HTTP 服务。
	if port := target.Port(); port != "" && port != "80" && port != "443" {
		return nil, fmt.Errorf("图片 URL 端口不受支持")
	}
	ips, err := net.LookupIP(target.Hostname())
	if err != nil {
		return nil, fmt.Errorf("解析图片域名失败: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("解析图片域名失败")
	}
	for _, ip := range ips {
		if !publicImageIP(ip) {
			return nil, fmt.Errorf("图片 URL 不允许访问内网地址")
		}
	}
	return target, nil
}

func decodeImageBase64(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := encoding.DecodeString(value); err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("无效 Base64")
}

func validateImage(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxImageBytes {
		return nil, fmt.Errorf("图片大小必须在 1 字节到 %d MB 之间", maxImageBytes>>20)
	}
	if _, ok := map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true}[http.DetectContentType(raw)]; !ok {
		return nil, fmt.Errorf("不支持的图片格式")
	}
	return raw, nil
}
