package utils

import "github.com/tiktoken-go/tokenizer/codec"

var textTokenizer = codec.NewCl100kBase()

// EstimateTokens 使用 cl100k_base 参考分词估算文本，不代表 Claude 分词或计费用量。
// 分词器出错时退回字符估算，避免一个异常输入让整个请求 goroutine panic。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	if tokens, err := textTokenizer.Count(text); err == nil {
		return tokens
	}
	return fallbackTokens(text)
}

// fallbackTokens 按 ASCII 4 字符 1 token、非 ASCII 1 字符 1 token 粗估。
func fallbackTokens(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if r < 0x80 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}

// Truncate 截断到 n 字节。
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
