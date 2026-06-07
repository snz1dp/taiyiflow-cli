---
name: taiyiflow-in-chrome
description: 自动化控制当前浏览器与网页交互，支持导航、点击、输入、截图、读取页面文本和控制台日志。当前实现支持两种模式：通过本地消息扩展接入当前浏览器会话，或启动一个独立隔离实例。
category: browser
metadata:
  request_types:
    - agent
---

当用户希望与网页交互、自动化浏览器任务、截取屏幕截图、读取控制台日志或执行任何基于浏览器的操作时，请调用此技能。
在尝试使用任何 c/local/taiyiflow-in-chrome__* 工具之前，务必先调用。

调用策略：
- 第一步优先使用 c/local/taiyiflow-in-chrome__tabs_context 获取当前标签页上下文，再决定是复用活动标签页还是新建标签页。
- 若需要当前用户浏览器会话，请确认浏览器模式已切到当前会话模式，并先完成 taiyiflow-cli browser install 的本地消息安装、扩展构建与浏览器加载。
- 若需要稳定、可控的独立自动化环境，请将浏览器模式设为 `isolated`；必要时可通过 `browser_executable_path` 指定浏览器可执行文件。
- 导航或新建标签页优先使用 c/local/taiyiflow-in-chrome__tabs_create 和 c/local/taiyiflow-in-chrome__navigate。
- 页面交互优先使用 c/local/taiyiflow-in-chrome__click 和 c/local/taiyiflow-in-chrome__type，读取结果优先使用 c/local/taiyiflow-in-chrome__read_text 和 c/local/taiyiflow-in-chrome__read_console。
- 截图使用 c/local/taiyiflow-in-chrome__screenshot；若服务端已配置 `image_flow_id`，结果会直接附带 `image_understanding`，应优先使用该结果理解页面内容，而不是再把截图交给 `document_unstructured`。未提供 path 时，客户端会自动把 PNG 保存到工作区内的 .snz1dp/browser/ 目录。

当前可用工具族：
- c/local/taiyiflow-in-chrome__tabs_context
- c/local/taiyiflow-in-chrome__tabs_create
- c/local/taiyiflow-in-chrome__navigate
- c/local/taiyiflow-in-chrome__click
- c/local/taiyiflow-in-chrome__type
- c/local/taiyiflow-in-chrome__screenshot
- c/local/taiyiflow-in-chrome__read_text
- c/local/taiyiflow-in-chrome__read_console

限制说明：
- 当前仓库已补齐浏览器扩展子项目、本地消息宿主安装链路、doctor 自检与当前会话模式执行链路；对最终用户，优先执行 taiyiflow-cli browser install 即可，随后加载 install 输出中的扩展 dist 目录。
- 对最终用户与发布产物，工作区中的 browser-extension/chrome 不是运行时必需项；若当前工作区没有该目录，browser install 会自动抽取 CLI 内置扩展资源到本地根目录，直接提供可加载的 dist 产物。
- 仓库中的 browser-extension/chrome 目前仍作为扩展开发、调试与手工构建入口保留。
- 浏览器工具的变更类操作会遵循浏览器权限模式与全局审批模式；当浏览器权限模式设为 `skip_all_permission_checks` 时，即使全局审批模式为 `readonly`，浏览器变更类工具也会按浏览器权限模式直接放行。
