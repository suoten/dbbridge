// Package orchestr_test - PG 分区表迁移端到端测试（嵌入式 PostgreSQL）
//
// 验证用户反馈场景：源库 origin_message PARTITION BY RANGE 带年份分区，
// 迁移后目标库应保持分区层级（父表 relkind='p' + 分区边界一致），
// 数据不重复（旧行为父表+子分区各迁一遍 = 每行 2 份）。
// 同时覆盖：先删后建复跑、旧版本平铺迁移遗留脏数据的备份兼容场景。
//
// 需联网下载 PG 二进制，设 DBBRIDGE_TEST_EMBEDDED_PG=1 启用。
package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	_ "dbbridge/internal/adapter/postgres" // 注册 postgres 适配器
	types "dbbridge/pkg"
)

// setupPartitionPG 启动嵌入式 PG 并返回管理连接（端口 54331，与插件测试错开）
func setupPartitionPG(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	runtimePath := filepath.Join(os.Getenv("ProgramData"), "DBBridgeEmbeddedPGPartTest")
	_ = os.RemoveAll(runtimePath)

	ep := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("postgres").
		Password("pgpass").
		Database("part_src").
		Port(54331).
		Locale("C").
		RuntimePath(runtimePath))
	if err := ep.Start(); err != nil {
		t.Fatalf("启动嵌入式 PG 失败: %v", err)
	}
	admin, err := sql.Open("postgres", "host=127.0.0.1 port=54331 user=postgres password=pgpass dbname=part_src sslmode=disable")
	if err != nil {
		_ = ep.Stop()
		t.Fatalf("打开管理连接失败: %v", err)
	}
	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE "part_tgt"`); err != nil {
		_ = admin.Close()
		_ = ep.Stop()
		t.Fatalf("创建目标库失败: %v", err)
	}
	cleanup := func() {
		_ = admin.Close()
		_ = ep.Stop()
		_ = os.RemoveAll(runtimePath)
	}
	return admin, cleanup
}

func dbURL(db string) string {
	return "host=127.0.0.1 port=54331 user=postgres password=pgpass dbname=" + db + " sslmode=disable"
}

// seedPartitionSource 源库建分区父表 + 2 个 RANGE 分区 + 行数据 + 一张普通表
func seedPartitionSource(t *testing.T, admin *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE origin_message (
			id bigint GENERATED ALWAYS AS IDENTITY,
			year int NOT NULL,
			content text,
			PRIMARY KEY (id, year)
		) PARTITION BY RANGE (year)`,
		`CREATE TABLE origin_message_2024 PARTITION OF origin_message FOR VALUES FROM (2024) TO (2025)`,
		`CREATE TABLE origin_message_2025 PARTITION OF origin_message FOR VALUES FROM (2025) TO (2026)`,
		`CREATE INDEX idx_origin_msg_year ON origin_message (year)`,
		`CREATE TABLE plain_table (id int PRIMARY KEY, note text)`,
	}
	for i := 1; i <= 30; i++ {
		year := 2024 + i%2 // 15 行落 2024 分区，15 行落 2025 分区
		stmts = append(stmts, fmt.Sprintf(
			`INSERT INTO origin_message (year, content) VALUES (%d, 'msg-%d')`, year, i))
	}
	stmts = append(stmts, `INSERT INTO plain_table (id, note) VALUES (1, 'n1'), (2, 'n2')`)
	for _, s := range stmts {
		if _, err := admin.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("源库准备失败: %v (SQL: %s)", err, s)
		}
	}
}

func runPGMigration(t *testing.T, backup bool) *types.MigrationReport {
	t.Helper()
	config := types.MigrationConfig{
		Source: types.ConnectionConfig{
			Type: "postgres", Host: "127.0.0.1", Port: 54331,
			Username: "postgres", Password: "pgpass", Database: "part_src",
		},
		Target: types.ConnectionConfig{
			Type: "postgres", Host: "127.0.0.1", Port: 54331,
			Username: "postgres", Password: "pgpass", Database: "part_tgt",
		},
		BatchSize:      100,
		DropIfExists:   true,
		BackupBefore:   backup,
		MigratePlugins: false,
	}
	orch := NewOrchestrator(config,
		func(types.ProgressInfo) {},
		func(entry types.LogEntry) { t.Logf("[log] %s", entry.Message) },
	)
	report, err := orch.Run(context.Background())
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return report
}

// assertPartitionTarget 校验目标库分区层级与数据量
func assertPartitionTarget(t *testing.T, tgt *sql.DB, parentRows int) {
	t.Helper()
	ctx := context.Background()

	var relKind, partKey string
	if err := tgt.QueryRowContext(ctx, `
		SELECT c.relkind::text, COALESCE(pg_get_partkeydef(c.oid), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = 'origin_message' AND n.nspname = 'public'`).
		Scan(&relKind, &partKey); err != nil {
		t.Fatalf("查询目标父表失败: %v", err)
	}
	if relKind != "p" {
		t.Errorf("目标 origin_message relkind = %q, want 'p'（分区结构丢失）", relKind)
	}
	if !strings.Contains(partKey, "RANGE") {
		t.Errorf("目标分区键 = %q, 应含 RANGE", partKey)
	}

	var partCount int
	if err := tgt.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'origin_message'`).Scan(&partCount); err != nil {
		t.Fatalf("查询目标分区数失败: %v", err)
	}
	if partCount != 2 {
		t.Errorf("目标分区数 = %d, want 2", partCount)
	}

	var rows int
	if err := tgt.QueryRowContext(ctx, `SELECT count(*) FROM origin_message`).Scan(&rows); err != nil {
		t.Fatalf("查询目标数据失败: %v", err)
	}
	if rows != parentRows {
		// 调试：多出的行来自哪里
		debugRows, _ := tgt.QueryContext(ctx, `SELECT id, year, content FROM origin_message ORDER BY id DESC LIMIT 3`)
		if debugRows != nil {
			for debugRows.Next() {
				var id, year int64
				var content sql.NullString
				_ = debugRows.Scan(&id, &year, &content)
				t.Logf("额外行: id=%d year=%d content=%q", id, year, content.String)
			}
			debugRows.Close()
		}
		t.Errorf("目标行数 = %d, want %d（父表+子分区各迁一遍会导致翻倍）", rows, parentRows)
	}

	// 子分区不再作为独立表出现在表列表里（GetTables 过滤 relispartition）
	var listed int
	if err := tgt.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name LIKE 'origin_message_%'`).
		Scan(&listed); err != nil {
		t.Fatalf("查询表列表失败: %v", err)
	}
	if listed != 2 {
		t.Errorf("目标 origin_message_* 应恰好 2 个（分区本身），实际 %d", listed)
	}
}

// TestPGPartitionMigrateE2E 分区表迁移主链路：结构保真 + 数据不翻倍 + 复跑
func TestPGPartitionMigrateE2E(t *testing.T) {
	if !strings.EqualFold(os.Getenv("DBBRIDGE_TEST_EMBEDDED_PG"), "1") {
		t.Skip("跳过嵌入式 PG 测试（设置 DBBRIDGE_TEST_EMBEDDED_PG=1 启用）")
	}
	admin, cleanup := setupPartitionPG(t)
	defer cleanup()
	seedPartitionSource(t, admin)

	// 第一遍：先删后建（UI 默认选项）
	report := runPGMigration(t, false)
	if report.TablesSuccess != 2 {
		t.Errorf("TablesSuccess = %d, want 2（父表 + 普通表；分区不单独迁移）", report.TablesSuccess)
	}

	tgt, err := sql.Open("postgres", dbURL("part_tgt"))
	if err != nil {
		t.Fatalf("打开目标库失败: %v", err)
	}
	defer tgt.Close()
	assertPartitionTarget(t, tgt, 30)

	// 第二遍复跑：覆盖 DROP 级联 + 游离子表清理路径
	report = runPGMigration(t, false)
	if report.TablesSuccess != 2 {
		t.Errorf("复跑 TablesSuccess = %d, want 2", report.TablesSuccess)
	}
	assertPartitionTarget(t, tgt, 30)
}

// TestPGPartitionBackupDirtyTargetE2E 存量脏数据场景：目标库是被旧版本
// 平铺迁移污染的「普通父表 + 游离子表」；开启迁移前备份后重跑，
// 旧数据应整体进 _bak_*，新层级正确建立。
func TestPGPartitionBackupDirtyTargetE2E(t *testing.T) {
	if !strings.EqualFold(os.Getenv("DBBRIDGE_TEST_EMBEDDED_PG"), "1") {
		t.Skip("跳过嵌入式 PG 测试（设置 DBBRIDGE_TEST_EMBEDDED_PG=1 启用）")
	}
	admin, cleanup := setupPartitionPG(t)
	defer cleanup()
	seedPartitionSource(t, admin)

	// 预置脏目标：模拟旧版本平铺迁移的产物（普通父表 + 游离子表，各带旧数据）。
	// 注意建在目标库 part_tgt（admin 连的是源库 part_src）
	tgtDirty, err := sql.Open("postgres", dbURL("part_tgt"))
	if err != nil {
		t.Fatalf("打开目标库失败: %v", err)
	}
	stmts := []string{
		`CREATE TABLE origin_message (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, year int NOT NULL, content text)`,
		`CREATE TABLE origin_message_2024 (id bigint GENERATED ALWAYS AS IDENTITY, year int NOT NULL, content text)`,
		`CREATE TABLE origin_message_2025 (id bigint GENERATED ALWAYS AS IDENTITY, year int NOT NULL, content text)`,
	}
	for _, s := range stmts {
		if _, err := tgtDirty.ExecContext(context.Background(), s); err != nil {
			_ = tgtDirty.Close()
			t.Fatalf("预置脏目标失败: %v (SQL: %s)", err, s)
		}
	}
	if _, err := tgtDirty.ExecContext(context.Background(), `INSERT INTO origin_message (year, content) VALUES (2024, 'old-1')`); err != nil {
		_ = tgtDirty.Close()
		t.Fatalf("预置脏目标失败: %v", err)
	}
	_ = tgtDirty.Close()

	// 备份 + 先删后建（UI 默认组合）
	report := runPGMigration(t, true)
	if report.TablesSuccess != 2 {
		t.Errorf("TablesSuccess = %d, want 2", report.TablesSuccess)
	}

	tgt, err := sql.Open("postgres", dbURL("part_tgt"))
	if err != nil {
		t.Fatalf("打开目标库失败: %v", err)
	}
	defer tgt.Close()
	assertPartitionTarget(t, tgt, 30)

	// 新层级必须是真分区（附着于父表），不是游离普通表
	var attached int
	if err := tgt.QueryRowContext(context.Background(), `
		SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_inherits i ON i.inhrelid = c.oid
		WHERE n.nspname = 'public' AND c.relispartition
		AND c.relname IN ('origin_message_2024', 'origin_message_2025')`).
		Scan(&attached); err != nil {
		t.Fatalf("查询分区附着关系失败: %v", err)
	}
	if attached != 2 {
		t.Errorf("attached 分区数 = %d, want 2", attached)
	}

	// 旧数据应进备份表（父表 + 游离子表都有 _bak 副本）
	var bakCount int
	if err := tgt.QueryRowContext(context.Background(), `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name LIKE '\_bak\_%' ESCAPE '\'`).
		Scan(&bakCount); err != nil {
		t.Fatalf("查询备份表失败: %v", err)
	}
	if bakCount < 2 {
		t.Errorf("备份表数 = %d, 应至少含父表与游离子表两份", bakCount)
	}
}
