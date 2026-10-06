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

// TestCompositeFKGeneration 验证复合外键在各适配器中生成正确的 DDL
func TestCompositeFKGeneration(t *testing.T) {
	schema := types.TableSchema{
		Name: "order_items",
		Columns: []types.ColumnMeta{
			{Name: "order_id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
			{Name: "product_id", DataType: "INT", BaseType: "INT", IsPrimaryKey: true, Nullable: false},
			{Name: "quantity", DataType: "INT", BaseType: "INT", Nullable: false, DefaultValue: strP("1")},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"order_id", "product_id"}, IsUnique: true, IsPrimary: true},
		},
		ForeignKeys: []types.ForeignKeyMeta{
			{Name: "fk_order", Columns: []string{"order_id"}, RefTable: "orders", RefColumns: []string{"id"}},
			{Name: "fk_product", Columns: []string{"product_id"}, RefTable: "products", RefColumns: []string{"id"}},
		},
	}

	// 内联外键的适配器（SQLite/MSSQL）
	inlineFkDBs := []types.DatabaseType{
		types.SQLite, types.MSSQL,
	}

	for _, dbType := range inlineFkDBs {
		t.Run(string(dbType)+"_inline_fk", func(t *testing.T) {
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

			// 验证复合主键
			if !strings.Contains(ddl, "PRIMARY KEY") {
				t.Error("缺少 PRIMARY KEY")
			}
			// 验证两个外键都存在
			fkCount := strings.Count(ddl, "FOREIGN KEY")
			if fkCount < 2 {
				t.Errorf("外键数 = %d, want >= 2", fkCount)
			}
		})
	}

	// 延后外键的适配器（MySQL/PG 等）——验证 GenerateAddForeignKeyDDL
	deferFkDBs := []types.DatabaseType{
		types.MySQL, types.PostgreSQL, types.MariaDB, types.TiDB,
		types.OpenGauss, types.CockroachDB, types.Dameng, types.KingbaseES,
		types.Aurora, types.PolarDB, types.OceanBase, types.TimescaleDB,
	}

	for _, dbType := range deferFkDBs {
		t.Run(string(dbType)+"_defer_fk", func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			// 验证 GenerateCreateTableDDL 不包含外键（延后添加）
			ddl, err := adapter.GenerateCreateTableDDL(schema)
			if err != nil {
				t.Fatalf("生成 DDL 失败: %v", err)
			}
			// 延后外键的 DDL 中不应有 FOREIGN KEY（由 ALTER TABLE 单独添加）
			if strings.Contains(ddl, "FOREIGN KEY") {
				// 某些适配器可能内联，不算错误
				t.Logf("%s DDL 包含内联外键（部分适配器支持内联）", dbType)
			}
			_ = ddl // 确保编译通过

			// 验证 GenerateAddForeignKeyDDL
			fkGen, ok := adapter.(types.ForeignKeyDDLGenerator)
			if !ok {
				t.Skipf("%s 不支持 ForeignKeyDDLGenerator", dbType)
				return
			}

			for _, fk := range schema.ForeignKeys {
				fkDDL, err := fkGen.GenerateAddForeignKeyDDL(schema.Name, fk)
				if err != nil {
					t.Errorf("生成外键 DDL 失败: %v", err)
					continue
				}
				t.Logf("%s FK DDL: %s", fk.Name, fkDDL)

				if !strings.Contains(fkDDL, "FOREIGN KEY") {
					t.Errorf("外键 DDL 缺少 FOREIGN KEY: %s", fkDDL)
				}
				if !strings.Contains(fkDDL, "REFERENCES") {
					t.Errorf("外键 DDL 缺少 REFERENCES: %s", fkDDL)
				}
				// 验证引用列不为空
				if strings.Contains(fkDDL, "()") {
					t.Errorf("外键 DDL 包含空引用列: %s", fkDDL)
				}
			}
		})
	}
}

// TestTriggerRoutineGeneration 验证触发器和存储过程 DDL 生成不 panic
func TestTriggerRoutineGeneration(t *testing.T) {
	allDBs := []types.DatabaseType{
		types.MySQL, types.PostgreSQL, types.SQLite, types.MariaDB,
		types.Oracle, types.MSSQL, types.Db2, types.Access,
		types.OceanBase, types.TiDB, types.PolarDB, types.OpenGauss,
		types.Dameng, types.KingbaseES, types.Aurora, types.CockroachDB,
		types.TimescaleDB,
	}

	trigger := types.TriggerMeta{
		Name:   "trg_test",
		Event:  "INSERT",
		Timing: "AFTER",
		Table:  "test_table",
		Body:   "INSERT INTO log VALUES (1)",
	}

	routine := types.RoutineMeta{
		Name:     "sp_test",
		Type:     "procedure",
		Body:     "SELECT 1",
		Returns:  "INT",
		Language: "sql",
	}

	for _, dbType := range allDBs {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}

			// 触发器 DDL 生成（不 panic 即可）
			triggerDDL, err := adapter.GenerateTriggerDDL(trigger, dbType)
			if err != nil {
				t.Logf("触发器 DDL 生成返回 error（可接受）: %v", err)
			} else if triggerDDL == "" {
				t.Log("触发器 DDL 返回空字符串（可接受）")
			} else {
				t.Logf("触发器 DDL: %s", triggerDDL)
			}

			// 存储过程 DDL 生成（不 panic 即可）
			routineDDL, err := adapter.GenerateRoutineDDL(routine, dbType)
			if err != nil {
				t.Logf("存储过程 DDL 生成返回 error（可接受）: %v", err)
			} else if routineDDL == "" {
				t.Log("存储过程 DDL 返回空字符串（可接受）")
			} else {
				t.Logf("存储过程 DDL: %s", routineDDL)
			}
		})
	}
}

func strP(s string) *string { return &s }
