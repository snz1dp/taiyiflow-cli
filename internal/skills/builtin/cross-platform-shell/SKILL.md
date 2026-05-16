---
name: cross-platform-shell
description: 在本地命令执行场景下，按当前 OS/ARCH 选择可用命令并处理 fork/exec 兼容问题。
category: tools
---

当任务需要执行本地命令时，先读取当前环境里的 OS、ARCH、工作目录和命令执行目标，再决定命令形式。

执行准则：
- 优先使用结构化的 `local.shell_exec`：把可执行文件放进 `command`，参数放进 `args`
- 只有确实需要管道、重定向、通配符或 shell 内建语法时，再使用 `rawCommand`
- 不要假设 Linux、macOS、Windows 的命令、参数或路径语义完全一致
- 涉及解释器时，确认当前平台上对应解释器真实可用

异常处理：
- 如果出现 `fork/exec ... not supported`，优先怀疑二进制与当前 OS/ARCH 不匹配，或脚本缺少可用解释器
- 遇到平台特定命令时，改用当前平台可执行程序，或通过合适的 shell / 解释器包装执行
- 如果不确定命令是否跨平台，先做更小的探测命令，再执行真实操作

目标：
- 让本地执行路径更稳定，减少因为平台差异导致的无效命令和重试
