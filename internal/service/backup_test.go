// Package service - 备份服务安全回归测试
//
// 针对 P1 级安全修复的回归：
//   - RestoreTable/RestoreAllTables/DeleteBackup 必须校验备份表名格式，
//     防止任意表名触发 DROP/RENAME（旧实现可删除生产库任意表）
//   - FailedItems 必须携带失败原因
package service

import (
	"context"
	"strings"
	"testing"

	"dbbridge/internal/adapter/sqlite"
	types "dbbridge/pkg"
)

// newTestAdapter 创建内存 SQLite 适配器并建一张业务表
func newTestAdapter(t *testing.T) types.DatabaseAdapter {
	t.Helper()
	a := &sqlite.Adapter{}
	if err := a.Connect(context.Background(), types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.ExecContext(context.Background(),
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return a
}

// TestDeleteBackupRejectsNonBackupName 非备份表名必须被拒绝，绝不能 DROP 任意表
func TestDeleteBackupRejectsNonBackupName(t *testing.T) {
	s := NewBackupService()
	config := types.ConnectionConfig{Type: types.SQLite, Database: ":memory:"}

	// 连接层无法共享内存库，直接验证服务层的格式校验逻辑（不需要真实库连接）
	err := s.DeleteBackup(context.Background(), config, "users")
	if err == nil {
		t.Fatal("DeleteBackup('users') 应报错：不是合法备份表名")
	}
	if !strings.Contains(err.Error(), "非法的备份表名") {
		t.Errorf("错误信息应说明原因，got: %v", err)
	}

	// 恶意构造：试图让 RestoreFromBackup DROP 其他表
	err = s.DeleteBackup(context.Background(), config, "important_data")
	if err == nil {
		t.Fatal("DeleteBackup('important_data') 应报错")
	}

	// 合法格式（任意原表名 + 时间戳）应通过格式校验（不要求表真实存在）
	err = s.DeleteBackup(context.Background(), config, "_bak_users_20260915_120000")
	if err != nil && strings.Contains(err.Error(), "非法的备份表名") {
		t.Errorf("合法格式的备份名不应被格式校验拒绝: %v", err)
	}
}

// TestRestoreTableRejectsNonBackupName RestoreTable 同样必须校验
func TestRestoreTableRejectsNonBackupName(t *testing.T) {
	s := NewBackupService()
	config := types.ConnectionConfig{Type: types.SQLite, Database: ":memory:"}

	_, err := s.RestoreTable(context.Background(), config, "production_table")
	if err == nil || !strings.Contains(err.Error(), "非法的备份表名") {
		t.Fatalf("RestoreTable 应拒绝非法备份名, got: %v", err)
	}
}

// TestRestoreAllTablesReportsFailureReason 批量恢复的 FailedItems 必须带原因
func TestRestoreAllTablesReportsFailureReason(t *testing.T) {
	s := NewBackupService()
	config := types.ConnectionConfig{Type: types.SQLite, Database: ":memory:"}

	result := s.RestoreAllTables(context.Background(), config, []string{"not_a_backup"})
	if result.FailedCount != 1 {
		t.Fatalf("FailedCount = %d, want 1", result.FailedCount)
	}
	if len(result.FailedItems) != 1 || !strings.Contains(result.FailedItems[0], "非法的备份表名") {
		t.Fatalf("FailedItems 应包含失败原因, got: %v", result.FailedItems)
	}
}

// TestSQLiteBackupVisibleInTableList 备份表必须出现在 GetTables 结果中，
// 否则备份管理页面列表为空（旧实现 NOT LIKE '\_%' 把下划线开头表全部过滤掉了）
func TestSQLiteBackupVisibleInTableList(t *testing.T) {
	a := newTestAdapter(t)
	ctx := context.Background()

	backupName, err := a.BackupTable(ctx, "users")
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}

	tables, err := a.GetTables(ctx)
	if err != nil {
		t.Fatalf("GetTables 失败: %v", err)
	}
	found := false
	for _, tb := range tables {
		if tb.Name == backupName {
			found = true
		}
	}
	if !found {
		t.Errorf("备份表 %s 未出现在 GetTables 结果中（备份管理页将看不到它）", backupName)
	}
}
