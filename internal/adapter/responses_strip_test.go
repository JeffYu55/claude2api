package adapter

import (
	"strings"
	"testing"
)

// 旧的整串替换 stripThinkingTags 在分片边界会出错；thinkingStripper 是流式替代。
// 用随机切分验证：任意切法下，流式结果必须与整串结果完全一致。
func TestThinkingStripperMatchesBatch(t *testing.T) {
	cases := []string{
		"",
		"plain text only",
		"before blank + " + openTag + "hidden" + closeTag + " after",
		openTag + "only thinking" + closeTag,
		"unclosed: " + openTag + "tail dropped",
		"lone " + closeTag + " survives",
		openTag + "a" + closeTag + openTag + "b" + closeTag + "end",
		openTag,
		openTag[:3],
		"prefix" + openTag[:4],
	}
	for _, whole := range cases {
		want := stripThinkingTags(whole)
		// 各种切分点逐个验证
		for i := 0; i <= len(whole); i++ {
			s := &thinkingStripper{}
			got := s.push(whole[:i]) + s.push(whole[i:]) + s.finish()
			if got != want {
				t.Fatalf("切分 %d 不一致：whole=%q got=%q want=%q", i, whole, got, want)
			}
		}
		// 整段一次喂入也要正确
		s := &thinkingStripper{}
		if got := s.push(whole) + s.finish(); got != want {
			t.Fatalf("整段不一致：whole=%q got=%q want=%q", whole, got, want)
		}
	}
}

// 标签跨分片切开时，半截标签不能泄漏给客户端。
func TestThinkingStripperSplitTagBoundary(t *testing.T) {
	// " blank 是 openTag 的前缀，必须留在缓冲区
	s := &thinkingStripper{}
	first := s.push("answer " + openTag[:5])
	if strings.Contains(first, openTag[:5]) {
		t.Fatalf("半截 open 标签泄漏: %q", first)
	}
	if first != "answer " {
		t.Fatalf("open 前的可见文本丢失: %q", first)
	}
	// 第二片补全标签 + thinking 内容 + 闭合 + 可见文本
	second := s.push(openTag[5:] + "secret" + closeTag + " visible")
	if strings.Contains(second, "secret") {
		t.Fatalf("thinking 内容泄漏: %q", second)
	}
	if !strings.Contains(second, " visible") {
		t.Fatalf("闭合后的可见文本丢失: %q", second)
	}
	if tail := s.finish(); tail != "" {
		t.Fatalf("缓冲应已清空: %q", tail)
	}
}

func TestKeepTagCandidate(t *testing.T) {
	if n := keepTagCandidate("xx"+openTag[:4], openTag); n != 4 {
		t.Fatalf("前缀长度错误: %d", n)
	}
	if n := keepTagCandidate("plain", openTag); n != 0 {
		t.Fatalf("普通文本不应保留: %d", n)
	}
	if n := keepTagCandidate(openTag, openTag); n != 0 {
		t.Fatalf("完整标签不是候选前缀: %d", n)
	}
	if n := keepTagCandidate("", openTag); n != 0 {
		t.Fatalf("空串: %d", n)
	}
}
