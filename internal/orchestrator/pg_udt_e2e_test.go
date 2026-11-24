// Package orchestr_test - PG 用户自定义/扩展类型迁移端到端测试（嵌入式 PostgreSQL）
//
// 验证 information_schema 报 USER-DEFINED/ARRAY 的列（枚举、复合类型、数组）
// 迁移后类型保真、数据往返一致，以及枚举类型在目标库的自动创建与标签补齐。
// inet/tsvector 是 pg_catalog 内置类型（data_type 直接给真实名称），一并纳入数据往返验证。
// PostGIS geometry 走同一条解析路径（format_type），但嵌入式 PG 不带 PostGIS，
// 其 DDL 生成由 pgcompat.TestGenerateCreateTableDDLExactType 覆盖。
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

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	_ "dbbridge/internal/adapter/postgres" // 注册 postgres 适配器
	types "dbbridge/pkg"
)

const udtPGPort = 54332 // 与 pgcompat(54329)/插件(54330)/分区(54331)测试错开

func udtDB(name string) *sql.DB {
	db, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=postgres password=pgpass dbname=%s sslmode=disable", udtPGPort, name))
	if err != nil {
		panic(err)
	}
	return db
}

// setupUDTPG 启动嵌入式 PG（端口 54332）并创建独立目标库，返回源库管理连接
func setupUDTPG(t *testing.T) *sql.DB {
	t.Helper()
	runtimePath := filepath.Join(os.Getenv("ProgramData"), "DBBridgeEmbeddedPGUdtTest")
	_ = os.RemoveAll(runtimePath)

	ep := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("postgres").
		Password("pgpass").
		Database("udt_src").
		Port(udtPGPort).
		Locale("C").
		RuntimePath(runtimePath))
	if err := ep.Start(); err != nil {
		t.Fatalf("启动嵌入式 PG 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = ep.Stop()
		_ = os.RemoveAll(runtimePath)
	})

	src := udtDB("udt_src")
	if _, err := src.Exec(`CREATE DATABASE "udt_tgt"`); err != nil {
		t.Fatalf("创建目标库失败: %v", err)
	}
	return src
}

// runUDTMigration 执行 PG→PG 迁移，同时返回报告与错误（缺类型场景需要检查错误文本）
func runUDTMigration(t *testing.T, tables []string) (*types.MigrationReport, error) {
	t.Helper()
	config := types.MigrationConfig{
		Source: types.ConnectionConfig{
			Type: "postgres", Host: "127.0.0.1", Port: udtPGPort,
			Username: "postgres", Password: "pgpass", Database: "udt_src",
		},
		Target: types.ConnectionConfig{
			Type: "postgres", Host: "127.0.0.1", Port: udtPGPort,
			Username: "postgres", Password: "pgpass", Database: "udt_tgt",
		},
		Tables:       tables,
		BatchSize:    100,
		DropIfExists: true,
	}
	orch := NewOrchestrator(config,
		func(types.ProgressInfo) {},
		func(entry types.LogEntry) { t.Logf("[log] %s", entry.Message) },
	)
	report, err := orch.Run(context.Background())
	return report, err
}

// dumpRows 把查询结果逐行拼成字符串（NULL 记为 <NULL>），用于源/目标库数据比对
func dumpRows(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("查询失败: %v (SQL: %s)", err, query)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("读取列信息失败: %v", err)
	}
	var out []string
	for rows.Next() {
		vals := make([]*sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			vals[i] = &sql.NullString{}
			ptrs[i] = vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("读取数据失败: %v", err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			if v.Valid {
				parts[i] = v.String
			} else {
				parts[i] = "<NULL>"
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func assertSameRows(t *testing.T, got, want []string, what string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s 不一致:\n got: %q\nwant: %q", what, got, want)
	}
}

const udtDataQuery = `SELECT id, m::text, tags::text, ints::text, addr::text, p::text, body::text FROM udt_src ORDER BY id`

// enumLabels 返回目标库 mood 类型的标签（逗号分隔，按定义顺序）
func enumLabels(t *testing.T, db *sql.DB) string {
	t.Helper()
	var labels string
	if err := db.QueryRow(`SELECT string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
		FROM pg_enum e JOIN pg_type ty ON ty.oid = e.enumtypid WHERE ty.typname = 'mood'`).Scan(&labels); err != nil {
		t.Fatalf("查询枚举标签失败: %v", err)
	}
	return labels
}

func TestPGUserDefinedTypesE2E(t *testing.T) {
	if !strings.EqualFold(os.Getenv("DBBRIDGE_TEST_EMBEDDED_PG"), "1") {
		t.Skip("跳过嵌入式 PG 测试（设置 DBBRIDGE_TEST_EMBEDDED_PG=1 启用）")
	}
	src := setupUDTPG(t)
	ctx := context.Background()

	// 源库：枚举（带默认值）、数组、inet、tsvector、复合类型——全是 information_schema
	// 报 USER-DEFINED/ARRAY 的列，旧实现会生成 "m" USER-DEFINED 导致建表语法错误
	for _, s := range []string{
		`CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy')`,
		`CREATE TYPE pair AS (a int, b text)`,
		`CREATE TABLE udt_src (
			id serial PRIMARY KEY,
			m mood NOT NULL DEFAULT 'ok',
			tags text[],
			ints integer[],
			addr inet,
			p pair,
			body tsvector
		)`,
		`INSERT INTO udt_src (m, tags, ints, addr, p, body) VALUES
			('happy', '{a,b}', '{1,2,3}', '192.168.1.10', '(1,abc)', to_tsvector('hello world')),
			('sad', NULL, NULL, NULL, NULL, NULL)`,
	} {
		if _, err := src.ExecContext(ctx, s); err != nil {
			t.Fatalf("源库准备失败: %v (SQL: %s)", err, s)
		}
	}

	// 目标库预先有复合类型 pair（复合/范围类型不自动创建，属已知限制）
	tgt := udtDB("udt_tgt")
	defer tgt.Close()
	if _, err := tgt.ExecContext(ctx, `CREATE TYPE pair AS (a int, b text)`); err != nil {
		t.Fatalf("目标库准备失败: %v", err)
	}

	t.Run("枚举自动创建且类型保真、数据往返一致", func(t *testing.T) {
		report, err := runUDTMigration(t, []string{"udt_src"})
		if err != nil {
			t.Fatalf("迁移失败: %v", err)
		}
		if report.TablesSuccess != 1 {
			t.Fatalf("TablesSuccess = %d, want 1", report.TablesSuccess)
		}

		// 目标库此前没有 mood 类型，应被自动创建且标签顺序一致
		if got := enumLabels(t, tgt); got != "sad,ok,happy" {
			t.Errorf("目标库 mood 标签 = %q, want %q", got, "sad,ok,happy")
		}

		// 列类型保真
		want := map[string]string{
			"m": "mood", "tags": "text[]", "ints": "integer[]",
			"addr": "inet", "p": "pair", "body": "tsvector",
		}
		// 不在 pg_catalog 的类型（枚举/复合类型）与数组才需要精确类型解析
		wantExact := map[string]bool{"m": true, "tags": true, "ints": true, "p": true}
		gotTypes := map[string]string{}
		rows, err := tgt.Query(`SELECT a.attname, format_type(a.atttypid, a.atttypmod)
			FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
			WHERE c.relname = 'udt_src' AND a.attnum > 0`)
		if err != nil {
			t.Fatalf("查询目标列类型失败: %v", err)
		}
		for rows.Next() {
			var name, typ string
			if err := rows.Scan(&name, &typ); err != nil {
				t.Fatalf("读取目标列类型失败: %v", err)
			}
			gotTypes[name] = typ
		}
		rows.Close()
		for col, want := range want {
			if gotTypes[col] != want {
				t.Errorf("目标列 %s 类型 = %q, want %q", col, gotTypes[col], want)
			}
		}

		// 数据往返一致（枚举/数组/inet/复合/tsvector 均按文本编码写入后由 PG 解析）
		assertSameRows(t, dumpRows(t, tgt, udtDataQuery), dumpRows(t, src, udtDataQuery), "迁移后数据")

		// 适配器对外暴露的结构信息不再出现 USER-DEFINED/ARRAY 占位名
		adapter := types.NewAdapter("postgres")
		if err := adapter.Connect(ctx, types.ConnectionConfig{
			Host: "127.0.0.1", Port: udtPGPort, Username: "postgres", Password: "pgpass", Database: "udt_src"}); err != nil {
			t.Fatalf("连接源库失败: %v", err)
		}
		defer adapter.Close()
		schema, err := adapter.GetTableSchema(ctx, "udt_src")
		if err != nil {
			t.Fatalf("读取源表结构失败: %v", err)
		}
		for _, c := range schema.Columns {
			if c.DataType == "USER-DEFINED" || c.DataType == "ARRAY" {
				t.Errorf("列 %s 的 DataType 仍是占位名 %q", c.Name, c.DataType)
			}
			if wantExact[c.Name] && !c.ExactType {
				t.Errorf("列 %s 应标记 ExactType", c.Name)
			}
			// inet/tsvector 是 pg_catalog 内置类型，information_schema 直接给出真实名称，
			// 不走精确类型解析（也不应被改成其他目标库的通用类型）
			if (c.Name == "addr" || c.Name == "body") && c.ExactType {
				t.Errorf("内置类型列 %s 不应标记 ExactType", c.Name)
			}
		}
	})

	t.Run("复跑幂等", func(t *testing.T) {
		report, err := runUDTMigration(t, []string{"udt_src"})
		if err != nil {
			t.Fatalf("复跑失败: %v", err)
		}
		if report.TablesSuccess != 1 {
			t.Errorf("复跑 TablesSuccess = %d, want 1", report.TablesSuccess)
		}
		assertSameRows(t, dumpRows(t, tgt, udtDataQuery), dumpRows(t, src, udtDataQuery), "复跑后数据")
	})

	t.Run("源库枚举新增标签自动补齐到目标库", func(t *testing.T) {
		for _, s := range []string{
			`ALTER TYPE mood ADD VALUE IF NOT EXISTS 'excited'`,
			`INSERT INTO udt_src (m, tags) VALUES ('excited', '{x}')`,
		} {
			if _, err := src.ExecContext(ctx, s); err != nil {
				t.Fatalf("源库准备失败: %v (SQL: %s)", err, s)
			}
		}
		if got := enumLabels(t, tgt); got != "sad,ok,happy" {
			t.Fatalf("前置条件不成立：目标库 mood 标签 = %q", got)
		}
		report, err := runUDTMigration(t, []string{"udt_src"})
		if err != nil {
			t.Fatalf("迁移失败: %v", err)
		}
		if report.TablesSuccess != 1 {
			t.Errorf("TablesSuccess = %d, want 1", report.TablesSuccess)
		}
		if got := enumLabels(t, tgt); got != "sad,ok,happy,excited" {
			t.Errorf("目标库 mood 标签 = %q, want %q", got, "sad,ok,happy,excited")
		}
		assertSameRows(t, dumpRows(t, tgt, udtDataQuery), dumpRows(t, src, udtDataQuery), "补标签后数据")
	})

	t.Run("目标库缺少自定义类型时给出可操作提示", func(t *testing.T) {
		// solo 类型只存在于源库：建表必失败，但错误里应说明缺的是哪个类型、该怎么处理
		for _, s := range []string{
			`CREATE TYPE solo AS (x int)`,
			`CREATE TABLE udt_missing (id int PRIMARY KEY, s solo)`,
		} {
			if _, err := src.ExecContext(ctx, s); err != nil {
				t.Fatalf("源库准备失败: %v (SQL: %s)", err, s)
			}
		}
		_, err := runUDTMigration(t, []string{"udt_missing"})
		if err == nil {
			t.Fatal("目标库缺少 solo 类型，迁移应失败")
		}
		if !strings.Contains(err.Error(), `type "solo" does not exist`) {
			t.Errorf("错误应包含 PG 原始原因，实际: %v", err)
		}
		if !strings.Contains(err.Error(), "扩展/自定义类型 solo") || !strings.Contains(err.Error(), "插件迁移") {
			t.Errorf("错误应包含缺类型提示，实际: %v", err)
		}
	})
}
