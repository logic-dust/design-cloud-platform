package ticket

import (
	"encoding/json"
	"strconv"
	"time"
)

// --- 请求 DTO ---

// CreateTicketRequest 创建工单
type CreateTicketRequest struct {
	ProjectID          uint                   `json:"projectId" binding:"required"`
	Type               int                    `json:"type" binding:"min=0,max=2"`
	Title              string                 `json:"title" binding:"required,max=256"`
	Description        string                 `json:"description"`
	AssigneeID         uint                   `json:"assigneeId" binding:"required"`
	DueDate            *time.Time             `json:"dueDate,omitempty"`
	Priority           int                    `json:"priority" binding:"min=0,max=3"`
	Status             int                    `json:"status" binding:"min=0,max=3"`
	AcceptanceCriteria *AcceptanceCriteriaDTO `json:"acceptanceCriteria,omitempty"`
	Attachments        []AttachmentDTO        `json:"attachments"`
}

// UpdateTicketRequest 更新工单
type UpdateTicketRequest struct {
	Type               *int                   `json:"type,omitempty"`
	Title              *string                `json:"title,omitempty"`
	Description        *string                `json:"description,omitempty"`
	AssigneeID         *uint                  `json:"assigneeId,omitempty"`
	DueDate            *time.Time             `json:"dueDate,omitempty"`
	Priority           *int                   `json:"priority,omitempty"`
	Status             *int                   `json:"status,omitempty"`
	AcceptanceCriteria *AcceptanceCriteriaDTO `json:"acceptanceCriteria,omitempty"`
	Attachments        *[]AttachmentDTO       `json:"attachments,omitempty"`
	SortOrder          *float64               `json:"sortOrder,omitempty"`
}

// Filter 工单列表过滤参数
type Filter struct {
	ProjectID      *uint `json:"projectId,omitempty"`
	ReporterID     *uint `json:"reporterId,omitempty"`
	AssigneeID     *uint `json:"assigneeId,omitempty"`
	Type           *int  `json:"type,omitempty"`
	Status         *int  `json:"status,omitempty"`
	Priority       *int  `json:"priority,omitempty"`
	Limit          int   `json:"limit,omitempty"`
	Offset         int   `json:"offset,omitempty"`
	IncludeDeleted bool  `json:"includeDeleted,omitempty"`
}

// AcceptanceCriteriaDTO 验收标准
type AcceptanceCriteriaDTO struct {
	Description string   `json:"description"`
	Checklist   []string `json:"checklist"`
}

// AttachmentDTO 附件
type AttachmentDTO struct {
	FileName string `json:"fileName"`
	FileSize int64  `json:"fileSize"`
	FilePath string `json:"filePath"`
	MimeType string `json:"mimeType"`
	UploadAt string `json:"uploadAt"`
}

// CreateProjectRequest 创建项目
type CreateProjectRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Description string `json:"description"`
}

// UpdateProjectRequest 更新项目
type UpdateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ArchiveProjectRequest 归档/取消归档项目
type ArchiveProjectRequest struct {
	Archived bool `json:"archived"`
}

// --- 响应定义 ---

// PagedResponse 分页响应
type PagedResponse struct {
	Items      any   `json:"items"`
	TotalCount int64 `json:"totalCount"`
	Limit      int   `json:"limit"`
	Offset     int   `json:"offset"`
	HasMore    bool  `json:"hasMore"`
}

// SummaryDTO 概览统计
type SummaryDTO struct {
	TotalTasks            int          `json:"totalTasks"`
	CompletedTasks        int          `json:"completedTasks"`
	OverallCompletionRate float64      `json:"overallCompletionRate"`
	Trends                []TrendPoint `json:"trends"`
}

// TrendPoint 趋势数据点
type TrendPoint struct {
	Date           time.Time `json:"date"`
	TaskCount      int       `json:"taskCount"`
	CompletionRate float64   `json:"completionRate"`
}

// serializeAcceptanceCriteria 将 DTO 序列化为 JSON 字符串
func serializeAcceptanceCriteria(criteria *AcceptanceCriteriaDTO) string {
	if criteria == nil {
		return ""
	}
	data, err := json.Marshal(criteria)
	if err != nil {
		return ""
	}
	return string(data)
}

// serializeAttachments 将附件 DTO 切片序列化为 JSON 字符串
func serializeAttachments(attachments []AttachmentDTO) string {
	if len(attachments) == 0 {
		return ""
	}
	data, err := json.Marshal(attachments)
	if err != nil {
		return ""
	}
	return string(data)
}

// formatUint 将 uint 格式化为字符串
func formatUint(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}

// formatUserIDs 将用户 ID 集合格式化为字符串切片（去重），用于事件推送目标。
// formatUserIDs 格式化用户列表（去重），可排除操作者。
// 操作者走请求-响应链路（response 已反馈结果），无需推送——只推被动方。
// 寻址：hub 按 username 路由，传入即为 username 列表。
func formatUserIDs(exclude string, usernames ...string) []string {
	seen := make(map[string]struct{}, len(usernames))
	out := make([]string, 0, len(usernames))
	for _, u := range usernames {
		if u == "" || u == exclude {
			continue
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}
