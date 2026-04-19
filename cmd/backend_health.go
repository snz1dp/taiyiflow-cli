package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

type backendStatus struct {
	Available bool
	Reason    string
}

func (a *App) inspectBackendStatus(ctx context.Context, client *providerclient.Client, backendBaseURL string) backendStatus {
	trimmedBaseURL := strings.TrimSpace(backendBaseURL)
	if trimmedBaseURL == "" {
		return backendStatus{Reason: "未配置后端服务地址"}
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := client.GetVersionInfo(checkCtx); err != nil {
		return backendStatus{Reason: explainBackendFailure(err)}
	}

	return backendStatus{Available: true}
}

func (a *App) ensureBackendAvailable(ctx context.Context, client *providerclient.Client, backendBaseURL string) error {
	status := a.inspectBackendStatus(ctx, client, backendBaseURL)
	if !status.Available {
		return fmt.Errorf("%s", status.Reason)
	}

	return nil
}

func explainBackendFailure(err error) string {
	if err == nil {
		return "当前无法连接服务端"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接服务端超时"
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "连接服务端超时"
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "connection refused"):
		return "后端服务未启动或当前地址不可访问"
	case strings.Contains(message, "no such host"), strings.Contains(message, "server misbehaving"), strings.Contains(message, "lookup "):
		return "后端服务地址无法解析"
	case strings.Contains(message, "401"), strings.Contains(message, "unauthorized"):
		return "服务端鉴权失败，请检查访问凭证"
	case strings.Contains(message, "403"), strings.Contains(message, "forbidden"):
		return "当前账号无权访问服务端"
	case strings.Contains(message, "404"):
		return "后端服务地址不正确"
	case strings.Contains(message, "502"), strings.Contains(message, "503"), strings.Contains(message, "504"), strings.Contains(message, "500"):
		return "后端服务暂时不可用"
	default:
		return "当前无法连接服务端"
	}
}