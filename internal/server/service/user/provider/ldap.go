package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/config"
	"design-cloud-engine/internal/db"

	"github.com/go-ldap/ldap/v3"
)

const (
	ldapDialTimeout = 10 * time.Second
	ldapOpTimeout   = 15 * time.Second
	ldapRetryDelay  = 200 * time.Millisecond
	ldapMaxAttempts = 3
	// idLookupChunk 是身份解析单次 Search 携带的最大 ID 数。
	//
	// 取值依据（真机 FreeIPA + go-ldap 同库同调用序列实测）：
	// 单次 Search 成本 ≈ 5µs×谓词项数 + 0.013×返回条数^1.8 —— 主导项是**返回条数的
	// 超线性增长**，谓词项数只贡献一个很小的线性量（实测 500 项 / 30 条命中仅 8.3ms）。
	// 因此整批总量随块大小单调上升（命中越多越明显），而块越小在未命中侧只多付
	// 每查询固定开销（本地 <0.3ms/次）：
	//
	//	块大小   3000 全命中   5000 全未命中   5000 混合
	//	   25       544ms          63ms          573ms
	//	   50       805ms          59ms          825ms
	//	  500      5750ms          32ms         5742ms
	//
	// 反向约束（不可取大）：单块全命中耗时不得逼近 idSearchTimeout——
	// 实测单块 1000 项全命中 > 3s 会直接超时失败。
	idLookupChunk = 25
)

// 身份解析（UsersByIDs / GroupsByIDs）专用预算：点查场景比全量列举更严格，
// 保证"单请求解析总耗时"受控，任何环节都不得突破总预算：
//
//	idLookupBudget — 单次解析调用的墙钟总预算（含建连、Bind、全部 Search）
//	idDialTimeout  — 单次建连超时（配合 idDialAttempts 次尝试）
//	idBindTimeout  — 服务账号 Bind 超时
//	idSearchTimeout— 单次 Search 超时（分块串行，每块都从剩余预算内切片）
const (
	idLookupBudget  = 15 * time.Second
	idDialTimeout   = 2 * time.Second
	idDialAttempts  = 2
	idBindTimeout   = 2 * time.Second
	idSearchTimeout = 3 * time.Second
)

var ldapUserAttrs = []string{"uid", "uidNumber", "gidNumber", "homeDirectory", "loginShell", "gecos", "memberOf", "krbPasswordExpiration", "pwdExpirationTime"}

type ldapProvider struct {
	db     *db.UserRepo
	logger *slog.Logger
	cfg    config.LdapConfig
	dialFn func(string, *tls.Config) (*ldap.Conn, error)
}

func (p *ldapProvider) cfgValue() config.LdapConfig { return p.cfg }
func (p *ldapProvider) idAttribute() string {
	if v := p.cfgValue().IdAttribute; v != "" {
		return v
	}
	return "uid"
}
func (p *ldapProvider) userFilter() string {
	if v := p.cfgValue().UserFilter; v != "" {
		return v
	}
	return "(objectClass=posixAccount)"
}
func (p *ldapProvider) groupBaseDN() string {
	cfg := p.cfgValue()
	if v := cfg.GroupBaseDN; v != "" {
		return v
	}
	return cfg.UserBaseDN
}
func (p *ldapProvider) groupFilter() string {
	if v := p.cfgValue().GroupFilter; v != "" {
		return v
	}
	return "(objectClass=posixGroup)"
}

func (p *ldapProvider) GetUser(ctx context.Context, username string) (*SystemUser, error) {
	l, err := p.ldapDial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return nil, err
	}
	entry, err := p.ldapSearchOneUser(l, username)
	if err != nil {
		return nil, err
	}
	u := p.entryToSystemUser(entry)
	if dbUser, err := p.db.GetByName(ctx, username); err == nil && dbUser.ID != 0 {
		u.ID = dbUser.ID
	}
	return &u, nil
}

func (p *ldapProvider) ListUsers(ctx context.Context) ([]SystemUser, error) {
	l, err := p.ldapDial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return nil, err
	}
	entries, err := p.ldapSearchAllUsers(l)
	if err != nil {
		return nil, err
	}
	dbIDByName := make(map[string]uint)
	if dbUsers, _, err := p.db.List(ctx, 0, 0); err == nil {
		for _, u := range dbUsers {
			dbIDByName[u.Username] = u.ID
		}
	}
	users := make([]SystemUser, 0, len(entries))
	for _, e := range entries {
		u := p.entryToSystemUser(e)
		if id, ok := dbIDByName[u.Username]; ok {
			u.ID = id
		}
		users = append(users, u)
	}
	return users, nil
}

// UsersByIDs resolves posix uidNumber → username via chunked LDAP point lookups.
// Missing IDs are omitted from the result map (not an error). IDMapping-source errors are returned.
func (p *ldapProvider) UsersByIDs(ctx context.Context, uids []uint32) (map[uint32]string, error) {
	return p.idLookup(ctx, uids, p.ldapSearchUsersByIDs)
}

// GroupsByIDs resolves posix gidNumber → cn via chunked LDAP point lookups.
func (p *ldapProvider) GroupsByIDs(ctx context.Context, gids []uint32) (map[uint32]string, error) {
	return p.idLookup(ctx, gids, p.ldapSearchGroupsByIDs)
}

// idLookup runs one ID-mapping resolution round under a single wall-clock
// budget (idLookupBudget): dial ≤2s ×2 attempts, bind ≤2s, each chunk
// search ≤3s — every step is further clamped to the remaining budget, so the
// call returns within the budget even on a slow/unreachable directory.
// 预算耗尽或任一步失败 → 返回 error，由上层进入失败回退链（stale/本机/503）。
func (p *ldapProvider) idLookup(
	ctx context.Context,
	ids []uint32,
	search func(*ldap.Conn, []uint32) (map[uint32]string, error),
) (map[uint32]string, error) {
	if len(ids) == 0 {
		return map[uint32]string{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, idLookupBudget)
	defer cancel()

	// clamp 将单步超时限制在剩余预算内；剩余预算耗尽 → 0（调用方按失败处理）。
	clamp := func(max time.Duration) time.Duration {
		deadline, ok := ctx.Deadline()
		if !ok {
			return max
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0
		}
		if remaining < max {
			return remaining
		}
		return max
	}

	l, err := p.ldapDialBudget(idDialTimeout, idDialAttempts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()

	// go-ldap 的协议操作不接收 context：通过在 ctx 取消/超时时主动断开连接，
	// 解除阻塞中的操作，保证"遵守调用方 ctx"。
	stopWatchdog := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stopWatchdog()

	bindTimeout := clamp(idBindTimeout)
	if bindTimeout <= 0 {
		return nil, fmt.Errorf("ID lookup budget exhausted before bind")
	}
	l.SetTimeout(bindTimeout)
	if err := p.ldapBindServiceAccount(l); err != nil {
		return nil, err
	}

	out := make(map[uint32]string, len(ids))
	for chunk := range slices.Chunk(ids, idLookupChunk) {
		opTimeout := clamp(idSearchTimeout)
		if opTimeout <= 0 {
			return nil, fmt.Errorf("ID lookup budget exhausted before search")
		}
		l.SetTimeout(opTimeout)
		m, err := search(l, chunk)
		if err != nil {
			return nil, err
		}
		maps.Copy(out, m)
	}
	return out, nil
}

func (p *ldapProvider) ListGroups(_ context.Context) (map[string][]string, error) {
	l, err := p.ldapDial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return nil, err
	}
	return p.ldapSearchAllGroups(l)
}

func (p *ldapProvider) GetGroupMembers(_ context.Context, groupName string) ([]string, error) {
	l, err := p.ldapDial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return nil, err
	}

	filter := fmt.Sprintf("(&%s(cn=%s))", p.groupFilter(), ldap.EscapeFilter(groupName))
	req := ldap.NewSearchRequest(p.groupBaseDN(), ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false, filter, []string{"member", "memberUid"}, nil)
	res, err := l.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap group members search failed: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, ErrGroupNotFound
	}

	e := res.Entries[0]
	idAttr := p.idAttribute()
	var members []string
	members = append(members, e.GetAttributeValues("memberUid")...)
	for _, dn := range e.GetAttributeValues("member") {
		if uid := parseMemberDN(dn, idAttr); uid != "" {
			members = append(members, uid)
		}
	}
	return members, nil
}

func (p *ldapProvider) ListUserGroups(_ context.Context) (map[string][]string, error) {
	l, err := p.ldapDial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return nil, err
	}
	allGroups, err := p.ldapSearchAllGroups(l)
	if err != nil {
		return nil, err
	}
	userGroupsMap := make(map[string][]string)
	for groupName, members := range allGroups {
		for _, member := range members {
			userGroupsMap[member] = append(userGroupsMap[member], groupName)
		}
	}
	return userGroupsMap, nil
}

func (p *ldapProvider) VerifyPassword(_ context.Context, username, password string) error {
	l, err := p.ldapDial()
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return err
	}
	entry, err := p.ldapSearchOneUser(l, username)
	if err != nil {
		return err
	}
	if err := l.Bind(entry.DN, password); err != nil {
		return mapLDAPBindError(err)
	}
	// Bind succeeded, but FreeIPA still lets expired passwords authenticate.
	// Surface the expired state so the login flow forces a password change.
	if passwordExpired(entry) {
		return ErrUserPasswordExpired
	}
	return nil
}

// passwordExpired reports whether the entry carries a password expiration
// time in the past. Entries without an expiration attribute are never expired.
func passwordExpired(e *ldap.Entry) bool {
	exp := parsePasswordExpiration(e)
	return exp != nil && time.Now().After(*exp)
}

func (p *ldapProvider) ChangePassword(_ context.Context, username, oldPassword, newPassword string) error {
	if !p.cfgValue().PasswordModify {
		return ErrLDAPPasswordNotSupported
	}
	l, err := p.ldapDial()
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	if err := p.ldapBindServiceAccount(l); err != nil {
		return err
	}
	entry, err := p.ldapSearchOneUser(l, username)
	if err != nil {
		return err
	}
	err = l.Bind(entry.DN, oldPassword)
	bindErr := mapLDAPBindError(err)
	if bindErr != nil && !errors.Is(bindErr, ErrUserPasswordExpired) {
		return bindErr
	}
	if errors.Is(bindErr, ErrUserPasswordExpired) {
		if err := p.ldapBindServiceAccount(l); err != nil {
			return err
		}
	}
	if _, err := l.PasswordModify(ldap.NewPasswordModifyRequest(entry.DN, oldPassword, newPassword)); err != nil {
		return fmt.Errorf("ldap password modify failed: %w", err)
	}
	return nil
}

func (p *ldapProvider) ResetPassword(_ context.Context, _, _ string) error {
	return ErrLDAPPasswordNotSupported
}

func (p *ldapProvider) HealthCheck(_ context.Context) (latencyMs int64, err error) {
	start := time.Now()
	l, err := p.ldapDial()
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	if err = p.ldapBindServiceAccount(l); err != nil {
		return 0, err
	}
	return time.Since(start).Milliseconds(), nil
}

func CheckLDAPHealth(ctx context.Context, p Provider) (latencyMs int64, err error, ok bool) {
	ldapProvider, ok := p.(*ldapProvider)
	if !ok {
		return 0, nil, false
	}
	latencyMs, err = ldapProvider.HealthCheck(ctx)
	return latencyMs, err, true
}

func (p *ldapProvider) ldapDial() (*ldap.Conn, error) {
	return p.ldapDialBudget(ldapDialTimeout, ldapMaxAttempts)
}

// ldapDialBudget dials using an explicit per-attempt timeout and attempt count.
// 常规操作使用默认预算；身份解析使用更严格的 idDialTimeout / idDialAttempts。
func (p *ldapProvider) ldapDialBudget(dialTimeout time.Duration, attempts int) (*ldap.Conn, error) {
	ldapURL := p.cfgValue().Server
	if ldapURL == "" {
		return nil, fmt.Errorf("ldap server not configured")
	}
	if !strings.Contains(ldapURL, "://") {
		ldapURL = "ldap://" + ldapURL
	}
	tlsConfig, err := p.buildLDAPTLSConfig(ldapURL)
	if err != nil {
		return nil, err
	}
	if p.dialFn != nil {
		return p.dialFn(ldapURL, tlsConfig)
	}
	return dialLDAPBudgeted(ldapURL, tlsConfig, dialTimeout, attempts)
}

func (p *ldapProvider) buildLDAPTLSConfig(ldapURL string) (*tls.Config, error) {
	cfg := p.cfgValue()
	// TLS is determined by the URL scheme (ldaps://) or explicit CA path.
	// TLSInsecureSkipVerify is intentionally hardcoded false — production must
	// always verify the server certificate.
	useTLS := strings.HasPrefix(strings.ToLower(ldapURL), "ldaps://") || cfg.TLSCAPath != ""
	if !useTLS {
		return nil, nil
	}
	tlsConfig := &tls.Config{InsecureSkipVerify: false, MinVersion: tls.VersionTLS12}
	if cfg.TLSCAPath != "" {
		caData, err := os.ReadFile(cfg.TLSCAPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read ldap CA file: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to parse ldap CA file")
		}
		tlsConfig.RootCAs = certPool
	}
	return tlsConfig, nil
}

// dialLDAPBudgeted dials with a bounded per-attempt timeout and attempt count;
// 建连后设置默认操作超时（调用方可在需要时用 SetTimeout 覆盖）。
//
// 已知取舍：拨号**不响应 context 取消**。go-ldap 的 DialURL 不吃 context（其内部
// 类型名 DialContext 只是承载 dialer/TLS 配置，并非 context 感知），所以取消只能
// 阻止"发起后续尝试"，无法中断已在进行的那一次拨号。之所以按现状接受：
//   - 各路径的拨号总耗时有硬上限（身份解析 2s×2，通用 10s×3），且都落在外层预算内
//     （解析总预算 15s；HTTP 请求级超时 60s），不会超出调用方预期地挂住；
//   - 拨号期间不持锁、不占 DB/连接池，其它请求不受影响；
//   - 优雅关闭不等待在途请求（Server.Stop 只取消 ctx，http.Server.Shutdown 有 10s 上限
//     且不作为退出前置），因此不会拖慢进程退出。
//
// 若将来提高 attempts 或并发量级，这里需要改为把 ctx 穿进拨号循环（至少跳过后续尝试）。
func dialLDAPBudgeted(ldapURL string, tlsConfig *tls.Config, dialTimeout time.Duration, attempts int) (*ldap.Conn, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	dialOpts := []ldap.DialOpt{ldap.DialWithDialer(dialer)}
	if tlsConfig != nil {
		dialOpts = append(dialOpts, ldap.DialWithTLSConfig(tlsConfig))
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		conn, err := ldap.DialURL(ldapURL, dialOpts...)
		if err == nil {
			conn.SetTimeout(ldapOpTimeout)
			return conn, nil
		}
		lastErr = err
		if attempt < attempts {
			time.Sleep(ldapRetryDelay)
		}
	}
	return nil, fmt.Errorf("failed to connect to LDAP server: %v", lastErr)
}

func (p *ldapProvider) ldapBindServiceAccount(l *ldap.Conn) error {
	cfg := p.cfgValue()
	if cfg.BindDN == "" {
		return nil
	}
	if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return fmt.Errorf("failed to bind service account: %w", err)
	}
	return nil
}

func (p *ldapProvider) ldapSearchOneUser(l *ldap.Conn, username string) (*ldap.Entry, error) {
	idAttr := p.idAttribute()
	filter := fmt.Sprintf("(&(%s=%s)%s)", idAttr, ldap.EscapeFilter(username), p.userFilter())
	attrs := ldapUserAttrs
	if idAttr != "uid" {
		attrs = append([]string{idAttr}, ldapUserAttrs...)
	}
	req := ldap.NewSearchRequest(p.cfgValue().UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false, filter, attrs, nil)
	res, err := l.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap search failed: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, ErrUserNotFound
	}
	return res.Entries[0], nil
}

func (p *ldapProvider) ldapSearchAllUsers(l *ldap.Conn) ([]*ldap.Entry, error) {
	idAttr := p.idAttribute()
	attrs := ldapUserAttrs
	if idAttr != "uid" {
		attrs = append([]string{idAttr}, ldapUserAttrs...)
	}
	req := ldap.NewSearchRequest(p.cfgValue().UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false, p.userFilter(), attrs, nil)
	res, err := l.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap user list search failed: %w", err)
	}
	return res.Entries, nil
}

// buildIDNumberFilter builds (&(attr=N)base) for one ID, or (&(|(attr=a)(attr=b)...)base) for many.
func buildIDNumberFilter(attr string, ids []uint32, baseFilter string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) == 1 {
		return fmt.Sprintf("(&(%s=%s)%s)", attr, ldap.EscapeFilter(strconv.FormatUint(uint64(ids[0]), 10)), baseFilter)
	}
	var b strings.Builder
	b.WriteString("(&(|")
	for _, id := range ids {
		fmt.Fprintf(&b, "(%s=%s)", attr, ldap.EscapeFilter(strconv.FormatUint(uint64(id), 10)))
	}
	b.WriteString(")")
	b.WriteString(baseFilter)
	b.WriteString(")")
	return b.String()
}

func parseUint32Attr(e *ldap.Entry, attr string) (uint32, bool) {
	raw := e.GetAttributeValue(attr)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// idNameMap builds idNumber → name from search results.
//
// 目录数据异常（同一 idNumber 对应多条 entry）不属于本程序的职责：它只能来自绕过
// IPA 唯一性检查的低层写入（--setattr / 直接 LDIF / 复制冲突），请求侧的去重
// （uniqueIDs）无法预防，只能覆盖。
func idNameMap(entries []*ldap.Entry, idNumberAttr, nameAttr string) map[uint32]string {
	out := make(map[uint32]string, len(entries))
	for _, e := range entries {
		id, ok := parseUint32Attr(e, idNumberAttr)
		if !ok {
			continue
		}
		name := e.GetAttributeValue(nameAttr)
		if name == "" {
			continue
		}
		out[id] = name
	}
	return out
}

func (p *ldapProvider) ldapSearchUsersByIDs(l *ldap.Conn, uids []uint32) (map[uint32]string, error) {
	if len(uids) == 0 {
		return map[uint32]string{}, nil
	}
	idAttr := p.idAttribute()
	filter := buildIDNumberFilter("uidNumber", uids, p.userFilter())
	attrs := []string{idAttr, "uidNumber"}
	req := ldap.NewSearchRequest(
		p.cfgValue().UserBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		// sizeLimit 传 0（不设客户端上限）：它隐含“目录必然规范”的断言，而规范上游
		// 目录数据不是本程序的职责。服务端自身的 nsslapd-sizelimit 仍是兜底，
		// 且超出时返回错误而非静默截断。
		0,
		0,
		false,
		filter,
		attrs,
		nil,
	)
	res, err := l.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap users-by-ids search failed: %w", err)
	}
	return idNameMap(res.Entries, "uidNumber", idAttr), nil
}

func (p *ldapProvider) ldapSearchGroupsByIDs(l *ldap.Conn, gids []uint32) (map[uint32]string, error) {
	if len(gids) == 0 {
		return map[uint32]string{}, nil
	}
	filter := buildIDNumberFilter("gidNumber", gids, p.groupFilter())
	req := ldap.NewSearchRequest(
		p.groupBaseDN(),
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, // 同上：不设客户端 sizeLimit
		0,
		false,
		filter,
		[]string{"cn", "gidNumber"},
		nil,
	)
	res, err := l.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap groups-by-ids search failed: %w", err)
	}
	return idNameMap(res.Entries, "gidNumber", "cn"), nil
}

func (p *ldapProvider) ldapSearchAllGroups(l *ldap.Conn) (map[string][]string, error) {
	req := ldap.NewSearchRequest(p.groupBaseDN(), ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false, p.groupFilter(), []string{"cn", "member", "memberUid"}, nil)
	res, err := l.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap group list search failed: %w", err)
	}
	idAttr := p.idAttribute()
	groups := make(map[string][]string, len(res.Entries))
	for _, e := range res.Entries {
		cn := e.GetAttributeValue("cn")
		if cn == "" {
			continue
		}
		var members []string
		members = append(members, e.GetAttributeValues("memberUid")...)
		for _, dn := range e.GetAttributeValues("member") {
			if uid := parseMemberDN(dn, idAttr); uid != "" {
				members = append(members, uid)
			}
		}
		groups[cn] = members
	}
	return groups, nil
}

func (p *ldapProvider) entryToSystemUser(e *ldap.Entry) SystemUser {
	idAttr := p.idAttribute()
	u := SystemUser{
		Username: e.GetAttributeValue(idAttr),
		Uid:      e.GetAttributeValue("uidNumber"),
		Gid:      e.GetAttributeValue("gidNumber"),
		Comment:  e.GetAttributeValue("gecos"),
		Home:     e.GetAttributeValue("homeDirectory"),
		Shell:    e.GetAttributeValue("loginShell"),
		Status:   "active",

		PasswordExpiresAt: parsePasswordExpiration(e)}
	groupBaseDN := strings.ToLower(p.groupBaseDN())
	for _, memberOf := range e.GetAttributeValues("memberOf") {
		if strings.HasSuffix(strings.ToLower(memberOf), ","+groupBaseDN) {
			if cn := parseCNFromDN(memberOf); cn != "" {
				u.Groups = append(u.Groups, cn)
			}
		}
	}
	return u
}

func parseCNFromDN(dn string) string {
	before, _, ok := strings.Cut(dn, ",")
	rdn := dn
	if ok {
		rdn = before
	}
	_, after, ok := strings.Cut(rdn, "=")
	if !ok {
		return ""
	}
	return after
}

func parseMemberDN(memberDN, idAttr string) string {
	before, _, ok := strings.Cut(memberDN, ",")
	rdn := memberDN
	if ok {
		rdn = before
	}
	before, after, ok := strings.Cut(rdn, "=")
	if !ok {
		return ""
	}
	if strings.EqualFold(before, idAttr) {
		return after
	}
	return ""
}

func mapLDAPBindError(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	if ldapErr, ok := errors.AsType[*ldap.Error](err); ok {
		switch ldapErr.ResultCode {
		case ldap.LDAPResultInvalidCredentials:
			if strings.Contains(errStr, "data 532") || strings.Contains(strings.ToLower(errStr), "password expired") {
				return ErrUserPasswordExpired
			}
			return ErrUserPasswordIncorrect
		case ldap.LDAPResultUnwillingToPerform:
			if strings.Contains(strings.ToLower(errStr), "password") && (strings.Contains(strings.ToLower(errStr), "expired") || strings.Contains(strings.ToLower(errStr), "reset")) {
				return ErrUserPasswordExpired
			}
		case ldap.LDAPResultNoSuchObject:
			return ErrUserNotFound
		}
	}
	if strings.Contains(errStr, "data 532") || strings.Contains(strings.ToLower(errStr), "password expired") {
		return ErrUserPasswordExpired
	}
	low := strings.ToLower(errStr)
	if strings.Contains(low, "invalid credentials") {
		return ErrUserPasswordIncorrect
	}
	if strings.Contains(low, "no such object") {
		return ErrUserNotFound
	}
	return fmt.Errorf("failed to bind to LDAP server: %w", err)
}

// parsePasswordExpiration extracts the password expiration time from an LDAP
// entry. FreeIPA exposes krbPasswordExpiration (GeneralizedTime); OpenLDAP
// ppolicy uses pwdExpirationTime. Returns nil when absent or unparseable.
func parsePasswordExpiration(e *ldap.Entry) *time.Time {
	for _, attr := range []string{"krbPasswordExpiration", "pwdExpirationTime"} {
		v := e.GetAttributeValue(attr)
		if v == "" {
			continue
		}
		// GeneralizedTime: 20260718125409Z — strip optional fractional seconds.
		if idx := strings.IndexByte(v, '.'); idx > 0 {
			v = v[:idx] + "Z"
		}
		if t, err := time.Parse("20060102150405Z", v); err == nil {
			return &t
		}
	}
	return nil
}
