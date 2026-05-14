package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/cmd"
)

func main() {
	application, err := cmd.NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := application.Run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "请求已中断")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
		os.Exit(1)
	}
}
