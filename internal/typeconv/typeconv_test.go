package typeconv

import (
	"testing"

	types "dbbridge/pkg"
)

func TestFormatDefault(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 数值字面量原样
		{"0", "0"},
		{"0.00", "0.00"},
		{"-12.5", "-12.5"},
		{"42", "42"},
		// 常见函数/关键字原样（大小写、带括号）
		{"CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP"},
		{"current_timestamp()", "current_timestamp()"},
		{"NOW()", "NOW()"},
		{"NULL", "NULL"},
		{"null", "null"},
		{"gen_random_uuid()", "gen_random_uuid()"},
		{"LOCALTIME", "LOCALTIME"},
		// 字符串默认值必须加引号（BUG-7 核心场景）
		{"pending", "'pending'"},
		{"PENDING", "'PENDING'"},
		{"0.00 元", "'0.00 元'"},
		{"it's", "'it''s'"},
		// 已带引号原样（PG/SQLite 元数据）
		{"'abc'", "'abc'"},
		{"'abc'::text", "'abc'::text"},
		// 空值
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := FormatDefault(c.in); got != c.want {
			t.Errorf("FormatDefault(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// MySQL 禁止 TEXT/BLOB/JSON 列带非常量 DEFAULT（Error 1101/1067），
// 需降级或语义还原（BUG-13）
func TestToMySQLTextualWithDefault(t *testing.T) {
	cases := []struct {
		kind Kind
		def  *string
		want string
	}{
		{KindText, nil, "TEXT"},
		{KindText, strPtr(""), "TEXT"},              // 空默认值 = 无默认
		{KindText, strPtr("NULL"), "TEXT"},          // DEFAULT NULL 在 TEXT 上合法
		{KindText, strPtr("pending"), "VARCHAR(255)"},
		{KindText, strPtr("CURRENT_TIMESTAMP"), "DATETIME"},  // 时间语义还原
		{KindText, strPtr("now()"), "DATETIME"},
		{KindBlob, nil, "BLOB"},
		{KindBlob, strPtr("NULL"), "BLOB"},
		{KindBlob, strPtr("pending"), "VARBINARY(255)"},
		{KindJSON, nil, "JSON"},
		{KindJSON, strPtr("NULL"), "JSON"},
		{KindJSON, strPtr("{}"), "VARCHAR(255)"},
	}
	for _, c := range cases {
		col := types.ColumnMeta{DataType: "X", DefaultValue: c.def}
		if got := ToMySQL(c.kind, col); got != c.want {
			t.Errorf("ToMySQL(%s, default=%q) = %q, want %q", c.kind, ptrVal(c.def), got, c.want)
		}
	}

	// 带长度的 TEXT 有默认值时保留长度
	n := 128
	col := types.ColumnMeta{Length: &n, DefaultValue: strPtr("pending")}
	if got := ToMySQL(KindText, col); got != "VARCHAR(128)" {
		t.Errorf("ToMySQL(KindText, len=128, default) = %q, want VARCHAR(128)", got)
	}
}

func strPtr(s string) *string { return &s }

// Go time.Time 字符串形式应还原为标准 datetime（SQLite time.Time 直写 TEXT 列的场景）
func TestNormalizeTimeValue(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		changed bool
	}{
		{"2026-08-30 16:35:31 +0000 UTC", "2026-08-30 16:35:31", true},
		{"2026-08-30 16:35:31.123456 +0800 CST", "2026-08-30 16:35:31.123456", true},
		{"2026-08-30 16:35:31 +0000 UTC m=+0.001", "2026-08-30 16:35:31", true},
		{"2026-08-30 16:35:31", "2026-08-30 16:35:31", false},
		{"2026-08-30T16:35:31Z", "2026-08-30T16:35:31Z", false},
		{"pending", "pending", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, changed := NormalizeTimeValue(c.in)
		if got != c.want || changed != c.changed {
			t.Errorf("NormalizeTimeValue(%q) = (%q, %v), want (%q, %v)", c.in, got, changed, c.want, c.changed)
		}
	}
}

func ptrVal(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
