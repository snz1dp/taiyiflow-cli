# 浏览器扩展子项目

该目录提供太乙智启命令行的最小浏览器扩展子项目，用于承接当前浏览器会话模式的本地消息接入链路。

## 当前范围

- 扩展后台启动后会主动连接本地消息宿主，并发送握手消息。
- 页面脚本会注入页面桥，转发控制台与报错事件，供后续读取控制台日志使用。
- 扩展后台已经承接标签页上下文、导航、点击、输入、截图、读取页面文本、读取控制台日志等请求，并将结果回传给命令行本地桥接层。

## 本地构建

对最终用户，先在仓库根目录执行：

```bash
taiyiflow-cli browser install
```

默认情况下，`browser install` 会优先使用仓库内的本目录；若当前工作区不是完整仓库，它会自动回退到 CLI 内置的扩展资源，并输出实际使用的扩展目录。

这意味着：

- 对最终用户与发布产物，只要 CLI 内置资源存在，运行 `browser install` 时通常不需要仓库内的本目录，也不需要安装 Node.js。
- 对当前仓库的开发者，本目录仍是扩展源码维护、手工调试与直接执行 `npm install && npm run build` 的主入口；内置资源只是运行时兜底副本，不是替代源码维护目录。

## 仓库维护

- `browser-extension/chrome` 是扩展源码的唯一真值源。
- `browser-extension` 目录下新增了专门的 Go 资源包，默认只嵌入本目录的 `dist/` 最小运行产物，再由 `internal/browserbridge` 引用；不再需要维护额外镜像副本。
- 修改本目录源码后，不需要再执行额外同步命令；直接运行相关测试即可。
- 提交前执行 `go test ./internal/browserbridge -run 'ExtractEmbeddedBrowserExtension|NativeMessaging'`，确认内置扩展抽取链路仍然正常。

如果你在当前仓库中修改了扩展源码，需要手工重建产物，可以改用：

```bash
taiyiflow-cli browser install --build
cd browser-extension/chrome
npm install
npm run build
```

默认情况下，`browser install` 输出中给出的扩展加载目录会直接指向可用的 `dist/`。在浏览器扩展管理页中开启开发者模式后，选择“加载已解压的扩展程序”，指向该目录即可。加载后建议点击一次扩展图标，触发扩展后台与本地消息宿主建链，再执行：

```bash
taiyiflow-cli doctor
```

确认本地消息链路与本地套接字连通性均为通过后，再在 TUI 中把浏览器模式切到当前会话模式，然后调用浏览器工具。

## 固定扩展 ID

- 扩展 ID：`agijaddhpaelilloojogabbiejogibdo`
- 浏览器消息宿主：`cn.snz1.taiyiflow_cli.browser_host`

固定扩展标识由 `manifest.json` 中的公钥决定，这样命令行侧生成的本地消息宿主清单可以直接写入 `allowed_origins`，不需要手工回填扩展标识。
