package typeconv

import (
	"testing"

	types "dbbridge/pkg"
)

func intPtr(i int) *int    { return &i }
func strPtr(s string) *string { return &s }

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		// MySQL 系
		{"TINYINT", KindTinyInt}, {"INT", KindInt}, {"MEDIUMINT", KindInt},
		{"BIGINT", KindBigInt}, {"DECIMAL", KindDecimal}, {"DATETIME", KindDateTime},
		{"TINYTEXT", KindText}, {"LONGBLOB", KindBlob}, {"VARBINARY", KindBinary},
		{"YEAR", KindYear}, {"ENUM", KindEnum}, {"SET", KindSet},
		// PostgreSQL 系
		{"INTEGER", KindInt}, {"INT2", KindSmallInt}, {"INT8", KindBigInt},
		{"NUMERIC", KindDecimal}, {"DOUBLE PRECISION", KindDouble}, {"REAL", KindFloat},
		{"CHARACTER VARYING", KindVarChar}, {"TIMESTAMP WITHOUT TIME ZONE", KindTimestamp},
		{"TIMESTAMPTZ", KindTimestamp}, {"JSONB", KindJSON}, {"BYTEA", KindBlob},
		{"BOOLEAN", KindBool}, {"UUID", KindUUID},
		// SQLite 系
		{"STRING", KindText}, {"LOGICAL", KindBool},
		// 未知
		{"GEOMETRY", KindUnknown}, {"", KindUnknown},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToMySQL(t *testing.T) {
	cases := []struct {
		name string
		col  types.ColumnMeta
		want string
	}{
		{"整型保留无符号", types.ColumnMeta{BaseType: "INT", Unsigned: true}, "INT UNSIGNED"},
		{"PG小整型", types.ColumnMeta{BaseType: "SMALLINT"}, "SMALLINT"},
		{"带精度小数", types.ColumnMeta{BaseType: "NUMERIC", Precision: intPtr(10), Scale: intPtr(2)}, "DECIMAL(10, 2)"},
		{"布尔转TINYINT", types.ColumnMeta{BaseType: "BOOLEAN"}, "TINYINT(1)"},
		{"BYTEA转BLOB", types.ColumnMeta{BaseType: "BYTEA"}, "BLOB"},
		{"JSONB转JSON", types.ColumnMeta{BaseType: "JSONB"}, "JSON"},
		{"UUID转VARCHAR36", types.ColumnMeta{BaseType: "UUID"}, "VARCHAR(36)"},
		{"带长度VARCHAR", types.ColumnMeta{BaseType: "CHARACTER VARYING", Length: intPtr(128)}, "VARCHAR(128)"},
		{"时间戳", types.ColumnMeta{BaseType: "TIMESTAMP WITHOUT TIME ZONE"}, "TIMESTAMP"},
		{"PG自增不映射AUTO_INCREMENT", types.ColumnMeta{BaseType: "INT", AutoIncrement: true}, "INT"},
		{"未知类型兜底保留原样", types.ColumnMeta{BaseType: "GEOMETRY", DataType: "GEOMETRY"}, "GEOMETRY"},
	}
	for _, c := range cases {
		if got := ToMySQL(Normalize(c.col.BaseType), c.col); got != c.want {
			t.Errorf("%s: ToMySQL = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToPostgres(t *testing.T) {
	cases := []struct {
		name string
		col  types.ColumnMeta
		want string
	}{
		{"MySQL自增转SERIAL", types.ColumnMeta{BaseType: "INT", AutoIncrement: true}, "SERIAL"},
		{"BIGINT自增转BIGSERIAL", types.ColumnMeta{BaseType: "BIGINT", AutoIncrement: true}, "BIGSERIAL"},
		{"普通整型", types.ColumnMeta{BaseType: "INT"}, "INTEGER"},
		{"无符号保留在数值域", types.ColumnMeta{BaseType: "BIGINT", Unsigned: true}, "BIGINT"},
		{"TINYINT转SMALLINT", types.ColumnMeta{BaseType: "TINYINT"}, "SMALLINT"},
		{"YEAR转SMALLINT", types.ColumnMeta{BaseType: "YEAR"}, "SMALLINT"},
		{"BOOLEAN保留", types.ColumnMeta{BaseType: "BOOLEAN"}, "BOOLEAN"},
		{"BIT1转BOOLEAN", types.ColumnMeta{BaseType: "BIT", Length: intPtr(1)}, "BOOLEAN"},
		{"BIT8转BIT", types.ColumnMeta{BaseType: "BIT", Length: intPtr(8)}, "BIT(8)"},
		{"DATETIME转TIMESTAMP", types.ColumnMeta{BaseType: "DATETIME"}, "TIMESTAMP"},
		{"BLOB转BYTEA", types.ColumnMeta{BaseType: "LONGBLOB"}, "BYTEA"},
		{"带精度小数", types.ColumnMeta{BaseType: "DECIMAL", Precision: intPtr(12), Scale: intPtr(4)}, "NUMERIC(12, 4)"},
		{"TEXT保留", types.ColumnMeta{BaseType: "MEDIUMTEXT"}, "TEXT"},
	}
	for _, c := range cases {
		if got := ToPostgres(Normalize(c.col.BaseType), c.col); got != c.want {
			t.Errorf("%s: ToPostgres = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToSQLite(t *testing.T) {
	cases := []struct {
		name string
		col  types.ColumnMeta
		want string
	}{
		{"整型", types.ColumnMeta{BaseType: "INT"}, "INTEGER"},
		{"布尔", types.ColumnMeta{BaseType: "BOOLEAN"}, "INTEGER"},
		{"小数", types.ColumnMeta{BaseType: "DECIMAL", Precision: intPtr(10), Scale: intPtr(2)}, "REAL"},
		{"字符串", types.ColumnMeta{BaseType: "VARCHAR", Length: intPtr(255)}, "TEXT"},
		{"时间", types.ColumnMeta{BaseType: "DATETIME"}, "TEXT"},
		{"二进制", types.ColumnMeta{BaseType: "BLOB"}, "BLOB"},
		{"未知兜底", types.ColumnMeta{BaseType: "GEOMETRY", DataType: "GEOMETRY"}, "GEOMETRY"},
	}
	for _, c := range cases {
		if got := ToSQLite(Normalize(c.col.BaseType), c.col); got != c.want {
			t.Errorf("%s: ToSQLite = %q, want %q", c.name, got, c.want)
		}
	}
}
