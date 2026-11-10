package main

import (
	"context"
	"errors"
	"flag"
	"os"

	"design-cloud-engine/cmd/engine/command"
	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/platform/config"
)

func main() {
	// 1. 平台前置准入门禁（Fail-Fast：第一行执行，配置平台级进程上下文）
	if err := checkPlatformPreconditions(); err != nil {
		console.PrintStderr("Error: %v\n", err)
		os.Exit(1)
	}

	// 2. 解析命令行参数
	flags, commandArgs, err := command.ParseFlags(os.Args[1:], os.Stderr, config.AppName)
	if err != nil {
		// 显式 --help/-h：Usage 已打印，属正常终止（exit 0，与 --version 对称）
		if errors.Is(err, command.ErrHelpRequested) {
			os.Exit(0)
		}
		console.PrintStderr("Error: %v\n", err)
		os.Exit(1)
	}

	// 3. 执行 CLI 子命令并在完成后退出（所有一次性子命令统一由 command 包调度执行）
	if len(commandArgs) > 0 {
		if err := command.Execute(flags, commandArgs); err != nil {
			if errors.Is(err, flag.ErrHelp) || errors.Is(err, command.ErrHelpRequested) {
				os.Exit(0)
			}
			console.PrintStderr("Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 4. 启动常驻 Engine（由各平台生命周期驱动统一托管运行，包含单例排他锁与退出清理）
	if err := runWithLifecycle(flags, func(ctx context.Context) error {
		return runEngine(ctx, flags)
	}); err != nil {
		console.PrintStderr("Error: %v\n", err)
		os.Exit(1)
	}
}
