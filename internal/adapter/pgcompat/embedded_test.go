package pgcompat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbbridge/internal/adapter/sqlite"
	types "dbbridge/pkg"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// TestNormalizePGDefault PG 默认值 ::类型 后缀剥离
func TestNormalizePGDefault(t *testing.T) {
	cases := []struct{ in, want string }{
		{`'pending'::character varying`, `'pending'`},
		{`NULL::text`, `NULL`},
		{`'0'::numeric`, `'0'`},
		{`0.00`, `0.00`},
		{`now()`, `now()`},
		// 函数式默认值剥掉尾部 cast：now()::timestamp 原样写进 MySQL 是语法错误
		{`now()::timestamp`, `now()`},
		{`CURRENT_TIMESTAMP`, `CURRENT_TIMESTAMP`},
		// 括号内的 ::（函数参数里的 cast）不能剥，否则函数调用被切碎
		{`nextval('users_id_seq'::regclass)`, `nextval('users_id_seq'::regclass)`},
		// 字面量内含 :: 的不是类型转换，保留原值
		{`'a::b'::text`, `'a::b'`},
		{`'a::b'`, `'a::b'`},
	}
	for _, c := range cases {
		if got := normalizePGDefault(c.in); got != c.want {
			t.Errorf("normalizePGDefault(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEmbeddedPGE2E 嵌入式 PostgreSQL 真实端到端：SQLite→PG→SQLite 双向迁移。
// 需联网下载 PG 二进制，设 DBBRIDGE_TEST_EMBEDDED_PG=1 启用。
func TestEmbeddedPGE2E(t *testing.T) {
	if !strings.EqualFold(envOr("DBBRIDGE_TEST_EMBEDDED_PG", ""), "1") {
		t.Skip("跳过嵌入式 PG 测试（设置 DBBRIDGE_TEST_EMBEDDED_PG=1 启用）")
	}
	ctx := context.Background()

	// Windows 用户目录常含中文，initdb 的 UTF8 编码无法处理 GBK 路径，
	// 必须用纯 ASCII 运行目录（测试结束后清理）
	runtimePath := filepath.Join(os.Getenv("ProgramData"), "DBBridgeEmbeddedPGTest")
	_ = os.RemoveAll(runtimePath)

	ep := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("postgres").
		Password("pgpass").
		Database("dbbridge_e2e").
		Port(54329).
		Locale("C").
		RuntimePath(runtimePath))
	if err := ep.Start(); err != nil {
		t.Fatalf("启动嵌入式 PG 失败: %v", err)
	}
	defer func() {
		_ = ep.Stop()
		_ = os.RemoveAll(runtimePath)
	}()

	pg := New("postgres")
	if err := pg.Connect(ctx, types.ConnectionConfig{
		Host: "127.0.0.1", Port: 54329, Username: "postgres", Password: "pgpass",
		Database: "dbbridge_e2e",
	}); err != nil {
		t.Fatalf("连接 PG 失败: %v", err)
	}
	defer pg.Close()

	// ============ 方向①：SQLite → PG ============
	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	seedDDL := `CREATE TABLE "users" (
  "id" INTEGER PRIMARY KEY,
  "name" TEXT NOT NULL,
  "email" TEXT,
  "balance" REAL,
  "status" TEXT DEFAULT 'pending',
  "created_at" TEXT,
  "image" BLOB
);CREATE UNIQUE INDEX "email" ON "users" ("email")`
	if err := src.ExecContext(ctx, seedDDL); err != nil {
		t.Fatalf("SQLite 建表失败: %v", err)
	}
	rows := make([]types.Row, 0, 100)
	for i := 1; i <= 100; i++ {
		img := []byte{byte(i), 2, 3}
		if i%10 == 0 {
			img = nil
		}
		rows = append(rows, types.Row{
			"id": int64(i), "name": fmt.Sprintf("user%d", i),
			"email": fmt.Sprintf("u%d@x.com", i), "balance": float64(i) * 1.5,
			"status": "pending", "created_at": "2026-08-30 10:00:00", "image": img,
		})
	}
	if err := src.WriteData(ctx, "users", []string{"id", "name", "email", "balance", "status", "created_at", "image"}, rows); err != nil {
		t.Fatalf("SQLite 写入失败: %v", err)
	}

	// 读源结构 → 生成 PG DDL → 建表 → 写数据
	schema, err := src.GetTableSchema(ctx, "users")
	if err != nil {
		t.Fatalf("读 SQLite 结构失败: %v", err)
	}
	ddl, err := pg.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 PG DDL 失败: %v", err)
	}
	for _, stmt := range strings.Split(ddl, ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := pg.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("执行 PG 建表 SQL 失败: %v (SQL: %s)", err, stmt)
		}
	}
	srcRows, err := src.ReadData(ctx, "users", 0, 1000)
	if err != nil {
		t.Fatalf("读 SQLite 数据失败: %v", err)
	}
	if err := pg.WriteData(ctx, "users", []string{"id", "name", "email", "balance", "status", "created_at", "image"}, srcRows); err != nil {
		t.Fatalf("写入 PG 失败: %v", err)
	}

	// 校验 PG 端：行数 + 结构（此处同时验证修复后的 8 列 Scan）+ 索引 + 默认值
	if n, err := pg.GetRowCount(ctx, "users"); err != nil || n != 100 {
		t.Fatalf("PG 行数 = %d, err = %v, want 100", n, err)
	}
	pgSchema, err := pg.GetTableSchema(ctx, "users")
	if err != nil {
		t.Fatalf("读 PG 结构失败: %v", err)
	}
	if len(pgSchema.Columns) != 7 {
		t.Fatalf("PG 列数 = %d, want 7（列读取曾因 Scan 参数不足全挂）", len(pgSchema.Columns))
	}
	foundEmailIdx := false
	for _, idx := range pgSchema.Indexes {
		if idx.Name == "email" && idx.IsUnique {
			foundEmailIdx = true
		}
	}
	if !foundEmailIdx {
		t.Error("PG 端未读到 email UNIQUE 索引")
	}
	for _, c := range pgSchema.Columns {
		if c.Name == "id" && !c.AutoIncrement {
			t.Error("SQLite INTEGER PRIMARY KEY → PG 应为 SERIAL（AutoIncrement）")
		}
		if c.Name == "status" {
			// PG column_default 按约定保留字符串引号（'pending'），FormatDefault 可原样透传
			if c.DefaultValue == nil || *c.DefaultValue != "'pending'" {
				t.Errorf("PG status 默认值 = %v, want 'pending'（带引号）", c.DefaultValue)
			}
		}
	}

	// ============ 方向②：PG → SQLite ============
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标 SQLite 失败: %v", err)
	}
	defer dst.Close()

	ddl2, err := dst.GenerateCreateTableDDL(pgSchema)
	if err != nil {
		t.Fatalf("生成 SQLite DDL 失败: %v", err)
	}
	for _, stmt := range strings.Split(ddl2, ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("执行 SQLite 建表 SQL 失败: %v (SQL: %s)", err, stmt)
		}
	}
	pgRows, err := pg.ReadData(ctx, "users", 0, 1000)
	if err != nil {
		t.Fatalf("读 PG 数据失败: %v", err)
	}
	if err := dst.WriteData(ctx, "users", []string{"id", "name", "email", "balance", "status", "created_at", "image"}, pgRows); err != nil {
		t.Fatalf("写入 SQLite 失败: %v", err)
	}
	if n, err := dst.GetRowCount(ctx, "users"); err != nil || n != 100 {
		t.Fatalf("回迁后 SQLite 行数 = %d, err = %v, want 100", n, err)
	}
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}
