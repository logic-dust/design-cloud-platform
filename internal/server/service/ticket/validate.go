package ticket

import (
	"cmp"
	"context"
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
)

const (
	maxTitleRunes         = 256
	maxDescriptionRunes   = 20000
	maxCriteriaDescRunes  = 2000
	maxChecklistItems     = 100
	maxChecklistTextRunes = 500
	maxAttachments        = 20
	maxAttachmentName     = 255
	maxAttachmentMime     = 127
	maxCommentRunes       = 10000
	maxProjectNameRunes   = 128
	maxProjectDescRunes   = 2000
	// MaxBatchIDs 是批量操作单次允许的 ID 数。
	MaxBatchIDs = 100
)

// AttachmentResolver 校验 username 能否在文件服务中引用 location 内的相对路径 relPath。
type AttachmentResolver func(ctx context.Context, location, relPath, username string) error

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func validateTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", invalid("title is required")
	case runeLen(s) > maxTitleRunes:
		return "", invalid("title is too long")
	}
	return s, nil
}

func validateDescription(s string) error {
	if runeLen(s) > maxDescriptionRunes {
		return invalid("description is too long")
	}
	return nil
}

func parseType(s string) (model.TicketType, error) {
	v, err := model.ParseTicketType(s)
	if err != nil {
		return 0, invalid("invalid type")
	}
	return v, nil
}

func parsePriority(s string) (model.TicketPriority, error) {
	v, err := model.ParseTicketPriority(s)
	if err != nil {
		return 0, invalid("invalid priority")
	}
	return v, nil
}

func parseStatus(s string) (model.TicketStatus, error) {
	v, err := model.ParseTicketStatus(s)
	if err != nil {
		return 0, invalid("invalid status")
	}
	return v, nil
}

func validateCriteria(ac *model.TicketAcceptanceCriteria) error {
	if ac == nil {
		return nil
	}
	if runeLen(ac.Description) > maxCriteriaDescRunes || len(ac.Checklist) > maxChecklistItems {
		return invalid("invalid acceptanceCriteria")
	}
	for i := range ac.Checklist {
		ac.Checklist[i].Text = strings.TrimSpace(ac.Checklist[i].Text)
		if n := runeLen(ac.Checklist[i].Text); n == 0 || n > maxChecklistTextRunes {
			return invalid("invalid acceptanceCriteria")
		}
	}
	if ac.Checklist == nil {
		ac.Checklist = []model.TicketChecklistItem{}
	}
	return nil
}

// validateAttachments 规范化并校验附件引用：路径须为位置内相对路径，
// 且通过文件服务以写入者身份解析成功。
func (s *Service) validateAttachments(ctx context.Context, atts []model.TicketAttachment, username string) error {
	if len(atts) > maxAttachments {
		return invalid("invalid attachment")
	}
	for i := range atts {
		a := &atts[i]
		a.Location = strings.TrimSpace(a.Location)
		a.Name = strings.TrimSpace(a.Name)
		if a.Location == "" || a.Path == "" || strings.HasPrefix(a.Path, "/") || strings.ContainsAny(a.Path, "\x00\r\n\t") {
			return invalid("invalid attachment")
		}
		clean := path.Clean(a.Path)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return invalid("invalid attachment")
		}
		a.Path = clean
		a.Name = cmp.Or(a.Name, path.Base(clean))
		if runeLen(a.Name) > maxAttachmentName || runeLen(a.MimeType) > maxAttachmentMime || a.Size < 0 {
			return invalid("invalid attachment")
		}
		if s.resolveAttachment == nil {
			return invalid("invalid attachment")
		}
		if err := s.resolveAttachment(ctx, a.Location, a.Path, username); err != nil {
			s.logger.Warn("attachment rejected", "location", a.Location, "path", a.Path, "user", username, "error", err)
			return invalid("invalid attachment")
		}
	}
	return nil
}

// validateAssignee 要求指派人为状态正常、无管理范围的普通角色用户。
func (s *Service) validateAssignee(ctx context.Context, id uint) (model.User, error) {
	u, err := s.userRepo.GetAssignable(ctx, id)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return u, invalid("invalid assignee")
		}
		return u, err
	}
	return u, nil
}

// activeProject 要求项目存在、未删除、未归档。
func (s *Service) activeProject(ctx context.Context, id uint) (model.TicketProject, error) {
	if id == 0 {
		return model.TicketProject{}, invalid("projectId is required")
	}
	p, err := s.projectRepo.Get(ctx, id, false)
	if err != nil {
		if errors.Is(err, contract.ErrNotFound) {
			return p, ErrProjectNotFound
		}
		return p, err
	}
	if p.IsArchived {
		return p, ErrProjectArchived
	}
	return p, nil
}

func requireVersion(v *int) (int, error) {
	if v == nil || *v <= 0 {
		return 0, invalid("version is required")
	}
	return *v, nil
}

func validateCommentBody(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("body is required")
	}
	if runeLen(s) > maxCommentRunes {
		return "", invalid("body is too long")
	}
	return s, nil
}
