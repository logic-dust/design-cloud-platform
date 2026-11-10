package security

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/model"
)

// DeleteByKey  removes the failure/Ban records for the given key from
// both the in-memory store and the DB.  Called after a successful
// authentication to reset the failure counter.
func (s *Service) DeleteByKey(ctx context.Context, keyName string) {
	if err := s.banCache.DeleteByKey(ctx, keyName); err != nil {
		s.logger.Warn("Failed to delete auth failures from DB", "key", keyName, "error", err)
	}
}

func (s *Service) getAuthSettings(ctx context.Context) schema.AuthSettings {
	v, err := s.settingsSvc.GetAuthSettings(ctx)
	if err != nil {
		return schema.AuthSettings{}
	}
	return v
}

func (s *Service) getBanSecondsForUser(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.UserBanDuration >= 60 {
		return authSettings.UserBanDuration
	}
	return defaultAuthSettings.UserBanDuration
}

func (s *Service) getBanSecondsForIP(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.IPBanDuration >= 60 {
		return authSettings.IPBanDuration
	}
	return defaultAuthSettings.IPBanDuration
}

func (s *Service) getUserMaxFailedTimes(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.MaxUserLoginAttempts > 0 {
		return authSettings.MaxUserLoginAttempts
	}
	return defaultAuthSettings.MaxUserLoginAttempts
}

func (s *Service) getIPMaxFailedTimes(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.MaxIPLoginAttempts > 0 {
		return authSettings.MaxIPLoginAttempts
	}
	return defaultAuthSettings.MaxIPLoginAttempts
}

// Ban applies an auto-triggered ban for the given record (Key/Subject/Source/
// IP/Username), filling in duration and default reason, and persists it via
// BanCache.ApplyBan so reason and expiry survive restarts.
func (s *Service) Ban(ctx context.Context, rec model.Ban) {
	var seconds int
	if rec.Subject == model.BanSubjectIP {
		seconds = s.getBanSecondsForIP(ctx)
	} else {
		seconds = s.getBanSecondsForUser(ctx)
	}
	until := time.Now().Add(time.Duration(seconds) * time.Second)
	rec.BannedUntil = &until
	rec.Reason = s.defaultBanReason(ctx, rec.Subject)

	if err := s.banCache.ApplyBan(ctx, rec); err != nil {
		s.logger.Error("failed to persist ban to DB", "key", rec.Key, "error", err)
	}
	if rec.Subject == model.BanSubjectUserIP && rec.Username != "" && s.onUserBanned != nil {
		s.onUserBanned(rec.Username)
	}
}

// defaultBanReason returns the user-facing reason string for an auto-triggered ban.
func (s *Service) defaultBanReason(ctx context.Context, subject model.BanSubject) string {
	switch subject {
	case model.BanSubjectIP:
		return "too many failed with this IP, please try again later"
	case model.BanSubjectUserIP:
		return fmt.Sprintf("too many failed tries, please try again after %d seconds", s.getBanSecondsForUser(ctx))
	default:
		return "banned, please contact the administrator"
	}
}

// Unban removes the ban entry for the given key from DB and in-memory store.
func (s *Service) Unban(ctx context.Context, keyName string) {
	if err := s.banCache.DeleteByKey(ctx, keyName); err != nil {
		s.logger.Warn("Failed to delete auth failures from DB", "key", keyName, "error", err)
		return
	}
	s.logger.Info("Removed banned record", "key", keyName)
}

// ClearAllBans removes every active ban.
func (s *Service) ClearAllBans(ctx context.Context) int {
	records := s.ListBannedRecords()
	count := len(records)
	if count == 0 {
		return 0
	}

	if err := s.banCache.DeleteAll(ctx); err != nil {
		s.logger.Warn("Failed to delete all auth failures from DB", "error", err)
	} else {
		s.logger.Info("Removed all banned records", "count", count)
	}
	return count
}

// IsBanned reports whether the given IP or IP:username is currently banned.
func (s *Service) IsBanned(ctx context.Context, IP, username string) (bool, BannedRecord) {
	keyNameCandidate := []string{IP + ":" + username, IP}
	now := time.Now()

	for _, keyName := range keyNameCandidate {
		dbRecord, ok := s.banCache.GetBanRecord(keyName)
		if ok && dbRecord.Key != "" && dbRecord.BannedUntil != nil && dbRecord.BannedUntil.After(now) {
			if s.IsAllowlisted(ctx, IP) {
				s.logger.Warn("ban allowlist hit: active ban ignored", "ip", IP, "key", keyName, "subject", dbRecord.Subject)
				return false, BannedRecord{}
			}
			return true, toBannedRecord(dbRecord)
		}
	}

	return false, BannedRecord{}
}

// IsBannedByIP checks if an IP is banned regardless of username.
// Used by session auth middleware where no username is available yet.
func (s *Service) IsBannedByIP(ctx context.Context, IP string) (bool, BannedRecord) {
	return s.IsBanned(ctx, IP, "")
}

// toBannedRecord converts a stored ban into its client-facing representation.
func toBannedRecord(r model.Ban) BannedRecord {
	return BannedRecord{
		Subject:  r.Subject,
		Source:   r.Source,
		Username: r.Username,
		IP:       r.IP,
		BanTime:  r.FirstFailedAt,
		Reason:   r.Reason,
	}
}

// ListBannedRecords returns all currently active bans.
func (s *Service) ListBannedRecords() map[string]any {
	allBannedRecords := make(map[string]any)
	now := time.Now()
	for _, record := range s.banCache.List() {
		if record.BannedUntil == nil || !record.BannedUntil.After(now) {
			continue
		}
		allBannedRecords[record.Key] = toBannedRecord(record)
	}
	return allBannedRecords
}

// RecordFailure increments the failure counter for the given subject/source and,
// if the threshold is exceeded, triggers a ban. It is the unified entry point for
// all failure sources (login, session token).
// Allowlisted IPs are silently ignored (with a WARN log) to avoid blind spots.
// Active bans skip further counting (no dirty/flush churn while the window lasts).
func (s *Service) RecordFailure(ctx context.Context, keyName string, subject model.BanSubject, source model.BanSource, IP, username string) {
	if s.IsAllowlisted(ctx, IP) {
		s.logger.Warn("ban allowlist hit: failure ignored", "ip", IP, "subject", subject, "source", source, "key", keyName)
		return
	}
	if banned, _ := s.IsBanned(ctx, IP, username); banned {
		return
	}

	record, err := s.banCache.AddBanRecord(model.Ban{
		Key:      keyName,
		IP:       IP,
		Username: username,
		Subject:  subject,
		Source:   source,
	})
	if err != nil {
		return
	}

	needBan := false
	switch subject {
	case model.BanSubjectIP:
		if int(record.Count) >= s.getIPMaxFailedTimes(ctx) {
			needBan = true
		}
	case model.BanSubjectUserIP:
		if int(record.Count) >= s.getUserMaxFailedTimes(ctx) {
			needBan = true
		}
	default:
		return
	}

	if needBan {
		s.Ban(ctx, model.Ban{Key: keyName, Subject: subject, Source: source, IP: IP, Username: username})
	}
}

// RecordIPFailure records an invalid session token attempt against the IP and
// bans if the threshold is reached.
func (s *Service) RecordIPFailure(ctx context.Context, IP string) {
	s.RecordFailure(ctx, IP, model.BanSubjectIP, model.BanSourceSessionToken, IP, "")
}

// ManualBan duration bounds.
const (
	minManualBanDuration = time.Minute
	maxManualBanDuration = 30 * 24 * time.Hour
)

// ManualBan applies an immediate administrator ban with Source=Manual (no failure
// counting involved). subject=UserIP requires username; the ban key is IP or
// IP:username accordingly.
func (s *Service) ManualBan(ctx context.Context, subject model.BanSubject, IP, username string, duration time.Duration, reason string) (BannedRecord, error) {
	if subject != model.BanSubjectIP && subject != model.BanSubjectUserIP {
		return BannedRecord{}, fmt.Errorf("subject must be %q or %q", model.BanSubjectIP, model.BanSubjectUserIP)
	}
	if _, err := netip.ParseAddr(IP); err != nil {
		return BannedRecord{}, fmt.Errorf("invalid ip %q", IP)
	}
	if subject == model.BanSubjectUserIP && username == "" {
		return BannedRecord{}, fmt.Errorf("username is required when subject is %q", model.BanSubjectUserIP)
	}
	if duration < minManualBanDuration || duration > maxManualBanDuration {
		return BannedRecord{}, fmt.Errorf("duration must be between %s and %s", minManualBanDuration, maxManualBanDuration)
	}
	reason = cmp.Or(reason, "banned by administrator")

	key := IP
	if subject == model.BanSubjectUserIP {
		key = IP + ":" + username
	}
	now := time.Now()
	until := now.Add(duration)
	if err := s.banCache.ApplyBan(ctx, model.Ban{
		Key:         key,
		Subject:     subject,
		Source:      model.BanSourceManual,
		IP:          IP,
		Username:    username,
		BannedUntil: &until,
		Reason:      reason,
	}); err != nil {
		return BannedRecord{}, fmt.Errorf("persist manual ban: %w", err)
	}
	s.logger.Info("manual ban applied", "key", key, "subject", subject, "ip", IP, "username", username, "until", until, "reason", reason)
	return BannedRecord{
		Subject:  subject,
		Source:   model.BanSourceManual,
		Username: username,
		IP:       IP,
		BanTime:  now,
		Reason:   reason,
	}, nil
}
