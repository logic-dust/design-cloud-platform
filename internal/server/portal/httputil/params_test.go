package httputil

import (
	"testing"

	"design-cloud-engine/internal/db"
)

func TestParsePageUsesDBListLimits(t *testing.T) {
	t.Parallel()

	// 传输层的分页默认值/上限必须与仓储的列表上限一致，否则请求值会被仓储静默截断
	// 或永远取不到。生产代码不 import db（见 params.go 注释），由本测试锁定一致性。
	if DefaultPageLimit != db.DefaultListLimit || MaxPageLimit != db.MaxListLimit {
		t.Fatalf("httputil limits (%d/%d) diverge from db limits (%d/%d)",
			DefaultPageLimit, MaxPageLimit, db.DefaultListLimit, db.MaxListLimit)
	}
}
