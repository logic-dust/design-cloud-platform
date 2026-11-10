package model

// CacheVersion 记录集群多节点间的缓存失效广播版本号
// Name 标识缓存失效主题（settings / sessions / bans / identity / node_overrides），非 schema.Domain。
type CacheVersion struct {
	Name    string `gorm:"primaryKey;type:varchar(64)"`
	Version int64  `gorm:"not null;default:0"`
}
