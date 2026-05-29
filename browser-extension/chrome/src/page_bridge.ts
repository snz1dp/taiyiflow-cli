const PAGE_BRIDGE_SOURCE = 'taiyiflow-page-console-bridge';

installConsoleBridge(PAGE_BRIDGE_SOURCE);

function installConsoleBridge(source: string): void {
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