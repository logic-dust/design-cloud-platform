//go:build dev

package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
)

// CustomHandler formats readable development logs.
type CustomHandler struct {
	writer    io.Writer
	addSource bool
	attrs     []slog.Attr
}

func createLogHandler(logWriter io.Writer, _ slog.Leveler, nodeID string) *slog.Logger {
	handler := &CustomHandler{
		writer:    logWriter,
		addSource: true,
	}
	return WithGlobalAttrs(slog.New(handler), nodeID)
}

// Enabled 实现slog.Handler接口
func (h *CustomHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true // 所有级别的日志都启用
}

// WithAttrs 实现slog.Handler接口
func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// 创建一个新的Handler，复制现有的属性并追加新的属性
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)

	return &CustomHandler{
		writer:    h.writer,
		addSource: h.addSource,
		attrs:     newAttrs,
	}
}

// WithGroup 实现slog.Handler接口
func (h *CustomHandler) WithGroup(string) slog.Handler {
	return h
}

// Handle 实现slog.Handler接口
func (h *CustomHandler) Handle(_ context.Context, r slog.Record) error {
	// 获取时间
	timeStr := r.Time.Format("2006-01-02 15:04:05.000")

	// 获取源码位置
	var source string

	// 仅在 Warn 及以上级别才显示源码位置，减少 Info/Debug 的噪音
	if h.addSource && r.Level >= slog.LevelWarn {
		// 从调用栈中查找源码位置，跳过内部调用
		for i := 1; i < 10; i++ { // 最多查找10层调用栈
			pc, file, line, ok := runtime.Caller(i)
			if !ok {
				break
			}

			// 获取函数名
			funcName := runtime.FuncForPC(pc).Name()

			// 精确匹配需要跳过的包路径
			if strings.Contains(file, "log/slog") ||
				strings.Contains(file, "/logger/app_logger.go") ||
				strings.Contains(funcName, "logger.") {
				continue
			}

			// 找到了调用点
			source = fmt.Sprintf("%s:%d", file, line)
			break
		}
	}

	// 构建基本日志信息
	parts := []string{
		timeStr,          // 时间
		r.Level.String(), // 日志级别
	}

	// 优先处理 component 属性
	var component string
	var otherAttrs []slog.Attr

	// 提取 attributes
	processAttr := func(a slog.Attr) {
		if a.Key == "component" {
			component = fmt.Sprintf("[%s]", a.Value)
		} else {
			otherAttrs = append(otherAttrs, a)
		}
	}

	for _, a := range h.attrs {
		processAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		processAttr(a)
		return true
	})

	// 组装顺序：时间 级别 [Component] 消息 其他属性
	if component != "" {
		parts = append(parts, component)
	}
	parts = append(parts, r.Message)

	// 追加剩余属性
	for _, a := range otherAttrs {
		// Apply MaskAttr logic to dev output as well
		replaced := MaskAttr(nil, a)
		parts = append(parts, fmt.Sprintf("%s=%v", replaced.Key, replaced.Value))
	}

	// 写入日志
	_, err := fmt.Fprintln(h.writer, strings.Join(parts, " "))

	// 如果有源码信息，写入新行
	if source != "" {
		_, err = fmt.Fprintf(h.writer, "    at %s\n", source)
	}

	return err
}
