package ticket

import (
	"context"
	"fmt"
	"time"

	"design-cloud-engine/internal/model"
)

// GetSummary 获取统计概览。projectID 为 0 时表示统计所有项目。
func (s *Service) GetSummary(ctx context.Context, projectID uint) (*SummaryDTO, error) {
	// 1. 按状态统计工单数
	statusCounts, err := s.ticketRepo.CountByStatus(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("count by status: %w", err)
	}

	totalTasks := 0
	completedTasks := 0
	for status, count := range statusCounts {
		totalTasks += count
		if status == model.TicketStatusDone {
			completedTasks = count
		}
	}

	completionRate := 0.0
	if totalTasks > 0 {
		completionRate = float64(completedTasks) / float64(totalTasks) * 100
	}

	// 2. 获取趋势数据（最近 7 天）
	now := time.Now()
	start := now.AddDate(0, 0, -6).Truncate(24 * time.Hour)
	end := now.Truncate(24 * time.Hour).Add(24*time.Hour - time.Second)

	dailyCounts, err := s.ticketRepo.CountByDateRange(ctx, projectID, start, end)
	if err != nil {
		return nil, fmt.Errorf("count by date range: %w", err)
	}

	// 构建趋势数据
	dailyMap := make(map[string]int)
	for _, dc := range dailyCounts {
		dailyMap[dc.Date.Format("2006-01-02")] = dc.Count
	}

	trends := make([]TrendPoint, 0, 7)
	for i := range 7 {
		date := start.AddDate(0, 0, i)
		dateStr := date.Format("2006-01-02")
		count := dailyMap[dateStr]

		// 截至当天的完成率
		trends = append(trends, TrendPoint{
			Date:           date,
			TaskCount:      count,
			CompletionRate: completionRate,
		})
	}

	return &SummaryDTO{
		TotalTasks:            totalTasks,
		CompletedTasks:        completedTasks,
		OverallCompletionRate: completionRate,
		Trends:                trends,
	}, nil
}
