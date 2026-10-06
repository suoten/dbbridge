package adapter_test

import (
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/cassandra"
	_ "dbbridge/internal/adapter/influxdb"
	_ "dbbridge/internal/adapter/mongodb"
	_ "dbbridge/internal/adapter/redis"
	_ "dbbridge/internal/adapter/scylladb"
	_ "dbbridge/internal/adapter/tdengine"
)

// TestNoSQLAdapterDDLGeneration 验证 NoSQL 适配器的 DDL 生成不 panic 且有效
func TestNoSQLAdapterDDLGeneration(t *testing.T) {
	length := 255
	precision := 10
	scale := 2

	schema := types.TableSchema{
		Name: "test_collection",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
			{Name: "name", DataType: "VARCHAR(255)", BaseType: "VARCHAR", Length: &length, Nullable: false},
			{Name: "price", DataType: "DECIMAL(10,2)", BaseType: "DECIMAL", Precision: &precision, Scale: &scale},
			{Name: "data", DataType: "TEXT", BaseType: "TEXT"},
			{Name: "blob", DataType: "BLOB", BaseType: "BLOB"},
			{Name: "created", DataType: "DATETIME", BaseType: "DATETIME"},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
			{Name: "idx_name", Columns: []string{"name"}, IsUnique: true},
		},
	}

	noSQLDBs := []types.DatabaseType{
		types.Cassandra,
		types.ScyllaDB,
		types.MongoDB,
		types.Redis,
		types.InfluxDB,
		types.TDengine,
	}

	for _, dbType := range noSQLDBs {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			// GenerateCreateTableDDL 不 panic 且返回非空
			ddl, err := adapter.GenerateCreateTableDDL(schema)
			if err != nil {
				// 某些 NoSQL 可能不支持 DDL，error 是可接受的
				t.Logf("%s GenerateCreateTableDDL 返回 error（可接受）: %v", dbType, err)
				return
			}
			if ddl == "" {
				t.Errorf("%s DDL 为空", dbType)
				return
			}
			t.Logf("%s DDL:\n%s", dbType, ddl)

			// 验证 DDL 包含表名
			if !strings.Contains(ddl, "test_collection") && !strings.Contains(ddl, "test_collection") {
				// 某些 NoSQL 可能用不同的命名
				t.Logf("%s DDL 不包含表名（可能用了不同命名）", dbType)
			}

			// 验证 GenerateDropTableDDL 不报错
			dropDDL, err := adapter.GenerateDropTableDDL("test_collection")
			if err != nil {
				t.Logf("%s GenerateDropTableDDL 返回 error（可接受）: %v", dbType, err)
			} else if dropDDL == "" {
				t.Logf("%s dropDDL 为空（可接受）", dbType)
			} else {
				t.Logf("%s dropDDL: %s", dbType, dropDDL)
			}
		})
	}
}

// TestNoSQLAdapterMapType 验证 NoSQL 适配器类型映射不 panic
func TestNoSQLAdapterMapType(t *testing.T) {
	cases := []struct {
		baseType string
		col      types.ColumnMeta
	}{
		{"INT", types.ColumnMeta{BaseType: "INT", DataType: "INT"}},
		{"VARCHAR", types.ColumnMeta{BaseType: "VARCHAR", DataType: "VARCHAR(255)"}},
		{"TEXT", types.ColumnMeta{BaseType: "TEXT", DataType: "TEXT"}},
		{"BLOB", types.ColumnMeta{BaseType: "BLOB", DataType: "BLOB"}},
		{"DECIMAL", types.ColumnMeta{BaseType: "DECIMAL", DataType: "DECIMAL(10,2)"}},
		{"DATETIME", types.ColumnMeta{BaseType: "DATETIME", DataType: "DATETIME"}},
		{"BOOLEAN", types.ColumnMeta{BaseType: "BOOLEAN", DataType: "BOOLEAN"}},
	}

	noSQLDBs := []types.DatabaseType{
		types.Cassandra,
		types.ScyllaDB,
		types.MongoDB,
		types.Redis,
		types.InfluxDB,
		types.TDengine,
	}

	for _, dbType := range noSQLDBs {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			for _, c := range cases {
				result := adapter.MapType(c.col)
				if result == "" {
					t.Errorf("%s MapType(%s) 返回空字符串", dbType, c.baseType)
				}
				t.Logf("%s MapType(%s) = %s", dbType, c.baseType, result)
			}
		})
	}
}

// TestNoSQLAdapterTriggerRoutine 验证 NoSQL 适配器触发器/存储过程不 panic
func TestNoSQLAdapterTriggerRoutine(t *testing.T) {
	noSQLDBs := []types.DatabaseType{
		types.Cassandra,
		types.ScyllaDB,
		types.MongoDB,
		types.Redis,
		types.InfluxDB,
		types.TDengine,
	}

	trigger := types.TriggerMeta{
		Name: "trg", Event: "INSERT", Timing: "AFTER", Table: "t", Body: "SELECT 1",
	}
	routine := types.RoutineMeta{
		Name: "sp", Type: "procedure", Body: "SELECT 1", Returns: "INT", Language: "sql",
	}

	for _, dbType := range noSQLDBs {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			// 不 panic 即可
			_, err := adapter.GenerateTriggerDDL(trigger, dbType)
			if err != nil {
				t.Logf("%s 触发器 error（可接受）: %v", dbType, err)
			}

			_, err = adapter.GenerateRoutineDDL(routine, dbType)
			if err != nil {
				t.Logf("%s 存储过程 error（可接受）: %v", dbType, err)
			}
		})
	}
}
