package typeconv

import (
	"testing"

	types "dbbridge/pkg"
)

// TestExactTypePassthroughAndFallback 精确类型（PG 扩展类型/枚举/数组等，
// 即 information_schema 报 USER-DEFINED/ARRAY 的列）：PG 系目标原样使用，
// 其他目标退回各自的通用类型（值按文本迁移）。
func TestExactTypePassthroughAndFallback(t *testing.T) {
	geometry := types.ColumnMeta{
		Name:      "geom",
		DataType:  "geometry(PointZ,4490)",
		BaseType:  "GEOMETRY(POINTZ,4490)",
		ExactType: true,
	}
	if got := Normalize(geometry.BaseType); got != KindUnknown {
		t.Errorf("Normalize(%q) = %q, want KindUnknown（精确类型不应被误归到中立类型）", geometry.BaseType, got)
	}

	enum := types.ColumnMeta{Name: "m", DataType: "mood", BaseType: "MOOD", ExactType: true}
	array := types.ColumnMeta{Name: "tags", DataType: "text[]", BaseType: "TEXT[]", ExactType: true}
	// BaseType 是 TEXT 时若不保持精确类型标记，数组会被归一成普通 TEXT 而丢失维度
	if got := Normalize(array.BaseType); got != KindUnknown {
		t.Errorf("Normalize(%q) = %q, want KindUnknown", array.BaseType, got)
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"PG geometry 保真", ToPostgres(Normalize(geometry.BaseType), geometry), "geometry(PointZ,4490)"},
		{"PG 枚举保真", ToPostgres(Normalize(enum.BaseType), enum), "mood"},
		{"PG 数组保真", ToPostgres(Normalize(array.BaseType), array), "text[]"},
		{"MySQL geometry", ToMySQL(Normalize(geometry.BaseType), geometry), "LONGTEXT"},
		{"MySQL 数组", ToMySQL(Normalize(array.BaseType), array), "LONGTEXT"},
		{"SQLite geometry", ToSQLite(Normalize(geometry.BaseType), geometry), "TEXT"},
		{"MSSQL geometry", ToMSSQL(Normalize(geometry.BaseType), geometry), "NVARCHAR(MAX)"},
		{"Oracle geometry", ToOracle(Normalize(geometry.BaseType), geometry), "CLOB"},
		{"Db2 geometry", ToDb2(Normalize(geometry.BaseType), geometry), "CLOB(1M)"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}

	// 带非常量默认值的精确类型列（典型：枚举 DEFAULT 'ok'）不能落成 TEXT 家族，
	// 否则 MySQL 报 Error 1101（TEXT 列不能有默认值）
	withDefault := enum
	v := "'ok'"
	withDefault.DefaultValue = &v
	if got := ToMySQL(Normalize(withDefault.BaseType), withDefault); got != "VARCHAR(500)" {
		t.Errorf("MySQL 带默认值精确类型: got %q, want VARCHAR(500)", got)
	}
}

// TestNonExactUnknownTypeUnchanged 非精确类型的未知类型保持原有行为：
// 各方言原样透传原始类型声明（回归保护）。
func TestNonExactUnknownTypeUnchanged(t *testing.T) {
	col := types.ColumnMeta{Name: "c", DataType: "weirdtype", BaseType: "WEIRDTYPE"}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"PG", ToPostgres(Normalize(col.BaseType), col), "weirdtype"},
		{"MySQL", ToMySQL(Normalize(col.BaseType), col), "weirdtype"},
		{"MSSQL", ToMSSQL(Normalize(col.BaseType), col), "weirdtype"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}

	// 已有中立类型映射的列不受 ExactType 逻辑影响
	text := types.ColumnMeta{Name: "c", DataType: "text", BaseType: "TEXT"}
	if got := ToPostgres(Normalize(text.BaseType), text); got != "TEXT" {
		t.Errorf("普通 TEXT 列: got %q, want TEXT", got)
	}
	if got := ToMySQL(Normalize(text.BaseType), text); got != "TEXT" {
		t.Errorf("普通 TEXT 列 MySQL: got %q, want TEXT", got)
	}
}
