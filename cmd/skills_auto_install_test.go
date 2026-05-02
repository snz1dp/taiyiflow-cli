package cmd

import (
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
)

func TestBuildSkillSearchKeywordsCombinesInputMissingAndSessionSummary(t *testing.T) {
	state := &agent.SessionState{
		SessionTitle:     "文档检索增强",
		FlowDisplayName:  "企业问答助手",
		ApprovedSkillNames: []string{"context-review"},
		TodoItems: []agent.TodoItem{
			{Title: "补齐知识库同步", Notes: "需要 milvus ingest pipeline"},
		},
	}
	keywords := buildSkillSearchKeywords(
		"请帮我处理 context review and repository search",
		[]string{"remote-skill", "Debug Helper"},
		state,
	)

	if len(keywords) == 0 {
		t.Fatal("expected non-empty keywords")
	}
	if keywords[0] != "remote-skill" {
		t.Fatalf("expected missing skill name to have highest priority, got %#v", keywords)
	}
	if !containsKeyword(keywords, "context") && !containsKeyword(keywords, "review") {
		t.Fatalf("expected input tokens to be included, got %#v", keywords)
	}
	if !containsKeyword(keywords, "企业问答助手") && !containsKeyword(keywords, "文档检索增强") {
		t.Fatalf("expected session summary keywords to be included, got %#v", keywords)
	}
	if len(keywords) > 8 {
		t.Fatalf("expected keyword list to be capped at 8, got %#v", keywords)
	}
}

func TestBuildSkillSearchKeywordsFiltersStopWordsAndDeduplicates(t *testing.T) {
	keywords := buildSkillSearchKeywords(
		"请 帮我 help please task this this debug debug",
		[]string{"Debug Helper", "debug helper"},
		nil,
	)

	if containsKeyword(keywords, "请") || containsKeyword(keywords, "help") || containsKeyword(keywords, "please") {
		t.Fatalf("expected stop words to be filtered, got %#v", keywords)
	}
	count := 0
	for _, item := range keywords {
		if item == "Debug Helper" || item == "debug helper" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected missing skill keyword to be deduplicated, got %#v", keywords)
	}
}

func containsKeyword(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
