package service

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "modernc.org/sqlite"
)

// setupValidateEnv 创建源/目标 SQLite 文件库并迁移数据，返回两个连接配置
func setupValidateEnv(t *testing.T) (source, target types.ConnectionConfig, srcDB *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "v_src.db")
	tgtPath := filepath.Join(dir, "v_tgt.db")

	db, err := sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE departments (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE employees (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		dept_id INTEGER NOT NULL,
		salary REAL,
		hired TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := db.Exec(`INSERT INTO departments VALUES (?, ?)`, i, fmt.Sprintf("D%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 50; i++ {
		if _, err := db.Exec(`INSERT INTO employees VALUES (?, ?, ?, ?, ?)`,
			i, fmt.Sprintf("E%03d", i), (i%3)+1, 1000.5+float64(i), "2024-01-02 03:04:05"); err != nil {
			t.Fatal(err)
		}
	}

	// 迁移到目标
	source = types.ConnectionConfig{Type: types.SQLite, Database: srcPath}
	target = types.ConnectionConfig{Type: types.SQLite, Database: tgtPath}
	mig := NewMigrationService()
	report, err := mig.Run(types.MigrationConfig{
		Source: source, Target: target,
		Tables: []string{"departments", "employees"},
	}, func(types.ProgressInfo) {}, func(types.LogEntry) {})
	if err != nil || report.Error != "" {
		t.Fatalf("迁移准备失败: %v / %s", err, report.Error)
	}
	return source, target, db
}

func TestValidateDataAllMatch(t *testing.T) {
	source, target, srcDB := setupValidateEnv(t)
	defer srcDB.Close()

	s := NewSQLService()
	report := s.ValidateData(context.Background(), ValidateRequest{
		Source: source, Target: target, SampleSize: 20,
	})
	if !report.Success {
		for _, tv := range report.Tables {
			t.Errorf("%s: status=%s err=%s fieldMismatch=%v", tv.Table, tv.Status, tv.Error, tv.FieldMismatch)
		}
		t.Fatalf("应全部一致: %s", report.Summary)
	}
	if report.MatchCount != 2 {
		t.Errorf("MatchCount = %d, want 2", report.MatchCount)
	}
}

func TestValidateDataDetectsRowCountMismatch(t *testing.T) {
	source, target, srcDB := setupValidateEnv(t)
	defer srcDB.Close()

	// 源库新增一行（模拟迁移后源库有新写入）
	if _, err := srcDB.Exec(`INSERT INTO departments VALUES (99, 'NEW')`); err != nil {
		t.Fatal(err)
	}

	s := NewSQLService()
	report := s.ValidateData(context.Background(), ValidateRequest{
		Source: source, Target: target,
	})
	if report.Success {
		t.Fatal("行数不一致时应返回不通过")
	}
	found := false
	for _, tv := range report.Tables {
		if tv.Table == "departments" && tv.Status == "row_count_mismatch" {
			found = true
		}
	}
	if !found {
		t.Errorf("departments 应为 row_count_mismatch, got: %+v", report.Tables)
	}
}

func TestValidateDataDetectsFieldValueDiff(t *testing.T) {
	source, target, srcDB := setupValidateEnv(t)
	defer srcDB.Close()

	// 篡改目标库一个字段值（模拟迁移丢精度/写错）
	tgt, err := sql.Open("sqlite", target.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer tgt.Close()
	if _, err := tgt.Exec(`UPDATE employees SET name = 'TAMPERED' WHERE id = 5`); err != nil {
		t.Fatal(err)
	}

	s := NewSQLService()
	report := s.ValidateData(context.Background(), ValidateRequest{
		Source: source, Target: target, SampleSize: 50,
	})
	if report.Success {
		t.Fatal("字段被篡改应返回不通过")
	}
	var emp *TableValidation
	for i := range report.Tables {
		if report.Tables[i].Table == "employees" {
			emp = &report.Tables[i]
		}
	}
	if emp == nil || emp.Status != "sample_mismatch" {
		t.Fatalf("employees 应为 sample_mismatch, got: %+v", emp)
	}
	joined := ""
	for _, m := range emp.FieldMismatch {
		joined += m + "\n"
	}
	if !strings.Contains(joined, "name") || !strings.Contains(joined, "TAMPERED") {
		t.Errorf("应指出 name 字段差异, got: %s", joined)
	}
}

func TestValidateDataMissingTarget(t *testing.T) {
	source, _, srcDB := setupValidateEnv(t)
	defer srcDB.Close()

	s := NewSQLService()
	report := s.ValidateData(context.Background(), ValidateRequest{
		Source: source,
		Target: types.ConnectionConfig{Type: types.SQLite, Database: filepath.Join(t.TempDir(), "empty.db")},
		Tables: []string{"departments"},
	})
	if report.Success {
		t.Fatal("目标缺表应返回不通过")
	}
	if report.Tables[0].Status != "missing_target" {
		t.Errorf("应为 missing_target, got: %s", report.Tables[0].Status)
	}
}

func TestValidateDataBadConnection(t *testing.T) {
	s := NewSQLService()
	report := s.ValidateData(context.Background(), ValidateRequest{
		Source: types.ConnectionConfig{Type: types.SQLite, Database: ""},
		Target: types.ConnectionConfig{Type: types.SQLite, Database: ""},
	})
	if report.Success || report.Summary == "" {
		t.Fatal("连接失败应返回错误摘要")
	}
}
