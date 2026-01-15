package service

import (
	"fmt"
	"time"
)

// formatRelativeTime 将时间转换为相对时间描述（如：2小时，3天）
func formatRelativeTime(t time.Time) string {
	duration := time.Since(t)

	if duration.Seconds() < 60 {
		return "刚刚"
	}
	if duration.Minutes() < 60 {
		return fmt.Sprintf("%d分钟", int(duration.Minutes()))
	}
	if duration.Hours() < 24 {
		return fmt.Sprintf("%d小时", int(duration.Hours()))
	}
	if duration.Hours() < 24*30 {
		return fmt.Sprintf("%d天", int(duration.Hours()/24))
	}
	return t.Format("2006-01-02")
}
