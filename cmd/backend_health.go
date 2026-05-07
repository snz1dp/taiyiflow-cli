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
	Available    bool
	AuthRequired bool
	Reason       string
}

var errStreamBackendIdleTimeout = errors.New("stream backend idle timeout")
var errStreamReadTimeout = errors.New("stream read timeout")
var errStreamDisconnected = errors.New("stream disconnected")

func (a *App) inspectBackendStatus(ctx context.Context, client *providerclient.Client, backendBaseURL string) backendStatus {
	trimmedBaseURL := strings.TrimSpace(backendBaseURL)
	if trimmedBaseURL == "" {
		return backendStatus{Reason: "未配置后端服务地址"}
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := client.GetVersionInfo(checkCtx); err != nil {
		if isAuthenticationError(err) {
			return backendStatus{Available: true, AuthRequired: true, Reason: explainBackendFailure(err)}
		}
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
		return "无法连接至太乙智启服务"
	case strings.Contains(message, "no such host"), strings.Contains(message, "server misbehaving"), strings.Contains(message, "lookup "):
		return "太乙智启服务地址无法解析"
	case strings.Contains(message, "401"), strings.Contains(message, "unauthorized"):
		return "请重新登录太乙智启服务"
	case strings.Contains(message, "403"), strings.Contains(message, "forbidden"):
		return "当前账号无权访问太乙智启服务"
	case strings.Contains(message, "404"):
		return "太乙智启服务地址不正确"
	case strings.Contains(message, "502"), strings.Contains(message, "503"), strings.Contains(message, "504"), strings.Contains(message, "500"):
		return "太乙智启服务暂时不可用"
	default:
		return "当前无法连接太乙智启服务"
	}
}

func normalizeStreamEventError(raw any) error {
	message := strings.TrimSpace(fmt.Sprintf("%v", raw))
	if message == "" || message == "<nil>" {
		return fmt.Errorf("服务端请求失败，请稍后重试")
	}

	lowerMessage := strings.ToLower(message)
	if strings.Contains(lowerMessage, "context canceled") || strings.Contains(lowerMessage, "context cancelled") {
		return context.Canceled
	}

	if strings.Contains(lowerMessage, "backend idle timeout") {
		return fmt.Errorf("%w: 后端长时间未返回输出，流式请求已超时", errStreamBackendIdleTimeout)
	}

	if strings.Contains(lowerMessage, "context deadline exceeded") || strings.Contains(lowerMessage, "client.timeout") {
		return fmt.Errorf("%w: 读取流式响应超时，连接可能已中断", errStreamReadTimeout)
	}

	if strings.Contains(lowerMessage, "读取 sse 失败") {
		switch {
		case strings.Contains(lowerMessage, "eof"):
			return fmt.Errorf("%w: 流式响应已提前结束，连接可能已中断", errStreamDisconnected)
		case strings.Contains(lowerMessage, "broken pipe"), strings.Contains(lowerMessage, "connection reset by peer"), strings.Contains(lowerMessage, "use of closed network connection"):
			return fmt.Errorf("%w: 流式连接已中断，请稍后重试", errStreamDisconnected)
		default:
			return fmt.Errorf("流式响应异常: %s", message)
		}
	}

	return fmt.Errorf("后端返回错误: %s", message)
}

func shouldRetryStreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	return errors.Is(err, errStreamBackendIdleTimeout) ||
		errors.Is(err, errStreamReadTimeout) ||
		errors.Is(err, errStreamDisconnected)
}

func shouldRetryStreamRequestError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	lowerMessage := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lowerMessage, "context deadline exceeded") ||
		strings.Contains(lowerMessage, "client.timeout") ||
		strings.Contains(lowerMessage, "timeout") ||
		strings.Contains(lowerMessage, "connection reset by peer") ||
		strings.Contains(lowerMessage, "broken pipe") ||
		strings.Contains(lowerMessage, "eof") ||
		strings.Contains(lowerMessage, "use of closed network connection")
}
