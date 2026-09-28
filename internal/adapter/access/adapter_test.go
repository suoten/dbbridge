package access

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// TestGenerateCreateTableDDL 验证 Access 建表 DDL 生成
func TestGenerateCreateTableDDL(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	length := 128
	precision := 10
	scale := 2
	schema := types.TableSchema{
		Name: "users",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "INT", BaseType: "INT", AutoIncrement: true, IsPrimaryKey: true, Nullable: false},
			{Name: "name", DataType: "VARCHAR(128)", BaseType: "VARCHAR", Length: &length, Nullable: false},
			{Name: "score", DataType: "DECIMAL(10,2)", BaseType: "DECIMAL", Precision: &precision, Scale: &scale},
			{Name: "active", DataType: "TINYINT", BaseType: "TINYINT"},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
			{Name: "idx_name", Columns: []string{"name"}, IsUnique: false},
		},
	}

	ddl, err := a.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("GenerateCreateTableDDL 失败: %v", err)
	}

	// 表名用方括号包裹
	if !strings.Contains(ddl, "CREATE TABLE [users]") {
		t.Errorf("DDL 应包含 CREATE TABLE [users]: %s", ddl)
	}

	// AUTOINCREMENT 列
	if !strings.Contains(ddl, "[id] AUTOINCREMENT") {
		t.Errorf("DDL 应包含 AUTOINCREMENT: %s", ddl)
	}

	// NOT NULL 列
	if !strings.Contains(ddl, "[name] VARCHAR(128) NOT NULL") {
		t.Errorf("DDL 应包含 NOT NULL: %s", ddl)
	}

	// DECIMAL → CURRENCY
	if !strings.Contains(ddl, "[score] CURRENCY") {
		t.Errorf("DDL 应包含 CURRENCY: %s", ddl)
	}

	// 主键约束
	if !strings.Contains(ddl, "PRIMARY KEY ([id])") {
		t.Errorf("DDL 应包含 PRIMARY KEY: %s", ddl)
	}

	// 二级索引
	if !strings.Contains(ddl, "CREATE INDEX [idx_name] ON [users]") {
		t.Errorf("DDL 应包含二级索引: %s", ddl)
	}
}

// TestGenerateDropTableDDL 验证删表 DDL
func TestGenerateDropTableDDL(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	ddl, err := a.GenerateDropTableDDL("test_table")
	if err != nil {
		t.Fatalf("GenerateDropTableDDL 失败: %v", err)
	}
	if ddl != "DROP TABLE IF EXISTS [test_table]" {
		t.Errorf("DROP DDL = %q, 期望 DROP TABLE IF EXISTS [test_table]", ddl)
	}
}

// TestEscapeIdent 验证标识符转义
func TestEscapeIdent(t *testing.T) {
	// 正常标识符原样返回
	if got := escapeIdent("users"); got != "users" {
		t.Errorf("escapeIdent(users) = %q", got)
	}
	// ] 双写
	if got := escapeIdent("a]b"); got != "a]]b" {
		t.Errorf("escapeIdent(a]b) = %q", got)
	}
}

// TestQuoteIdentifiers 验证列名列表加方括号
func TestQuoteIdentifiers(t *testing.T) {
	got := quoteIdentifiers([]string{"id", "name", "email"})
	if got != "[id], [name], [email]" {
		t.Errorf("quoteIdentifiers = %q", got)
	}
}

// TestMapType 验证类型映射
func TestMapType(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	cases := []struct {
		baseType string
		want     string
	}{
		{"INT", "LONG"},
		{"BIGINT", "DOUBLE"},
		{"TINYINT", "SHORT"},
		{"SMALLINT", "SHORT"},
		{"DECIMAL", "CURRENCY"},
		{"FLOAT", "SINGLE"},
		{"DOUBLE", "DOUBLE"},
		{"BOOL", "YESNO"},
		{"TEXT", "LONGTEXT"},
		{"BLOB", "OLEOBJECT"},
		{"DATETIME", "DATETIME"},
		{"DATE", "DATETIME"},
	}
	for _, c := range cases {
		col := types.ColumnMeta{DataType: c.baseType, BaseType: c.baseType}
		got := a.MapType(col)
		if got != c.want {
			t.Errorf("MapType(%s) = %q, 期望 %q", c.baseType, got, c.want)
		}
	}
}

// TestMapTypeAutoIncrement 验证自增列映射
func TestMapTypeAutoIncrement(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	col := types.ColumnMeta{DataType: "INT", BaseType: "INT", AutoIncrement: true}
	got := a.MapType(col)
	if got != "AUTOINCREMENT" {
		t.Errorf("MapType(INT, AutoIncrement) = %q, 期望 AUTOINCREMENT", got)
	}
}

// TestGetTriggersNotSupported 验证 Access 不支持触发器
func TestGetTriggersNotSupported(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	triggers, err := a.GetTriggers(nil)
	if err != nil {
		t.Errorf("GetTriggers 不应返回错误: %v", err)
	}
	if triggers != nil {
		t.Errorf("GetTriggers 应返回 nil")
	}
}

// TestGetRoutinesNotSupported 验证 Access 不支持存储过程
func TestGetRoutinesNotSupported(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	routines, err := a.GetRoutines(nil)
	if err != nil {
		t.Errorf("GetRoutines 不应返回错误: %v", err)
	}
	if routines != nil {
		t.Errorf("GetRoutines 应返回 nil")
	}
}

// TestGenerateTriggerDDLNotSupported 验证 Access 不支持触发器迁移
func TestGenerateTriggerDDLNotSupported(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	_, err := a.GenerateTriggerDDL(types.TriggerMeta{}, types.MySQL)
	if err == nil {
		t.Error("GenerateTriggerDDL 应返回错误")
	}
}

// TestGenerateRoutineDDLNotSupported 验证 Access 不支持存储过程迁移
func TestGenerateRoutineDDLNotSupported(t *testing.T) {
	a := &Adapter{brand: "Microsoft Access"}
	_, err := a.GenerateRoutineDDL(types.RoutineMeta{}, types.MySQL)
	if err == nil {
		t.Error("GenerateRoutineDDL 应返回错误")
	}
}
