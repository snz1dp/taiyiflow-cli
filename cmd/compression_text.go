package cmd

import (
	"fmt"
	"strconv"
	"strings"
)

func buildCompressionProgressText(payload map[string]any) string {
	text := strings.TrimSpace(stringMapValue(payload, "text"))
	stage := strings.TrimSpace(stringMapValue(payload, "stage"))
	beforeTokens := compressionPayloadIntValue(payload, "before_tokens")
	afterTokens := compressionPayloadIntValue(payload, "after_tokens")
	contextSummary := buildCompressionContextSummary(payload)
	if beforeTokens <= 0 && afterTokens <= 0 {
		return ""
	}
	if stage == "start" || strings.Contains(text, "开始进行上下文压缩") {
		if text != "" && contextSummary != "" {
			return text + "（" + contextSummary + "）"
		}
		return text
	}
	text = fmt.Sprintf("上下文压缩完成：%s -> %s", formatCompressionTokenK(beforeTokens), formatCompressionTokenK(afterTokens))
	if contextSummary != "" {
		return text + "（" + contextSummary + "）"
	}
	return text
}

func buildCompressionContextSummary(payload map[string]any) string {
	rawContextWindow, ok := payload["context_window"]
	if !ok || rawContextWindow == nil {
		return ""
	}
	contextWindow, ok := rawContextWindow.(map[string]any)
	if !ok {
		return ""
	}
	currentTokens := compressionPayloadIntValue(contextWindow, "current_round_input_tokens")
	maxTokens := compressionPayloadIntValue(contextWindow, "max_input_tokens")
	availableTokens := compressionPayloadIntValue(contextWindow, "next_round_available_tokens")
	if availableTokens <= 0 {
		availableTokens = compressionPayloadIntValue(contextWindow, "current_round_available_tokens")
	}
	parts := make([]string, 0, 2)
	if currentTokens > 0 && maxTokens > 0 {
		parts = append(parts, fmt.Sprintf("当前上下文 %s/%s", formatCompressionTokenK(currentTokens), formatCompressionTokenK(maxTokens)))
	} else if currentTokens > 0 {
		parts = append(parts, "当前上下文 "+formatCompressionTokenK(currentTokens))
	}
	if availableTokens > 0 {
		parts = append(parts, "可用 "+formatCompressionTokenK(availableTokens))
	}
	return strings.Join(parts, "，")
}

func compressionPayloadIntValue(payload map[string]any, key string) int {
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func formatCompressionTokenK(value int) string {
	formatted := strconv.FormatFloat(float64(value)/1000, 'f', 1, 64)
	formatted = strings.TrimSuffix(formatted, ".0")
	return formatted + "K"
}
