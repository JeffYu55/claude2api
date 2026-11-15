package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	// MinStatusCheckSeconds 是账号巡检的最小间隔。
	MinStatusCheckSeconds = 21600
	// DefaultMaxChatHistoryLength 是长上下文转附件的默认阈值。
	DefaultMaxChatHistoryLength = 12000
	// MinMaxChatHistoryLength 是长上下文阈值的下限，低于该值会把正常对话也转成附件。
	MinMaxChatHistoryLength = 1000
	// MaxRetryCount 是换号重试次数上限。
	MaxRetryCount = 8
)

// Settings 是运行时配置。
type Settings struct {
	Proxy                      string `json:"proxy" yaml:"proxy"`
	WebHost                    string `json:"web_host" yaml:"web_host"`
	WebPort                    int    `json:"web_port" yaml:"-"`
	WebUCHost                  string `json:"-" yaml:"web_uc_host"`
	AdminPassword              string `json:"-" yaml:"admin_password"`
	StatusCheckIntervalSeconds int    `json:"status_check_interval_seconds" yaml:"status_check_seconds"`
	RetryCount                 int    `json:"retry_count" yaml:"retry_count"`
	ChatDelete                 bool   `json:"chat_delete" yaml:"delete_chat"`
	MaxChatHistoryLength       int    `json:"max_chat_history_length" yaml:"max_history_length"`
	RemoveInvalidAccount       bool   `json:"remove_invalid_account" yaml:"remove_invalid_account"`
	DetailedAPILog             bool   `json:"detailed_api_log" yaml:"detailed_api_log"`
}

var (
	baseDir    string
	configPath string

	current   Settings
	currentMu sync.RWMutex
)

// Load 加载配置。
func Load() {
	exe, err := os.Executable()
	if err == nil {
		baseDir = filepath.Dir(exe)
	} else {
		baseDir, _ = os.Getwd()
	}
	// 开发运行时优先使用当前目录的配置。
	if wd, err := os.Getwd(); err == nil {
		_, e1 := os.Stat(filepath.Join(wd, "config.yaml"))
		_, e2 := os.Stat(filepath.Join(wd, "config.example.yaml"))
		if e1 == nil || e2 == nil {
			baseDir = wd
		}
	}
	configPath = filepath.Join(baseDir, "config.yaml")
	ensureConfig(configPath)
	current = Settings{WebHost: "127.0.0.1", WebPort: 8787, WebUCHost: "localhost", StatusCheckIntervalSeconds: MinStatusCheckSeconds, ChatDelete: true, MaxChatHistoryLength: DefaultMaxChatHistoryLength}
	data, _ := os.ReadFile(configPath)
	_ = yaml.Unmarshal(data, &current)
	normalize(&current)
	ensureAdminPassword()
}

// normalize 把配置收敛到安全区间。加载和后台热更新共用同一套规则，
// 避免把 retry_count 或 max_history_length 改成负数后整个服务行为异常。
func normalize(s *Settings) {
	s.Proxy = strings.TrimSpace(s.Proxy)
	s.AdminPassword = strings.TrimSpace(s.AdminPassword)
	s.WebHost = strings.TrimSpace(s.WebHost)
	if s.WebHost == "" {
		s.WebHost = "127.0.0.1"
	}
	s.WebUCHost = strings.TrimSpace(s.WebUCHost)
	if s.WebUCHost == "" {
		s.WebUCHost = "localhost"
	}
	if s.StatusCheckIntervalSeconds < MinStatusCheckSeconds {
		s.StatusCheckIntervalSeconds = MinStatusCheckSeconds
	}
	if s.MaxChatHistoryLength < MinMaxChatHistoryLength {
		s.MaxChatHistoryLength = DefaultMaxChatHistoryLength
	}
	if s.RetryCount < 0 {
		s.RetryCount = 0
	}
	if s.RetryCount > MaxRetryCount {
		s.RetryCount = MaxRetryCount
	}
}

// ensureAdminPassword 在未配置管理密码时生成随机密码并写回配置，
// 避免用示例文件里的固定口令直接暴露后台。
func ensureAdminPassword() {
	if current.AdminPassword != "" {
		return
	}
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		slog.Error("[配置] 生成随机管理密码失败，请手动在 config.yaml 设置 admin_password", "err", err)
		return
	}
	current.AdminPassword = base64.RawURLEncoding.EncodeToString(b)
	writeConfig()
	slog.Warn("[配置] 未设置 admin_password，已生成随机管理密码并写入 config.yaml，请妥善保存",
		"path", configPath, "admin_password", current.AdminPassword)
}

func Get() Settings {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current
}

// ensureConfig 缺配置时复制示例文件。
func ensureConfig(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	example := filepath.Join(filepath.Dir(path), "config.example.yaml")
	data, err := os.ReadFile(example)
	if err != nil {
		slog.Warn("[配置] 未找到 config.yaml，也无 config.example.yaml 可复制，使用内置默认值", "err", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("[配置] 复制 config.example.yaml 失败，使用内置默认值", "err", err)
		return
	}
	slog.Info("[配置] 未找到 config.yaml，已从 config.example.yaml 复制生成", "path", path)
}

// DataDir 返回数据目录。
func DataDir() string {
	return filepath.Join(baseDir, "data")
}

// DBPath 返回数据库路径。
func DBPath() string {
	return filepath.Join(baseDir, "data", "app.db")
}

func writeConfig() {
	data, _ := yaml.Marshal(&current)
	_ = os.WriteFile(configPath, data, 0o644)
}

// Update 应用后台提交的配置补丁，所有数值项都会按 normalize 的区间收敛，
// 返回值是收敛后的实际生效配置。
func Update(patch map[string]any) Settings {
	currentMu.Lock()
	defer currentMu.Unlock()
	for k, v := range patch {
		switch k {
		case "proxy":
			current.Proxy = strings.TrimSpace(fmt.Sprint(v))
		case "admin_password":
			// 空值代表「不修改」，避免误提交把管理密码清空。
			if pw := strings.TrimSpace(fmt.Sprint(v)); pw != "" {
				current.AdminPassword = pw
			}
		case "status_check_interval_seconds":
			current.StatusCheckIntervalSeconds = mustAtoi(v)
		case "retry_count":
			current.RetryCount = mustAtoi(v)
		case "chat_delete":
			current.ChatDelete = mustBool(v)
		case "max_chat_history_length":
			current.MaxChatHistoryLength = mustAtoi(v)
		case "remove_invalid_account":
			current.RemoveInvalidAccount = mustBool(v)
		case "detailed_api_log":
			current.DetailedAPILog = mustBool(v)
		}
	}
	normalize(&current)
	writeConfig()
	return current
}

func mustAtoi(v any) int {
	n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(v)))
	return n
}

func mustBool(v any) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(v)))
	return b
}
