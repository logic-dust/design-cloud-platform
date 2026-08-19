package command

import (
	"cmp"
	"fmt"
	"os"

	"design-cloud-engine/internal/common"
	"design-cloud-engine/internal/config"
)

// runCheckConfigCommand 加载并校验配置文件（TOML 解析 → 运行时解析 → 业务校验），
// 用于部署/调试前预检：如 .vscode/remote-build-debug.sh 在启动 dlv 前校验远程 config.toml。
//
// 背景：远程 config.toml 是运行期配置，不随 rsync 同步，代码重构 config 字段后
// 容易与二进制 schema 脱节，导致"dlv 正常监听但 engine 立即退出"的迷惑现象。
// 预检通过即等价于 engine 能完成配置加载阶段。
func runCheckConfigCommand(flags *Flags, args []string) error {
	fs := newCommandFlagSet("check-config", os.Stderr)
	configPath := fs.String("config", "", "config file path to validate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 优先级：子命令 --config > 顶层 --config > 默认 config.toml
	path := cmp.Or(*configPath, flags.ConfigFile, config.DefaultConfigFile)

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("config check failed for %s: %w", path, err)
	}

	common.PrintStdout("config OK: %s\n", path)
	common.PrintStdout("  node_id=%s server=%t agent=true\n",
		cfg.Runtime.NodeID, cfg.Runtime.ServerEnabled)
	common.PrintStdout("  portal=%s:%s tls=%t\n", cfg.Portal.Host, cfg.Portal.Port, cfg.Portal.TLS)
	common.PrintStdout("  cluster.port=%d servers=%d\n", cfg.Cluster.Port, len(cfg.Cluster.Servers))
	common.PrintStdout("  database=%s@%s:%s/%s\n",
		cfg.Database.User, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
	return nil
}
