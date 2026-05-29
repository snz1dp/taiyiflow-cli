const PAGE_BRIDGE_SOURCE = 'taiyiflow-page-console-bridge';

window.addEventListener('message', (event) => {
  if (event.source !== window) {
    return;
  }
  const data = event.data;
  if (!isPageConsoleMessage(data)) {
    return;
  }
  void chrome.runtime.sendMessage({
    type: 'taiyiflow-console-event',
    payload: data.payload,
  });
});

void requestPageBridgeInjection();

async function requestPageBridgeInjection(): Promise<void> {
  try {
    await chrome.runtime.sendMessage({ type: 'taiyiflow-install-page-bridge' });
  } catch {
    // 页面桥注入失败时仅会缺少控制台采集，不阻断其他扩展能力。
  }
}

function isPageConsoleMessage(value: unknown): value is { source: string; payload: { level: string; text: string; href: string; timestamp: string } } {
  return typeof value === 'object' && value !== null && (value as { source?: unknown }).source === PAGE_BRIDGE_SOURCE && typeof (value as { payload?: { level?: unknown } }).payload?.level === 'string' && typeof (value as { payload?: { text?: unknown } }).payload?.text === 'string' && typeof (value as { payload?: { href?: unknown } }).payload?.href === 'string' && typeof (value as { payload?: { timestamp?: unknown } }).payload?.timestamp === 'string';
}