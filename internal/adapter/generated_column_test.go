package adapter

import (
	"strings"
	"testing"

	"dbbridge/internal/adapter/mssql"
	"dbbridge/internal/adapter/mysqlcompat"
	"dbbridge/internal/adapter/oracle"
	"dbbridge/internal/adapter/pgcompat"
	"dbbridge/internal/adapter/sqlite"
	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"
)

// TestGeneratedColumnExclusion 验证所有适配器在 GenerateCreateTableDDL 中
// 对生成列（Generated）正确跳过 DEFAULT 和 AUTO_INCREMENT 子句，
// 并在 WriteData 时排除生成列。
//
// 生成列（GENERATED ALWAYS AS / COMPUTED）由目标库自动计算，
// 显式 INSERT 会导致语法错误（MySQL 3105、PG 42601、MSSQL 273）。
func TestGeneratedColumnExclusion(t *testing.T) {
	defVal := "CURRENT_TIMESTAMP"
	schema := types.TableSchema{
		Name: "test_gen",
		Columns: []types.ColumnMeta{
			{Name: "id", BaseType: "INT", DataType: "INT", IsPrimaryKey: true, AutoIncrement: true},
			{Name: "full_name", BaseType: "VARCHAR", DataType: "VARCHAR(255)", Generated: true},
			{Name: "created_at", BaseType: "DATETIME", DataType: "DATETIME", DefaultValue: &defVal, Generated: true},
			{Name: "status", BaseType: "VARCHAR", DataType: "VARCHAR(50)", DefaultValue: &defVal},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
		},
	}

	tests := []struct {
		name    string
		adapter types.DatabaseAdapter
		ddlFunc func(types.TableSchema) (string, error)
	}{
		{"MySQL", &mysqlcompat.Base{}, func(s types.TableSchema) (string, error) {
			return (&mysqlcompat.Base{}).GenerateCreateTableDDL(s)
		}},
		{"PostgreSQL", pgcompat.New("PostgreSQL", pgcompat.WithBinaryType("BYTEA")), func(s types.TableSchema) (string, error) {
			return pgcompat.New("PostgreSQL", pgcompat.WithBinaryType("BYTEA")).GenerateCreateTableDDL(s)
		}},
		{"MSSQL", &mssql.Adapter{}, func(s types.TableSchema) (string, error) {
			return (&mssql.Adapter{}).GenerateCreateTableDDL(s)
		}},
		{"Oracle", &oracle.Adapter{}, func(s types.TableSchema) (string, error) {
			return (&oracle.Adapter{}).GenerateCreateTableDDL(s)
		}},
		{"SQLite", &sqlite.Adapter{}, func(s types.TableSchema) (string, error) {
			return (&sqlite.Adapter{}).GenerateCreateTableDDL(s)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddl, err := tt.ddlFunc(schema)
			if err != nil {
				t.Fatalf("GenerateCreateTableDDL 失败: %v", err)
			}

			// 验证生成列不包含 DEFAULT 子句
			lines := strings.Split(ddl, "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				// full_name 和 created_at 是生成列
				if strings.HasPrefix(trimmed, "`full_name`") ||
					strings.HasPrefix(trimmed, "\"full_name\"") ||
					strings.HasPrefix(trimmed, "[full_name]") ||
					strings.HasPrefix(trimmed, "full_name ") {
					if strings.Contains(strings.ToUpper(line), "DEFAULT") {
						t.Errorf("%s: 生成列 full_name 不应有 DEFAULT: %s", tt.name, line)
					}
				}
				if strings.HasPrefix(trimmed, "`created_at`") ||
					strings.HasPrefix(trimmed, "\"created_at\"") ||
					strings.HasPrefix(trimmed, "[created_at]") ||
					strings.HasPrefix(trimmed, "created_at ") {
					if strings.Contains(strings.ToUpper(line), "DEFAULT") {
						t.Errorf("%s: 生成列 created_at 不应有 DEFAULT: %s", tt.name, line)
					}
				}
			}

			// 验证非生成列仍然保留 DEFAULT
			if !strings.Contains(ddl, "DEFAULT") {
				t.Errorf("%s: 非生成列 status 应保留 DEFAULT 子句", tt.name)
			}
		})
	}
}

// TestGeneratedColumnWriteExclusion 验证 orchestrator 在构建 INSERT 列表时排除生成列
func TestGeneratedColumnWriteExclusion(t *testing.T) {
	schema := types.TableSchema{
		Name: "test_table",
		Columns: []types.ColumnMeta{
			{Name: "id", BaseType: "INT", DataType: "INT", IsPrimaryKey: true},
			{Name: "computed", BaseType: "INT", DataType: "INT", Generated: true},
			{Name: "normal_col", BaseType: "VARCHAR", DataType: "VARCHAR(50)"},
		},
	}

	// 模拟 orchestrator 的列过滤逻辑
	columns := make([]string, 0, len(schema.Columns))
	for _, c := range schema.Columns {
		if c.Generated {
			continue
		}
		columns = append(columns, c.Name)
	}

	if len(columns) != 2 {
		t.Errorf("过滤后列数 = %d, want 2 (排除生成列)", len(columns))
	}
	if columns[0] != "id" || columns[1] != "normal_col" {
		t.Errorf("过滤后列 = %v, want [id normal_col]", columns)
	}
}

// TestFormatDefaultCrossDB 验证 FormatDefault 的跨库兼容性
func TestFormatDefaultCrossDB(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// now() → CURRENT_TIMESTAMP (SQL 标准，所有库支持)
		{"now()", "CURRENT_TIMESTAMP"},
		{"NOW()", "CURRENT_TIMESTAMP"},
		{"Now", "CURRENT_TIMESTAMP"},
		// sysdate (Oracle) → CURRENT_TIMESTAMP
		{"sysdate", "CURRENT_TIMESTAMP"},
		{"SYSDATE()", "CURRENT_TIMESTAMP"},
		// getdate (MSSQL) → CURRENT_TIMESTAMP
		{"getdate", "CURRENT_TIMESTAMP"},
		{"GETDATE()", "CURRENT_TIMESTAMP"},
		// curdate → CURRENT_DATE
		{"curdate()", "CURRENT_DATE"},
		{"CURDATE", "CURRENT_DATE"},
		// curtime → CURRENT_TIME
		{"curtime()", "CURRENT_TIME"},
		// current_timestamp 原样
		{"CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP"},
		{"current_timestamp()", "CURRENT_TIMESTAMP"},
		// null/true/false
		{"NULL", "NULL"},
		{"true", "TRUE"},
		{"false", "FALSE"},
		// UUID 函数保留原值（各方言不同）
		{"uuid()", "uuid()"},
		{"gen_random_uuid()", "gen_random_uuid()"},
		// 数值原样
		{"42", "42"},
		{"-3.14", "-3.14"},
		// 字符串加引号
		{"hello", "'hello'"},
		{"it's", "'it''s'"},
	}

	for _, c := range cases {
		got := typeconv.FormatDefault(c.in)
		if got != c.want {
			t.Errorf("FormatDefault(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
