package alert

import (
	"sync"
	"time"
)

// DefaultHysteresisMargin 告警恢复判定的默认迟滞回差裕度（5%）。
// 达到阈值触发告警后，指标必须回落（或回升）超过此裕度才确认恢复，避免指标在阈值边缘微幅波动时引发告警风暴。
const DefaultHysteresisMargin = 0.05

// alertState 维护单个告警规则的状态。
// 每个实例有独立的 mu，允许不同 key 并发评估，减少锁竞争。
type alertState struct {
	mu          sync.Mutex
	active      bool      // 当前是否处于告警态
	lastFire    time.Time // 上次触发（或重推）的时间
	triggerFree bool      // true = 最近一次由容量空间不足（MinFreeSpace）触发
}

// Rule 描述一条告警规则的评估参数。
type Rule struct {
	Threshold        float64       // 告警阈值，0=跳过
	Cooldown         time.Duration // 持续告警的重推间隔
	Level            string        // "warning" | "error"
	Below            bool          // true = metric < Threshold 时触发（用于 minFree 等下限指标）
	Exceeding        *bool         // 显式超标标识（nil = 由 Eval 计算，非 nil = 调用方显式指定）
	HysteresisMargin float64       // 迟滞裕度比率（0 = 使用 DefaultHysteresisMargin）
	IsFreeSpace      bool          // true = 指标量纲为容量空间 (KiB)，用于持续告警与恢复时对齐量纲
	Unit             string        // 指标单位符号（如 "%"、"KiB"），用于格式化告警消息
}
