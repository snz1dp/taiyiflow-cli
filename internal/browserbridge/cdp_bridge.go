package browserbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	defaultRemoteDebuggingURL = "http://127.0.0.1:9222"
	maxConsoleEntriesPerTab   = 200
	defaultPageReadySelector  = "html"
	defaultPageActionTimeout  = 15 * time.Second
)

type Status struct {
	Mode               string
	Channel            string
	ExecutablePath     string
	RemoteDebuggingURL string
	Available          bool
	Reachable          bool
	Message            string
}

type ConsoleEntry struct {
	Level     string `json:"level"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

type Manager struct {
	mu      sync.Mutex
	session *session
}

type session struct {
	key                string
	browserConfig      internalconfig.BrowserConfig
	browserCtx         context.Context
	close              func()
	tabs               map[target.ID]*tabSession
	activeTabID        target.ID
	consoleMu          sync.Mutex
	consoleLogs        map[target.ID][]ConsoleEntry
	executablePath     string
	remoteDebuggingURL string
	tempProfileDir     string
}

type tabSession struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != nil {
		m.session.closeLocked()
		m.session = nil
	}
	return nil
}

func (m *Manager) Diagnose(config internalconfig.BrowserConfig) Status {
	normalized := internalconfig.NormalizeBrowserConfig(config)
	status := Status{
		Mode:    normalized.Mode,
		Channel: normalized.Channel,
	}
	if !internalconfig.BrowserEnabled(normalized) {
		status.Message = "浏览器自动化已在配置中禁用"
		return status
	}
	if normalized.Mode == internalconfig.BrowserModeChromeSession {
		nativeStatus := DiagnoseNativeMessaging(normalized, "")
		status.Available = nativeStatus.ManifestInstalled || nativeStatus.LauncherInstalled || nativeStatus.ExtensionBuildFound
		status.Reachable = nativeStatus.ExtensionConnected && nativeStatus.SocketReachable
		status.Message = nativeStatus.Message
		return status
	}
	executablePath, err := ResolveExecutablePath(normalized)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	status.Available = true
	status.Reachable = true
	status.ExecutablePath = executablePath
	status.Message = "已检测到可启动的隔离浏览器实例"
	return status
}

func (m *Manager) Execute(
	ctx context.Context,
	config internalconfig.BrowserConfig,
	toolName string,
	arguments map[string]any,
) (map[string]any, error) {
	normalized := internalconfig.NormalizeBrowserConfig(config)
	if !internalconfig.BrowserEnabled(normalized) {
		return nil, fmt.Errorf("浏览器自动化已在配置中禁用")
	}
	if normalized.Mode == internalconfig.BrowserModeChromeSession {
		return ExecuteNativeMessagingTool(ctx, normalized, toolName, arguments)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionValue, err := m.ensureSessionLocked(normalized)
	if err != nil {
		return nil, err
	}
	return sessionValue.executeLocked(ctx, toolName, arguments)
}

func ResolveExecutablePath(config internalconfig.BrowserConfig) (string, error) {
	if explicit := strings.TrimSpace(config.ExecutablePath); explicit != "" {
		if info, err := os.Stat(explicit); err == nil && !info.IsDir() {
			return explicit, nil
		}
		return "", fmt.Errorf("browser.executable_path 不存在或不是文件: %s", explicit)
	}
	for _, candidate := range browserExecutableCandidates(config.Channel) {
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("未找到 %s 浏览器可执行文件，请设置 browser.executable_path", config.Channel)
}

func (m *Manager) ensureSessionLocked(config internalconfig.BrowserConfig) (*session, error) {
	keyParts := []string{config.Mode, config.Channel, strings.TrimSpace(config.ProfileDir), strconv.FormatBool(config.Headless)}
	if config.Mode == internalconfig.BrowserModeChromeSession {
		keyParts = append(keyParts, normalizeRemoteDebuggingURL(config.RemoteDebuggingURL))
	} else {
		executablePath, err := ResolveExecutablePath(config)
		if err != nil {
			return nil, err
		}
		keyParts = append(keyParts, executablePath)
	}
	key := strings.Join(keyParts, "|")
	if m.session != nil && m.session.key == key {
		return m.session, nil
	}
	if m.session != nil {
		m.session.closeLocked()
		m.session = nil
	}
	var (
		sessionValue *session
		err          error
	)
	if config.Mode == internalconfig.BrowserModeChromeSession {
		sessionValue, err = newRemoteSession(config, key)
	} else {
		sessionValue, err = newIsolatedSession(config, key)
	}
	if err != nil {
		return nil, err
	}
	m.session = sessionValue
	return sessionValue, nil
}

func newIsolatedSession(config internalconfig.BrowserConfig, key string) (*session, error) {
	executablePath, err := ResolveExecutablePath(config)
	if err != nil {
		return nil, err
	}
	profileDir := strings.TrimSpace(config.ProfileDir)
	tempProfileDir := ""
	if profileDir == "" {
		tempProfileDir, err = os.MkdirTemp("", "taiyiflow-browser-*")
		if err != nil {
			return nil, fmt.Errorf("创建隔离浏览器数据目录失败: %w", err)
		}
		profileDir = tempProfileDir
	}
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options,
		chromedp.ExecPath(executablePath),
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", config.Headless),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.Flag("mute-audio", false),
	)
	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(context.Background(), options...)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	connectCtx, cancel := context.WithTimeout(browserCtx, browserTimeout(config))
	defer cancel()
	if err := chromedp.Run(connectCtx, chromedp.Navigate("about:blank")); err != nil {
		browserCancel()
		allocatorCancel()
		if tempProfileDir != "" {
			_ = os.RemoveAll(tempProfileDir)
		}
		return nil, fmt.Errorf("启动隔离浏览器失败: %w", err)
	}
	sessionValue := &session{
		key:            key,
		browserConfig:  config,
		browserCtx:     browserCtx,
		tabs:           map[target.ID]*tabSession{},
		consoleLogs:    map[target.ID][]ConsoleEntry{},
		executablePath: executablePath,
		tempProfileDir: tempProfileDir,
	}
	sessionValue.close = func() {
		browserCancel()
		allocatorCancel()
		if tempProfileDir != "" {
			_ = os.RemoveAll(tempProfileDir)
		}
	}
	if _, err := sessionValue.syncTargetsLocked(); err != nil {
		sessionValue.closeLocked()
		return nil, err
	}
	return sessionValue, nil
}

func newRemoteSession(config internalconfig.BrowserConfig, key string) (*session, error) {
	remoteURL := normalizeRemoteDebuggingURL(config.RemoteDebuggingURL)
	wsURL, err := fetchWebSocketDebuggerURL(remoteURL, browserTimeout(config))
	if err != nil {
		return nil, fmt.Errorf("连接 Chrome 当前会话失败: %w", err)
	}
	allocatorCtx, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), wsURL)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	connectCtx, cancel := context.WithTimeout(browserCtx, browserTimeout(config))
	defer cancel()
	if err := chromedp.Run(connectCtx); err != nil {
		browserCancel()
		allocatorCancel()
		return nil, fmt.Errorf("连接 Chrome 当前会话失败: %w", err)
	}
	sessionValue := &session{
		key:                key,
		browserConfig:      config,
		browserCtx:         browserCtx,
		tabs:               map[target.ID]*tabSession{},
		consoleLogs:        map[target.ID][]ConsoleEntry{},
		remoteDebuggingURL: remoteURL,
	}
	sessionValue.close = func() {
		browserCancel()
		allocatorCancel()
	}
	if _, err := sessionValue.syncTargetsLocked(); err != nil {
		sessionValue.closeLocked()
		return nil, err
	}
	return sessionValue, nil
}

func (s *session) closeLocked() {
	for _, item := range s.tabs {
		item.cancel()
	}
	s.tabs = map[target.ID]*tabSession{}
	if s.close != nil {
		s.close()
	}
}

func (s *session) executeLocked(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error) {
	_ = ctx
	switch strings.TrimSpace(toolName) {
	case "taiyiflow-in-chrome__tabs_context":
		return s.tabsContextLocked()
	case "taiyiflow-in-chrome__tabs_create":
		return s.tabsCreateLocked(arguments)
	case "taiyiflow-in-chrome__navigate":
		return s.navigateLocked(arguments)
	case "taiyiflow-in-chrome__click":
		return s.clickLocked(arguments)
	case "taiyiflow-in-chrome__type":
		return s.typeLocked(arguments)
	case "taiyiflow-in-chrome__screenshot":
		return s.screenshotLocked(arguments)
	case "taiyiflow-in-chrome__read_text":
		return s.readTextLocked(arguments)
	case "taiyiflow-in-chrome__read_console":
		return s.readConsoleLocked(arguments)
	default:
		return nil, fmt.Errorf("不支持的浏览器工具: %s", toolName)
	}
}

func (s *session) tabsContextLocked() (map[string]any, error) {
	infos, err := s.syncTargetsLocked()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		items = append(items, map[string]any{
			"tab_id":   string(info.TargetID),
			"title":    info.Title,
			"url":      info.URL,
			"active":   info.TargetID == s.activeTabID,
			"attached": info.Attached,
		})
	}
	return map[string]any{
		"mode":          s.browserConfig.Mode,
		"channel":       s.browserConfig.Channel,
		"active_tab_id": string(s.activeTabID),
		"tabs":          items,
		"count":         len(items),
	}, nil
	}

func (s *session) tabsCreateLocked(arguments map[string]any) (map[string]any, error) {
	url := strings.TrimSpace(stringArgument(arguments, "url"))
	if url == "" {
		url = "about:blank"
	}
	callCtx, cancel := context.WithTimeout(s.browserCtx, browserTimeout(s.browserConfig))
	defer cancel()
	createdID, err := target.CreateTarget(url).Do(callCtx)
	if err != nil {
		return nil, fmt.Errorf("创建浏览器标签页失败: %w", err)
	}
	s.activeTabID = createdID
	if _, err := s.ensureTabContextLocked(createdID); err != nil {
		return nil, err
	}
	if boolArgument(arguments, true, "activate") {
		_ = target.ActivateTarget(createdID).Do(callCtx)
	}
	result, err := s.currentTabStateLocked(createdID)
	if err != nil {
		return nil, err
	}
	result["created"] = true
	return result, nil
}

func (s *session) navigateLocked(arguments map[string]any) (map[string]any, error) {
	url := strings.TrimSpace(stringArgument(arguments, "url"))
	if url == "" {
		return nil, fmt.Errorf("navigate 缺少 url")
	}
	targetID, err := s.resolveTargetIDLocked(arguments)
	if err != nil {
		return nil, err
	}
	tab, err := s.ensureTabContextLocked(targetID)
	if err != nil {
		return nil, err
	}
	actionCtx, cancel := context.WithTimeout(tab.ctx, browserTimeout(s.browserConfig))
	defer cancel()
	if err := chromedp.Run(actionCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady(defaultPageReadySelector, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("页面导航失败: %w", err)
	}
	s.activeTabID = targetID
	return s.currentTabStateLocked(targetID)
}

func (s *session) clickLocked(arguments map[string]any) (map[string]any, error) {
	selector := strings.TrimSpace(stringArgument(arguments, "selector"))
	if selector == "" {
		return nil, fmt.Errorf("click 缺少 selector")
	}
	targetID, err := s.resolveTargetIDLocked(arguments)
	if err != nil {
		return nil, err
	}
	tab, err := s.ensureTabContextLocked(targetID)
	if err != nil {
		return nil, err
	}
	actionCtx, cancel := context.WithTimeout(tab.ctx, browserTimeout(s.browserConfig))
	defer cancel()
	if err := chromedp.Run(actionCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("点击页面元素失败: %w", err)
	}
	s.activeTabID = targetID
	result, err := s.currentTabStateLocked(targetID)
	if err != nil {
		return nil, err
	}
	result["selector"] = selector
	return result, nil
}

func (s *session) typeLocked(arguments map[string]any) (map[string]any, error) {
	selector := strings.TrimSpace(stringArgument(arguments, "selector"))
	if selector == "" {
		return nil, fmt.Errorf("type 缺少 selector")
	}
	text := stringArgument(arguments, "text")
	targetID, err := s.resolveTargetIDLocked(arguments)
	if err != nil {
		return nil, err
	}
	tab, err := s.ensureTabContextLocked(targetID)
	if err != nil {
		return nil, err
	}
	actions := []chromedp.Action{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Focus(selector, chromedp.ByQuery),
	}
	if !boolArgument(arguments, false, "append") {
		actions = append(actions, chromedp.Evaluate(clearSelectorExpression(selector), nil))
	}
	actions = append(actions, chromedp.SendKeys(selector, text, chromedp.ByQuery))
	if boolArgument(arguments, false, "press_enter", "pressEnter") {
		actions = append(actions, chromedp.SendKeys(selector, kb.Enter, chromedp.ByQuery))
	}
	actionCtx, cancel := context.WithTimeout(tab.ctx, browserTimeout(s.browserConfig))
	defer cancel()
	if err := chromedp.Run(actionCtx, actions...); err != nil {
		return nil, fmt.Errorf("输入页面内容失败: %w", err)
	}
	s.activeTabID = targetID
	result, err := s.currentTabStateLocked(targetID)
	if err != nil {
		return nil, err
	}
	result["selector"] = selector
	result["text_length"] = len(text)
	return result, nil
}

func (s *session) screenshotLocked(arguments map[string]any) (map[string]any, error) {
	path := strings.TrimSpace(stringArgument(arguments, "path"))
	if path == "" {
		return nil, fmt.Errorf("screenshot 缺少 path")
	}
	targetID, err := s.resolveTargetIDLocked(arguments)
	if err != nil {
		return nil, err
	}
	tab, err := s.ensureTabContextLocked(targetID)
	if err != nil {
		return nil, err
	}
	var imageBytes []byte
	actionCtx, cancel := context.WithTimeout(tab.ctx, browserTimeout(s.browserConfig))
	defer cancel()
	if err := chromedp.Run(actionCtx, chromedp.FullScreenshot(&imageBytes, 90)); err != nil {
		return nil, fmt.Errorf("截取页面截图失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建截图目录失败: %w", err)
	}
	if err := os.WriteFile(path, imageBytes, 0o644); err != nil {
		return nil, fmt.Errorf("写入截图文件失败: %w", err)
	}
	s.activeTabID = targetID
	result, err := s.currentTabStateLocked(targetID)
	if err != nil {
		return nil, err
	}
	result["path"] = path
	result["size"] = len(imageBytes)
	result["content_type"] = "image/png"
	return result, nil
}

func (s *session) readTextLocked(arguments map[string]any) (map[string]any, error) {
	targetID, err := s.resolveTargetIDLocked(arguments)
	if err != nil {
		return nil, err
	}
	tab, err := s.ensureTabContextLocked(targetID)
	if err != nil {
		return nil, err
	}
	selector := strings.TrimSpace(stringArgument(arguments, "selector"))
	maxChars := intArgument(arguments, 8000, "max_chars", "maxChars")
	if maxChars < 1 {
		maxChars = 8000
	}
	var raw map[string]any
	actionCtx, cancel := context.WithTimeout(tab.ctx, browserTimeout(s.browserConfig))
	defer cancel()
	if err := chromedp.Run(actionCtx, chromedp.Evaluate(readTextExpression(selector, maxChars), &raw)); err != nil {
		return nil, fmt.Errorf("读取页面文本失败: %w", err)
	}
	result, err := s.currentTabStateLocked(targetID)
	if err != nil {
		return nil, err
	}
	result["selector"] = selector
	result["text"] = strings.TrimSpace(stringMapValue(raw, "text"))
	result["found"] = boolMapValue(raw, "found")
	result["truncated"] = boolMapValue(raw, "truncated")
	result["total_length"] = intMapValue(raw, "total_length")
	return result, nil
}

func (s *session) readConsoleLocked(arguments map[string]any) (map[string]any, error) {
	targetID, err := s.resolveTargetIDLocked(arguments)
	if err != nil {
		return nil, err
	}
	if _, err := s.ensureTabContextLocked(targetID); err != nil {
		return nil, err
	}
	limit := intArgument(arguments, 20, "limit")
	if limit < 1 {
		limit = 20
	}
	entries := s.consoleEntries(targetID, limit)
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, map[string]any{
			"level":      entry.Level,
			"text":       entry.Text,
			"timestamp":  entry.Timestamp,
			"tab_id":     string(targetID),
		})
	}
	result, err := s.currentTabStateLocked(targetID)
	if err != nil {
		return nil, err
	}
	result["entries"] = items
	result["count"] = len(items)
	return result, nil
}

func (s *session) resolveTargetIDLocked(arguments map[string]any) (target.ID, error) {
	requested := strings.TrimSpace(stringArgument(arguments, "tab_id", "tabId"))
	if requested != "" {
		targetID := target.ID(requested)
		infos, err := s.syncTargetsLocked()
		if err != nil {
			return "", err
		}
		for _, info := range infos {
			if info.TargetID == targetID {
				return targetID, nil
			}
		}
		return "", fmt.Errorf("未找到标签页: %s", requested)
	}
	if s.activeTabID != "" {
		infos, err := s.syncTargetsLocked()
		if err != nil {
			return "", err
		}
		for _, info := range infos {
			if info.TargetID == s.activeTabID {
				return s.activeTabID, nil
			}
		}
	}
	infos, err := s.syncTargetsLocked()
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", fmt.Errorf("当前浏览器没有可用标签页，请先调用 tabs_create")
	}
	first := infos[0].TargetID
	s.activeTabID = first
	return first, nil
}

func (s *session) ensureTabContextLocked(targetID target.ID) (*tabSession, error) {
	if existing, ok := s.tabs[targetID]; ok {
		return existing, nil
	}
	tabCtx, cancel := chromedp.NewContext(s.browserCtx, chromedp.WithTargetID(targetID))
	chromedp.ListenTarget(tabCtx, func(event any) {
		s.handleTargetEvent(targetID, event)
	})
	enableCtx, enableCancel := context.WithTimeout(tabCtx, defaultPageActionTimeout)
	defer enableCancel()
	if err := chromedp.Run(enableCtx, cdpruntime.Enable()); err != nil {
		cancel()
		return nil, fmt.Errorf("启用标签页运行时失败: %w", err)
	}
	tab := &tabSession{ctx: tabCtx, cancel: cancel}
	s.tabs[targetID] = tab
	return tab, nil
}

func (s *session) currentTabStateLocked(targetID target.ID) (map[string]any, error) {
	infos, err := s.syncTargetsLocked()
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if info.TargetID != targetID {
			continue
		}
		return map[string]any{
			"tab_id": string(info.TargetID),
			"title":  info.Title,
			"url":    info.URL,
			"active": info.TargetID == s.activeTabID,
		}, nil
		}
	return nil, fmt.Errorf("未找到标签页: %s", targetID)
}

func (s *session) syncTargetsLocked() ([]*target.Info, error) {
	callCtx, cancel := context.WithTimeout(s.browserCtx, defaultPageActionTimeout)
	defer cancel()
	infos, err := target.GetTargets().Do(callCtx)
	if err != nil {
		return nil, fmt.Errorf("读取浏览器标签页失败: %w", err)
	}
	pages := make([]*target.Info, 0, len(infos))
	seen := map[target.ID]struct{}{}
	for _, info := range infos {
		if info == nil || info.Type != "page" {
			continue
		}
		pages = append(pages, info)
		seen[info.TargetID] = struct{}{}
	}
	for id, tab := range s.tabs {
		if _, ok := seen[id]; ok {
			continue
		}
		tab.cancel()
		delete(s.tabs, id)
	}
	if len(pages) == 0 {
		s.activeTabID = ""
		return pages, nil
	}
	if s.activeTabID == "" {
		s.activeTabID = pages[0].TargetID
	}
	activePresent := false
	for _, info := range pages {
		if info.TargetID == s.activeTabID {
			activePresent = true
			break
		}
	}
	if !activePresent {
		s.activeTabID = pages[0].TargetID
	}
	sort.SliceStable(pages, func(i, j int) bool {
		if pages[i].TargetID == s.activeTabID {
			return true
		}
		if pages[j].TargetID == s.activeTabID {
			return false
		}
		if pages[i].URL == pages[j].URL {
			return pages[i].Title < pages[j].Title
		}
		return pages[i].URL < pages[j].URL
	})
	return pages, nil
}

func (s *session) handleTargetEvent(targetID target.ID, event any) {
	switch typed := event.(type) {
	case *cdpruntime.EventConsoleAPICalled:
		parts := make([]string, 0, len(typed.Args))
		for _, arg := range typed.Args {
			if text := remoteObjectText(arg); text != "" {
				parts = append(parts, text)
			}
		}
		s.appendConsoleEntry(targetID, ConsoleEntry{
			Level:     string(typed.Type),
			Text:      strings.TrimSpace(strings.Join(parts, " ")),
			Timestamp: time.Now().Format(time.RFC3339Nano),
		})
	case *cdpruntime.EventExceptionThrown:
		message := ""
		if typed.ExceptionDetails != nil {
			message = strings.TrimSpace(typed.ExceptionDetails.Text)
			if message == "" && typed.ExceptionDetails.Exception != nil {
				message = strings.TrimSpace(typed.ExceptionDetails.Exception.Description)
			}
		}
		s.appendConsoleEntry(targetID, ConsoleEntry{
			Level:     "exception",
			Text:      message,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		})
	}
}

func (s *session) appendConsoleEntry(targetID target.ID, entry ConsoleEntry) {
	s.consoleMu.Lock()
	defer s.consoleMu.Unlock()
	items := append(s.consoleLogs[targetID], entry)
	if len(items) > maxConsoleEntriesPerTab {
		items = items[len(items)-maxConsoleEntriesPerTab:]
	}
	s.consoleLogs[targetID] = items
}

func (s *session) consoleEntries(targetID target.ID, limit int) []ConsoleEntry {
	s.consoleMu.Lock()
	defer s.consoleMu.Unlock()
	items := append([]ConsoleEntry(nil), s.consoleLogs[targetID]...)
	if len(items) <= limit {
		return items
	}
	return items[len(items)-limit:]
}

func normalizeRemoteDebuggingURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultRemoteDebuggingURL
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://") {
		return trimmed
	}
	return "http://" + trimmed
}

func fetchWebSocketDebuggerURL(endpoint string, timeout time.Duration) (string, error) {
	if strings.HasPrefix(strings.ToLower(endpoint), "ws://") || strings.HasPrefix(strings.ToLower(endpoint), "wss://") {
		return endpoint, nil
	}
	client := &http.Client{Timeout: timeout}
	versionURL := strings.TrimRight(endpoint, "/") + "/json/version"
	response, err := client.Get(versionURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("%s 返回 HTTP %d", versionURL, response.StatusCode)
	}
	var payload struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("解析 %s 失败: %w", versionURL, err)
	}
	if strings.TrimSpace(payload.WebSocketDebuggerURL) == "" {
		return "", fmt.Errorf("%s 未返回 webSocketDebuggerUrl", versionURL)
	}
	return strings.TrimSpace(payload.WebSocketDebuggerURL), nil
}

func browserExecutableCandidates(channel string) []string {
	switch runtime.GOOS {
	case "darwin":
		switch channel {
		case internalconfig.BrowserChannelChromium:
			return []string{"/Applications/Chromium.app/Contents/MacOS/Chromium"}
		case internalconfig.BrowserChannelEdge:
			return []string{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}
		case internalconfig.BrowserChannelBrave:
			return []string{"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"}
		default:
			return []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
		}
	case "windows":
		programFiles := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")}
		candidates := []string{}
		appendCandidate := func(parts ...string) {
			base := strings.TrimSpace(parts[0])
			if base == "" {
				return
			}
			candidates = append(candidates, filepath.Join(parts...))
		}
		for _, base := range programFiles {
			switch channel {
			case internalconfig.BrowserChannelChromium:
				appendCandidate(base, "Chromium", "Application", "chrome.exe")
			case internalconfig.BrowserChannelEdge:
				appendCandidate(base, "Microsoft", "Edge", "Application", "msedge.exe")
			case internalconfig.BrowserChannelBrave:
				appendCandidate(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe")
			default:
				appendCandidate(base, "Google", "Chrome", "Application", "chrome.exe")
			}
		}
		return candidates
	default:
		switch channel {
		case internalconfig.BrowserChannelChromium:
			return []string{"chromium", "chromium-browser"}
		case internalconfig.BrowserChannelEdge:
			return []string{"microsoft-edge", "microsoft-edge-stable"}
		case internalconfig.BrowserChannelBrave:
			return []string{"brave-browser", "brave"}
		default:
			return []string{"google-chrome", "google-chrome-stable", "chrome"}
		}
	}
}

func browserTimeout(config internalconfig.BrowserConfig) time.Duration {
	seconds := internalconfig.NormalizeBrowserConnectTimeoutSeconds(config.ConnectTimeoutSeconds)
	if seconds < 1 {
		return defaultPageActionTimeout
	}
	return time.Duration(seconds) * time.Second
}

func browserHostIdleTimeout(config internalconfig.BrowserConfig) time.Duration {
	seconds := internalconfig.NormalizeBrowserHostIdleTimeoutSeconds(config.HostIdleTimeoutSeconds)
	if seconds <= 0 {
		seconds = internalconfig.DefaultBrowserHostIdleTimeout
	}
	return time.Duration(seconds) * time.Second
}

func clearSelectorExpression(selector string) string {
	return fmt.Sprintf(`(() => {
		const node = document.querySelector(%s);
		if (!node) {
			return false;
		}
		if ("value" in node) {
			node.value = "";
		}
		if (node.isContentEditable) {
			node.textContent = "";
		}
		return true;
	})()`, javascriptStringLiteral(selector))
}

func readTextExpression(selector string, maxChars int) string {
	return fmt.Sprintf(`(() => {
		const selector = %s;
		const limit = %d;
		const node = selector ? document.querySelector(selector) : document.body;
		if (!node) {
			return { found: false, text: "", total_length: 0, truncated: false };
		}
		const rawText = ((node.innerText || node.textContent || "") + "").trim();
		return {
			found: true,
			text: rawText.length > limit ? rawText.slice(0, limit) : rawText,
			total_length: rawText.length,
			truncated: rawText.length > limit
		};
	})()`, javascriptStringLiteral(selector), maxChars)
}

func javascriptStringLiteral(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func remoteObjectText(object *cdpruntime.RemoteObject) string {
	if object == nil {
		return ""
	}
	if description := strings.TrimSpace(object.Description); description != "" {
		return description
	}
	if len(object.Value) > 0 {
		return strings.TrimSpace(string(object.Value))
	}
	if value := strings.TrimSpace(string(object.UnserializableValue)); value != "" {
		return value
	}
	return strings.TrimSpace(string(object.Type))
}

func stringArgument(arguments map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := arguments[key]; ok {
			if trimmed := strings.TrimSpace(fmt.Sprintf("%v", value)); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func boolArgument(arguments map[string]any, defaultValue bool, keys ...string) bool {
	for _, key := range keys {
		value, ok := arguments[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			trimmed := strings.ToLower(strings.TrimSpace(typed))
			if trimmed == "true" {
				return true
			}
			if trimmed == "false" {
				return false
			}
		}
	}
	return defaultValue
}

func intArgument(arguments map[string]any, defaultValue int, keys ...string) int {
	for _, key := range keys {
		value, ok := arguments[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed
		case int64:
			return int(typed)
		case float64:
			return int(typed)
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
				return parsed
			}
		}
	}
	return defaultValue
}

func stringMapValue(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func boolMapValue(payload map[string]any, key string) bool {
	value, ok := payload[key]
	if !ok {
		return false
	}
	typed, ok := value.(bool)
	if !ok {
		return false
	}
	return typed
}

func intMapValue(payload map[string]any, key string) int {
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}