package controlplane

import (
	"sync"
	"time"
)

// ClusterEventDefaultTTL 是集群事件的默认存活时长：发送端生成 expires_at 时使用，
// 接收端在发送端未填 expires_at 时以此作为去重记录的兜底过期时间。
const ClusterEventDefaultTTL = 30 * time.Second

const eventDedupMaxEntries = 4096

// eventDedup 是集群事件的短期去重集合（内存 TTL + 容量上界 FIFO）。
// 要解决的是双路径重复送达：变更节点直发所有 peer（路径 1）+ Leader 收到后再扇出
// （路径 2），同一 event_id 会经两条路径到达同一节点。进程重启后集合丢失可接受，
// 因为事件本身有 TTL，重启窗口内的重复投递随过期自然收敛。
type eventDedup struct {
	mu     sync.Mutex
	expiry map[string]time.Time
	order  []string // FIFO，用于容量淘汰
}

func newEventDedup() *eventDedup {
	return &eventDedup{
		expiry: make(map[string]time.Time),
	}
}

// seenOrAdd 报告 event_id 是否已见过（未过期）；未见过则按 expiresAt 记录。
// 已过期的既有记录视为未见过并覆盖。
func (d *eventDedup) seenOrAdd(eventID string, expiresAt time.Time) bool {
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	if exp, ok := d.expiry[eventID]; ok && now.Before(exp) {
		return true
	}

	// 惰性清理：插入新记录时顺带淘汰已过期项，避免无界增长
	if len(d.order) >= eventDedupMaxEntries {
		d.evictLocked(now)
	}

	if _, ok := d.expiry[eventID]; !ok {
		d.order = append(d.order, eventID)
	}
	d.expiry[eventID] = expiresAt
	return false
}

// evictLocked 先清过期项；仍超容量则按 FIFO 淘汰最旧记录。
func (d *eventDedup) evictLocked(now time.Time) {
	kept := d.order[:0]
	for _, id := range d.order {
		if exp, ok := d.expiry[id]; ok && now.Before(exp) {
			kept = append(kept, id)
		} else {
			delete(d.expiry, id)
		}
	}
	d.order = kept

	for len(d.order) >= eventDedupMaxEntries {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.expiry, oldest)
	}
}
