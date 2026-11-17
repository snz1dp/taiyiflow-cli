package taiyiflow

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestReadHTTPErrorBoundsBody(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Status:     "500 Internal Server Error",
		Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20))),
	}
	err := readHTTPError("提交失败", response)
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("应返回 HTTPError: %v", err)
	}
	if len(httpErr.Body) > 64<<10 {
		t.Fatalf("错误体必须截断到 64KB，实际 %d 字节", len(httpErr.Body))
	}
}