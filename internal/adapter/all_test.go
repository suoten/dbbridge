// Package adapter_test 验证所有数据库适配器的注册和基本接口完整性。
//
// 测试策略：
//   - 不依赖真实数据库实例（用户本地没有 MSSQL/MySQL 等）
//   - 通过 blank import 触发所有适配器的 init() 注册
//   - 验证每种类型都能 NewAdapter 成功
//   - 验证适配器实现的 DDL 生成等纯函数不 panic
package adapter_test

import (
	"strings"
	"testing"

	types "dbbridge/pkg"

	// blank import 触发所有适配器注册（与 app.go 保持一致）
	// 第一阶段
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	_ "dbbridge/internal/adapter/mariadb"
	_ "dbbridge/internal/adapter/oceanbase"
	// 第二阶段
	_ "dbbridge/internal/adapter/tidb"
	_ "dbbridge/internal/adapter/polardb"
	_ "dbbridge/internal/adapter/opengauss"
	_ "dbbridge/internal/adapter/dameng"
	_ "dbbridge/internal/adapter/kingbase"
	_ "dbbridge/internal/adapter/aurora"
	_ "dbbridge/internal/adapter/cockroachdb"
	// 第三阶段
	_ "dbbridge/internal/adapter/oracle"
	_ "dbbridge/internal/adapter/mssql"
	_ "dbbridge/internal/adapter/db2"
	_ "dbbridge/internal/adapter/mongodb"
	_ "dbbridge/internal/adapter/redis"
	_ "dbbridge/internal/adapter/cassandra"
	_ "dbbridge/internal/adapter/scylladb"
	_ "dbbridge/internal/adapter/influxdb"
	_ "dbbridge/internal/adapter/timescaledb"
	_ "dbbridge/internal/adapter/tdengine"
)

// TestAllAdaptersRegistered 验证所有数据库类型都已注册。
// 如果新增了 DatabaseType 常量但忘记注册适配器，用户选择该类型会静默失败。
func TestAllAdaptersRegistered(t *testing.T) {
	expected := []types.DatabaseType{
		// 第一阶段
		types.MySQL,
		types.PostgreSQL,
		types.SQLite,
		types.MariaDB,
		types.OceanBase,
		// 第二阶段
		types.TiDB,
		types.PolarDB,
		types.OpenGauss,
		types.Dameng,
		types.KingbaseES,
		types.Aurora,
		types.CockroachDB,
		// 第三阶段
		types.Oracle,
		types.MSSQL,
		types.Db2,
		types.MongoDB,
		types.Redis,
		types.Cassandra,
		types.ScyllaDB,
		types.InfluxDB,
		types.TimescaleDB,
		types.TDengine,
	}

	for _, dbType := range expected {
		t.Run(string(dbType), func(t *testing.T) {
			a := types.NewAdapter(dbType)
			if a == nil {
				t.Errorf("数据库类型 %s 未注册适配器（NewAdapter 返回 nil）", dbType)
			}
		})
	}
}

// TestAdapterGenerateDDL 验证所有适配器都能对统一 schema 生成有效的 DDL。
// 这是不依赖数据库连接的纯函数测试，能捕获：
//   - MapType 类型映射 panic
//   - DDL 生成格式错误
//   - 各适配器 GenerateCreateTableDDL / GenerateDropTableDDL 的基本可用性
func TestAdapterGenerateDDL(t *testing.T) {
	dbTypes := []types.DatabaseType{
		types.MySQL,
		types.PostgreSQL,
		types.SQLite,
		types.MariaDB,
		types.OceanBase,
		types.TiDB,
		types.PolarDB,
		types.OpenGauss,
		types.Dameng,
		types.KingbaseES,
		types.Aurora,
		types.CockroachDB,
		types.Oracle,
		types.MSSQL,
		types.Db2,
		types.MongoDB,
		types.Redis,
		types.Cassandra,
		types.ScyllaDB,
		types.InfluxDB,
		types.TimescaleDB,
		types.TDengine,
	}

	// 统一的测试 schema（包含常见类型、主键、自增、默认值、索引、外键、CHECK 约束）
	score := 10
	scale := 2
	length := 128
	schema := types.TableSchema{
		Name: "test_users",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", AutoIncrement: true, IsPrimaryKey: true, Nullable: false},
			{Name: "name", DataType: "VARCHAR(128)", BaseType: "VARCHAR", Length: &length, Nullable: false},
			{Name: "email", DataType: "VARCHAR(255)", BaseType: "VARCHAR", Length: &score},
			{Name: "score", DataType: "DECIMAL(10,2)", BaseType: "DECIMAL", Precision: &score, Scale: &scale},
			{Name: "status", DataType: "VARCHAR(20)", BaseType: "VARCHAR", Nullable: false, DefaultValue: strPtr("active")},
			{Name: "created_at", DataType: "DATETIME", BaseType: "DATETIME", DefaultValue: strPtr("CURRENT_TIMESTAMP")},
			{Name: "data", DataType: "TEXT", BaseType: "TEXT"},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
			{Name: "idx_email", Columns: []string{"email"}, IsUnique: true},
		},
		ForeignKeys: []types.ForeignKeyMeta{
			{Name: "fk_dept", Columns: []string{"dept_id"}, RefTable: "departments", RefColumns: []string{"id"}},
		},
		Checks: []types.CheckMeta{
			{Name: "ck_score", Definition: "(score >= 0)"},
		},
	}

	for _, dbType := range dbTypes {
		t.Run(string(dbType), func(t *testing.T) {
			a := types.NewAdapter(dbType)
			if a == nil {
				t.Skipf("适配器 %s 未注册，跳过", dbType)
			}

			// 生成建表 DDL
			ddl, err := a.GenerateCreateTableDDL(schema)
			if err != nil {
				t.Fatalf("GenerateCreateTableDDL 失败: %v", err)
			}
			if strings.TrimSpace(ddl) == "" {
				t.Error("GenerateCreateTableDDL 返回空 DDL")
			}

			// DDL 应包含表名
			if !strings.Contains(ddl, "test_users") {
				t.Errorf("DDL 未包含表名 test_users: %s", ddl)
			}

			// DDL 应包含列名（NoSQL/时序数据库无建表 DDL，跳过列名检查）
			noSQLTypes := map[types.DatabaseType]bool{
				types.MongoDB: true, types.Redis: true, types.InfluxDB: true,
			}
			if !noSQLTypes[dbType] {
				for _, col := range schema.Columns {
					if !strings.Contains(ddl, col.Name) {
						t.Errorf("DDL 未包含列 %s: %s", col.Name, ddl)
					}
				}
			}

			// 生成删表 DDL
			dropDDL, err := a.GenerateDropTableDDL("test_users")
			if err != nil {
				t.Fatalf("GenerateDropTableDDL 失败: %v", err)
			}
			if strings.TrimSpace(dropDDL) == "" {
				t.Error("GenerateDropTableDDL 返回空 DDL")
			}
		})
	}
}

// TestAdapterGetTriggersAndRoutines 验证所有适配器都实现了触发器和存储过程接口方法。
// 不连接数据库，仅验证方法存在且不会 panic（返回空切片或 error 均可）。
func TestAdapterGetTriggersAndRoutines(t *testing.T) {
	dbTypes := []types.DatabaseType{
		types.MySQL,
		types.PostgreSQL,
		types.SQLite,
		types.MariaDB,
		types.OceanBase,
		types.TiDB,
		types.PolarDB,
		types.OpenGauss,
		types.Dameng,
		types.KingbaseES,
		types.Aurora,
		types.CockroachDB,
		types.Oracle,
		types.MSSQL,
		types.Db2,
		types.MongoDB,
		types.Redis,
		types.Cassandra,
		types.ScyllaDB,
		types.InfluxDB,
		types.TimescaleDB,
		types.TDengine,
	}

	for _, dbType := range dbTypes {
		t.Run(string(dbType), func(t *testing.T) {
			a := types.NewAdapter(dbType)
			if a == nil {
				t.Skipf("适配器 %s 未注册", dbType)
			}

			// GenerateTriggerDDL 是纯函数，不连数据库也能调用
			trigger := types.TriggerMeta{
				Name:   "test_trigger",
				Event:  "INSERT",
				Timing: "AFTER",
				Table:  "test_table",
				Body:   "INSERT INTO log VALUES (1)",
			}

			// 尝试生成到各种目标方言
			targets := []types.DatabaseType{
				types.MySQL, types.PostgreSQL, types.SQLite,
			}

			for _, target := range targets {
				ddl, err := a.GenerateTriggerDDL(trigger, target)
				// 某些适配器可能不支持某些目标方言，err != nil 是可接受的
				if err == nil && ddl == "" {
					t.Errorf("GenerateTriggerDDL(%s→%s) 返回空 DDL 且无错误", dbType, target)
				}
			}

			// GenerateRoutineDDL
			routine := types.RoutineMeta{
				Name: "test_proc",
				Type: "procedure",
				Body: "SELECT 1",
			}
			for _, target := range targets {
				ddl, err := a.GenerateRoutineDDL(routine, target)
				if err == nil && ddl == "" {
					t.Errorf("GenerateRoutineDDL(%s→%s) 返回空 DDL 且无错误", dbType, target)
				}
			}
		})
	}
}

func strPtr(s string) *string { return &s }
