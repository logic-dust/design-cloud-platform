package security

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/service/settings/schema"
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
	v, err := s.authSettingsCache.Get(ctx, authSettingsKey, func(ctx context.Context, _ string) (schema.AuthSettings, error) {
		return s.settingsSvc.GetAuthSettings(ctx)
	})
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
	return defaultBanSecondsForUser
}

func (s *Service) getBanSecondsForIP(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.IPBanDuration >= 60 {
		return authSettings.IPBanDuration
	}
	return defaultBanSecondsForIP
}

func (s *Service) getUserMaxFailedTimes(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.MaxUserLoginAttempts > 0 {
		return authSettings.MaxUserLoginAttempts
	}
	return defaultMaxUserFailedAttempts
}

func (s *Service) getIPMaxFailedTimes(ctx context.Context) int {
	authSettings := s.getAuthSettings(ctx)
	if authSettings.MaxIPLoginAttempts > 0 {
		return authSettings.MaxIPLoginAttempts
	}
	return defaultMaxIPFailedAttempts
}

// Ban creates a ban entry for the given key (IP or IP:username) persisting
// it to DB via BanCache.
func (s *Service) Ban(ctx context.Context, keyName string, record *BannedRecord) {
	if record == nil {
		return
	}
	if record.Reason == "" {
		switch record.Type {
		case model.BanTypeIP:
			record.Reason = "too many failed with this IP, please try again later"
		case model.BanTypeUser:
			record.Reason = fmt.Sprintf("too many failed tries, please try again after %d seconds", s.getBanSecondsForUser(ctx))
		}
	}
	record.BanTime = time.Now()

	var duration time.Duration
	if record.Type == model.BanTypeIP {
		duration = time.Duration(s.getBanSecondsForIP(ctx)) * time.Second
	} else {
		duration = time.Duration(s.getBanSecondsForUser(ctx)) * time.Second
	}
	if err := s.banCache.SetBannedUntil(ctx, keyName, new(record.BanTime.Add(duration))); err != nil {
		s.logger.Error("failed to persist ban to DB", "key", keyName, "error", err)
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
			record := BannedRecord{
				Type:     dbRecord.Type,
				Username: dbRecord.Username,
				IP:       dbRecord.IP,
				BanTime:  dbRecord.CreatedAt,
			}
			switch record.Type {
			case model.BanTypeIP:
				record.Reason = "too many failed with this IP, please try again later"
			default:
				record.Reason = fmt.Sprintf("too many failed tries, please try again after %d seconds", s.getBanSecondsForUser(ctx))
			}
			return true, record
		}
	}

	return false, BannedRecord{}
}

// IsBannedByIP checks if an IP is banned regardless of username.
// Used by session auth middleware where no username is available yet.
func (s *Service) IsBannedByIP(_ context.Context, IP string) (bool, BannedRecord) {
	dbRecord, ok := s.banCache.GetBanRecord(IP)
	if !ok || dbRecord.Key == "" || dbRecord.BannedUntil == nil || !dbRecord.BannedUntil.After(time.Now()) {
		return false, BannedRecord{}
	}
	record := BannedRecord{
		Type:    dbRecord.Type,
		IP:      dbRecord.IP,
		BanTime: dbRecord.CreatedAt,
		Reason:  "too many failed requests from this IP, please try again later",
	}
	return true, record
}

// ListBannedRecords returns all currently active bans.
func (s *Service) ListBannedRecords() map[string]any {
	allBannedRecords := make(map[string]any)
	now := time.Now()
	for _, record := range s.banCache.List() {
		if record.BannedUntil == nil || !record.BannedUntil.After(now) {
			continue
		}
		allBannedRecords[record.Key] = BannedRecord{
			Type:     record.Type,
			Username: record.Username,
			IP:       record.IP,
			BanTime:  record.CreatedAt,
		}
	}
	return allBannedRecords
}

// AddFailedCount increments the failure counter and, if the threshold is
// exceeded, triggers a ban.
func (s *Service) AddFailedCount(ctx context.Context, keyName string, recordType model.BanType, IP, username string) {
	failureRecord := model.BanRecord{
		Key:      keyName,
		IP:       IP,
		Username: username,
		Type:     recordType,
	}

	record, err := s.banCache.AddBanRecord(failureRecord)
	if err != nil {
		return
	}

	needBan := false
	switch recordType {
	case model.BanTypeIP:
		if int(record.Count) >= s.getIPMaxFailedTimes(ctx) {
			needBan = true
		}
	case model.BanTypeUser:
		if int(record.Count) >= s.getUserMaxFailedTimes(ctx) {
			needBan = true
		}
	default:
		return
	}

	if needBan {
		s.Ban(ctx, keyName, &BannedRecord{Type: recordType, Username: username, IP: IP})
	}
}

// RecordIPFailure increments the IP failure counter and bans if the threshold
// is reached.  Used for both login failures and invalid session token attempts.
func (s *Service) RecordIPFailure(ctx context.Context, IP string) {
	s.AddFailedCount(ctx, IP, model.BanTypeIP, IP, "")
}
