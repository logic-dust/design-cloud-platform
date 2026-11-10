package api

import (
	"strconv"
	"time"

	echoMW "github.com/labstack/echo/v5/middleware"
)

// perUserLimiter 是按用户 ID 的令牌桶限流，叠加在全局 IP 限流与会话限流之上，
// 用于需要更严格频率的单个接口。空闲 3 分钟的用户桶自动回收。
type perUserLimiter struct {
	store *echoMW.RateLimiterMemoryStore
}

func newPerUserLimiter(perSecond float64, burst int) *perUserLimiter {
	return &perUserLimiter{store: echoMW.NewRateLimiterMemoryStoreWithConfig(echoMW.RateLimiterMemoryStoreConfig{
		Rate: perSecond, Burst: burst, ExpiresIn: 3 * time.Minute,
	})}
}

func (l *perUserLimiter) allow(userID uint) bool {
	ok, err := l.store.Allow(strconv.FormatUint(uint64(userID), 10))
	return err == nil && ok
}
