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
		{KindText, strPtr(""), "TEXT"},     // 空默认值 = 无默认
		{KindText, strPtr("NULL"), "TEXT"}, // DEFAULT NULL 在 TEXT 上合法
		{KindText, strPtr("pending"), "VARCHAR(255)"},
		{KindText, strPtr("CURRENT_TIMESTAMP"), "DATETIME"}, // 时间语义还原
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

// TestToMSSQL 验证中立类型 → MSSQL 类型映射
func TestToMSSQL(t *testing.T) {
	p10 := 10
	s2 := 2
	len255 := 255
	len128 := 128
	len5000 := 5000

	cases := []struct {
		name string
		kind Kind
		col  types.ColumnMeta
		want string
	}{
		// 整数
		{"TINYINT", KindTinyInt, types.ColumnMeta{}, "TINYINT"},
		{"SMALLINT", KindSmallInt, types.ColumnMeta{}, "SMALLINT"},
		{"INT", KindInt, types.ColumnMeta{}, "INT"},
		{"BIGINT", KindBigInt, types.ColumnMeta{}, "BIGINT"},
		{"YEAR→SMALLINT", KindYear, types.ColumnMeta{}, "SMALLINT"},
		// 小数
		{"DECIMAL(p,s)", KindDecimal, types.ColumnMeta{Precision: &p10, Scale: &s2}, "DECIMAL(10, 2)"},
		{"DECIMAL无参数", KindDecimal, types.ColumnMeta{}, "DECIMAL(18,0)"},
		{"FLOAT→REAL", KindFloat, types.ColumnMeta{}, "REAL"},
		{"DOUBLE→FLOAT(53)", KindDouble, types.ColumnMeta{}, "FLOAT(53)"},
		// 布尔/位
		{"BOOL→BIT", KindBool, types.ColumnMeta{}, "BIT"},
		{"BIT单列", KindBit, types.ColumnMeta{}, "BIT"},
		{"BIT多列→BINARY", KindBit, types.ColumnMeta{Length: &len128}, "BINARY(128)"},
		// 字符串
		{"CHAR默认", KindChar, types.ColumnMeta{}, "NCHAR(1)"},
		{"CHAR带长度", KindChar, types.ColumnMeta{Length: &len128}, "NCHAR(128)"},
		{"VARCHAR默认", KindVarChar, types.ColumnMeta{}, "NVARCHAR(255)"},
		{"VARCHAR带长度", KindVarChar, types.ColumnMeta{Length: &len128}, "NVARCHAR(128)"},
		{"VARCHAR超4000→MAX", KindVarChar, types.ColumnMeta{Length: &len5000}, "NVARCHAR(MAX)"},
		{"TEXT→NVARCHAR(MAX)", KindText, types.ColumnMeta{}, "NVARCHAR(MAX)"},
		// 二进制
		{"BLOB无长度→MAX", KindBlob, types.ColumnMeta{}, "VARBINARY(MAX)"},
		{"BLOB有长度", KindBlob, types.ColumnMeta{Length: &len255}, "VARBINARY(255)"},
		{"BINARY有长度", KindBinary, types.ColumnMeta{Length: &len255}, "VARBINARY(255)"},
		// 日期时间
		{"DATE", KindDate, types.ColumnMeta{}, "DATE"},
		{"TIME", KindTime, types.ColumnMeta{}, "TIME"},
		{"DATETIME→DATETIME2", KindDateTime, types.ColumnMeta{}, "DATETIME2"},
		{"TIMESTAMP→DATETIME2", KindTimestamp, types.ColumnMeta{}, "DATETIME2"},
		// 其他
		{"JSON→NVARCHAR(MAX)", KindJSON, types.ColumnMeta{}, "NVARCHAR(MAX)"},
		{"UUID→UNIQUEIDENTIFIER", KindUUID, types.ColumnMeta{}, "UNIQUEIDENTIFIER"},
		{"ENUM→NVARCHAR(50)", KindEnum, types.ColumnMeta{}, "NVARCHAR(50)"},
		{"SET→NVARCHAR(200)", KindSet, types.ColumnMeta{}, "NVARCHAR(200)"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ToMSSQL(c.kind, c.col)
			if got != c.want {
				t.Errorf("ToMSSQL(%s) = %q, want %q", c.kind, got, c.want)
			}
		})
	}
}

// TestNormalizeMSSQLTypes 验证 MSSQL 特有类型名能被 Normalize 正确识别
func TestNormalizeMSSQLTypes(t *testing.T) {
	cases := []struct {
		input string
		want  Kind
	}{
		// MSSQL 特有类型
		{"DATETIME2", KindDateTime},
		{"SMALLDATETIME", KindDateTime},
		{"NVARCHAR", KindVarChar},
		{"NCHAR", KindChar},
		{"NTEXT", KindText},
		{"IMAGE", KindBlob},
		// 通用类型（确保 MSSQL 与其他方言共用时不冲突）
		{"INT", KindInt},
		{"BIGINT", KindBigInt},
		{"VARCHAR", KindVarChar},
		{"TEXT", KindText},
		{"DECIMAL", KindDecimal},
		{"BIT", KindBit},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := Normalize(c.input)
			if got != c.want {
				t.Errorf("Normalize(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestToMSSQLToSizeNVarChar 验证 toSizeNVarChar 超限处理
func TestToMSSQLToSizeNVarChar(t *testing.T) {
	cases := []struct {
		name string
		n    *int
		def  int
		want string
	}{
		{"默认值", nil, 255, "NVARCHAR(255)"},
		{"有长度", intPtr(100), 255, "NVARCHAR(100)"},
		{"超4000用MAX", intPtr(4001), 255, "NVARCHAR(MAX)"},
		{"正好4000", intPtr(4000), 255, "NVARCHAR(4000)"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := toSizeNVarChar("NVARCHAR", c.n, c.def)
			if got != c.want {
				t.Errorf("toSizeNVarChar(NVARCHAR, %v, %d) = %q, want %q", c.n, c.def, got, c.want)
			}
		})
	}
}

func intPtr(i int) *int { return &i }
