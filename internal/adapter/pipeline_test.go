package adapter

import (
	"strings"
	"testing"

	"claude2api/internal/utils"
)

func TestOutputFilterStopSequence(t *testing.T) {
	f := newOutputFilter([]string{"END"}, 0)
	got := f.push("abcEN", false) + f.push("D", false) + f.push("", true)
	if got != "abc" {
		t.Fatalf("stop 序列处理错误: %q", got)
	}
	if !f.done {
		t.Fatal("命中 stop 后应停止输出")
	}
}

// max_tokens 现在按 token 估算收敛，不再用 max_tokens*4 字节近似。
func TestOutputFilterTokenBudget(t *testing.T) {
	const budget = 200
	f := newOutputFilter(nil, budget)
	var out strings.Builder
	for i := 0; i < 5000; i++ {
		out.WriteString(f.push("hello world ", false))
	}
	out.WriteString(f.push("", true))
	if !f.done {
		t.Fatal("达到 max_tokens 后应停止输出")
	}
	tokens := utils.EstimateTokens(out.String())
	if tokens < budget/2 || tokens > budget+120 {
		t.Fatalf("输出应落在 token 预算附近，得到 %d tokens", tokens)
	}
}

// 中文约 3 字节/token，旧的字节近似会超出预算约三分之一，这里校验按 token 收敛。
func TestOutputFilterCJKTokenBudget(t *testing.T) {
	const budget = 200
	f := newOutputFilter(nil, budget)
	text := strings.Repeat("这是一段用来验证中文预算的测试文本", 40)
	var out strings.Builder
	for _, r := range text {
		out.WriteString(f.push(string(r), false))
	}
	out.WriteString(f.push("", true))
	if !f.done {
		t.Fatal("中文输出也应在达到预算后停止")
	}
	tokens := utils.EstimateTokens(out.String())
	if tokens > budget+120 {
		t.Fatalf("中文输出明显超出预算: %d tokens", tokens)
	}
	if tokens < budget/2 {
		t.Fatalf("中文输出被截得过短: %d tokens", tokens)
	}
	if !strings.HasPrefix(text, out.String()) {
		t.Fatal("截断必须按字符边界进行，不能破坏 UTF-8")
	}
}

func TestOutputFilterWithoutBudgetPassesThrough(t *testing.T) {
	f := newOutputFilter(nil, 0)
	text := strings.Repeat("内容", 200)
	if got := f.push(text, true); got != text {
		t.Fatal("未设置 max_tokens 时不应截断")
	}
}

func TestOutputFilterByteCapSafetyNet(t *testing.T) {
	// maxTokens=1 时字节兜底为 16 字节，防止分词器异常导致无限输出。
	f := newOutputFilter(nil, 1)
	got := f.push(strings.Repeat("a", 1000), false)
	if len(got) > 16 {
		t.Fatalf("字节兜底未生效: %d", len(got))
	}
}
