package history

import (
	"encoding/json"

	types "dbbridge/pkg"
)

// BuildRecord 从迁移结果构建历史记录（app.go 桌面模式与 webserver Web 模式共用）。
// report.Error 或 migErr 非空时按 TablesSuccess 判定 partial/failed，不写假 success。
// 序列化失败时对应字段留空（记录仍保存，与原 app.go 行为一致）。
func BuildRecord(source, target types.ConnectionConfig, report *types.MigrationReport, migErr error) MigrationRecord {
	status := "success"
	// 迁移被拒绝/中止时 orchestrator 返回 (report, err)，report.Error 已带原因；
	// 也有 err==nil 但 report.Error 非空的路径（如取消），都要落为失败
	if report.Error != "" {
		if report.TablesSuccess > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	} else if migErr != nil {
		if report.TablesSuccess > 0 {
			status = "partial"
		} else {
			status = "failed"
		}
	}

	// SQLite 是文件库，没有主机概念
	sourceHost := source.Host
	if source.Type == "sqlite" {
		sourceHost = "(file)"
	}
	targetHost := target.Host
	if target.Type == "sqlite" {
		targetHost = "(file)"
	}
	var tableDetailsJSON, backupsJSON json.RawMessage
	if len(report.TableDetails) > 0 {
		if data, err := json.Marshal(report.TableDetails); err == nil {
			tableDetailsJSON = data
		}
	}
	if len(report.Backups) > 0 {
		if data, err := json.Marshal(report.Backups); err == nil {
			backupsJSON = data
		}
	}

	return MigrationRecord{
		StartTime:     report.StartTime,
		EndTime:       report.EndTime,
		Duration:      report.Duration,
		SourceType:    string(source.Type),
		SourceHost:    sourceHost,
		SourceDB:      source.Database,
		TargetType:    string(target.Type),
		TargetHost:    targetHost,
		TargetDB:      target.Database,
		TablesTotal:   report.TablesTotal,
		TablesSuccess: report.TablesSuccess,
		TablesFailed:  report.TablesFailed,
		TotalRows:     report.TotalRows,
		Status:        status,
		TableDetails:  tableDetailsJSON,
		Backups:       backupsJSON,
	}
}
