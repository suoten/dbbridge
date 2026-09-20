//go:build integration

// Package integration 真实数据库集成验证（生产级把关）。
// 需先启动容器（scripts/integration-env.ps1），然后：
//
//	go test -tags integration ./internal/integration/ -c -o test_integration.exe
//	./test_integration.exe "-test.v"
//
// 覆盖矩阵（全部走与生产一致的 orchestrator.Run 路径）：
//   - MySQL 8 / PostgreSQL 16 / MSSQL 2022 三目标 × Schema 映射三轮迁移
//     （首轮建表 → 备份轮 → 先删后建轮），逐轮校验行数精确一致、无重复
//   - 未映射回归轮（确认裸表名路径行为不变）
//   - 按表覆盖优先于全局默认（users→special、orders→ods 跨 schema 落位）
//   - 延迟外键跨 schema 补建（FKErrors 必须为空）
//   - 备份→恢复往返（RENAME 目标侧裸名/逐段限定等修复点）
//   - DataOnly + 映射（MSSQL GetTableSchema schema 感知修复点）
//   - 自增序列修复（迁移后无 id 插入不冲突）
//   - 表空间 DDL（PG 真 TABLESPACE / MSSQL ON [PRIMARY]）
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/microsoft/go-mssqldb"

	_ "dbbridge/internal/adapter/mssql"
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/oracle"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"

	"dbbridge/internal/orchestrator"
	types "dbbridge/pkg"
)

const (
	srcUsers  = 50
	srcOrders = 200
)

// ====================================================================
// 源库（SQLite 文件库）：orders.user_id → users.id 外键链
// ====================================================================

func buildSQLiteSource(t *testing.T) types.ConnectionConfig {
	t.Helper()
	dir := t.TempDir()
	cfg := types.ConnectionConfig{Type: types.SQLite, Database: filepath.Join(dir, "src.db")}
	a := connect(t, cfg)
	exec(t, a,
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL REFERENCES users(id), amount REAL)`,
	)
	for i := 1; i <= srcUsers; i++ {
		exec(t, a, fmt.Sprintf(`INSERT INTO users(name) VALUES ('user_%d')`, i))
	}
	for i := 1; i <= srcOrders; i++ {
		uid := (i-1)%srcUsers + 1
		exec(t, a, fmt.Sprintf(`INSERT INTO orders(user_id, amount) VALUES (%d, %d.5)`, uid, i))
	}
	return cfg
}

// ====================================================================
// 通用工具
// ====================================================================

func connect(t *testing.T, cfg types.ConnectionConfig) types.DatabaseAdapter {
	t.Helper()
	a := types.NewAdapter(cfg.Type)
	if a == nil {
		t.Fatalf("不支持的数据库类型: %s", cfg.Type)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Connect(ctx, cfg); err != nil {
		t.Fatalf("连接 %s(%s:%d) 失败: %v", cfg.Type, cfg.Host, cfg.Port, err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func exec(t *testing.T, a types.DatabaseAdapter, stmts ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, s := range stmts {
		if err := a.ExecContext(ctx, s); err != nil {
			t.Fatalf("执行 SQL 失败: %v\nSQL: %s", err, s)
		}
	}
}

func runMigration(t *testing.T, src, dst types.ConnectionConfig, tables []string, mutate func(*types.MigrationConfig)) *types.MigrationReport {
	t.Helper()
	cfg := types.MigrationConfig{
		Source:    src,
		Target:    dst,
		Tables:    tables,
		BatchSize: 1000,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	orch := orchestrator.NewOrchestrator(cfg, nil, nil)
	report, err := orch.Run(context.Background())
	if err != nil {
		t.Fatalf("迁移失败: %v\n报告: %+v", err, report)
	}
	if report.Error != "" {
		t.Fatalf("迁移报告携带错误: %s", report.Error)
	}
	return report
}

func assertReportClean(t *testing.T, report *types.MigrationReport) {
	t.Helper()
	if len(report.FKErrors) > 0 {
		t.Fatalf("外键补建存在错误: %v", report.FKErrors)
	}
	for _, tr := range report.TableDetails {
		if tr.Status != "success" {
			t.Fatalf("表 %s 状态 %s: %s", tr.TableName, tr.Status, tr.Error)
		}
	}
}

// rawDB 目标库原生 database/sql 连接（仅测试校验用，与适配器驱动同源）
type rawDB struct {
	t  *testing.T
	db *sql.DB
}

func openRaw(t *testing.T, driver, dsn string) *rawDB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("打开 %s 校验连接失败: %v", driver, err)
	}
	t.Cleanup(func() { db.Close() })
	return &rawDB{t: t, db: db}
}

func (r *rawDB) count(query string, args ...any) int64 {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var n int64
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		r.t.Fatalf("计数查询失败: %v\nSQL: %s", err, query)
	}
	return n
}

func (r *rawDB) countRows(table string) int64 {
	return r.count(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table))
}

func (r *rawDB) exec(query string) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := r.db.ExecContext(ctx, query); err != nil {
		r.t.Fatalf("校验连接执行失败: %v\nSQL: %s", err, query)
	}
}

func (r *rawDB) mustZero(name string, got int64) {
	r.t.Helper()
	if got != 0 {
		r.t.Fatalf("%s 应为 0，实际 %d", name, got)
	}
}

// qualifiedFrom 限定备份表名转校验用 FROM 片段（PG 双引号风格）："ods._bak_x" → ods."_bak_x"
func qualifiedFrom(qualified string) string {
	for i := 0; i < len(qualified); i++ {
		if qualified[i] == '.' {
			return qualified[:i] + `."` + qualified[i+1:] + `"`
		}
	}
	return `"` + qualified + `"`
}

// qualifiedFromBacktick 限定备份表名转 MySQL 反引号风格：ods._bak_x → `ods`.`_bak_x`
func qualifiedFromBacktick(qualified string) string {
	for i := 0; i < len(qualified); i++ {
		if qualified[i] == '.' {
			return "`" + qualified[:i] + "`.`" + qualified[i+1:] + "`"
		}
	}
	return "`" + qualified + "`"
}

func (r *rawDB) mustEqual(name string, want, got int64) {
	r.t.Helper()
	if want != got {
		r.t.Fatalf("%s = %d，期望 %d", name, got, want)
	}
}

func (r *rawDB) mustContain(name, got, substr string) {
	r.t.Helper()
	if !strings.Contains(got, substr) {
		r.t.Fatalf("%s = %q，应包含 %q", name, got, substr)
	}
}
