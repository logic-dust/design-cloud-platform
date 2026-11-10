package scheduler

import (
	"strconv"
	"strings"

	"design-cloud-engine/internal/contract"
)

// ValidateJobID 校验调度器作业号：仅十进制数字且为正整数（> 0）。
// 拒绝 "0"/"00"、前后空白、符号前缀与数组下标形式（123[1] / 123_1），
// 防止作业号被调度器 CLI 解析为选项或作用于全部作业。
// strconv.ParseUint（base 10）本身拒绝空白、正负号与下划线，无需逐字节预检。
func ValidateJobID(id string) error {
	if id == "" {
		return contract.NewInvalidParamError("job id is required")
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return contract.NewInvalidParamError("job id must be a positive integer: " + id)
	}
	return nil
}

// validateArg 校验作为调度器 CLI 独立参数传递的字符串（队列名、作业名）：
// 非空且不以 '-' 开头（避免被解析为命令行选项）。
func validateArg(field, v string) error {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return contract.NewInvalidParamError(field + " is required")
	}
	if strings.HasPrefix(trimmed, "-") {
		return contract.NewInvalidParamError(field + " must not start with '-'")
	}
	return nil
}
