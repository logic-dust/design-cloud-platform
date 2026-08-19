package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultConfigFile = "config.toml" // 默认配置文件路径
)

// Load 读取配置，配置按层级覆盖
func Load(cliConfigFile string) (*Config, error) {
	// --- 1. 获取默认配置
	cfg := getDefault()

	// --- 2. 确定并加载配置文件（必须存在）：命令行 > 默认路径。
	// 引导类环境变量 DCE_CONFIG_FILE 已废弃：命令行是唯一引导事实来源。
	configFile := DefaultConfigFile
	if cliConfigFile != "" {
		configFile = cliConfigFile
	}

	if err := parseConfigFile(configFile, &cfg); err != nil {
		return nil, err
	}
	slog.Info("parse config file successfully", "config_file", configFile)

	// --- 3. 应用环境变量覆盖（白名单，脱敏审计）
	overrides := applyEnvOverrides(&cfg)
	logEnvOverrides(overrides)

	// --- 4. 解析运行时配置（路径、可执行文件、节点 ID、角色、密码）
	if err := cfg.resolve(configFile); err != nil {
		// 失败时重置部分初始化的 Runtime，避免错误路径读到半初始化状态
		cfg.Runtime = RuntimeState{}
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func parseConfigFile(path string, target *Config) error {
	if err := validateConfigFilePermissions(path); err != nil {
		return fmt.Errorf("INSECURE: %w", err)
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	decoder := toml.NewDecoder(file)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		// Check if this is a StrictMissingError (unknown field)
		if strictErr, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			// For strict mode violations, show the human-readable format
			return fmt.Errorf("failed to parse %s: unknown field(s) in config file\n\n%s", path, strictErr.String())
		}
		// For other errors, go-toml provides contextualized messages with line/column info
		return fmt.Errorf("failed to parse %s:\n\n%w", path, err)
	}

	slog.Debug("config file parsed successfully", "config_file", path)
	return nil
}
