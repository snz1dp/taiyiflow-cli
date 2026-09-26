package browserbridge

import (
	"context"
	"fmt"
	"testing"
	"time"
)

/**
 * waitPageInteractiveForTest 测试辅助：以注入的 evaluate 函数执行页面就绪核查。
 *
 * @param tab 目标标签页会话
 * @param timeout 最长等待时长
 * @param evaluate 模拟的 readyState 查询函数
 * @returns 最后观测到的 readyState 字符串或错误
 */
func waitPageInteractiveForTest(tab *tabSession, timeout time.Duration, evaluate func(ctx context.Context, expr string) (string, error)) (string, error) {
	return waitPageInteractiveWithEvaluator(tab, timeout, evaluate)
}

/**
 * waitPageInteractiveLocked 页面加载状态核查单元测试
 *
 * 通过注入自定义 evaluate 函数模拟不同的页面加载状态，
 * 验证核查函数的等待、重试与超时兜底行为。
 */
func TestWaitPageInteractiveLocked(t *testing.T) {
	t.Run("页面已 complete 时立即返回", func(t *testing.T) {
		tab := &tabSession{ctx: context.Background()}
		// readyState 为 complete，应立即返回
		state, err := waitPageInteractiveForTest(tab, 2*time.Second, func(ctx context.Context, expr string) (string, error) {
			return "complete", nil
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if state != "complete" {
			t.Fatalf("期望 complete, 实际 %s", state)
		}
	})

	t.Run("页面为 interactive 时立即返回", func(t *testing.T) {
		tab := &tabSession{ctx: context.Background()}
		state, err := waitPageInteractiveForTest(tab, 2*time.Second, func(ctx context.Context, expr string) (string, error) {
			return "interactive", nil
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if state != "interactive" {
			t.Fatalf("期望 interactive, 实际 %s", state)
		}
	})

	t.Run("导航中上下文销毁后重试成功", func(t *testing.T) {
		tab := &tabSession{ctx: context.Background()}
		calls := 0
		state, err := waitPageInteractiveForTest(tab, 2*time.Second, func(ctx context.Context, expr string) (string, error) {
			calls++
			if calls < 3 {
				// 模拟导航中执行上下文被销毁
				return "", fmt.Errorf("Execution context was destroyed")
			}
			return "complete", nil
		})
		if err != nil {
			t.Fatalf("重试后应成功: %v", err)
		}
		if state != "complete" {
			t.Fatalf("期望 complete, 实际 %s", state)
		}
		if calls < 3 {
			t.Fatalf("期望至少重试 3 次, 实际 %d 次", calls)
		}
	})

	t.Run("持续 loading 时超时兜底返回最后状态", func(t *testing.T) {
		tab := &tabSession{ctx: context.Background()}
		start := time.Now()
		state, err := waitPageInteractiveForTest(tab, 600*time.Millisecond, func(ctx context.Context, expr string) (string, error) {
			return "loading", nil
		})
		if err != nil {
			t.Fatalf("超时兜底不应报错: %v", err)
		}
		if state != "loading" {
			t.Fatalf("期望兜底返回 loading, 实际 %s", state)
		}
		if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
			t.Fatalf("应等待到超时, 实际耗时 %v", elapsed)
		}
	})

	t.Run("持续报错且无状态时返回错误", func(t *testing.T) {
		tab := &tabSession{ctx: context.Background()}
		_, err := waitPageInteractiveForTest(tab, 600*time.Millisecond, func(ctx context.Context, expr string) (string, error) {
			return "", fmt.Errorf("connection refused")
		})
		if err == nil {
			t.Fatalf("持续失败应返回错误")
		}
	})
}
