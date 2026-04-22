# 太乙智启命令行（CLI）客户端

太乙智启命令行（CLI）客户端是一个面向本地终端的智能体客户端项目，当前已完成 Go 客户端基础骨架、TaiyiFlow stream 对接、本地工具回调链路，以及 Bubble Tea 单入口 TUI 主界面。

## 特性目标

- 单入口交互，统一处理代码、文档、脚本和排障任务
- 本地优先，围绕当前工作目录、本地文件和本机命令运行
- 代码优先，同时支持开发过程中的文档整理与中文协作文本处理
- 首版默认对齐太乙智启后台 stream 接口实现

## 当前状态

- 状态：开发中
- 主语言：Go
- 首版联调基线：../taiyiflow/src/backend/base
- 默认界面：Bubble Tea TUI

## 本地技能（Skill）

- 默认从工作区 `.snz1dp/skills` 与用户级技能目录发现本地技能（Skill），并支持 `skills list`、`skills view`、`skills enable`、`skills disable` 管理。
- 已启用的本地技能会在发起 stream 请求时通过 `client_skill_packages` 一并上送，包含技能元数据、正文内容与完整 markdown 文档，便于服务端按名称匹配或直接消费技能内容。

## 构建

- VS Code 默认构建任务：build taiyiflow
- 命令行构建产物：go build -o taiyiflow-cli .
- 运行：./taiyiflow-cli

## 配置示例

命令行（CLI）支持通过启动参数临时覆盖当前任务的外部资源访问策略，也支持通过配置文件持久化默认策略。

单次任务临时覆盖：

```bash
./taiyiflow-cli task --external-resource-access-policy confirm "读取工作目录外的参考文件并整理差异"
```

持久化默认策略：

```bash
./taiyiflow-cli config set external_resource_access_policy deny
./taiyiflow-cli config show
```

常见取值：

- `confirm`：访问工作目录外资源前确认
- `deny`：拒绝访问工作目录外资源
- `allow`：允许访问工作目录外资源

兼容性说明：

- 旧启动参数 `--sandbox-escape-policy` 仍可用，但新文档统一推荐 `--external-resource-access-policy`
- 配置键 `external_resource_access_policy` 会映射到现有配置结构，兼容旧键 `sandbox_escape_policy`

## 文档

- [规格计划](specs/taiyiflow-go-client.md)
- [仓库级 Copilot 指令](.github/copilot-instructions.md)
- [项目专用智能体](.github/agents/taiyiflow-cli.agent.md)

## 近期路线

1. 搭建 Go 项目骨架
2. 实现 internal/provider/taiyiflow/ 后端适配层
3. 对接后台 stream 接口与会话机制
4. 完成单入口命令行（CLI）交互主流程
