package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func TestInspectBackendStatusTreatsAuthenticationErrorAsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/version" {
			http.NotFound(writer, request)
			return
		}
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	app := &App{}
	status := app.inspectBackendStatus(context.Background(), providerclient.NewClient(server.URL, ""), server.URL)
	if !status.Available {
		t.Fatalf("expected authentication failure to keep backend available, got %#v", status)
	}
	if !status.AuthRequired {
		t.Fatalf("expected authentication failure to mark auth required, got %#v", status)
	}
	if got := status.Reason; got != "请重新登录太乙智启服务" {
		t.Fatalf("unexpected auth-required backend reason: %q", got)
	}
}

func TestNormalizeStreamEventError(t *testing.T) {
	t.Run("user canceled", func(t *testing.T) {
		err := normalizeStreamEventError("读取 SSE 失败: context canceled")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("stream timeout", func(t *testing.T) {
		err := normalizeStreamEventError("读取 SSE 失败: context deadline exceeded (Client.Timeout or context cancellation while reading body)")
		if !errors.Is(err, errStreamReadTimeout) {
			t.Fatalf("expected stream read timeout marker, got %v", err)
		}
		if got := err.Error(); !strings.Contains(got, "读取流式响应超时，连接可能已中断") {
			t.Fatalf("unexpected timeout message: %q", got)
		}
	})

	t.Run("backend idle timeout", func(t *testing.T) {
		err := normalizeStreamEventError("读取 SSE 失败: backend idle timeout after 2m0s without SSE activity")
		if !errors.Is(err, errStreamBackendIdleTimeout) {
			t.Fatalf("expected backend idle timeout marker, got %v", err)
		}
		if got := err.Error(); !strings.Contains(got, "后端长时间未返回输出，流式请求已超时") {
			t.Fatalf("unexpected backend idle timeout message: %q", got)
		}
	})

	t.Run("stream disconnected", func(t *testing.T) {
		err := normalizeStreamEventError("读取 SSE 失败: read tcp 127.0.0.1:1->127.0.0.1:2: connection reset by peer")
		if !errors.Is(err, errStreamDisconnected) {
			t.Fatalf("expected stream disconnected marker, got %v", err)
		}
		if got := err.Error(); !strings.Contains(got, "流式连接已中断，请稍后重试") {
			t.Fatalf("unexpected disconnect message: %q", got)
		}
	})

	t.Run("backend business error", func(t *testing.T) {
		err := normalizeStreamEventError("quota exceeded")
		if got := err.Error(); got != "后端返回错误: quota exceeded" {
			t.Fatalf("unexpected backend error message: %q", got)
		}
	})
}

func TestShouldRetryStreamError(t *testing.T) {
	if shouldRetryStreamError(context.Canceled) {
		t.Fatal("expected canceled context not to retry")
	}

	if !shouldRetryStreamError(fmt.Errorf("%w: demo", errStreamDisconnected)) {
		t.Fatal("expected disconnected stream to retry")
	}

	if shouldRetryStreamError(errors.New("quota exceeded")) {
		t.Fatal("expected business error not to retry")
	}
}

func TestShouldRetryStreamRequestError(t *testing.T) {
	if !shouldRetryStreamRequestError(context.DeadlineExceeded) {
		t.Fatal("expected deadline exceeded to retry")
	}

	if !shouldRetryStreamRequestError(errors.New("read tcp: connection reset by peer")) {
		t.Fatal("expected connection reset to retry")
	}

	if shouldRetryStreamRequestError(context.Canceled) {
		t.Fatal("expected canceled context not to retry")
	}
}
