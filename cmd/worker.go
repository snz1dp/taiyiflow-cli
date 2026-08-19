package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

func (a *App) runWorker(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("worker", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	maxConcurrency := 0
	recentLimit := 0
	reconnectSeconds := 0
	pingSeconds := 0
	flagSet.IntVar(&maxConcurrency, "max-concurrency", 0, "协同驻留进程允许同时执行的子任务数；默认 4")
	flagSet.IntVar(&recentLimit, "recent-limit", 0, "连接就绪后补抓的最近任务条数；默认 20")
	flagSet.IntVar(&reconnectSeconds, "reconnect-seconds", 0, "长连接断开后的重连间隔秒数；默认 3")
	flagSet.IntVar(&pingSeconds, "ping-seconds", 0, "在线状态保活心跳秒数；默认 45")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if len(flagSet.Args()) > 0 {
		return fmt.Errorf("协同驻留命令不接受额外参数: %s", strings.Join(flagSet.Args(), " "))
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	config, _, err = a.ensureClientIdentity(config)
	if err != nil {
		return err
	}
	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return err
	}
	callbackProvider := a.providerWithCallbackAuthRefresh(providerClient, &config)

	runtimeClient := a.runtime
	if runtimeClient == nil {
		runtimeClient = clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	}
	// 注入子任务能力字段：worker 驻留进程没有会话级 override，使用全局配置值。
	// 确保 worker 执行的子任务至少继承配置中的深度思考和联网搜索设定。
	runtimeClient.SetSubtaskCapabilities(
		nil, // worker 无技能包上下文
		nil,
		resolveReasoningEnabled(config, ""),
		resolveInternetSearchEnabled(""),
		boolPtr(config.CrossSessionMemoryEnabled),
		nil,
		nil,
	)
	return runtimeClient.RunCollaborationWorker(ctx, callbackProvider, &config, clientruntime.CollaborationWorkerOptions{
		RecentTaskLimit: recentLimit,
		MaxConcurrency:  maxConcurrency,
		ReconnectDelay:  time.Duration(reconnectSeconds) * time.Second,
		PingInterval:    time.Duration(pingSeconds) * time.Second,
		Logf: func(format string, values ...any) {
			if a == nil || a.stderr == nil {
				return
			}
			_, _ = fmt.Fprintf(a.stderr, format+"\n", values...)
		},
	})
}