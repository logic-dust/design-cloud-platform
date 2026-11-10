package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// IsUniqueConstraintError 报告错误是否为唯一键冲突错误。
// 优先匹配 GORM TranslateError 转化后的 gorm.ErrDuplicatedKey，
// 并对 PostgreSQL SQLSTATE 23505 驱动错误进行结构化解包断言兜底。
func IsUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return true
	}
	return false
}
