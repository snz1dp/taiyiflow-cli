## Plan: SSE 拆分与 HTTP 超时

在 taiyiflow-cli 内同时完成两项收敛：一是消费后端新增的 split_assistant_message SSE 标记，让 TUI 消息列表与普通命令行流式输出都能识别“下一段内容属于新的一条 AI 消息”；二是把标准 HTTP 请求的 60 秒超时从硬编码提升为全局配置项，并接入配置文件、命令行 config 子命令与 /config 表单。后端 taiyiflow 仓库本次不改，流式请求继续使用独立的 stream idle timeout 机制。

**Steps**
1. 阶段一，扩展配置模型与 provider 客户端。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go，新增 http_timeout_seconds 常量、Config 字段、默认值与归一化函数；同时修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go，在 NewClient 中移除 60 秒硬编码常量来源，新增 SetHTTPTimeoutSeconds 或等价 setter，仅作用于 httpClient，不影响 streamHTTPClient。该步骤阻塞后续配置接入与验证。
2. 阶段二，接入全局配置入口。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 的 buildProviderClient，把 config.HTTPTimeoutSeconds 注入 provider client；修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go 的 assignConfigValue 与 resetConfigValue，支持 config set/unset http_timeout_seconds，并沿用现有整数校验风格。此步骤依赖步骤 1。
3. 阶段三，补齐 /config 表单。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 中 ConfigFormValues、configField 常量顺序、configFormFieldSpecs、configFormFieldValue、configFormValuesFromInputs、isConfigTextField，以及提交校验链路，把“HTTP 请求超时（秒）”作为新的文本字段插入到现有超时相关字段附近。此步骤依赖步骤 1，可与步骤 2 并行，但提交前要和 cmd/config.go 的键名、默认值、校验范围保持一致。
4. 阶段四，接入 SSE 拆分语义到 TUI。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的 buildTUIAssistantChunkEvent，从 payload 读取 split_assistant_message 并映射到 tui.Event 新字段；修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 Event 结构与 model.applyEvent，在收到该标记时把下一段 assistant chunk 强制落成新 entry，而不是继续拼接到上一条消息。此步骤可独立于超时配置实现。
5. 阶段五，接入 SSE 拆分语义到普通命令行输出。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 的 streamTask 和 cliStreamRenderState，记录“下一段 assistant 输出需要分隔”的状态；当收到 split_assistant_message 标记后，在下一段可见 assistant chunk 输出前补一个空行，避免把两条 AI 消息直接粘连。需要处理好首段输出 trim、show-thinking 过滤以及纯标记消息无 chunk 的情况。此步骤可与步骤 4 并行设计，但最终验证建议一起做。
6. 阶段六，补测试并回归。更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go，覆盖默认 HTTP 超时、setter 行为以及 streamHTTPClient 不受影响；更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config_test.go，覆盖新默认值、归一化与非法配置回退；更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go，覆盖 buildTUIAssistantChunkEvent 透传 split_assistant_message 以及 streamTask 的普通终端分隔行为；更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go，覆盖 split_assistant_message 强制分条与 /config 表单字段映射。该步骤依赖前述实现完成。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 复用 Default、NormalizeCommandTimeoutSeconds、NormalizeStreamIdleTimeoutSeconds 的模式，新增 HTTP 超时字段与归一化逻辑。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go — 复用 command_timeout_seconds、stream_idle_timeout_seconds 的解析与 reset 模式，新增 http_timeout_seconds。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — buildProviderClient 是超时配置注入点；streamTask 与 cliStreamRenderState 是普通终端流式输出的消息分隔控制点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — NewClient 当前硬编码 60 秒；SetStreamIdleTimeoutSeconds 的实现方式可作为新 setter 模板。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — buildTUIAssistantChunkEvent 是后端 payload 到 TUI 事件的协议适配层，适合吸收 split_assistant_message。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — Event、model.applyEvent、ConfigFormValues 与 configFormFieldSpecs 等共同决定 TUI 消息拼接和 /config 表单行为。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go — 已有 NewClient 与 SetStreamIdleTimeoutSeconds 测试，可直接扩展。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go — 已有 buildTUIAssistantChunkEvent 与 streamTask 流式输出测试，是新增 SSE 拆分断言的主测试面。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — 已有 splitNextAssistant 与 assistant chunk 拼接测试，可在相邻测试组补 split_assistant_message 语义。

**Verification**
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli 运行 go test ./internal/provider/taiyiflow ./internal/config ./internal/tui ./cmd，优先观察新增 HTTP 超时与 SSE 拆分相关测试。
2. 运行 go build -o taiyiflow-cli .，确认配置字段扩展后没有编译层面的索引漂移或接口不匹配。
3. 手工验证 TUI：启动 taiyiflow-cli，进入 /config，确认 HTTP 请求超时字段可显示、修改、提交、重开后回显正确。
4. 手工验证流式行为：连接已支持 split_assistant_message 的后端，确认 TUI 中会新增 assistant 条目，普通终端输出会在新消息开始前补空行，而不会打断 show-thinking 的过滤。
5. 回归流式连接：确认 stream_idle_timeout_seconds 仍只作用于 SSE 空闲检测，长连接不会因为新的 http_timeout_seconds 默认值而在 60 秒被 http.Client 主动切断。

**Decisions**
- split_assistant_message 的展示范围已确认包含 TUI 与普通命令行流式输出，不只限于 TUI。
- http_timeout_seconds 作为全局标准 HTTP 请求超时配置，默认值保持 60 秒，以维持当前行为兼容。
- streamHTTPClient 继续保持无总超时，由 stream_idle_timeout_seconds 单独控制，避免破坏 SSE 长连接。
- 本次范围仅限 taiyiflow-cli；taiyiflow 服务端已提供新 SSE 标记，不纳入本次实施。

**Further Considerations**
1. 建议给 http_timeout_seconds 增加与 command_timeout_seconds 相同的上限约束，而不是允许无限大值；这样能保持配置语义一致，也便于 /config 表单文案说明。
2. 普通终端输出的“新消息分隔”建议采用单个空行而不是前缀标签，原因是当前 CLI 输出设计默认追求最小噪声，与现有纯文本流式体验更一致。
