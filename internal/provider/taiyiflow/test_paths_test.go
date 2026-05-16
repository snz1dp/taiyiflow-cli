package taiyiflow

import (
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func backendAPIPath(path string) string {
	return internalconfig.DefaultBackendAPIPrefix + normalizeTestAPIPath(path)
}

func authAPIPath(path string) string {
	return internalconfig.DefaultAuthAPIPrefix + normalizeTestAPIPath(path)
}

func normalizeTestAPIPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}
