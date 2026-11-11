package main

import (
	"fmt"

	"github.com/kaxi9527/WorkShelf/internal/autoorganize"
)

func (s *ShelfService) reportAutoOrganize(operation autoorganize.Operation, restored bool) {
	s.autoOrganize.mu.Lock()
	report := s.autoOrganize.report
	s.autoOrganize.mu.Unlock()
	if report != nil {
		report(operation, restored)
	}
}

func autoReportText(operation autoorganize.Operation, restored bool) (string, string) {
	if restored {
		if operation.Status != "restored" {
			return "文件恢复未完成", fmt.Sprintf("%s：%d 项未恢复，请在序屿查看结果。", operation.Project, operation.Restore.Failed+operation.Restore.Skipped)
		}
		return "文件已恢复", fmt.Sprintf("%s：已恢复 %d 项，可在序屿查看记录。", operation.Project, operation.Restore.Restored)
	}
	if operation.Status == "partial" {
		return "项目部分整理完成", fmt.Sprintf("%s：已收纳 %d 项，请在序屿查看结果。", operation.Project, operation.Summary.Moved+operation.Summary.Copied)
	}
	return "项目整理完成", fmt.Sprintf("%s：已收纳 %d 项，可在序屿查看或撤销。", operation.Project, operation.Summary.Moved+operation.Summary.Copied)
}
