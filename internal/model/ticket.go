// noinspection GoMixedReceiver
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TicketType 工单类型
type TicketType int

const (
	TicketTypeBug     TicketType = 0 // 缺陷
	TicketTypeFeature TicketType = 1 // 功能
	TicketTypeTask    TicketType = 2 // 任务
)

// TicketPriority 工单优先级
type TicketPriority int

const (
	TicketPriorityLow      TicketPriority = 0
	TicketPriorityMedium   TicketPriority = 1
	TicketPriorityHigh     TicketPriority = 2
	TicketPriorityCritical TicketPriority = 3
)

// TicketStatus 工单状态
type TicketStatus int

const (
	TicketStatusToDo       TicketStatus = 0
	TicketStatusInProgress TicketStatus = 1
	TicketStatusInReview   TicketStatus = 2
	TicketStatusDone       TicketStatus = 3
)

func (t TicketType) String() string {
	switch t {
	case TicketTypeBug:
		return "bug"
	case TicketTypeFeature:
		return "feature"
	case TicketTypeTask:
		return "task"
	default:
		return fmt.Sprintf("ticketType(%d)", t)
	}
}

func ParseTicketType(s string) (TicketType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bug":
		return TicketTypeBug, nil
	case "feature":
		return TicketTypeFeature, nil
	case "task":
		return TicketTypeTask, nil
	default:
		return 0, fmt.Errorf("unknown ticket type: %q", s)
	}
}

func (t TicketType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *TicketType) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid TicketType: %s", string(data))
	}
	parsed, err := ParseTicketType(strVal)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

func (p TicketPriority) String() string {
	switch p {
	case TicketPriorityLow:
		return "low"
	case TicketPriorityMedium:
		return "medium"
	case TicketPriorityHigh:
		return "high"
	case TicketPriorityCritical:
		return "critical"
	default:
		return fmt.Sprintf("ticketPriority(%d)", p)
	}
}

func ParseTicketPriority(s string) (TicketPriority, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return TicketPriorityLow, nil
	case "medium":
		return TicketPriorityMedium, nil
	case "high":
		return TicketPriorityHigh, nil
	case "critical":
		return TicketPriorityCritical, nil
	default:
		return 0, fmt.Errorf("unknown ticket priority: %q", s)
	}
}

func (p TicketPriority) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

func (p *TicketPriority) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid TicketPriority: %s", string(data))
	}
	parsed, err := ParseTicketPriority(strVal)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

func (s TicketStatus) String() string {
	switch s {
	case TicketStatusToDo:
		return "todo"
	case TicketStatusInProgress:
		return "inProgress"
	case TicketStatusInReview:
		return "inReview"
	case TicketStatusDone:
		return "done"
	default:
		return fmt.Sprintf("ticketStatus(%d)", s)
	}
}

func ParseTicketStatus(s string) (TicketStatus, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "todo":
		return TicketStatusToDo, nil
	case "inprogress":
		return TicketStatusInProgress, nil
	case "inreview":
		return TicketStatusInReview, nil
	case "done":
		return TicketStatusDone, nil
	default:
		return 0, fmt.Errorf("unknown ticket status: %q", s)
	}
}

func (s TicketStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *TicketStatus) UnmarshalJSON(data []byte) error {
	var strVal string
	if err := json.Unmarshal(data, &strVal); err != nil {
		return fmt.Errorf("invalid TicketStatus: %s", string(data))
	}
	parsed, err := ParseTicketStatus(strVal)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// Ticket 工单数据模型
type Ticket struct {
	ID          uint       `json:"id" gorm:"primarykey"`
	ProjectID   uint       `json:"projectId" gorm:"not null;index"`
	Type        TicketType `json:"type" gorm:"index"`
	Title       string     `json:"title" gorm:"type:varchar(256);not null"`
	Description string     `json:"description" gorm:"type:text"`

	// 人员关联
	ReporterID uint `json:"reporterId" gorm:"not null;index"`
	AssigneeID uint `json:"assigneeId" gorm:"not null;index"`

	// 时间与优先级
	DueDate  *time.Time     `json:"dueDate,omitempty"`
	Priority TicketPriority `json:"priority" gorm:"index"`
	Status   TicketStatus   `json:"status" gorm:"default:0;index"`

	// 扩展字段（JSON）
	AcceptanceCriteria string `json:"acceptanceCriteria" gorm:"type:text"`
	Attachments        string `json:"attachments" gorm:"type:text"`

	// 看板排序
	SortOrder float64 `json:"sortOrder" gorm:"default:0;index"`

	// 时间戳
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"deletedAt" gorm:"index"`

	// 关联对象
	Project  TicketProject `json:"-" gorm:"foreignKey:ProjectID;constraint:false"`
	Reporter User          `json:"-" gorm:"foreignKey:ReporterID;constraint:false"`
	Assignee User          `json:"-" gorm:"foreignKey:AssigneeID;constraint:false"`
}
