# 太乙智启命令行（CLI）客户端

太乙智启命令行（CLI）客户端是一个面向本地终端的智能体客户端项目，目标是构建单入口、本地优先、代码任务优先的命令行（CLI）工具。
项目当前仍处于规划阶段，尚未开始首版代码实现。

## 特性目标

- 单入口交互，统一处理代码、文档、脚本和排障任务
- 本地优先，围绕当前工作目录、本地文件和本机命令运行
- 代码优先，同时支持开发过程中的文档整理与中文协作文本处理
- 首版默认对齐太乙智启后台 stream 接口实现

## 当前状态

- 状态：规划中
- 主语言方向：Go
- 首版联调基线：../taiyiflow/src/backend/base

## 文档

- [规格计划](specs/taiyiflow-go-client.md)
- [仓库级 Copilot 指令](.github/copilot-instructions.md)
- [项目专用智能体](.github/agents/taiyiflow-cli.agent.md)

## 近期路线

1. 搭建 Go 项目骨架
2. 实现 internal/provider/taiyiflow/ 后端适配层
3. 对接后台 stream 接口与会话机制
4. 完成单入口命令行（CLI）交互主流程
