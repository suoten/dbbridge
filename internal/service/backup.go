package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	types "dbbridge/pkg"
)

// BackupService 备份与回滚服务
type BackupService struct{}

// NewBackupService 创建备份服务
func NewBackupService() *BackupService {
	return &BackupService{}
}

// BackupTableItem 备份表信息（前端展示用）
type BackupTableItem struct {
	BackupName   string `json:"backupName"`
	OriginalName string `json:"originalName"`
}

// backupNameRe 备份表名格式: _bak_原表名_20260830_153022
var backupNameRe = regexp.MustCompile(`^_bak_(.+)_\d{8}_\d{6}$`)

// ParseBackupName 从备份表名解析出原表名（正则匹配时间戳后缀，避免数下划线的歧义）
func ParseBackupName(backupName string) string {
	if m := backupNameRe.FindStringSubmatch(backupName); m != nil {
		return m[1]
	}
	return strings.TrimPrefix(backupName, "_bak_")
}

func newAdapter(config types.ConnectionConfig) (types.DatabaseAdapter, error) {
	adapter := types.NewAdapter(config.Type)
	if adapter == nil {
		return nil, fmt.Errorf("不支持的数据库类型: %s", config.Type)
	}
	return adapter, nil
}

// GetBackupTables 获取目标库中所有备份表（以 _bak_ 开头的表）
func (s *BackupService) GetBackupTables(ctx context.Context, config types.ConnectionConfig) ([]BackupTableItem, error) {
	adapter, err := newAdapter(config)
	if err != nil {
		return nil, err
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		return nil, err
	}

	allTables, err := adapter.GetTables(ctx)
	if err != nil {
		return nil, err
	}

	var backups []BackupTableItem
	for _, t := range allTables {
		if strings.HasPrefix(t.Name, "_bak_") {
			backups = append(backups, BackupTableItem{
				BackupName:   t.Name,
				OriginalName: ParseBackupName(t.Name),
			})
		}
	}
	return backups, nil
}

// RestoreTable 从备份恢复单张表（删除当前同名表，将备份表重命名回原表名）
func (s *BackupService) RestoreTable(ctx context.Context, config types.ConnectionConfig, backupName string) (string, error) {
	adapter, err := newAdapter(config)
	if err != nil {
		return "", err
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		return "", err
	}

	originalName := ParseBackupName(backupName)
	if err := adapter.RestoreFromBackup(ctx, backupName, originalName); err != nil {
		return "", err
	}
	return originalName, nil
}

// RestoreResult 批量回滚结果
type RestoreResult struct {
	SuccessCount int      `json:"successCount"`
	FailedCount  int      `json:"failedCount"`
	FailedItems  []string `json:"failedItems,omitempty"`
}

// RestoreAllTables 批量从备份恢复多张表
func (s *BackupService) RestoreAllTables(ctx context.Context, config types.ConnectionConfig, backupNames []string) RestoreResult {
	result := RestoreResult{}

	adapter, err := newAdapter(config)
	if err != nil {
		result.FailedCount = len(backupNames)
		result.FailedItems = backupNames
		return result
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		result.FailedCount = len(backupNames)
		result.FailedItems = backupNames
		return result
	}

	for _, backupName := range backupNames {
		originalName := ParseBackupName(backupName)
		if err := adapter.RestoreFromBackup(ctx, backupName, originalName); err != nil {
			result.FailedCount++
			result.FailedItems = append(result.FailedItems, backupName)
		} else {
			result.SuccessCount++
		}
	}

	return result
}

// DeleteBackup 删除一个备份表（确认迁移无误后清理）
func (s *BackupService) DeleteBackup(ctx context.Context, config types.ConnectionConfig, backupName string) error {
	adapter, err := newAdapter(config)
	if err != nil {
		return err
	}
	defer adapter.Close()

	if err := adapter.Connect(ctx, config); err != nil {
		return err
	}
	return adapter.DropBackup(ctx, backupName)
}
