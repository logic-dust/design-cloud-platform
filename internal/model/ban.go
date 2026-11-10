package model

import "time"

// BanSubject 标识封禁/失败计数的统计维度（决定 Key 的构成）。
type BanSubject string

const (
	// BanSubjectIP 按来源 IP 计数，Key = IP。
	BanSubjectIP BanSubject = "IP"
	// BanSubjectUserIP 按 用户@来源IP 组合计数，Key = IP:username。
	// 不提供全局 User 维度：攻击者可枚举用户名造成账号级 DoS；
	// 账号级持续锁定由 UserStatus.UserLocked 承担。
	BanSubjectUserIP BanSubject = "UserIP"
)

// BanSource 标识触发计数/封禁的来源。
type BanSource string

const (
	BanSourceLogin        BanSource = "Login"        // 登录失败
	BanSourceSessionToken BanSource = "SessionToken" // 无效会话 Token
	BanSourceManual       BanSource = "Manual"       // 管理员手动封禁
)

// Ban 是一条失败计数/封禁记录，主键 Key 为 Subject 维度的标识（IP 或 IP:username）。
// BannedUntil 为 nil 表示仅累计失败计数、尚未封禁。
type Ban struct {
	Key      string     `gorm:"primaryKey;type:varchar(128)"`
	Subject  BanSubject `gorm:"type:varchar(16)"`
	Source   BanSource  `gorm:"type:varchar(16)"`
	Count    uint
	IP       string `gorm:"type:varchar(45)"`
	Username string `gorm:"type:varchar(45)"`
	// 时间戳为业务语义（首次失败/最后失败），非 GORM 约定的"创建/修改时间"，
	// 因此由内存缓存 BanCache 显式维护并在 flush 时写入，不使用 GORM 自动特性。
	FirstFailedAt time.Time // 首次失败/封禁时间（BanTime 展示语义）
	LastFailedAt  time.Time // 最后失败时间（无封禁失败记录的清理保留期判断）
	BannedUntil   *time.Time
	// Reason 记录封禁原因；自动封禁由 Service 按 Subject 生成默认文案，手动封禁为管理员填写的理由。
	Reason string `gorm:"type:varchar(255)"`
}
