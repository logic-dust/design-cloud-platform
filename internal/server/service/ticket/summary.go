package ticket

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"design-cloud-engine/internal/model"
)

const (
	// DefaultSummaryDays 与 MaxSummaryDays 限定统计趋势的天数。
	DefaultSummaryDays = 7
	MaxSummaryDays     = 90
)

// GetSummary 统计未删除工单；projectID 为 0 表示全部项目。
// 趋势按 loc 的自然日切分，最后一天为今天。
func (s *Service) GetSummary(ctx context.Context, projectID uint, days int, loc *time.Location) (SummaryDTO, error) {
	counts, err := s.ticketRepo.CountByStatus(ctx, projectID)
	if err != nil {
		return SummaryDTO{}, fmt.Errorf("count by status: %w", err)
	}
	out := SummaryDTO{Timezone: ZoneName(loc), ByStatus: map[string]int{}}
	for _, st := range []model.TicketStatus{model.TicketStatusToDo, model.TicketStatusInProgress, model.TicketStatusInReview, model.TicketStatusDone} {
		out.ByStatus[st.String()] = counts[st]
		out.TotalTasks += counts[st]
	}
	out.CompletedTasks = counts[model.TicketStatusDone]
	if out.TotalTasks > 0 {
		out.OverallCompletionRate = float64(out.CompletedTasks) / float64(out.TotalTasks) * 100
	}

	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)

	index := make(map[string]int, days)
	out.Trends = make([]TrendPoint, days)
	for i := range days {
		d := start.AddDate(0, 0, i).Format(time.DateOnly)
		out.Trends[i].Date = d
		index[d] = i
	}
	for column, add := range map[string]func(*TrendPoint){
		"created_at":   func(p *TrendPoint) { p.Created++ },
		"completed_at": func(p *TrendPoint) { p.Completed++ },
	} {
		// 以服务器本地时间传参，与库中时间值的写入方式一致。
		times, err := s.ticketRepo.TimesInRange(ctx, column, projectID, start.In(time.Local), end.In(time.Local))
		if err != nil {
			return SummaryDTO{}, fmt.Errorf("load %s: %w", column, err)
		}
		for _, t := range times {
			if i, ok := index[t.In(loc).Format(time.DateOnly)]; ok {
				add(&out.Trends[i])
			}
		}
	}
	return out, nil
}

// ZoneName 返回时区的 IANA 名称；服务器本地时区由 TZ 环境变量或 /etc/localtime 推断。
func ZoneName(loc *time.Location) string {
	if loc != time.Local {
		return loc.String()
	}
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		return tz
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			return name
		}
	}
	return "Local"
}
