package cmd

import (
	"fmt"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
)

const (
	aboutProductName  = "太乙智启 CLI"
	aboutOrganization = "长沙慧码至一信息科技有限公司"
	aboutSummary      = "本地优先的命令行智能体客户端，面向代码任务、会话协作与太乙智启后台流式能力。"
)

func renderAboutText() string {
	lines := []string{
		fmt.Sprintf("产品: %s", aboutProductName),
		fmt.Sprintf("组织: %s", aboutOrganization),
		fmt.Sprintf("简介: %s", aboutSummary),
		fmt.Sprintf("版本: %s", buildinfo.Version),
		fmt.Sprintf("提交: %s", buildinfo.Commit),
	}
	if strings.TrimSpace(buildinfo.Date) != "" {
		lines = append(lines, fmt.Sprintf("构建时间: %s", strings.TrimSpace(buildinfo.Date)))
	}
	return strings.Join(lines, "\n")
}

func (a *App) runAbout(args []string) error {
	fmt.Fprintln(a.stdout, renderAboutText())
	return nil
}
