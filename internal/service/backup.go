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

// isValidBackupName 校验表名确实是本工具生成的备份表，防止恶意/误传表名
// 直接触发 DROP/RENAME（旧实现任意表名都会被执行，可删除生产库任意表）
func isValidBackupName(backupName string) bool {
	return backupNameRe.MatchString(backupName)
}

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
	if !isValidBackupName(backupName) {
		return "", fmt.Errorf("非法的备份表名: %s（备份表名应形如 _bak_原表名_20260830_153022）", backupName)
	}
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
		if !isValidBackupName(backupName) {
			result.FailedCount++
			result.FailedItems = append(result.FailedItems, fmt.Sprintf("%s: 非法的备份表名", backupName))
			continue
		}
		originalName := ParseBackupName(backupName)
		if err := adapter.RestoreFromBackup(ctx, backupName, originalName); err != nil {
			result.FailedCount++
			// 带上具体原因，否则前端只能看到名字列表，无法判断哪张表为何失败
			result.FailedItems = append(result.FailedItems, fmt.Sprintf("%s: %v", backupName, err))
		} else {
			result.SuccessCount++
		}
	}

	return result
}

// DeleteBackup 删除一个备份表（确认迁移无误后清理）
func (s *BackupService) DeleteBackup(ctx context.Context, config types.ConnectionConfig, backupName string) error {
	if !isValidBackupName(backupName) {
		return fmt.Errorf("非法的备份表名: %s（备份表名应形如 _bak_原表名_20260830_153022）", backupName)
	}
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
