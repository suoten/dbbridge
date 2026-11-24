package pgcompat

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// TestStripTypeSchema 类型名的 schema 限定在目标 DDL 中统一去掉
// （类型随目标库默认 schema 创建，与表名映射策略一致）。
func TestStripTypeSchema(t *testing.T) {
	cases := []struct{ in, want string }{
		{"s1.mood", "mood"},
		{"public.geometry", "geometry"},
		{"s1.mood[]", "mood[]"},
		{"geometry(PointZ,4490)", "geometry(PointZ,4490)"},
		{"text[]", "text[]"},
		{"inet", "inet"},
		{"numeric(10,2)", "numeric(10,2)"},
		{`"weird.name"`, `"weird.name"`},
	}
	for _, c := range cases {
		if got := stripTypeSchema(c.in); got != c.want {
			t.Errorf("stripTypeSchema(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGenerateCreateTableDDLExactType 精确类型列（PostGIS geometry、枚举、数组）
// 必须按精确类型名建表，绝不能再出现 information_schema 的 USER-DEFINED / ARRAY 占位名。
func TestGenerateCreateTableDDLExactType(t *testing.T) {
	a := New("postgres")
	ddl, err := a.GenerateCreateTableDDL(types.TableSchema{
		Name: "building_collect_data",
		Columns: []types.ColumnMeta{
			{Name: "id", DataType: "bigint", BaseType: "BIGINT"},
			{Name: "geom", DataType: "geometry(PointZ,4490)", BaseType: "GEOMETRY(POINTZ,4490)", ExactType: true},
			{Name: "tags", DataType: "text[]", BaseType: "TEXT[]", ExactType: true},
			{Name: "m", DataType: "mood", BaseType: "MOOD", ExactType: true},
			{Name: "address", DataType: "character varying", BaseType: "CHARACTER VARYING", Length: intPtr(255)},
		},
	})
	if err != nil {
		t.Fatalf("生成建表 DDL 失败: %v", err)
	}
	for _, want := range []string{
		`"geom" geometry(PointZ,4490)`,
		`"tags" text[]`,
		`"m" mood`,
		`"address" VARCHAR(255)`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL 缺少 %q:\n%s", want, ddl)
		}
	}
	for _, bad := range []string{"USER-DEFINED", " ARRAY", "GEOMETRY("} {
		if strings.Contains(ddl, bad) {
			t.Errorf("DDL 不应包含 %q:\n%s", bad, ddl)
		}
	}
}

func intPtr(n int) *int { return &n }
