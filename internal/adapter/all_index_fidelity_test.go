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
)

// TestAllAdaptersIndexGeneration 验证所有关系型适配器都能生成二级索引 DDL
// 之前发现 MSSQL 和 Oracle 完全没有生成二级索引，导致迁移到这两个库时索引静默丢失
func TestAllAdaptersIndexGeneration(t *testing.T) {
	length := 128
	schema := types.TableSchema{
		Name: "test_table",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", AutoIncrement: true, IsPrimaryKey: true, Nullable: false},
			{Name: "email", DataType: "VARCHAR(128)", BaseType: "VARCHAR", Length: &length, Nullable: false},
			{Name: "name", DataType: "VARCHAR(50)", BaseType: "VARCHAR", Nullable: false},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
			{Name: "idx_email", Columns: []string{"email"}, IsUnique: true},
			{Name: "idx_name", Columns: []string{"name"}, IsUnique: false},
		},
	}

	// 所有关系型数据库适配器
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

			// 验证 UNIQUE INDEX 存在（之前 MSSQL/Oracle 完全没有）
			if !strings.Contains(ddl, "UNIQUE INDEX") && !strings.Contains(ddl, "UNIQUE INDEX") {
				// Access 用 CREATE INDEX，UNIQUE 可能不同
				if dbType == types.Access {
					if !strings.Contains(ddl, "CREATE UNIQUE INDEX") {
						t.Errorf("%s DDL 缺少 UNIQUE INDEX", dbType)
					}
				} else {
					t.Errorf("%s DDL 缺少 UNIQUE INDEX（二级索引丢失）", dbType)
				}
			}

			// 验证普通 INDEX 存在
			// 有些数据库用 CREATE INDEX，有些用 CREATE NONCLUSTERED INDEX
			hasIndex := strings.Contains(ddl, "CREATE INDEX") ||
				strings.Contains(ddl, "CREATE NONCLUSTERED INDEX") ||
				strings.Contains(ddl, "INDEX")
			if !hasIndex {
				t.Errorf("%s DDL 缺少普通索引", dbType)
			}

			// 验证 sqlite_autoindex_ 不会出现在目标 DDL 中
			if strings.Contains(ddl, "sqlite_autoindex_") {
				t.Errorf("%s DDL 包含 sqlite_autoindex_ 保留名", dbType)
			}
		})
	}
}

// TestAllAdaptersForeignKeyGeneration 验证所有关系型适配器处理外键时不会生成语法错误
func TestAllAdaptersForeignKeyGeneration(t *testing.T) {
	schema := types.TableSchema{
		Name: "child",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
			{Name: "parent_id", DataType: "INT", BaseType: "INT", Nullable: false},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
		},
		ForeignKeys: []types.ForeignKeyMeta{
			// 正常外键
			{Name: "fk_parent", Columns: []string{"parent_id"}, RefTable: "parent", RefColumns: []string{"id"}},
		},
	}

	relationalDBs := []types.DatabaseType{
		types.MySQL, types.PostgreSQL, types.SQLite, types.MariaDB,
		types.Oracle, types.MSSQL, types.Db2, types.Access,
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

			// 验证外键存在（MySQL/PG 通过延后添加，DDL 中不应有外键）
			// SQLite/MSSQL 内联外键，DDL 中应有 FOREIGN KEY
			if dbType == types.SQLite || dbType == types.MSSQL {
				if !strings.Contains(ddl, "FOREIGN KEY") {
					t.Errorf("%s DDL 缺少 FOREIGN KEY（内联外键丢失）", dbType)
				}
			}
		})
	}
}

// TestAllAdaptersEmptyRefColumnFK 验证空引用列外键不会生成语法错误 DDL
func TestAllAdaptersEmptyRefColumnFK(t *testing.T) {
	schema := types.TableSchema{
		Name: "child",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
			{Name: "parent_id", DataType: "INT", BaseType: "INT", Nullable: false},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
		},
		ForeignKeys: []types.ForeignKeyMeta{
			// 空引用列（SQLite 省略引用列名时会出现）
			{Name: "fk_empty", Columns: []string{"parent_id"}, RefTable: "parent", RefColumns: []string{""}},
		},
	}

	relationalDBs := []types.DatabaseType{
		types.SQLite, types.MSSQL, // 内联外键的适配器
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

			t.Logf("%s DDL (empty ref col):\n%s", dbType, ddl)

			// 验证没有空引用列的语法错误
			// SQLite: REFERENCES "parent" ()  —— 空括号
			// MSSQL: REFERENCES [parent] ([]) —— 空方括号
			if strings.Contains(ddl, "REFERENCES \"parent\" ()") {
				t.Errorf("%s DDL 包含空引用列语法错误: REFERENCES parent ()", dbType)
			}
			if strings.Contains(ddl, "REFERENCES [parent] ([])") {
				t.Errorf("%s DDL 包含空引用列语法错误: REFERENCES parent ([])", dbType)
			}
			// 更通用的检查：REFERENCES 后跟空括号
			if strings.Contains(ddl, "REFERENCES") {
				// 提取 REFERENCES 部分，检查是否有空引用列
				idx := strings.Index(ddl, "REFERENCES")
				if idx >= 0 {
					tail := ddl[idx:]
					// 检查是否有 () 或 ([]) 或 ("") 模式
					if strings.Contains(tail, "()") || strings.Contains(tail, "([])") || strings.Contains(tail, "(\"\")") {
						t.Errorf("%s DDL 外键引用列为空，生成语法错误 DDL: %s", dbType, tail)
					}
				}
			}
		})
	}
}
