package adapter

import (
	"strings"
	"testing"
)

// feedAll 把整段文本喂入流式解析器并收尾，返回全部事件。
func feedAll(t *testing.T, chunks ...string) []TaggedStreamEvent {
	t.Helper()
	p := NewTaggedStreamParser()
	var all []TaggedStreamEvent
	for _, c := range chunks {
		ev, err := p.Feed(c)
		if err != nil {
			t.Fatalf("Feed 不应返回错误: %v", err)
		}
		all = append(all, ev...)
	}
	ev, err := p.Finish()
	if err != nil {
		t.Fatalf("Finish 不应返回错误: %v", err)
	}
	return append(all, ev...)
}

func collectText(ev []TaggedStreamEvent) string {
	var s string
	for _, e := range ev {
		if e.Type == EventBlockDelta {
			s += e.Text
		}
	}
	return s
}

func hasToolCall(ev []TaggedStreamEvent) bool {
	for _, e := range ev {
		if e.Type == EventToolCall {
			return true
		}
	}
	return false
}

// 上游完全不遵守协议：纯文本应原样降级为文本，不报错。
func TestStreamPlainTextDegrade(t *testing.T) {
	ev := feedAll(t, "Hello, this is plain prose without any tags.")
	if got := collectText(ev); got != "Hello, this is plain prose without any tags." {
		t.Fatalf("文本不匹配: %q", got)
	}
	if hasToolCall(ev) {
		t.Fatal("不应产生工具调用")
	}
}

// 流在标签中途截断：半截标签当普通文本吐出，不报错。
func TestStreamIncompleteTagDegrade(t *testing.T) {
	ev := feedAll(t, "partial answer <tool_cal")
	txt := collectText(ev)
	if txt != "partial answer <tool_cal" {
		t.Fatalf("文本不匹配: %q", txt)
	}
}

// 正常的 final_answer 协议。
func TestStreamFinalAnswer(t *testing.T) {
	ev := feedAll(t, "<think>reasoning</think><final_answer>done</final_answer>")
	if got := collectText(ev); got != "reasoningdone" {
		t.Fatalf("文本不匹配: %q", got)
	}
}

// 正常的工具调用协议（分片喂入）。
func TestStreamToolCallSplit(t *testing.T) {
	ev := feedAll(t, `<tool_ca`, `lls>[{"name":"Read",`, `"arguments":{"path":"a"}}]</tool_calls>`)
	if !hasToolCall(ev) {
		t.Fatal("应产生工具调用")
	}
}

// 工具块内 JSON 非法：降级为文本，不报错。
func TestStreamBadToolJSONDegrade(t *testing.T) {
	ev := feedAll(t, `<tool_calls>not json</tool_calls>`)
	if hasToolCall(ev) {
		t.Fatal("非法 JSON 不应产生工具调用")
	}
	if got := collectText(ev); got == "" {
		t.Fatal("非法 JSON 应降级为文本")
	}
}

// 非流式容错解析：纯文本降级为 final answer。
func TestParseTolerantPlainText(t *testing.T) {
	out := ParseTaggedOutputTolerant("just plain text")
	if !out.IsFinalAnswer() || out.FinalAnswer != "just plain text" {
		t.Fatalf("应降级为 final answer, 得到 %+v", out)
	}
	if out.IsToolCall() {
		t.Fatal("不应有工具调用")
	}
}

// 非流式容错解析：合法协议仍正常解析。
func TestParseTolerantValid(t *testing.T) {
	out := ParseTaggedOutputTolerant(`<tool_calls>[{"name":"Read","arguments":{"p":"x"}}]</tool_calls>`)
	if !out.IsToolCall() || len(out.ToolCalls) != 1 || out.ToolCalls[0].Name != "Read" {
		t.Fatalf("应解析出工具调用, 得到 %+v", out)
	}
}

func TestOutputOptions(t *testing.T) {
	f := newOutputFilter([]string{"STOP"}, 0)
	f.maxBytes = 100
	if got := f.push("hello ST", false) + f.push("OP hidden", false) + f.push("", true); got != "hello " {
		t.Fatalf("stop 过滤失败: %q", got)
	}
	if got := toolChoiceInstruction([]byte(`{"type":"function","function":{"name":"Read"}}`)); !strings.Contains(got, "Read") {
		t.Fatalf("tool_choice 未生效: %q", got)
	}
	f = newOutputFilter(nil, 0)
	f.maxBytes = 4
	if got := f.push("123456", false); got != "1234" {
		t.Fatalf("max_tokens 过滤失败: %q", got)
	}
}

// repairToolJSON 应修复模型输出的常见毛病，修复后才解析。
func TestRepairToolJSON(t *testing.T) {
	cases := []struct {
		name, raw string
		want      string
	}{
		{"尾逗号", `[{"name":"Read","arguments":{"p":"x"},}]`, `[{"name":"Read","arguments":{"p":"x"}}]`},
		{"代码围栏", "```json\n[{\"name\":\"Read\"}]\n```", `[{"name":"Read"}]`},
		{"无围栏语言", "```\n[{\"name\":\"Read\"}]\n```", `[{"name":"Read"}]`},
		{"智能引号", "[{\"name\":\"Read\"}]", `[{"name":"Read"}]`},
	}
	for _, tc := range cases {
		if got := repairToolJSON(tc.raw); got != tc.want {
			t.Fatalf("%s 修复失败: got=%q want=%q", tc.name, got, tc.want)
		}
	}
	// 正常 JSON 不应被改动
	if got := repairToolJSON(`[{"name":"Read"}]`); got != `[{"name":"Read"}]` {
		t.Fatalf("正常 JSON 被改动: %q", got)
	}
}

// 带毛病的工具 JSON 应修复后成功解析，而不是降级。
func TestParseToolCallsWithRepairs(t *testing.T) {
	calls, err := parseToolCallsBlock(`[{"name":"Read","arguments":{"path":"a"},},{"name":"Grep","arguments":{"q":"b"},}]`)
	if err != nil {
		t.Fatalf("尾逗号应被修复: %v", err)
	}
	if len(calls) != 2 || calls[0].Name != "Read" || calls[1].Name != "Grep" {
		t.Fatalf("解析结果错误: %+v", calls)
	}

	// arguments 被整体序列化成字符串也应解开
	calls, err = parseToolCallsBlock(`[{"name":"Read","arguments":"{\"path\":\"a\"}"}]`)
	if err != nil {
		t.Fatalf("字符串化 arguments 应被解开: %v", err)
	}
	if calls[0].Arguments["path"] != "a" {
		t.Fatalf("参数解析错误: %+v", calls[0])
	}

	// 完全缺省 arguments 视为空参调用
	calls, err = parseToolCallsBlock(`[{"name":"Read"}]`)
	if err != nil || len(calls[0].Arguments) != 0 {
		t.Fatalf("缺省 arguments 应视为空参: %v %+v", err, calls)
	}
}
