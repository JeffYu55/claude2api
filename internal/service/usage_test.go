package service

import (
	"bytes"
	"io"
	"os"
	"testing"
	"testing/iotest"
	"time"

	"claude2api/internal/repository"
	"claude2api/internal/utils"
)

func TestCapturedCompletionUsage(t *testing.T) {
	body, err := os.ReadFile("testdata/completion.sse")
	if err != nil {
		t.Fatal(err)
	}
	for _, complete := range []bool{true, false} {
		input := body
		if !complete {
			input = body[:bytes.Index(body, []byte("event: message_stop"))]
		}
		record := &repository.UsageRecord{CreatedAt: time.Now().UTC()}
		stream := CaptureAccountUsage(io.NopCloser(iotest.OneByteReader(bytes.NewReader(input))), record, 7)
		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(input, output) {
			t.Fatal("采集改变了对话流")
		}
		if record.InputTokens != 7 || record.OutputTokens != utils.EstimateTokens("17 × 23 = **391**") || record.ThinkingTokens != 0 || record.Model != "claude-sonnet-4-6" || record.Success != complete {
			t.Fatalf("采集结果不符合真实样本，complete=%v record=%+v", complete, record)
		}
	}
}

// Close 返回后采集 goroutine 必须已经结束，调用方读取 record 才不会与它竞争。
func TestUsageStreamCloseWaitsForCollector(t *testing.T) {
	body, err := os.ReadFile("testdata/completion.sse")
	if err != nil {
		t.Fatal(err)
	}
	record := &repository.UsageRecord{CreatedAt: time.Now().UTC()}
	stream := CaptureAccountUsage(io.NopCloser(bytes.NewReader(body)), record, 11)
	if _, err := io.ReadAll(stream); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if record.InputTokens != 11 || record.OutputTokens == 0 || !record.Success {
		t.Fatalf("Close 返回时采集尚未完成: %+v", record)
	}
	// 显式关闭与 defer 关闭并存时，重复关闭也要安全。
	if err := stream.Close(); err != nil {
		t.Fatalf("重复关闭失败: %v", err)
	}
}
