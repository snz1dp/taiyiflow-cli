const HOST_NAME = 'cn.snz1.taiyiflow_cli.browser_host';
const PROTOCOL_VERSION = 1;
const RECONNECT_DELAY_MS = 3000;
const MAX_CONSOLE_ENTRIES = 200;
const KEEPALIVE_ALARM = 'taiyiflow-native-host-keepalive';

type NativeHostMessage = {
  id?: string;
  type?: string;
  ok?: boolean;
  error?: string;
  host_name?: string;
  protocol_version?: number;
  extension_id?: string;
  extension_version?: string;
  capabilities?: string[];
  payload?: Record<string, unknown>;
  timestamp?: string;
};

type ConsoleEntry = {
  level: string;
  text: string;
  href: string;
  timestamp: string;
};

type TabsContextResult = {
  active_tab_id: string;
  tabs: Array<{
    tab_id: string;
    title: string;
    url: string;
    active: boolean;
    window_id: number;
  }>;
};

const consoleEntriesByTab = new Map<number, ConsoleEntry[]>();
let nativePort: chrome.runtime.Port | null = null;
let reconnectTimer: number | null = null;
let lastNativeHostError = '';

void bootstrap();

chrome.runtime.onInstalled.addListener(() => {
  void bootstrap();
});

chrome.runtime.onStartup.addListener(() => {
  void bootstrap();
});

chrome.action.onClicked.addListener(() => {
  void ensureNativeHostPort();
});

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === KEEPALIVE_ALARM) {
    void ensureNativeHostPort();
  }
});

chrome.runtime.onMessage.addListener((message: unknown, sender) => {
  if (!isConsoleBridgeMessage(message)) {
    return;
  }
  const tabId = sender.tab?.id;
  if (typeof tabId !== 'number') {
    return;
  }
  const entries = consoleEntriesByTab.get(tabId) ?? [];
  entries.push({
    level: message.payload.level,
    text: message.payload.text,
    href: message.payload.href,
    timestamp: message.payload.timestamp,
  });
  if (entries.length > MAX_CONSOLE_ENTRIES) {
    entries.splice(0, entries.length - MAX_CONSOLE_ENTRIES);
  }
  consoleEntriesByTab.set(tabId, entries);
});

chrome.tabs.onRemoved.addListener((tabId) => {
  consoleEntriesByTab.delete(tabId);
});

async function bootstrap(): Promise<void> {
  chrome.alarms.create(KEEPALIVE_ALARM, { periodInMinutes: 1 });
  await ensureNativeHostPort();
}

async function ensureNativeHostPort(): Promise<void> {
  if (nativePort) {
    return;
  }
  try {
    const port = chrome.runtime.connectNative(HOST_NAME);
    nativePort = port;
    lastNativeHostError = '';
    port.onMessage.addListener((message) => {
      void handleNativeHostMessage(message as NativeHostMessage);
    });
    port.onDisconnect.addListener(() => {
      const disconnectError = chrome.runtime.lastError?.message?.trim() ?? '';
      nativePort = null;
      if (disconnectError !== '') {
        logNativeHostError(disconnectError);
      }
      scheduleReconnect();
    });
    port.postMessage({
      id: nextMessageID(),
      type: 'hello',
      extension_id: chrome.runtime.id,
      extension_version: chrome.runtime.getManifest().version,
      capabilities: [
        'tabs_context',
        'tabs_create',
        'navigate',
        'click',
        'type',
        'screenshot',
        'read_text',
        'read_console',
      ],
      protocol_version: PROTOCOL_VERSION,
    } satisfies NativeHostMessage);
  } catch (error) {
    scheduleReconnect();
    logNativeHostError(error instanceof Error ? error.message : String(error));
  }
}

function logNativeHostError(message: string): void {
  const normalized = message.trim();
  if (normalized === '' || normalized === lastNativeHostError) {
    return;
  }
  lastNativeHostError = normalized;
  if (normalized.includes('Specified native messaging host not found')) {
    console.error(
      '太乙智启浏览器消息宿主未找到。请先运行 "taiyiflow-cli browser install --build"，然后在当前浏览器通道重新加载太乙智启扩展。如果刚切换过 chrome、chromium、edge 或 brave，也请重新执行一次安装。',
      { host: HOST_NAME, lastError: normalized, extensionID: chrome.runtime.id },
    );
    return;
  }
  console.warn('太乙智启浏览器消息宿主连接失败', { host: HOST_NAME, lastError: normalized, extensionID: chrome.runtime.id });
}

function scheduleReconnect(): void {
  if (reconnectTimer !== null) {
    return;
  }
  reconnectTimer = self.setTimeout(() => {
    reconnectTimer = null;
    void ensureNativeHostPort();
  }, RECONNECT_DELAY_MS);
}

async function handleNativeHostMessage(message: NativeHostMessage): Promise<void> {
  if (message.type === 'hello_ack' || message.type === 'pong') {
    return;
  }
  if (message.type !== 'tool_request') {
    return;
  }
  const port = nativePort;
  if (!port) {
    return;
  }
  try {
    const result = await executeTool(message.payload ?? {});
    port.postMessage({
      id: message.id,
      type: 'tool_result',
      ok: true,
      payload: { result },
    } satisfies NativeHostMessage);
  } catch (error) {
    port.postMessage({
      id: message.id,
      type: 'tool_result',
      ok: false,
      error: error instanceof Error ? error.message : String(error),
    } satisfies NativeHostMessage);
  }
}

async function executeTool(payload: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tool = normalizeToolName(payload.tool);
  const args = isRecord(payload.arguments) ? payload.arguments : {};
  switch (tool) {
    case 'tabs_context':
      return await handleTabsContext();
    case 'tabs_create':
      return await handleTabsCreate(args);
    case 'navigate':
      return await handleNavigate(args);
    case 'click':
      return await handleClick(args);
    case 'type':
      return await handleType(args);
    case 'screenshot':
      return await handleScreenshot(args);
    case 'read_text':
      return await handleReadText(args);
    case 'read_console':
      return await handleReadConsole(args);
    default:
      throw new Error(`unsupported tool request: ${String(payload.tool ?? '')}`);
  }
}

function normalizeToolName(value: unknown): string {
  const tool = typeof value === 'string' ? value.trim() : '';
  if (tool.startsWith('taiyiflow-in-chrome__')) {
    return tool.slice('taiyiflow-in-chrome__'.length);
  }
  return tool;
}

async function handleTabsContext(): Promise<TabsContextResult> {
  const tabs = await chrome.tabs.query({});
  const activeTab = tabs.find((tab) => tab.active && tab.lastFocusedWindow) ?? tabs.find((tab) => tab.active) ?? tabs[0];
  if (!activeTab?.id) {
    throw new Error('no browser tab is available');
  }
  return {
    active_tab_id: String(activeTab.id),
    tabs: tabs
      .filter((tab): tab is chrome.tabs.Tab & { id: number; windowId: number } => typeof tab.id === 'number' && typeof tab.windowId === 'number')
      .map((tab) => ({
        tab_id: String(tab.id),
        title: tab.title ?? '',
        url: tab.url ?? '',
        active: Boolean(tab.active),
        window_id: tab.windowId,
      })),
  };
}

async function handleTabsCreate(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const url = readRequiredString(args, 'url');
  const active = readBoolean(args, 'active', true);
  const tab = await chrome.tabs.create({ url, active });
  if (typeof tab.id !== 'number') {
    throw new Error('created tab does not expose tab id');
  }
  return {
    tab_id: String(tab.id),
    title: tab.title ?? '',
    url: tab.url ?? url,
    active: Boolean(tab.active),
  };
}

async function handleNavigate(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tab = await resolveTargetTab(args);
  const url = readRequiredString(args, 'url');
  const updated = await chrome.tabs.update(tab.id, { url, active: true });
  await waitForTabComplete(updated.id ?? tab.id);
  return {
    tab_id: String(updated.id ?? tab.id),
    title: updated.title ?? '',
    url: updated.url ?? url,
    active: Boolean(updated.active),
  };
}

async function handleClick(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tab = await resolveTargetTab(args);
  const selector = readRequiredString(args, 'selector');
  await runTabFunction(tab.id, (receivedSelector: string) => {
    const element = document.querySelector<HTMLElement>(receivedSelector);
    if (!element) {
      return { ok: false, message: `selector not found: ${receivedSelector}` };
    }
    element.click();
    return { ok: true, message: receivedSelector };
  }, selector);
  return { tab_id: String(tab.id), selector };
}

async function handleType(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tab = await resolveTargetTab(args);
  const selector = readRequiredString(args, 'selector');
  const text = readRequiredString(args, 'text');
  await runTabFunction(tab.id, (receivedSelector: string, receivedText: string) => {
    const element = document.querySelector<HTMLInputElement | HTMLTextAreaElement>(receivedSelector);
    if (!element) {
      return { ok: false, message: `selector not found: ${receivedSelector}` };
    }
    element.focus();
    element.value = receivedText;
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
    return { ok: true, message: receivedSelector };
  }, selector, text);
  return { tab_id: String(tab.id), selector, text_length: text.length };
}

async function handleScreenshot(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tab = await resolveTargetTab(args);
  await chrome.tabs.update(tab.id, { active: true });
  await chrome.windows.update(tab.windowId, { focused: true });
  await waitForTabComplete(tab.id);
  const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
  const imageBase64 = dataUrl.replace(/^data:image\/png;base64,/, '');
  return {
    tab_id: String(tab.id),
    image_base64: imageBase64,
    format: 'png',
  };
}

async function handleReadText(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tab = await resolveTargetTab(args);
  const selector = readOptionalString(args, 'selector');
  const maxLength = readNumber(args, 'max_length', 4000);
  const result = await runTabFunction(tab.id, (receivedSelector: string | null, receivedMaxLength: number) => {
    const target = receivedSelector ? document.querySelector(receivedSelector) : document.body;
    const rawText = (target?.textContent ?? '').trim();
    return rawText.slice(0, Math.max(receivedMaxLength, 0));
  }, selector, maxLength);
  return {
    tab_id: String(tab.id),
    selector: selector ?? '',
    text: String(result),
  };
}

async function handleReadConsole(args: Record<string, unknown>): Promise<Record<string, unknown>> {
  const tab = await resolveTargetTab(args);
  const limit = readNumber(args, 'limit', 50);
  const entries = consoleEntriesByTab.get(tab.id) ?? [];
  return {
    tab_id: String(tab.id),
    entries: entries.slice(Math.max(entries.length - limit, 0)),
  };
}

async function resolveTargetTab(args: Record<string, unknown>): Promise<chrome.tabs.Tab & { id: number; windowId: number }> {
  const explicitTabID = readOptionalString(args, 'tab_id');
  if (explicitTabID) {
    const tab = await chrome.tabs.get(Number(explicitTabID));
    if (typeof tab.id !== 'number' || typeof tab.windowId !== 'number') {
      throw new Error(`tab not found: ${explicitTabID}`);
    }
    return tab as chrome.tabs.Tab & { id: number; windowId: number };
  }
  const tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  const activeTab = tabs[0];
  if (!activeTab || typeof activeTab.id !== 'number' || typeof activeTab.windowId !== 'number') {
    throw new Error('no active tab found');
  }
  return activeTab as chrome.tabs.Tab & { id: number; windowId: number };
}

async function runTabFunction<Args extends unknown[], Result>(tabId: number, func: (...args: Args) => Result, ...args: Args): Promise<Result> {
  const injections = await chrome.scripting.executeScript({
    target: { tabId },
    func,
    args,
  });
  const firstResult = injections[0]?.result;
  if (isRecord(firstResult) && firstResult.ok === false) {
    throw new Error(String(firstResult.message ?? 'script execution failed'));
  }
  return firstResult as Result;
}

async function waitForTabComplete(tabId: number): Promise<void> {
  const tab = await chrome.tabs.get(tabId);
  if (tab.status === 'complete') {
    return;
  }
  await new Promise<void>((resolve) => {
    const timeout = self.setTimeout(() => {
      chrome.tabs.onUpdated.removeListener(listener);
      resolve();
    }, 5000);
    const listener = (updatedTabId: number, changeInfo: chrome.tabs.TabChangeInfo) => {
      if (updatedTabId !== tabId) {
        return;
      }
      if (changeInfo.status !== 'complete') {
        return;
      }
      self.clearTimeout(timeout);
      chrome.tabs.onUpdated.removeListener(listener);
      resolve();
    };
    chrome.tabs.onUpdated.addListener(listener);
  });
}

function nextMessageID(): string {
  return `msg-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
}

function readRequiredString(record: Record<string, unknown>, key: string): string {
  const value = readOptionalString(record, key);
  if (!value) {
    throw new Error(`missing string argument: ${key}`);
  }
  return value;
}

function readOptionalString(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  return trimmed === '' ? null : trimmed;
}

function readBoolean(record: Record<string, unknown>, key: string, fallback: boolean): boolean {
  const value = record[key];
  return typeof value === 'boolean' ? value : fallback;
}

function readNumber(record: Record<string, unknown>, key: string, fallback: number): number {
  const value = record[key];
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return fallback;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isConsoleBridgeMessage(value: unknown): value is { type: 'taiyiflow-console-event'; payload: ConsoleEntry } {
  return isRecord(value) && value.type === 'taiyiflow-console-event' && isRecord(value.payload) && typeof value.payload.level === 'string' && typeof value.payload.text === 'string' && typeof value.payload.href === 'string' && typeof value.payload.timestamp === 'string';
}