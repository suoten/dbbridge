package typeconv

import (
	"testing"

	types "dbbridge/pkg"
)

// TestToAccess 验证中立类型 → Access 类型映射
func TestToAccess(t *testing.T) {
	len128 := 128
	len50 := 50

	cases := []struct {
		name string
		kind Kind
		col  types.ColumnMeta
		want string
	}{
		{"TINYINT→SHORT", KindTinyInt, types.ColumnMeta{}, "SHORT"},
		{"SMALLINT→SHORT", KindSmallInt, types.ColumnMeta{}, "SHORT"},
		{"YEAR→SHORT", KindYear, types.ColumnMeta{}, "SHORT"},
		{"INT→LONG", KindInt, types.ColumnMeta{}, "LONG"},
		{"INT自增→AUTOINCREMENT", KindInt, types.ColumnMeta{AutoIncrement: true}, "AUTOINCREMENT"},
		{"BIGINT→DOUBLE", KindBigInt, types.ColumnMeta{}, "DOUBLE"},
		{"DECIMAL→CURRENCY", KindDecimal, types.ColumnMeta{}, "CURRENCY"},
		{"FLOAT→SINGLE", KindFloat, types.ColumnMeta{}, "SINGLE"},
		{"DOUBLE→DOUBLE", KindDouble, types.ColumnMeta{}, "DOUBLE"},
		{"BOOL→YESNO", KindBool, types.ColumnMeta{}, "YESNO"},
		{"BIT→YESNO", KindBit, types.ColumnMeta{}, "YESNO"},
		{"CHAR默认→VARCHAR(50)", KindChar, types.ColumnMeta{}, "VARCHAR(50)"},
		{"CHAR带长度", KindChar, types.ColumnMeta{Length: &len50}, "VARCHAR(50)"},
		{"VARCHAR默认→VARCHAR(255)", KindVarChar, types.ColumnMeta{}, "VARCHAR(255)"},
		{"VARCHAR带长度", KindVarChar, types.ColumnMeta{Length: &len128}, "VARCHAR(128)"},
		{"TEXT→LONGTEXT", KindText, types.ColumnMeta{}, "LONGTEXT"},
		{"JSON→LONGTEXT", KindJSON, types.ColumnMeta{}, "LONGTEXT"},
		{"BLOB→OLEOBJECT", KindBlob, types.ColumnMeta{}, "OLEOBJECT"},
		{"BINARY→OLEOBJECT", KindBinary, types.ColumnMeta{}, "OLEOBJECT"},
		{"DATE→DATETIME", KindDate, types.ColumnMeta{}, "DATETIME"},
		{"TIME→DATETIME", KindTime, types.ColumnMeta{}, "DATETIME"},
		{"DATETIME→DATETIME", KindDateTime, types.ColumnMeta{}, "DATETIME"},
		{"TIMESTAMP→DATETIME", KindTimestamp, types.ColumnMeta{}, "DATETIME"},
		{"UUID→VARCHAR(38)", KindUUID, types.ColumnMeta{}, "VARCHAR(38)"},
		{"ENUM→VARCHAR(255)", KindEnum, types.ColumnMeta{}, "VARCHAR(255)"},
		{"SET→VARCHAR(255)", KindSet, types.ColumnMeta{}, "VARCHAR(255)"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ToAccess(c.kind, c.col)
			if got != c.want {
				t.Errorf("ToAccess(%s) = %q, want %q", c.kind, got, c.want)
			}
		})
	}
}

// TestNormalizeAccessTypes 验证 Access 特有类型名能被 Normalize 正确识别
func TestNormalizeAccessTypes(t *testing.T) {
	cases := []struct {
		input string
		want  Kind
	}{
		{"COUNTER", KindInt},       // Access 自增类型
		{"AUTOINCREMENT", KindInt}, // Access 自增类型别名
		{"YESNO", KindBool},
		{"OLEOBJECT", KindBlob},
		{"LONGTEXT", KindText},
		{"CURRENCY", KindDecimal},
		{"SINGLE", KindFloat},
		{"SHORT", KindSmallInt},
		{"LONG", KindInt},
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

// TestNormalizeEdgeCases 验证 Normalize 边界用例
func TestNormalizeEdgeCases(t *testing.T) {
	cases := []struct {
		input string
		want  Kind
	}{
		// 空字符串
		{"", KindUnknown},
		// 大小写混合
		{"Int", KindInt},
		{"VarChar", KindVarChar},
		{"Decimal", KindDecimal},
		// 带参数
		{"INT(11)", KindInt},
		{"VARCHAR(255)", KindVarChar},
		{"DECIMAL(10,2)", KindDecimal},
		// 空格
		{"  INT  ", KindInt},
		// UNSIGNED
		{"INT UNSIGNED", KindInt},
		{"BIGINT UNSIGNED", KindBigInt},
		// ZEROFILL
		{"INT ZEROFILL", KindInt},
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

// TestToOracle 验证中立类型 → Oracle 类型映射
func TestToOracle(t *testing.T) {
	len128 := 128
	p10 := 10
	s2 := 2

	cases := []struct {
		name string
		kind Kind
		col  types.ColumnMeta
		want string
	}{
		{"INT→NUMBER(10)", KindInt, types.ColumnMeta{}, "NUMBER(10)"},
		{"BIGINT→NUMBER(19)", KindBigInt, types.ColumnMeta{}, "NUMBER(19)"},
		{"SMALLINT→NUMBER(5)", KindSmallInt, types.ColumnMeta{}, "NUMBER(5)"},
		{"TINYINT→NUMBER(3)", KindTinyInt, types.ColumnMeta{}, "NUMBER(3)"},
		{"DECIMAL→NUMBER(p,s)", KindDecimal, types.ColumnMeta{Precision: &p10, Scale: &s2}, "NUMBER(10, 2)"},
		{"FLOAT→BINARY_FLOAT", KindFloat, types.ColumnMeta{}, "BINARY_FLOAT"},
		{"DOUBLE→BINARY_DOUBLE", KindDouble, types.ColumnMeta{}, "BINARY_DOUBLE"},
		{"VARCHAR→VARCHAR2", KindVarChar, types.ColumnMeta{Length: &len128}, "VARCHAR2(128)"},
		{"TEXT→CLOB", KindText, types.ColumnMeta{}, "CLOB"},
		{"BLOB→BLOB", KindBlob, types.ColumnMeta{}, "BLOB"},
		{"DATE→DATE", KindDate, types.ColumnMeta{}, "DATE"},
		{"DATETIME→TIMESTAMP", KindDateTime, types.ColumnMeta{}, "TIMESTAMP"},
		{"BOOL→NUMBER(1)", KindBool, types.ColumnMeta{}, "NUMBER(1)"},
		{"JSON→CLOB", KindJSON, types.ColumnMeta{}, "CLOB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ToOracle(c.kind, c.col)
			if got != c.want {
				t.Errorf("ToOracle(%s) = %q, want %q", c.kind, got, c.want)
			}
		})
	}
}
