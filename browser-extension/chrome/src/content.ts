const PAGE_BRIDGE_SOURCE = 'taiyiflow-page-console-bridge';

installPageBridge();

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

function installPageBridge(): void {
  const root = document.documentElement ?? document.head ?? document.body;
  if (!root) {
    window.addEventListener('DOMContentLoaded', installPageBridge, { once: true });
    return;
  }
  const script = document.createElement('script');
  script.textContent = `(${injectConsoleBridge.toString()})(${JSON.stringify(PAGE_BRIDGE_SOURCE)});`;
  root.appendChild(script);
  script.remove();
}

function injectConsoleBridge(source: string): void {
  const globalMarker = '__taiyiflowConsoleBridgeInstalled__';
  const markerHost = window as Window & { [key: string]: boolean | undefined };
  if (markerHost[globalMarker]) {
    return;
  }
  markerHost[globalMarker] = true;

  const serialize = (value: unknown): string => {
    if (typeof value === 'string') {
      return value;
    }
    if (value instanceof Error) {
      return value.stack ?? value.message;
    }
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  };

  const forward = (level: string, args: unknown[]): void => {
    window.postMessage(
      {
        source,
        payload: {
          level,
          text: args.map((item) => serialize(item)).join(' '),
          href: location.href,
          timestamp: new Date().toISOString(),
        },
      },
      '*',
    );
  };

  for (const level of ['log', 'info', 'warn', 'error', 'debug'] as const) {
    const original = console[level];
    console[level] = (...args: unknown[]) => {
      forward(level, args);
      original.apply(console, args);
    };
  }

  window.addEventListener('error', (event) => {
    forward('error', [event.message]);
  });

  window.addEventListener('unhandledrejection', (event) => {
    forward('error', [event.reason]);
  });
}

function isPageConsoleMessage(value: unknown): value is { source: string; payload: { level: string; text: string; href: string; timestamp: string } } {
  return typeof value === 'object' && value !== null && (value as { source?: unknown }).source === PAGE_BRIDGE_SOURCE && typeof (value as { payload?: { level?: unknown } }).payload?.level === 'string' && typeof (value as { payload?: { text?: unknown } }).payload?.text === 'string' && typeof (value as { payload?: { href?: unknown } }).payload?.href === 'string' && typeof (value as { payload?: { timestamp?: unknown } }).payload?.timestamp === 'string';
}