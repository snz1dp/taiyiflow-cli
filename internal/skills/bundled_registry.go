package skills

import "strings"

type BundledSkillDefinition struct {
	Name           string
	RelativeDir    string
	Summary        string
	DefaultEnabled bool
}

var bundledSkillDefinitions = []BundledSkillDefinition{
	{Name: "update-config", RelativeDir: "update-config", Summary: "更新 CLI 配置并说明配置项影响。", DefaultEnabled: true},
	{Name: "keybindings", RelativeDir: "keybindings", Summary: "解释当前客户端快捷键与交互方式。", DefaultEnabled: true},
	{Name: "verify", RelativeDir: "verify", Summary: "围绕刚完成的任务做结果核对与验收。", DefaultEnabled: true},
	{Name: "debug", RelativeDir: "debug", Summary: "读取日志与上下文，定位当前问题。", DefaultEnabled: true},
	{Name: "developer", RelativeDir: "developer", Summary: "提供编码阶段的通用行为准则与约束。", DefaultEnabled: true},
	{Name: "askuser", RelativeDir: "askuser", Summary: "在 CLI 执行过程中向用户补齐关键信息或确认关键动作。", DefaultEnabled: true},
	{Name: "cross-platform-shell", RelativeDir: "cross-platform-shell", Summary: "在本地命令执行场景下处理 OS/ARCH 与 fork/exec 兼容问题。", DefaultEnabled: true},
	{Name: "document-unstructured", RelativeDir: "document-unstructured", Summary: "在音频、视频、PDF 等非图片结构化解析场景下优先使用 local.document_unstructured。", DefaultEnabled: true},
	{Name: "loremipsum", RelativeDir: "loremipsum", Summary: "生成占位文本或演示内容。", DefaultEnabled: true},
	{Name: "skillify", RelativeDir: "skillify", Summary: "把当前流程整理成可复用技能。", DefaultEnabled: true},
	{Name: "remember", RelativeDir: "remember", Summary: "整理长期有效的偏好、模式与经验。", DefaultEnabled: true},
	{Name: "simplify", RelativeDir: "simplify", Summary: "识别可删减的复杂度并提出收敛建议。", DefaultEnabled: true},
	{Name: "batch", RelativeDir: "batch", Summary: "规划可批量执行的任务序列。", DefaultEnabled: true},
	{Name: "stuck", RelativeDir: "stuck", Summary: "当推进受阻时，快速定位阻塞点并拆解下一步。", DefaultEnabled: true},
	{Name: "dream", RelativeDir: "dream", Summary: "整理探索性想法与后续实验方向。", DefaultEnabled: true},
	{Name: "hunter", RelativeDir: "hunter", Summary: "围绕 review 线索继续追踪风险与遗漏。", DefaultEnabled: true},
	{Name: "loop", RelativeDir: "loop", Summary: "为重复性流程设计循环执行框架。", DefaultEnabled: true},
	{Name: "schedule-remote-agents", RelativeDir: "schedule-remote-agents", Summary: "描述远程智能体调度方案与约束。", DefaultEnabled: true},
	{Name: "taiyiflow-api", RelativeDir: "taiyiflow-api", Summary: "整理太乙智启接口与文档检索工作流。", DefaultEnabled: true},
	{Name: "taiyiflow-computer", RelativeDir: "taiyiflow-computer", Summary: "通过本地桌面操作工具读取屏幕、窗口与输入能力，并根据 doctor/capability 选择可用动作。", DefaultEnabled: true},
	{Name: "taiyiflow-in-chrome", RelativeDir: "taiyiflow-in-chrome", Summary: "通过本地浏览器工具控制当前浏览器会话或隔离实例，执行导航、点击、输入、截图与页面读取。", DefaultEnabled: true},
	{Name: "run-skill-generator", RelativeDir: "run-skill-generator", Summary: "生成或更新技能模板与说明。", DefaultEnabled: true},
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
