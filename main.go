package main

import (
	"context"
	"fmt"
	"os"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/cmd"
)

func main() {
	application, err := cmd.NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}

	if err := application.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
		os.Exit(1)
	}
}
