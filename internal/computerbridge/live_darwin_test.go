//go:build darwin && cgo

package computerbridge

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/browserbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	livePointerTestEnv      = "TAIYIFLOW_COMPUTER_LIVE_TEST"
	livePointerBrowserEnv   = "TAIYIFLOW_COMPUTER_LIVE_BROWSER"
	liveBrowserWindowX      = 120
	liveBrowserWindowY      = 120
	liveBrowserWindowWidth  = 1280
	liveBrowserWindowHeight = 920
)

type liveRect struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type livePoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type liveDragState struct {
	Start     *livePoint `json:"start"`
	End       *livePoint `json:"end"`
	MoveCount int        `json:"moveCount"`
	Last      *livePoint `json:"last"`
}

type livePageMetrics struct {
	ScreenX     float64  `json:"screenX"`
	ScreenY     float64  `json:"screenY"`
	OuterWidth  float64  `json:"outerWidth"`
	OuterHeight float64  `json:"outerHeight"`
	InnerWidth  float64  `json:"innerWidth"`
	InnerHeight float64  `json:"innerHeight"`
	Target      liveRect `json:"target"`
	Handle      liveRect `json:"handle"`
	Scrollbox   liveRect `json:"scrollbox"`
}

type livePageState struct {
	ClickCount         int           `json:"clickCount"`
	DblClickCount      int           `json:"dblClickCount"`
	LastClickDetail    int           `json:"lastClickDetail"`
	ContextMenuCount   int           `json:"contextMenuCount"`
	Drag               liveDragState `json:"drag"`
	WheelEventCount    int           `json:"wheelEventCount"`
	LastWheelDeltaX    float64       `json:"lastWheelDeltaX"`
	LastWheelDeltaY    float64       `json:"lastWheelDeltaY"`
	LastWheelDirection string        `json:"lastWheelDirection"`
	ScrollTop          int           `json:"scrollTop"`
}

func TestDarwinLivePointerVerification(t *testing.T) {
	if os.Getenv(livePointerTestEnv) != "1" {
		t.Skipf("set %s=1 to run macOS live pointer verification", livePointerTestEnv)
	}
	if reason := darwinPointerInputError(darwinAccessibilityError()); reason != "" {
		t.Fatalf("desktop pointer input unavailable: %s", reason)
	}
	browserConfig := internalconfig.BrowserConfig{
		Mode:    internalconfig.BrowserModeIsolated,
		Channel: livePointerBrowserChannel(),
	}
	executablePath, err := browserbridge.ResolveExecutablePath(browserConfig)
	if err != nil {
		t.Fatalf("resolve browser executable: %v", err)
	}
	originalX, originalY, err := darwinCurrentMousePosition()
	if err != nil {
		t.Fatalf("read original mouse position: %v", err)
	}
	t.Cleanup(func() {
		_ = darwinPostMouseMove(originalX, originalY)
	})

	browserCtx, closeBrowser := openLiveBrowser(t, executablePath)
	defer closeBrowser()
	pageURL := liveVerificationPageURL(t)
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitVisible(`#target`, chromedp.ByQuery),
		chromedp.WaitVisible(`#drag-handle`, chromedp.ByQuery),
		chromedp.WaitVisible(`#scrollbox`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open live verification page: %v", err)
	}
	metrics, err := readLiveMetrics(browserCtx)
	if err != nil {
		t.Fatalf("read page metrics: %v", err)
	}
	if metrics.Target.Width < 200 || metrics.Scrollbox.Height < 100 {
		t.Fatalf("unexpected live page geometry: %#v", metrics)
	}
	t.Logf("live page metrics: %+v", metrics)

	manager := NewManager()
	actionCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	doubleClickX, doubleClickY := liveGlobalPoint(metrics, metrics.Target, 0.52, 0.38)
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolMouseMove, map[string]any{"x": doubleClickX, "y": doubleClickY}); err != nil {
		t.Fatalf("move before double click: %v", err)
	}
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolMouseClick, map[string]any{"x": doubleClickX, "y": doubleClickY, "button": "left", "click_count": 2}); err != nil {
		t.Fatalf("double click failed: %v", err)
	}
	state, err := waitForLiveState(browserCtx, func(current livePageState) bool {
		return current.DblClickCount >= 1 && current.LastClickDetail >= 2
	})
	if err != nil {
		t.Fatalf("observe double click: %v", err)
	}
	t.Logf("double click observed: %+v", state)

	rightClickX, rightClickY := liveGlobalPoint(metrics, metrics.Target, 0.78, 0.38)
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolMouseClick, map[string]any{"x": rightClickX, "y": rightClickY, "button": "right"}); err != nil {
		t.Fatalf("right click failed: %v", err)
	}
	state, err = waitForLiveState(browserCtx, func(current livePageState) bool {
		return current.ContextMenuCount >= 1
	})
	if err != nil {
		t.Fatalf("observe right click: %v", err)
	}
	t.Logf("right click observed: %+v", state)

	dragStartX, dragStartY := liveGlobalPoint(metrics, metrics.Handle, 0.5, 0.5)
	dragEndX, dragEndY := liveGlobalPoint(metrics, metrics.Target, 0.82, 0.92)
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolMouseDrag, map[string]any{
		"from_x":      dragStartX,
		"from_y":      dragStartY,
		"to_x":        dragEndX,
		"to_y":        dragEndY,
		"duration_ms": 280,
	}); err != nil {
		t.Fatalf("mouse drag failed: %v", err)
	}
	state, err = waitForLiveState(browserCtx, func(current livePageState) bool {
		return current.Drag.Start != nil && current.Drag.End != nil && current.Drag.MoveCount > 0
	})
	if err != nil {
		t.Fatalf("observe drag: %v", err)
	}
	if state.Drag.End == nil || state.Drag.Start == nil {
		t.Fatalf("drag did not capture start/end: %+v", state)
	}
	dragDeltaX := math.Abs(state.Drag.End.X - state.Drag.Start.X)
	dragDeltaY := math.Abs(state.Drag.End.Y - state.Drag.Start.Y)
	if dragDeltaX < 80 || dragDeltaY < 40 {
		t.Fatalf("drag delta too small: dx=%.2f dy=%.2f start=%+v end=%+v", dragDeltaX, dragDeltaY, state.Drag.Start, state.Drag.End)
	}
	t.Logf("drag observed: %+v", state.Drag)

	scrollX, scrollY := liveGlobalPoint(metrics, metrics.Scrollbox, 0.5, 0.5)
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolMouseMove, map[string]any{"x": scrollX, "y": scrollY}); err != nil {
		t.Fatalf("move before scroll: %v", err)
	}
	beforeScroll, err := readLiveState(browserCtx)
	if err != nil {
		t.Fatalf("read state before scroll: %v", err)
	}
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolScroll, map[string]any{"delta_x": 0, "delta_y": 6}); err != nil {
		t.Fatalf("positive scroll failed: %v", err)
	}
	afterPositive, err := waitForLiveState(browserCtx, func(current livePageState) bool {
		return current.WheelEventCount >= beforeScroll.WheelEventCount+1 && current.ScrollTop > beforeScroll.ScrollTop
	})
	if err != nil {
		t.Fatalf("observe positive scroll: %v", err)
	}
	if afterPositive.ScrollTop <= beforeScroll.ScrollTop {
		t.Fatalf("expected positive delta_y to scroll down; before=%d after=%d state=%+v", beforeScroll.ScrollTop, afterPositive.ScrollTop, afterPositive)
	}
	if _, err := manager.Execute(actionCtx, internalconfig.ComputerConfig{}, toolScroll, map[string]any{"delta_x": 0, "delta_y": -6}); err != nil {
		t.Fatalf("negative scroll failed: %v", err)
	}
	afterNegative, err := waitForLiveState(browserCtx, func(current livePageState) bool {
		return current.WheelEventCount >= afterPositive.WheelEventCount+1 && current.ScrollTop < afterPositive.ScrollTop
	})
	if err != nil {
		t.Fatalf("observe negative scroll: %v", err)
	}
	if afterNegative.ScrollTop >= afterPositive.ScrollTop {
		t.Fatalf("expected negative delta_y to scroll up; before=%d after=%d state=%+v", afterPositive.ScrollTop, afterNegative.ScrollTop, afterNegative)
	}
	t.Logf("wheel observed: +dy => scrollTop %d -> %d, -dy => %d, last direction=%s", beforeScroll.ScrollTop, afterPositive.ScrollTop, afterNegative.ScrollTop, afterNegative.LastWheelDirection)
}

func livePointerBrowserChannel() string {
	channel := strings.ToLower(strings.TrimSpace(os.Getenv(livePointerBrowserEnv)))
	switch channel {
	case internalconfig.BrowserChannelEdge:
		return internalconfig.BrowserChannelEdge
	case internalconfig.BrowserChannelBrave:
		return internalconfig.BrowserChannelBrave
	case internalconfig.BrowserChannelChromium:
		return internalconfig.BrowserChannelChromium
	default:
		return internalconfig.BrowserChannelChrome
	}
}

func openLiveBrowser(t *testing.T, executablePath string) (context.Context, func()) {
	t.Helper()
	profileDir := filepath.Join(t.TempDir(), "browser-profile")
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options,
		chromedp.ExecPath(executablePath),
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", false),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("window-position", fmt.Sprintf("%d,%d", liveBrowserWindowX, liveBrowserWindowY)),
		chromedp.Flag("window-size", fmt.Sprintf("%d,%d", liveBrowserWindowWidth, liveBrowserWindowHeight)),
	)
	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(context.Background(), options...)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	sessionCtx, cancel := context.WithTimeout(browserCtx, 2*time.Minute)
	if err := chromedp.Run(sessionCtx, chromedp.Navigate("about:blank")); err != nil {
		cancel()
		browserCancel()
		allocatorCancel()
		t.Fatalf("start isolated browser: %v", err)
	}
	return sessionCtx, func() {
		cancel()
		browserCancel()
		allocatorCancel()
	}
}

func liveVerificationPageURL(t *testing.T) string {
	t.Helper()
	pagePath := filepath.Join(t.TempDir(), "pointer-live.html")
	if err := os.WriteFile(pagePath, []byte(liveVerificationHTML), 0o600); err != nil {
		t.Fatalf("write live verification page: %v", err)
	}
	return (&url.URL{Scheme: "file", Path: pagePath}).String()
}

func readLiveMetrics(ctx context.Context) (livePageMetrics, error) {
	var metrics livePageMetrics
	err := chromedp.Run(ctx, chromedp.Evaluate(`(() => ({
		screenX: window.screenX,
		screenY: window.screenY,
		outerWidth: window.outerWidth,
		outerHeight: window.outerHeight,
		innerWidth: window.innerWidth,
		innerHeight: window.innerHeight,
		target: (() => { const r = document.getElementById("target").getBoundingClientRect(); return {left: r.left, top: r.top, width: r.width, height: r.height}; })(),
		handle: (() => { const r = document.getElementById("drag-handle").getBoundingClientRect(); return {left: r.left, top: r.top, width: r.width, height: r.height}; })(),
		scrollbox: (() => { const r = document.getElementById("scrollbox").getBoundingClientRect(); return {left: r.left, top: r.top, width: r.width, height: r.height}; })(),
	}))()`, &metrics))
	return metrics, err
}

func readLiveState(ctx context.Context) (livePageState, error) {
	var state livePageState
	err := chromedp.Run(ctx, chromedp.Evaluate(`(() => JSON.parse(JSON.stringify(window.__taiyiState)))()`, &state))
	return state, err
}

func waitForLiveState(ctx context.Context, predicate func(livePageState) bool) (livePageState, error) {
	deadline := time.Now().Add(8 * time.Second)
	for {
		state, err := readLiveState(ctx)
		if err != nil {
			return livePageState{}, err
		}
		if predicate(state) {
			return state, nil
		}
		if time.Now().After(deadline) {
			return state, fmt.Errorf("timed out waiting for live browser state")
		}
		time.Sleep(120 * time.Millisecond)
	}
}

func waitForLiveWheel(ctx context.Context, minCount int) (livePageState, error) {
	return waitForLiveState(ctx, func(state livePageState) bool {
		return state.WheelEventCount >= minCount
	})
}

func liveGlobalPoint(metrics livePageMetrics, rect liveRect, horizontal float64, vertical float64) (int, int) {
	frameX := (metrics.OuterWidth - metrics.InnerWidth) / 2
	frameY := metrics.OuterHeight - metrics.InnerHeight
	x := metrics.ScreenX + frameX + rect.Left + rect.Width*horizontal
	y := metrics.ScreenY + frameY + rect.Top + rect.Height*vertical
	return int(math.Round(x)), int(math.Round(y))
}

const liveVerificationHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>taiyiflow computer live verify</title>
<style>
  html, body {
    margin: 0;
    padding: 0;
    background: #f5f1e8;
    color: #1b1b1b;
    font-family: "Helvetica Neue", sans-serif;
    overflow: hidden;
  }
  body {
    display: flex;
    justify-content: center;
  }
  #frame {
    width: 980px;
    padding: 24px;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }
  #target {
    position: relative;
    height: 280px;
    border: 4px solid #1f2937;
    border-radius: 20px;
    background: linear-gradient(135deg, #f97316, #fde68a 55%, #fff7ed);
    user-select: none;
    overflow: hidden;
  }
  #target-label {
    position: absolute;
    left: 28px;
    top: 24px;
    font-size: 28px;
    font-weight: 700;
  }
  #drag-handle {
    position: absolute;
    left: 72px;
    top: 144px;
    width: 128px;
    height: 76px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 16px;
    background: #115e59;
    color: #ecfeff;
    font-weight: 700;
    box-shadow: 0 18px 40px rgba(0, 0, 0, 0.18);
    user-select: none;
  }
  #scrollbox {
    height: 240px;
    overflow: auto;
    border: 4px solid #1f2937;
    border-radius: 20px;
    background: #fffdf7;
  }
  #scrollcontent {
    height: 1680px;
    padding: 24px;
    background: linear-gradient(180deg, #ffffff 0%, #dbeafe 40%, #bfdbfe 100%);
  }
  #status {
    min-height: 72px;
    padding: 16px 18px;
    border-radius: 16px;
    border: 2px solid #1f2937;
    background: #fff;
    white-space: pre-wrap;
    font-family: Menlo, monospace;
    font-size: 13px;
  }
</style>
</head>
<body>
  <div id="frame">
    <div id="target">
      <div id="target-label">double click / right click / drag</div>
      <div id="drag-handle">drag me</div>
    </div>
    <div id="scrollbox">
      <div id="scrollcontent">scroll verification zone</div>
    </div>
    <div id="status"></div>
  </div>
<script>
(() => {
  const state = {
    clickCount: 0,
    dblClickCount: 0,
    lastClickDetail: 0,
    contextMenuCount: 0,
    drag: { start: null, end: null, moveCount: 0, last: null },
    wheelEventCount: 0,
    lastWheelDeltaX: 0,
    lastWheelDeltaY: 0,
    lastWheelDirection: "",
    scrollTop: 0,
  };
  const target = document.getElementById("target");
  const handle = document.getElementById("drag-handle");
  const scrollbox = document.getElementById("scrollbox");
  const status = document.getElementById("status");
  const render = () => {
    status.textContent = JSON.stringify(state, null, 2);
  };
  window.__taiyiState = state;
  target.addEventListener("click", (event) => {
    state.clickCount += 1;
    state.lastClickDetail = event.detail;
    render();
  });
  target.addEventListener("dblclick", () => {
    state.dblClickCount += 1;
    render();
  });
  target.addEventListener("contextmenu", (event) => {
    event.preventDefault();
    state.contextMenuCount += 1;
    render();
  });
  handle.addEventListener("mousedown", (event) => {
    state.drag.start = { x: event.clientX, y: event.clientY };
    state.drag.end = null;
    state.drag.moveCount = 0;
    state.drag.last = null;
    event.preventDefault();
    render();
  });
  document.addEventListener("mousemove", (event) => {
    if (!state.drag.start || event.buttons === 0) {
      return;
    }
    state.drag.moveCount += 1;
    state.drag.last = { x: event.clientX, y: event.clientY };
    render();
  });
  document.addEventListener("mouseup", (event) => {
    if (!state.drag.start) {
      return;
    }
    state.drag.end = { x: event.clientX, y: event.clientY };
    render();
  });
  scrollbox.scrollTop = 420;
  state.scrollTop = scrollbox.scrollTop;
	scrollbox.addEventListener("scroll", () => {
	  state.scrollTop = scrollbox.scrollTop;
	  render();
	}, { passive: true });
  scrollbox.addEventListener("wheel", (event) => {
    state.wheelEventCount += 1;
    state.lastWheelDeltaX = event.deltaX;
    state.lastWheelDeltaY = event.deltaY;
    state.lastWheelDirection = event.deltaY > 0 ? "down" : (event.deltaY < 0 ? "up" : "flat");
    requestAnimationFrame(() => {
      state.scrollTop = scrollbox.scrollTop;
      render();
    });
  }, { passive: true });
  render();
})();
</script>
</body>
</html>`