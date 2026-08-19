package file

import (
	"context"
	"io"
)

type uploadLimitReader struct {
	reader    io.Reader
	remain    int64
	tooLarge  bool
	truncated bool // true if the underlying reader returned EOF before the limit was reached
}

func newUploadLimitReader(reader io.Reader, limit int64) *uploadLimitReader {
	return &uploadLimitReader{reader: reader, remain: limit}
}

func (r *uploadLimitReader) Read(p []byte) (int, error) {
	if r.remain < 0 {
		r.tooLarge = true
		return 0, ErrUploadTooLarge
	}
	if int64(len(p)) > r.remain {
		p = p[:r.remain]
	}
	// remain == 0: 已读完限额字节，探测底层 reader 是否还有数据。
	// 有数据则超限；到达 EOF 则文件恰好合法。
	if len(p) == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			r.remain = -1
			r.tooLarge = true
			return 0, ErrUploadTooLarge
		}
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.remain -= int64(n)
	if r.remain < 0 {
		r.tooLarge = true
		return 0, ErrUploadTooLarge
	}
	if err != nil && r.remain > 0 {
		r.truncated = true
	}
	return n, err
}

func (r *uploadLimitReader) TooLarge() bool {
	return r.tooLarge
}

// Truncated returns true if the body was shorter than the declared Content-Length.
func (r *uploadLimitReader) Truncated() bool {
	return r.truncated
}

// CheckUploadPermission returns nil if the user and global settings allow uploading.
func (s *Service) CheckUploadPermission(ctx context.Context, requestUser string) error {
	fileSetting, err := s.settingsSvc.GetFileSettings(ctx)
	if err != nil {
		s.logger.Warn("failed to load file settings for upload permission", "error", err)
		return ErrNoUploadPermission
	}
	if !fileSetting.Enabled {
		return ErrFileDisabled
	}
	if !fileSetting.UploadEnabled {
		return ErrNoUploadPermission
	}

	userRecord, err := s.userSvc.GetDBUser(ctx, requestUser)
	if err != nil {
		s.logger.Warn("failed to load user for upload permission", "user", requestUser, "error", err)
		return ErrNoUploadPermission
	}
	if !userRecord.UploadAccess {
		return ErrNoUploadPermission
	}
	return nil
}
