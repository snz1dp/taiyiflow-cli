---
layout: home

hero:
  name: 太乙智启 CLI
  text: 命令行智能体客户端
  tagline: 本地优先、代码任务优先的单入口命令行（CLI）智能体客户端，直连太乙智启后台流式能力。
  actions:
    - theme: brand
      text: 快速开始
      link: /getting-started
    - theme: alt
      text: 命令参考
      link: /commands
    - theme: alt
      text: 交互模式
      link: /tui

features:
  - title: 单入口任务执行
    details: 一条 taiyiflow-cli task 命令发起智能体任务，支持 agent / ask / plan 三种请求模式、文件上传、会话续接与检查点恢复。
  - title: 本地工具链
    details: 内置文件读写、搜索、Shell 执行、后台任务、子智能体等本地工具，命令在本地沙盒中受控执行，结果回传后台流式接口。
  - title: 交互式终端界面
    details: 不带任务参数即进入交互模式（TUI），斜杠命令切换模式、管理技能与权限、查看待办与后台任务，支持终端图片协议显示验证码。
  - title: 技能与 MCP 扩展
    details: 本地技能目录渐进式披露，服务端技能仓库搜索安装；模型上下文协议（MCP）服务器按智能体供应，本地启停与桌面端共享配置。
  - title: 安全与权限
    details: 审批模式、Shell 沙盒（macOS sandbox-exec / Linux bubblewrap）、工具级与命令前缀级权限规则、外部资源访问策略多层防护。
  - title: 协同与常驻执行
    details: worker 驻留进程接收协同任务，daemon 守护进程托管（launchd / systemd），定时任务与跨会话记忆开箱即用。
---