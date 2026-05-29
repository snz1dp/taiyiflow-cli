declare const chrome: {
  action: any;
  alarms: any;
  runtime: any;
  scripting: any;
  tabs: any;
  windows: any;
};

declare namespace chrome {
  namespace runtime {
    interface Port {
      onMessage: { addListener(listener: (message: unknown) => void): void };
      onDisconnect: { addListener(listener: () => void): void };
      postMessage(message: unknown): void;
    }

    interface MessageSender {
      tab?: chrome.tabs.Tab;
    }
  }

  namespace alarms {
    interface Alarm {
      name: string;
    }
  }

  namespace tabs {
    interface Tab {
      active?: boolean;
      id?: number;
      lastFocusedWindow?: boolean;
      status?: string;
      title?: string;
      url?: string;
      windowId?: number;
    }

    interface TabChangeInfo {
      status?: string;
    }
  }
}