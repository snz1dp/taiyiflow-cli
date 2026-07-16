package skills

import "strings"

type BundledSkillDefinition struct {
	Name           string
	RelativeDir    string
	Summary        string
	DefaultEnabled bool
	RequestTypes   []string
}

var bundledSkillDefinitions = []BundledSkillDefinition{
	{Name: "update-config", RelativeDir: "update-config", Summary: "当需要修改 taiyiflow-cli 配置时，读取当前配置，确认变更内容，给出最小变更方案，并说明影响范围。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "keybindings", RelativeDir: "keybindings", Summary: "当用户询问 CLI/TUI 的快捷键、面板切换、交互命令或操作方式时，总结当前已有能力并指出对应路径。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "verify", RelativeDir: "verify", Summary: "当完成代码修改、配置变更或部署操作后，选择最窄的验证方式确认结果正确。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "debug", RelativeDir: "debug", Summary: "当遇到报错、测试失败、命令异常或行为不符合预期时，用最短路径定位根因。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "developer", RelativeDir: "developer", Summary: "程序开发与调试实现的通用行为准则，涵盖编码规范、最小改动原则、子任务调度策略和质量要求。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "planner", RelativeDir: "planner", Summary: "把目标收敛为可执行计划，并明确依赖、风险、回退与验收。", DefaultEnabled: true, RequestTypes: []string{"plan", "agent"}},
	{Name: "pua", RelativeDir: "pua", Summary: "在多次失败、循环卡壳、想放弃或把问题推回用户时，强制执行更彻底的排查与交付动作。", DefaultEnabled: false, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "askuser", RelativeDir: "askuser", Summary: "在 CLI 执行过程中向用户补齐关键信息或确认关键动作。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "cross-platform-shell", RelativeDir: "cross-platform-shell", Summary: "在本地命令执行场景下，按当前 OS/ARCH 选择可用命令并处理 fork/exec 兼容问题。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "document-unstructured", RelativeDir: "document-unstructured", Summary: "当需要从本地音频、视频、PDF 等非图片文件中提取正文或结构化结果时，优先使用 local.document_unstructured。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "loremipsum", RelativeDir: "loremipsum", Summary: "当需要生成占位文本、示例数据或演示内容时，提供结构清晰的占位内容并标注待替换部分。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "skillify", RelativeDir: "skillify", Summary: "当一个任务流程被有效执行后，将目标、步骤和验证方式提取为可复用的技能草案。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "remember", RelativeDir: "remember", Summary: "当对话中出现用户偏好、项目约定或常见踩坑点时，判断是否值得写入长期记忆。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "simplify", RelativeDir: "simplify", Summary: "当方案看起来过于复杂、存在过度抽象或不必要配置时，检查是否可以删减或收敛。", DefaultEnabled: true, RequestTypes: []string{"plan", "agent"}},
	{Name: "batch", RelativeDir: "batch", Summary: "当需要处理多个相近任务时，按依赖和风险分批组织执行，适用于批量修改、多模块重构等场景。", DefaultEnabled: true, RequestTypes: []string{"plan", "agent"}},
	{Name: "stuck", RelativeDir: "stuck", Summary: "当实现、验证或环境准备卡住超过两轮时，停止当前思路，列出阻塞点并拆出最小可执行的下一步。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "dream", RelativeDir: "dream", Summary: "当用户提出想法或方案但还没进入实现阶段时，帮助整理思路，区分已验证事实和待验证假设。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "hunter", RelativeDir: "hunter", Summary: "当代码审查或日志分析中发现可疑线索时，继续向下追查，补充证据和影响面。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "loop", RelativeDir: "loop", Summary: "当任务需要固定节奏的重复执行时，先定义停止条件和失败回退，再设计循环步骤。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "schedule-remote-agents", RelativeDir: "schedule-remote-agents", Summary: "当需要将任务分发到远程设备执行时，说明当前远程调度能力的状态和约束。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "taiyiflow-api", RelativeDir: "taiyiflow-api", Summary: "当需要核对太乙智启平台的接口定义、参数格式或文档入口时，先说明已知来源再给出检索路径。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "taiyiflow-computer", RelativeDir: "taiyiflow-computer", Summary: "当需要与本机桌面环境交互时（截屏、窗口列表、焦点切换、键盘输入等），使用桌面操作工具族。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "taiyiflow-in-chrome", RelativeDir: "taiyiflow-in-chrome", Summary: "当需要与网页交互时（导航、点击、输入、截图、读取页面文本或控制台日志），使用浏览器自动化工具族。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "run-skill-generator", RelativeDir: "run-skill-generator", Summary: "当需要批量创建或调整技能文件时，先统一技能结构和元数据约定，再生成最小可维护的模板集合。", DefaultEnabled: true, RequestTypes: []string{"agent"}},
	{Name: "docx", RelativeDir: "docx", Summary: "当用户需要创建、读取、编辑或操作 Word 文档（.docx 文件）时使用此技能。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "pdf", RelativeDir: "pdf", Summary: "当用户需要对 PDF 文件执行任何操作时使用此技能，包括读取、合并、拆分、表单填写、OCR 等。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "pptx", RelativeDir: "pptx", Summary: "当 .pptx 文件以任何方式涉及时使用此技能——创建、读取、编辑演示文稿。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
	{Name: "xlsx", RelativeDir: "xlsx", Summary: "当电子表格文件是主要输入或输出时使用此技能，包括读取、编辑、创建 .xlsx/.csv 文件。", DefaultEnabled: true, RequestTypes: []string{"ask", "plan", "agent"}},
}

func bundledSkillDefinitionByName(name string) (BundledSkillDefinition, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	for _, definition := range bundledSkillDefinitions {
		if strings.ToLower(definition.Name) == needle {
			return definition, true
		}
	}
	return BundledSkillDefinition{}, false
}
