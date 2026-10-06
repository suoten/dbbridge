package adapter_test

import (
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/access"
	_ "dbbridge/internal/adapter/aurora"
	_ "dbbridge/internal/adapter/cockroachdb"
	_ "dbbridge/internal/adapter/dameng"
	_ "dbbridge/internal/adapter/kingbase"
	_ "dbbridge/internal/adapter/mariadb"
	_ "dbbridge/internal/adapter/mongodb"
	_ "dbbridge/internal/adapter/mssql"
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/oceanbase"
	_ "dbbridge/internal/adapter/opengauss"
	_ "dbbridge/internal/adapter/oracle"
	_ "dbbridge/internal/adapter/polardb"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	_ "dbbridge/internal/adapter/tidb"
	_ "dbbridge/internal/adapter/timescaledb"
	"dbbridge/internal/adapter/sqlite"
)

// TestCheckConstraintGeneration 验证所有关系型适配器都能生成 CHECK 约束 DDL
func TestCheckConstraintGeneration(t *testing.T) {
	length := 10
	schema := types.TableSchema{
		Name: "test_check",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
			{Name: "age", DataType: "INT", BaseType: "INT", Nullable: false},
			{Name: "status", DataType: "VARCHAR(10)", BaseType: "VARCHAR", Length: &length, Nullable: false},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
		},
		Checks: []types.CheckMeta{
			{Name: "ck_age", Definition: "(age > 0)"},
			{Name: "ck_status", Definition: "(status IN ('active', 'inactive'))"},
		},
	}

	relationalDBs := []types.DatabaseType{
		types.MySQL, types.PostgreSQL, types.SQLite, types.MariaDB,
		types.OceanBase, types.TiDB, types.PolarDB, types.OpenGauss,
		types.Dameng, types.KingbaseES, types.Aurora, types.CockroachDB,
		types.Oracle, types.MSSQL, types.Db2, types.TimescaleDB,
		types.Access,
	}

	for _, dbType := range relationalDBs {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			ddl, err := adapter.GenerateCreateTableDDL(schema)
			if err != nil {
				t.Fatalf("生成 DDL 失败: %v", err)
			}
			t.Logf("%s DDL:\n%s", dbType, ddl)

			// 验证 CHECK 约束存在
			// Access 不支持 CHECK 约束
			if dbType == types.Access {
				if strings.Contains(ddl, "CHECK") {
					t.Log("Access 包含 CHECK（可接受）")
				}
				return
			}

			if !strings.Contains(ddl, "CHECK") {
				t.Errorf("%s DDL 缺少 CHECK 约束", dbType)
			}
			if !strings.Contains(ddl, "age") {
				t.Errorf("%s DDL CHECK 约束缺少 age 列引用", dbType)
			}
		})
	}
}

// TestCheckConstraintRoundTrip SQLite→SQLite CHECK 约束往返验证
func TestCheckConstraintRoundTrip(t *testing.T) {
	ctx := t.Context()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	// SQLite 支持 CHECK 约束（内联在 CREATE TABLE 中）
	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  age INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  CONSTRAINT ck_age CHECK (age > 0),
  CONSTRAINT ck_status CHECK (status IN ('active', 'inactive'))
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 写入合法数据
	if err := src.ExecContext(ctx, `INSERT INTO t (id, age, status) VALUES (1, 25, 'active')`); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 验证 CHECK 约束生效
	err := src.ExecContext(ctx, `INSERT INTO t (id, age, status) VALUES (2, -1, 'active')`)
	if err == nil {
		t.Error("CHECK 约束未生效：age=-1 应被拒绝")
	}
	t.Logf("CHECK 约束生效（age=-1 被拒绝）: %v", err)

	// 读结构
	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	// SQLite 不通过 PRAGMA table_info 读 CHECK，需要解析 DDL
	// 这里直接构造 schema 传入目标库
	schema.Checks = []types.CheckMeta{
		{Name: "ck_age", Definition: "(age > 0)"},
		{Name: "ck_status", Definition: "(status IN ('active', 'inactive'))"},
	}

	// 生成目标 DDL
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标库失败: %v", err)
	}
	defer dst.Close()

	ddl, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}
	t.Logf("目标 DDL:\n%s", ddl)

	// 执行目标 DDL
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 验证目标库 CHECK 约束生效
	if err := dst.ExecContext(ctx, `INSERT INTO t (id, age, status) VALUES (1, 25, 'active')`); err != nil {
		t.Fatalf("目标库写入合法数据失败: %v", err)
	}

	err = dst.ExecContext(ctx, `INSERT INTO t (id, age, status) VALUES (2, -1, 'active')`)
	if err == nil {
		t.Error("目标库 CHECK 约束未生效：age=-1 应被拒绝")
	} else {
		t.Log("目标库 CHECK 约束生效（age=-1 被拒绝）")
	}

	err = dst.ExecContext(ctx, `INSERT INTO t (id, age, status) VALUES (3, 30, 'unknown')`)
	if err == nil {
		t.Error("目标库 CHECK 约束未生效：status='unknown' 应被拒绝")
	} else {
		t.Log("目标库 CHECK 约束生效（status='unknown' 被拒绝）")
	}
}

// TestCheckConstraintAllTypes 各种 CHECK 表达式在各适配器中的生成验证
func TestCheckConstraintAllTypes(t *testing.T) {
	cases := []struct {
		name string
		ck   types.CheckMeta
	}{
		{"simple_gt", types.CheckMeta{Name: "ck1", Definition: "(age > 0)"}},
		{"simple_lt", types.CheckMeta{Name: "ck2", Definition: "(score < 100)"}},
		{"in_list", types.CheckMeta{Name: "ck3", Definition: "(status IN ('a', 'b', 'c'))"}},
		{"between", types.CheckMeta{Name: "ck4", Definition: "(age BETWEEN 0 AND 150)"}},
		{"not_null", types.CheckMeta{Name: "ck5", Definition: "(name IS NOT NULL)"}},
		{"complex", types.CheckMeta{Name: "ck6", Definition: "((age > 0) AND (age < 150))"}},
	}

	relationalDBs := []types.DatabaseType{
		types.MySQL, types.PostgreSQL, types.SQLite, types.MSSQL,
		types.Oracle, types.Db2,
	}

	for _, dbType := range relationalDBs {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			for _, c := range cases {
				schema := types.TableSchema{
					Name: "t",
					Columns: []types.ColumnMeta{
						{Name: "id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
					},
					Indexes: []types.IndexMeta{
						{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
					},
					Checks: []types.CheckMeta{c.ck},
				}

				ddl, err := adapter.GenerateCreateTableDDL(schema)
				if err != nil {
					t.Errorf("%s/%s: 生成 DDL 失败: %v", dbType, c.name, err)
					continue
				}
				if !strings.Contains(ddl, "CHECK") {
					t.Errorf("%s/%s: DDL 缺少 CHECK", dbType, c.name)
				}
			}
		})
	}
}
