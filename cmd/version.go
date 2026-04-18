package cmd

import (
	"fmt"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
)

func (a *App) runVersion(args []string) error {
	fmt.Fprintf(a.stdout, "version: %s\n", buildinfo.Version)
	fmt.Fprintf(a.stdout, "commit: %s\n", buildinfo.Commit)
	if buildinfo.Date != "" {
		fmt.Fprintf(a.stdout, "date: %s\n", buildinfo.Date)
	}
	return nil
}
