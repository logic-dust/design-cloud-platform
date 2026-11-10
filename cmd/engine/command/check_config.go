package command

import (
	"cmp"
	"fmt"
	"os"

	"design-cloud-engine/cmd/engine/console"
	"design-cloud-engine/internal/platform/config"
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

	console.PrintStdout("config OK: %s\n", path)
	console.PrintStdout("  node_id=%s server=%t agent=true\n",
		cfg.Runtime.NodeID, cfg.Runtime.ServerEnabled)
	console.PrintStdout("  portal=%s:%s tls=%t\n", cfg.Portal.Host, cfg.Portal.Port, cfg.Portal.TLS)
	// data_dir 是生效后的 DataDir（配置为空时已回退 BaseDir）。部署/调试脚本据此预检目录可写性：
	// DataDir 不可写会让 engine 在初始化集群 CA 阶段退出（dlv 仍在监听 → “dlv 起来但 engine 秒退”）。
	console.PrintStdout("  data_dir=%s\n", cfg.Runtime.DataDir)
	console.PrintStdout("  cluster.port=%d servers=%d\n", cfg.Cluster.Port, len(cfg.Cluster.Servers))
	console.PrintStdout("  database=%s@%s:%s/%s\n",
		cfg.Database.User, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
	return nil
}
