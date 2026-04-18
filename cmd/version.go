package cmd

import (
	"fmt"

	"github.com/taiyiflow/taiyiflow-cli/internal/buildinfo"
)

func (a *App) runVersion(args []string) error {
	fmt.Fprintf(a.stdout, "version: %s\n", buildinfo.Version)
	fmt.Fprintf(a.stdout, "commit: %s\n", buildinfo.Commit)
	if buildinfo.Date != "" {
		fmt.Fprintf(a.stdout, "date: %s\n", buildinfo.Date)
	}
	return nil
}
