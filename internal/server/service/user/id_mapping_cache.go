package user

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/db"
	"design-cloud-engine/internal/server/cache"
	userprovider "design-cloud-engine/internal/server/service/user/provider"

	"design-cloud-engine/internal/contract"
	"golang.org/x/sync/errgroup"
)

const (
	idMappingCacheTopic = db.CacheVersionTopicIdentity
	// MaxIDMappingsPerRequest 是去重后 uids+gids 的合计上限。
	// 身份源侧无压力：单次 LDAP Search 实测可携 10 万项 OR。
	// 该值实际受两个量约束（均为线性，故只是成本而非硬墙）：
	//   - Provider 侧分块数 = ceil(本值 / idLookupChunk)，决定健康态耗时
	//     与“退化态能否跑完”的边界（超预算即 fail-fast，见 ldap.go）；
	//   - 响应体积（无 gzip）：约 25 字节/命中，满额时百余 KB。
	MaxIDMappingsPerRequest = 5000
	// MaxRawIDMappingsPerArray 是去重前单个数组的长度上限（防止"海量重复项"在去重前消耗解析资源）。
	// 必须 ≥ MaxIDMappingsPerRequest，否则"装满唯一值"的请求无法通过。
	MaxRawIDMappingsPerArray = 8192
	// IDMappingDegradedCacheTTL 是失败回退（降级）正结果的短 TTL（秒）。
	// 代码常量、不进 Settings：降级结果缺少权威确认，必须短命，
	// 以便身份源恢复后尽快被正常结果覆盖。
	IDMappingDegradedCacheTTL = 300
	// idMappingCacheMaxEntries 是每个命名空间（user/group）的条数上限（单节点口径，含负缓存）。
	idMappingCacheMaxEntries = 100_000
)

// IDMapping is a cached UID/GID resolution result.
// Found=false means the ID source confirmed the ID does not exist.
// 条目类别（正常正/负/降级正）只体现在写入时选用的 TTL 长短，不额外存储来源元数据。
type IDMapping struct {
	Name  string
	Found bool
}

// idMappingNamespace 区分用户/组两个独立命名空间。
type idMappingNamespace int

const (
	namespaceUser idMappingNamespace = iota
	namespaceGroup
)

func (n idMappingNamespace) String() string {
	if n == namespaceGroup {
		return "group"
	}
	return "user"
}

// idMappingTTLs 是本次解析实际生效的 TTL 集合。
type idMappingTTLs struct {
	positive time.Duration // 正常正结果
	negative time.Duration // 确认不存在（已按 ≤ positive clamp）
	degraded time.Duration // 失败回退正结果（代码常量）
}

type idMappingCache struct {
	userCache  *cache.TTLCache[uint32, IDMapping]
	groupCache *cache.TTLCache[uint32, IDMapping]

	// generation 已交由 TTLCache 维护（Invalidate/Clear 递增，与数据变更同锁）；
	// 回填用 ttlCache.Generation() + SetIfGeneration 校验，无需本层再记一份。
	ttlMu    sync.Mutex
	haveTTLs bool
	lastPos  int
	lastNeg  int
}

func newIDMappingCache() *idMappingCache {
	return &idMappingCache{
		userCache:  cache.NewTTLCache[uint32, IDMapping](time.Duration(schema.IDMappingFoundCacheTTLDefault) * time.Second).SetMaxEntries(idMappingCacheMaxEntries).EnableJanitor(),
		groupCache: cache.NewTTLCache[uint32, IDMapping](time.Duration(schema.IDMappingFoundCacheTTLDefault) * time.Second).SetMaxEntries(idMappingCacheMaxEntries).EnableJanitor(),
	}
}

func (c *idMappingCache) cacheOf(ns idMappingNamespace) *cache.TTLCache[uint32, IDMapping] {
	if ns == namespaceGroup {
		return c.groupCache
	}
	return c.userCache
}

// Clear invalidates all entries: wipe both namespaces.
// 所有清空路径（管理员 refresh / 远端广播 / TTL 配置变更）必须走本方法；
// 代次由各自的 TTLCache.Clear 递增（与清空同锁），回填侧的守卫因此覆盖一切失效来源。
func (c *idMappingCache) Clear() {
	c.userCache.Clear()
	c.groupCache.Clear()
}

// noteTTLs records the TTL pair read from settings and reports whether it changed
// since the previous observation (first observation never counts as change).
// 只应在**成功读到配置**时调用：它是变更检测的基线，写错会让调用方误清缓存。
func (c *idMappingCache) noteTTLs(pos, neg int) bool {
	c.ttlMu.Lock()
	defer c.ttlMu.Unlock()
	if c.haveTTLs && c.lastPos == pos && c.lastNeg == neg {
		return false
	}
	changed := c.haveTTLs
	c.haveTTLs, c.lastPos, c.lastNeg = true, pos, neg
	return changed
}

// lastKnownTTLs returns the TTL pair of the last successful settings read.
// 设置读取失败时用它兜底，而不是退到代码默认值：默认 86400s 远比典型配置大，
// 既会被当作回填 TTL 放大缓存寿命，又会污染变更检测基线。从未成功读取过时 ok=false。
func (c *idMappingCache) lastKnownTTLs() (pos, neg int, ok bool) {
	c.ttlMu.Lock()
	defer c.ttlMu.Unlock()
	return c.lastPos, c.lastNeg, c.haveTTLs
}

func (c *idMappingCache) InvalidateUser(uid uint32) {
	c.userCache.Invalidate(uid)
}

func (c *idMappingCache) InvalidateGroup(gid uint32) {
	c.groupCache.Invalidate(gid)
}

// IDMappingsRequest is the body for POST /api/users/id-mappings.
type IDMappingsRequest struct {
	UIDs []uint32 `json:"uids"`
	GIDs []uint32 `json:"gids"`
}

// IDMappingsResult is the successful response payload for ID → name resolution.
type IDMappingsResult struct {
	Users    map[uint32]string `json:"users"`
	Groups   map[uint32]string `json:"groups"`
	NotFound IDMappingsMissing `json:"notFound"`
	// TTL/NegativeTTL 仅反映服务端当前配置（秒），供调试参考，不构成客户端一致性承诺。
	TTL         int `json:"ttl"`
	NegativeTTL int `json:"negativeTtl"`
}

// IDMappingsMissing lists IDs that were looked up and confirmed absent.
// 顺序为对应请求数组中首次出现的顺序；空列表序列化为 []（非 null）。
type IDMappingsMissing struct {
	UIDs []uint32 `json:"uids"`
	GIDs []uint32 `json:"gids"`
}

// ResolveIDMappings maps numeric UIDs/GIDs to names via ID mapping cache + sources.
func (s *Service) ResolveIDMappings(ctx context.Context, req IDMappingsRequest) (*IDMappingsResult, error) {
	if len(req.UIDs) > MaxRawIDMappingsPerArray || len(req.GIDs) > MaxRawIDMappingsPerArray {
		return nil, contract.NewInvalidParamError(fmt.Sprintf(
			"too many ids: uids and gids may each contain at most %d entries before deduplication", MaxRawIDMappingsPerArray))
	}
	uids := uniqueIDs(req.UIDs)
	gids := uniqueIDs(req.GIDs)
	if len(uids)+len(gids) > MaxIDMappingsPerRequest {
		return nil, contract.NewInvalidParamError(fmt.Sprintf(
			"at most %d unique ids allowed per request", MaxIDMappingsPerRequest))
	}
	// 空输入短路：不需要解析，就无所谓平台是否具备身份源。放在平台检查之前，避免
	// 同一份空请求在有无身份源的平台上分别得到 503 / 200（分页客户端末页可能为空）。
	if len(uids)+len(gids) == 0 {
		ttls := s.idMappingCacheTTLs(ctx)
		return &IDMappingsResult{
			Users:       map[uint32]string{},
			Groups:      map[uint32]string{},
			NotFound:    IDMappingsMissing{UIDs: []uint32{}, GIDs: []uint32{}},
			TTL:         int(ttls.positive / time.Second),
			NegativeTTL: int(ttls.negative / time.Second),
		}, nil
	}
	// 平台静态不可用：既无本机身份源（无 passwd/group 动作）也未启用 LDAP。
	if s.localProv == nil && s.ldapProv == nil {
		return nil, contract.NewUnavailableError("ID-mapping resolution is not available on this platform")
	}

	ttls := s.idMappingCacheTTLs(ctx)

	var (
		users  map[uint32]IDMapping
		groups map[uint32]IDMapping
	)
	userLocal, userLDAP := idMappingFetchers(s.localProv, s.ldapProv, true)
	groupLocal, groupLDAP := idMappingFetchers(s.localProv, s.ldapProv, false)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		users, err = s.resolveNamespace(gctx, namespaceUser, uids, ttls, userLocal, userLDAP)
		return err
	})
	g.Go(func() error {
		var err error
		groups, err = s.resolveNamespace(gctx, namespaceGroup, gids, ttls, groupLocal, groupLDAP)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	result := &IDMappingsResult{
		Users:  make(map[uint32]string),
		Groups: make(map[uint32]string),
		NotFound: IDMappingsMissing{
			UIDs: make([]uint32, 0),
			GIDs: make([]uint32, 0),
		},
		TTL:         int(ttls.positive / time.Second),
		NegativeTTL: int(ttls.negative / time.Second),
	}
	for _, uid := range uids {
		id := users[uid]
		if id.Found {
			result.Users[uid] = id.Name
		} else {
			result.NotFound.UIDs = append(result.NotFound.UIDs, uid)
		}
	}
	for _, gid := range gids {
		id := groups[gid]
		if id.Found {
			result.Groups[gid] = id.Name
		} else {
			result.NotFound.GIDs = append(result.NotFound.GIDs, gid)
		}
	}
	return result, nil
}

// idMappingFetchers returns the local (base) and LDAP (overlay) fetchers for one
// namespace; nil means the source does not exist on this deployment:
//   - local nil → platform has no passwd/group actions (静态缺失，视为"确认空集")
//   - ldap nil  → AuthMode != ldap
func idMappingFetchers(local, ldap userprovider.Provider, users bool) (
	localFetch, ldapFetch func(context.Context, []uint32) (map[uint32]string, error),
) {
	if local != nil {
		if users {
			localFetch = local.UsersByIDs
		} else {
			localFetch = local.GroupsByIDs
		}
	}
	if ldap != nil {
		if users {
			ldapFetch = ldap.UsersByIDs
		} else {
			ldapFetch = ldap.GroupsByIDs
		}
	}
	return localFetch, ldapFetch
}

// resolveNamespace resolves one namespace (users or groups) for the given IDs:
//
//  1. consume valid cache entries (positive & negative) without touching sources;
//  2. fetch misses from the available sources (local base + LDAP overlay);
//  3. merge (§3.1): LDAP overrides local, local-only names are kept;
//  4. confirmed-absent (= all active sources succeeded without the ID) → negative cache;
//  5. on source failure: stale positive → local hit → otherwise fail the side (503 upstream).
//
// All cache fills are guarded by the generation captured at query start.
func (s *Service) resolveNamespace(
	ctx context.Context,
	ns idMappingNamespace,
	ids []uint32,
	ttls idMappingTTLs,
	localFetch func(context.Context, []uint32) (map[uint32]string, error),
	ldapFetch func(context.Context, []uint32) (map[uint32]string, error),
) (map[uint32]IDMapping, error) {
	start := time.Now()
	ids = uniqueIDs(ids)
	out := make(map[uint32]IDMapping, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	identCache := s.idMappingCache
	ttlCache := identCache.cacheOf(ns)

	var (
		hits, misses int
		staleUsed    int
		degradedUsed int
		notFoundNew  int
		conflicts    int
	)
	// missList 保持请求内首次出现顺序。
	missList := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if v, ok := ttlCache.GetIfPresent(id); ok {
			out[id] = v
			hits++
			continue
		}
		missList = append(missList, id)
	}
	if len(missList) == 0 {
		s.logResolve(ns, len(ids), hits, 0, 0, 0, 0, 0, 0, start)
		return out, nil
	}
	misses = len(missList)

	// 查询开始时捕获代次：任何回填（含失败回退写回）前若代次变化则丢弃写入。
	// 代次由 TTLCache 在 Invalidate/Clear 时递增，与数据变更同锁。
	fillGen := ttlCache.Generation()

	localAvail := localFetch != nil
	ldapActive := ldapFetch != nil

	var (
		localMap map[uint32]string
		localErr error
	)
	if localAvail {
		localMap, localErr = localFetch(ctx, missList)
	}
	var (
		ldapMap map[uint32]string
		ldapErr error
	)
	if ldapActive {
		ldapMap, ldapErr = ldapFetch(ctx, missList)
	}
	localOK := localAvail && localErr == nil
	ldapOK := ldapActive && ldapErr == nil

	var sourceErrs []error
	if localAvail && localErr != nil {
		sourceErrs = append(sourceErrs, fmt.Errorf("local: %w", localErr))
	}
	if ldapActive && ldapErr != nil {
		sourceErrs = append(sourceErrs, fmt.Errorf("ldap: %w", ldapErr))
	}

	var (
		writes     []pendingWrite
		unresolved []uint32
	)
	// 确认不存在：所有"存在的源"都成功且未命中（本机源静态缺失不算未决）。
	canConfirmAbsent := func() bool {
		return (!ldapActive || ldapOK) && (!localAvail || localOK)
	}
	// stalePositive returns the last successfully resolved positive value
	// (expired allowed) for an ID during the failure path.
	// 注意：stale 负缓存绝不充当 notFound（"已确认不存在"必须由本次成功查询得出）。
	stalePositive := func(id uint32) (IDMapping, bool) {
		v, ok := ttlCache.GetStale(id)
		return v, ok && v.Found
	}

	for _, id := range missList {
		ldapName, ldapHas := ldapMap[id]
		localName, localHas := localMap[id]
		if ldapOK && ldapHas {
			// LDAP 权威：覆盖本机同名/异名。
			if localOK && localHas && localName != ldapName {
				conflicts++
				s.logger.Debug("ID mapping name conflict; LDAP name wins",
					"namespace", ns.String(), "id", id, "ldap", ldapName, "local", localName)
			}
			out[id] = IDMapping{Name: ldapName, Found: true}
			writes = append(writes, pendingWrite{id, out[id], ttls.positive})
			continue
		}
		if localOK && localHas {
			if ldapActive && !ldapOK {
				// LDAP 故障：失败回退链（stale 正 > 本机命中），一律降级短 TTL 回写。
				if stale, ok := stalePositive(id); ok {
					out[id] = stale
					staleUsed++
					writes = append(writes, pendingWrite{id, stale, ttls.degraded})
				} else {
					ident := IDMapping{Name: localName, Found: true}
					out[id] = ident
					degradedUsed++
					writes = append(writes, pendingWrite{id, ident, ttls.degraded})
				}
				continue
			}
			// LDAP 成功但无此 ID（或未启用 LDAP）：本机名即最终合并结果。
			ident := IDMapping{Name: localName, Found: true}
			out[id] = ident
			writes = append(writes, pendingWrite{id, ident, ttls.positive})
			continue
		}
		if canConfirmAbsent() {
			ident := IDMapping{Found: false}
			out[id] = ident
			notFoundNew++
			writes = append(writes, pendingWrite{id, ident, ttls.negative})
			continue
		}
		// 失败回退链（stale 正 > unresolved）。
		if stale, ok := stalePositive(id); ok {
			out[id] = stale
			staleUsed++
			writes = append(writes, pendingWrite{id, stale, ttls.degraded})
			continue
		}
		unresolved = append(unresolved, id)
	}

	// 逐条校验代次并写入。代次变化来源包括：Clear（refresh / TTL 配置变更 / 远端广播）
	// 以及按 ID 精准失效（用户同步的 InvalidateUser/Group——单键也会递增整份缓存代次）。
	// 一旦发现代次变化立即停止：只在循环前查一次不够（单批上限 5000 条），其后的写入
	// 会绕过守卫，使 refresh/sync 表面成功而旧值仍留下。
	written := 0
	for _, w := range writes {
		// 容量裁剪由 TTLCache 在写入路径自生效，此处无需预留。
		if !ttlCache.SetIfGeneration(fillGen, w.id, w.val, w.ttl) {
			break
		}
		written++
	}
	discarded := len(writes) - written
	if discarded > 0 {
		s.logger.Info("ID mapping cache fill discarded after generation change",
			"namespace", ns.String(), "discarded", discarded)
	}

	s.logResolve(ns, len(ids), hits, misses, staleUsed, degradedUsed, notFoundNew, conflicts, discarded, start)

	if len(unresolved) > 0 {
		cause := errors.Join(sourceErrs...)
		if cause == nil {
			cause = fmt.Errorf("%d ids unresolved", len(unresolved))
		}
		s.logger.Warn("ID source unavailable",
			"namespace", ns.String(), "unresolved", len(unresolved), "error", cause)
		return nil, contract.NewUnavailableError("ID source unavailable", cause)
	}
	return out, nil
}

func (s *Service) logResolve(
	ns idMappingNamespace, requested, hits, misses, stale, degraded, notFound, conflicts, discarded int,
	start time.Time,
) {
	s.logger.Debug("ID-mapping resolution",
		"namespace", ns.String(),
		"requested", requested,
		"hits", hits,
		"misses", misses,
		"stale", stale,
		"degraded", degraded,
		"not_found", notFound,
		"conflicts", conflicts,
		"discarded", discarded,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// RefreshIDMappingCache clears the local ID mapping cache and bumps identity
// so every other node drops its copy within one poll interval.
// SyncUsers does the same bump after a completed sync (local per-id invalidate
// plus cluster clear). TTL setting changes do not bump; each node clears locally
// when it observes the new TTL via the settings topic.
//
// 本机 Clear 不回滚；Bump 失败时返回 503 并记审计（其它节点靠各自 TTL 兜底）。
func (s *Service) RefreshIDMappingCache(ctx context.Context, operator string) error {
	s.idMappingCache.Clear()
	bumpErr := s.bumpIDMappingCache(ctx)
	s.auditIDMappingRefresh(ctx, operator, bumpErr)
	if bumpErr != nil {
		return contract.NewUnavailableError("ID mapping cache refresh broadcast failed", bumpErr)
	}
	return nil
}

func (s *Service) auditIDMappingRefresh(ctx context.Context, operator string, bumpErr error) {
	if s.auditSvc == nil {
		return
	}
	result := "success"
	details := map[string]any{
		"node": s.cfg.SyncLeaseOwnerID,
	}
	switch {
	case bumpErr != nil:
		result = "failed"
		details["bump"] = "failed"
		details["error"] = bumpErr.Error()
	case s.cacheVersion != nil:
		details["bump"] = "ok"
	default:
		details["bump"] = "skipped"
	}
	s.auditSvc.LogUser(ctx, "ID_MAPPING_CACHE_REFRESH", result, operator, slog.LevelInfo, details)
}

// idMappingCacheTTLs reads the configured TTL pair and detects configuration
// changes: on change the LOCAL cache is cleared (generation++).
//
// 不做 identity 广播：settings 主题把新 TTL 传到各节点，各节点经 noteTTLs 清空本机缓存。
//
// 读取失败时不改变更检测基线、不清缓存，本次解析沿用**上次成功读取的 TTL 对**
// （无历史才退到 schema 默认值）。若用代码默认值兜底会带来两重副作用：默认
// 86400s 会被当作回填 TTL 静默把缓存寿命放大两个数量级；它同时会污染基线，使
// 抖动恢复后的第一次成功读取又被判定为"发生了变化"而再清一次缓存。
func (s *Service) idMappingCacheTTLs(ctx context.Context) idMappingTTLs {
	pos, neg := schema.IDMappingFoundCacheTTLDefault, schema.IDMappingNotFoundCacheTTLDefault
	if lastPos, lastNeg, ok := s.idMappingCache.lastKnownTTLs(); ok {
		pos, neg = lastPos, lastNeg
	}

	readOK := false
	if s.authSettings != nil {
		if auth, err := s.authSettings.GetAuthSettings(ctx); err == nil {
			pos = auth.IDMappingFoundCacheTTL
			neg = auth.IDMappingNotFoundCacheTTL
			readOK = true
		}
	}

	// 兜底（非兼容层）：字段为必填且有 min 约束，正常写入路径不可能落 0；
	// 但历史行或绕过校验的直改 DB 可以是 0，而 TTL=0 会让缓存退化到内建默认 TTL，
	// 静默把负缓存从 10min 放大 24 倍。此处按字段语义回退默认值。
	// 跨字段不变量同理：正常路径由 validate 保证，存量数据仍需读取侧夹紧。
	if pos <= 0 {
		pos = schema.IDMappingFoundCacheTTLDefault
	}
	if neg <= 0 {
		neg = schema.IDMappingNotFoundCacheTTLDefault
	}
	neg = min(neg, pos)

	// 变更检测只在本次成功读到配置时进行：失败不该被误判成"TTL 改了"。
	if readOK && s.idMappingCache.noteTTLs(pos, neg) {
		s.idMappingCache.Clear()
		s.logger.Info("ID mapping TTL settings changed; local ID mapping cache cleared",
			"positive_ttl", pos, "negative_ttl", neg)
	}

	return idMappingTTLs{
		positive: time.Duration(pos) * time.Second,
		negative: time.Duration(neg) * time.Second,
		degraded: IDMappingDegradedCacheTTL * time.Second,
	}
}

// pendingWrite 是一条待回填的缓存写入（解析结果 → 缓存）。
type pendingWrite struct {
	id  uint32
	val IDMapping
	ttl time.Duration
}

func uniqueIDs(ids []uint32) []uint32 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[uint32]struct{}, len(ids))
	out := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func parseUint32ID(raw string) (uint32, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// bumpIDMappingCache increments cache_versions identity. Nil repo skips (tests / unwired).
// Callers clear or invalidate locally first. Other nodes clear on the next poll and
// refill from the identity source, not from this DB.
func (s *Service) bumpIDMappingCache(ctx context.Context) error {
	if s.cacheVersion == nil {
		return nil
	}
	return s.cacheVersion.Bump(ctx, idMappingCacheTopic)
}

func (s *Service) invalidateIDMappingsFromSystemUsers(systemUsers []userprovider.SystemUser) {
	for _, su := range systemUsers {
		if uid, ok := parseUint32ID(su.Uid); ok {
			s.idMappingCache.InvalidateUser(uid)
		}
		if gid, ok := parseUint32ID(su.Gid); ok {
			s.idMappingCache.InvalidateGroup(gid)
		}
	}
}
