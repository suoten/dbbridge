// Package orchestr_test - PG→PG 插件（扩展）迁移端到端测试
//
// 使用嵌入式 PostgreSQL（fergusstrange/embedded-postgres）验证：
//   - MigratePlugins 开启后，源库已装插件（pg_trgm）先于建表在目标库创建
//   - 依赖插件的表 DDL（GIN trgm 索引）随之迁移成功
//   - 报告统计 PluginsMigrated / PluginErrors 正确
//   - 门控路径：SQLite（无插件概念）开启选项后安全跳过
//
// 需联网下载 PG 二进制，设 DBBRIDGE_TEST_EMBEDDED_PG=1 启用真实 PG 测试。
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

	_ "dbbridge/internal/adapter/postgres" // 注册 postgres 适配器（types.NewAdapter 用）
	"dbbridge/internal/adapter/sqlite"
	types "dbbridge/pkg"
)

// TestMigratePluginsGatingSQLite 门控路径（无 PG 依赖，常规 CI 跑）：
// SQLite 无插件概念，开启 MigratePlugins 后应安全跳过且不阻塞迁移。
func TestMigratePluginsGatingSQLite(t *testing.T) {
	ctx := context.Background()

	// 用临时文件库而非 :memory:——orchestrator 内部会按配置自建连接，
	// :memory: 是连接级隔离，种子数据对 orchestrator 不可见
	srcPath := filepath.Join(t.TempDir(), "plugin_gate_src.db")
	tgtPath := filepath.Join(t.TempDir(), "plugin_gate_tgt.db")

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: srcPath}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()
	if err := src.ExecContext(ctx, `CREATE TABLE t1 (id INTEGER PRIMARY KEY, name TEXT);INSERT INTO t1 (id, name) VALUES (1, 'a'), (2, 'b')`); err != nil {
		t.Fatalf("源库建表失败: %v", err)
	}

	config := types.MigrationConfig{
		Source:         types.ConnectionConfig{Type: "sqlite", Database: srcPath},
		Target:         types.ConnectionConfig{Type: "sqlite", Database: tgtPath},
		Tables:         []string{"t1"},
		BatchSize:      100,
		MigratePlugins: true,
	}
	orch := NewOrchestrator(config,
		func(types.ProgressInfo) {},
		func(entry types.LogEntry) { t.Logf("[log] %s", entry.Message) },
	)
	report, err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if report.PluginsMigrated != 0 || len(report.PluginErrors) != 0 {
		t.Errorf("SQLite 门控路径不应迁移插件: PluginsMigrated=%d, PluginErrors=%v",
			report.PluginsMigrated, report.PluginErrors)
	}
	if report.TablesSuccess != 1 {
		t.Errorf("TablesSuccess = %d, want 1", report.TablesSuccess)
	}
}

// TestPGToPGPluginMigrateE2E 真实 PG→PG：源库装 pg_trgm + 依赖它的 trgm 索引表，
// 开启 MigratePlugins 迁移后目标库应出现插件，且依赖插件的对象建表成功。
func TestPGToPGPluginMigrateE2E(t *testing.T) {
	if !strings.EqualFold(os.Getenv("DBBRIDGE_TEST_EMBEDDED_PG"), "1") {
		t.Skip("跳过嵌入式 PG 测试（设置 DBBRIDGE_TEST_EMBEDDED_PG=1 启用）")
	}
	ctx := context.Background()

	// Windows 中文用户目录会让 initdb 的 UTF8 编码挂掉，必须纯 ASCII 路径
	runtimePath := filepath.Join(os.Getenv("ProgramData"), "DBBridgeEmbeddedPGPluginTest")
	_ = os.RemoveAll(runtimePath)

	ep := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("postgres").
		Password("pgpass").
		Database("plugin_src").
		Port(54330).
		Locale("C").
		RuntimePath(runtimePath))
	if err := ep.Start(); err != nil {
		t.Fatalf("启动嵌入式 PG 失败: %v", err)
	}
	defer func() {
		_ = ep.Stop()
		_ = os.RemoveAll(runtimePath)
	}()

	// 创建独立目标库（同一实例，两个库互不共享扩展）
	admin, err := sql.Open("postgres", "host=127.0.0.1 port=54330 user=postgres password=pgpass dbname=plugin_src sslmode=disable")
	if err != nil {
		t.Fatalf("打开管理连接失败: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "plugin_tgt"`); err != nil {
		t.Fatalf("创建目标库失败: %v", err)
	}
	_ = admin.Close()

	// ============ 准备源库：插件 + 依赖插件的表 ============
	srcSetup, err := sql.Open("postgres", "host=127.0.0.1 port=54330 user=postgres password=pgpass dbname=plugin_src sslmode=disable")
	if err != nil {
		t.Fatalf("打开源库连接失败: %v", err)
	}
	defer srcSetup.Close()

	// pg_trgm 属 contrib，嵌入式 PG 发行版自带
	if _, err := srcSetup.ExecContext(ctx, `CREATE EXTENSION pg_trgm`); err != nil {
		t.Fatalf("源库安装 pg_trgm 失败（嵌入式 PG 缺 contrib?）: %v", err)
	}
	seedDDL := []string{
		`CREATE TABLE articles (
			id serial PRIMARY KEY,
			title text NOT NULL,
			body text
		)`,
		// trgm GIN 索引依赖 pg_trgm——目标库没有该扩展时此索引 DDL 必失败，
		// 因此插件必须先于建表创建（本测试的核心断言点）
		`CREATE INDEX idx_articles_title_trgm ON articles USING gin (title gin_trgm_ops)`,
	}
	for i := 1; i <= 30; i++ {
		seedDDL = append(seedDDL, fmt.Sprintf(
			`INSERT INTO articles (title, body) VALUES ('水资源点位报告 %d', 'body-%d')`, i, i))
	}
	for _, stmt := range seedDDL {
		if _, err := srcSetup.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("源库准备失败: %v (SQL: %s)", err, stmt)
		}
	}
	_ = srcSetup.Close()

	// ============ 跑迁移：PG → PG，开启插件迁移 ============
	config := types.MigrationConfig{
		Source: types.ConnectionConfig{
			Type: "postgres", Host: "127.0.0.1", Port: 54330,
			Username: "postgres", Password: "pgpass", Database: "plugin_src",
		},
		Target: types.ConnectionConfig{
			Type: "postgres", Host: "127.0.0.1", Port: 54330,
			Username: "postgres", Password: "pgpass", Database: "plugin_tgt",
		},
		Tables:         []string{"articles"},
		BatchSize:      100,
		DropIfExists:   true,
		MigratePlugins: true,
	}
	orch := NewOrchestrator(config,
		func(types.ProgressInfo) {},
		func(entry types.LogEntry) { t.Logf("[log] %s", entry.Message) },
	)
	report, err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	// ============ 校验报告 ============
	if report.PluginsMigrated != 1 {
		t.Errorf("PluginsMigrated = %d, want 1", report.PluginsMigrated)
	}
	if len(report.PluginErrors) != 0 {
		t.Errorf("PluginErrors = %v, want empty", report.PluginErrors)
	}
	if report.TablesSuccess != 1 {
		t.Errorf("TablesSuccess = %d, want 1（依赖插件的表应建成功）", report.TablesSuccess)
	}

	// ============ 校验目标库 ============
	tgt, err := sql.Open("postgres", "host=127.0.0.1 port=54330 user=postgres password=pgpass dbname=plugin_tgt sslmode=disable")
	if err != nil {
		t.Fatalf("打开目标库连接失败: %v", err)
	}
	defer tgt.Close()

	var extCount int
	if err := tgt.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'`).Scan(&extCount); err != nil {
		t.Fatalf("查询目标库插件失败: %v", err)
	}
	if extCount != 1 {
		t.Errorf("目标库 pg_trgm 扩展数 = %d, want 1", extCount)
	}

	var rows int
	if err := tgt.QueryRowContext(ctx, `SELECT count(*) FROM articles`).Scan(&rows); err != nil {
		t.Fatalf("查询目标库数据失败: %v", err)
	}
	if rows != 30 {
		t.Errorf("目标库 articles 行数 = %d, want 30", rows)
	}

	// 索引名在先删后建模式下会追加运行后缀去重（RunSuffix 机制），
	// 按索引定义里的 trgm 操作符类做语义级断言
	var idxCount int
	if err := tgt.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'articles' AND indexdef LIKE '%gin_trgm_ops%'`).Scan(&idxCount); err != nil {
		t.Fatalf("查询目标库索引失败: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("目标库 trgm 索引缺失（依赖插件的对象未建成）: count=%d", idxCount)
	}
}
