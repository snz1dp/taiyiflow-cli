package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/textsanitize"
)

// loopDecideResult 表示跨消息自动续跑判定的结果
type loopDecideResult struct {
	IsNeedRerun bool   `json:"is_need_rerun"`
	Reason      string `json:"reason"`
	NextPrompt  string `json:"next_prompt"`
}

// callLoopDecide 调用任务续跑判定流程，判断是否需要自动续跑
func callLoopDecide(ctx context.Context, client provider.Provider, loopDecideFlowID string, originalInput string, taskResult string) (*loopDecideResult, error) {
	if strings.TrimSpace(loopDecideFlowID) == "" {
		return nil, fmt.Errorf("未配置任务续跑判定流程(loop_decide_flow_id)")
	}

	taskResult = textsanitize.StripWatermarkCharacters(taskResult)

	result, err := client.RunFlow(ctx, provider.RunFlowRequest{
		FlowID:    loopDecideFlowID,
		Input:     originalInput,
		InputType: "any",
		Tweaks: map[string]any{
			"task_result": taskResult,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("调用任务续跑判定流程失败: %w", err)
	}

	text := strings.TrimSpace(textsanitize.StripWatermarkCharacters(result.Output))
	if text == "" {
		return nil, fmt.Errorf("任务续跑判定结果为空")
	}

	// 尝试解析 JSON
	var decideResult loopDecideResult
	if err := json.Unmarshal([]byte(text), &decideResult); err != nil {
		// 尝试从文本中提取 JSON 块
		if extracted := extractJSONFromText(text); extracted != "" {
			if err := json.Unmarshal([]byte(extracted), &decideResult); err != nil {
				return nil, fmt.Errorf("解析任务续跑判定结果失败: %w", err)
			}
		} else {
			return nil, fmt.Errorf("解析任务续跑判定结果失败: %w", err)
		}
	}
	decideResult.Reason = textsanitize.StripWatermarkCharacters(decideResult.Reason)
	decideResult.NextPrompt = textsanitize.StripWatermarkCharacters(decideResult.NextPrompt)

	return &decideResult, nil
}

// extractJSONFromText 从文本中提取 JSON 块
func extractJSONFromText(text string) string {
	// 查找 ```json ... ``` 块
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	// 查找 ``` ... ``` 块
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(text[start:], "```"); end >= 0 {
			extracted := strings.TrimSpace(text[start : start+end])
			if strings.HasPrefix(extracted, "{") {
				return extracted
			}
		}
	}
	// 查找 { ... } 块
	if idx := strings.Index(text, "{"); idx >= 0 {
		if end := strings.LastIndex(text, "}"); end > idx {
			return text[idx : end+1]
		}
	}
	return ""
}

// shouldAutoLoop 判断是否应该进行自动续跑
func shouldAutoLoop(
	autoLoopEnabled bool,
	loopDecideFlowID string,
	autoLoopCurrentRound int,
	autoLoopMaxRounds int,
) bool {
	if !autoLoopEnabled {
		return false
	}
	if strings.TrimSpace(loopDecideFlowID) == "" {
		return false
	}
	if autoLoopMaxRounds <= 0 {
		return false
	}
	if autoLoopCurrentRound >= autoLoopMaxRounds {
		return false
	}
	return true
}
