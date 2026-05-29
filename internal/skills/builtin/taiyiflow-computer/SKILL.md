---
name: taiyiflow-computer
description: 使用本地 desktop ComputerUse 工具读取显示器、窗口和桌面截图，并在 capability 允许时执行焦点切换或键盘输入。
---

你可以通过桌面 ComputerUse 工具族 `taiyiflow-computer__*` 与本机桌面环境交互，但要先尊重 CLI 当前的 capability 与权限状态。

执行原则：

- 先调用 `taiyiflow-computer__displays_context` 或 `taiyiflow-computer__windows_context` 建立屏幕与窗口上下文，再决定是否截图或输入。
- 涉及写入型桌面动作时，先确认当前 `computer.permission_mode` 与审批模式是否允许；若 tool 返回权限错误，不要反复重试，应提醒用户去系统权限页或 `doctor` 查看状态。
- 需要保留视觉证据时，优先使用 `taiyiflow-computer__screenshot`，并在后续步骤中复用返回的 `path`。
- 当前 capability 若未放行某个工具，例如鼠标或滚轮注入，直接说明“当前平台/权限下未实现或不可用”，不要假设可以绕过。

当前 CLI 基线说明：

- desktop ComputerUse 与浏览器自动化是两套独立工具族，不要把 DOM 选择器语义混到桌面坐标或窗口语义里。
- macOS 基线已优先接入显示器上下文、桌面截图，以及受辅助功能权限约束的窗口列表、焦点切换、键盘输入。
- 鼠标移动、点击、拖拽、滚轮等高风险输入在当前基线中可能仍不可用，应先看 capability/doctor 再决定是否尝试。