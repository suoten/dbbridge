package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	types "dbbridge/pkg"
)

// setupGuideEnv 创建一个 SQLite 源库供指南生成使用
func setupGuideEnv(t *testing.T) types.ConnectionConfig {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "g_src.db")
	db, err := sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE products (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		price REAL,
		status TEXT DEFAULT 'active',
		created_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	return types.ConnectionConfig{Type: types.SQLite, Database: srcPath}
}

func TestGenerateMigrationGuideSQLiteToMySQL(t *testing.T) {
	src := setupGuideEnv(t)
	r := (&SQLService{}).GenerateMigrationGuide(context.Background(), src, "mysql", nil)
	if !r.Success {
		t.Fatalf("指南生成失败: %s", r.Error)
	}
	if r.Tables != 1 {
		t.Errorf("应评估 1 张表，实际 %d", r.Tables)
	}
	if len(r.Changes) == 0 {
		t.Errorf("应产出类型映射差异: %+v", r)
	}
	// SQLite 自增主键 → MySQL：应保留自增语义
	if len(r.Items) == 0 {
		t.Errorf("应包含方言提示条目")
	}
	if r.Summary == "" {
		t.Errorf("摘要不应为空")
	}
}

func TestGenerateMigrationGuideSameDialect(t *testing.T) {
	src := setupGuideEnv(t)
	r := (&SQLService{}).GenerateMigrationGuide(context.Background(), src, "sqlite", nil)
	if r.Success {
		t.Errorf("同方言应报错: %+v", r)
	}
}

func TestGenerateMigrationGuideBadDialect(t *testing.T) {
	src := setupGuideEnv(t)
	r := (&SQLService{}).GenerateMigrationGuide(context.Background(), src, "oracle99", nil)
	if r.Success {
		t.Errorf("非法方言应报错: %+v", r)
	}
}

func TestGuideBadConnection(t *testing.T) {
	// 构建一个父目录不存在的路径，SQLite 无法创建文件（跨平台可靠失败）
	badDir := filepath.Join(t.TempDir(), "no_such_dir")
	badPath := filepath.Join(badDir, "nope.db")
	// 确保父目录确实不存在（t.TempDir 创建了根目录，但子目录不创建）
	if _, err := os.Stat(badDir); !os.IsNotExist(err) {
		t.Skipf("无法构造不存在的父目录，跳过: %s", badDir)
	}
	src := types.ConnectionConfig{Type: types.SQLite, Database: badPath}
	r := (&SQLService{}).GenerateMigrationGuide(context.Background(), src, "postgres", nil)
	if r.Success {
		t.Errorf("连接失败应报错: %+v", r)
	}
}
