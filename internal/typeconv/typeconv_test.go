package typeconv

import "testing"

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
