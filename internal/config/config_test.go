package config

import "testing"

func resetForTest(t *testing.T) {
	t.Helper()
	current = Settings{
		WebHost: "127.0.0.1", WebUCHost: "localhost",
		StatusCheckIntervalSeconds: MinStatusCheckSeconds,
		MaxChatHistoryLength:       DefaultMaxChatHistoryLength,
	}
	t.Cleanup(func() { current = Settings{} })
}

func TestUpdateClampsUnsafeValues(t *testing.T) {
	resetForTest(t)

	got := Update(map[string]any{
		"retry_count":                   "-3",
		"max_chat_history_length":       "10",
		"status_check_interval_seconds": "60",
	})
	if got.RetryCount != 0 {
		t.Fatalf("retry_count 负数应归零，得到 %d", got.RetryCount)
	}
	if got.MaxChatHistoryLength != DefaultMaxChatHistoryLength {
		t.Fatalf("max_chat_history_length 过小应回落默认值，得到 %d", got.MaxChatHistoryLength)
	}
	if got.StatusCheckIntervalSeconds != MinStatusCheckSeconds {
		t.Fatalf("巡检间隔过小应抬到下限，得到 %d", got.StatusCheckIntervalSeconds)
	}

	got = Update(map[string]any{"retry_count": 99})
	if got.RetryCount != MaxRetryCount {
		t.Fatalf("retry_count 上限应为 %d，得到 %d", MaxRetryCount, got.RetryCount)
	}
}

func TestUpdateKeepsValidValues(t *testing.T) {
	resetForTest(t)
	got := Update(map[string]any{
		"retry_count":             3,
		"max_chat_history_length": 20000,
		"chat_delete":             false,
		"detailed_api_log":        true,
	})
	if got.RetryCount != 3 || got.MaxChatHistoryLength != 20000 || got.ChatDelete || !got.DetailedAPILog {
		t.Fatalf("合法配置被改动: %+v", got)
	}
}

func TestUpdateIgnoresEmptyPassword(t *testing.T) {
	resetForTest(t)
	current.AdminPassword = "keep-me"
	if got := Update(map[string]any{"admin_password": "  "}); got.AdminPassword != "keep-me" {
		t.Fatalf("空密码不应清空管理口令，得到 %q", got.AdminPassword)
	}
	if got := Update(map[string]any{"admin_password": "new-pass"}); got.AdminPassword != "new-pass" {
		t.Fatalf("管理口令未更新，得到 %q", got.AdminPassword)
	}
}

func TestSessionDaysClamp(t *testing.T) {
	resetForTest(t)
	if got := Update(map[string]any{"session_days": 0}); got.SessionDays != DefaultSessionDays {
		t.Fatalf("0 应回落默认 %d，得到 %d", DefaultSessionDays, got.SessionDays)
	}
	if got := Update(map[string]any{"session_days": -5}); got.SessionDays != DefaultSessionDays {
		t.Fatalf("负数应回落默认 %d，得到 %d", DefaultSessionDays, got.SessionDays)
	}
	if got := Update(map[string]any{"session_days": 9999}); got.SessionDays != MaxSessionDays {
		t.Fatalf("超上限应钳到 %d，得到 %d", MaxSessionDays, got.SessionDays)
	}
	if got := Update(map[string]any{"session_days": 7}); got.SessionDays != 7 {
		t.Fatalf("合法值应保留，得到 %d", got.SessionDays)
	}
}
