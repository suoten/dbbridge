// Package typeconv 提供跨数据库的列类型映射。
//
// 迁移路径：源方言原生类型 --Normalize--> 中立类型(Kind) --ToXxx--> 目标方言原生类型。
// 所有映射均为纯函数、表驱动，可被单元测试全覆盖。
package typeconv

import (
	"fmt"

	types "dbbridge/pkg"
)

// Kind 中立类型
type Kind string

const (
	KindUnknown   Kind = ""
	KindTinyInt   Kind = "TINYINT"
	KindSmallInt  Kind = "SMALLINT"
	KindInt       Kind = "INT"
	KindBigInt    Kind = "BIGINT"
	KindYear      Kind = "YEAR"
	KindDecimal   Kind = "DECIMAL"
	KindFloat     Kind = "FLOAT"  // 单精度
	KindDouble    Kind = "DOUBLE" // 双精度
	KindBool      Kind = "BOOLEAN"
	KindBit       Kind = "BIT"
	KindChar      Kind = "CHAR"
	KindVarChar   Kind = "VARCHAR"
	KindText      Kind = "TEXT"
	KindBlob      Kind = "BLOB"   // 大二进制对象
	KindBinary    Kind = "BINARY" // 定长/变长二进制
	KindDate      Kind = "DATE"
	KindTime      Kind = "TIME"
	KindDateTime  Kind = "DATETIME"
	KindTimestamp Kind = "TIMESTAMP"
	KindJSON      Kind = "JSON"
	KindUUID      Kind = "UUID"
	KindEnum      Kind = "ENUM"
	KindSet       Kind = "SET"
)

// kindAliases 各方言基础类型名 → 中立类型
// 覆盖 MySQL/MariaDB、PostgreSQL/openGauss/Kingbase、SQLite 的常见类型名。
var kindAliases = map[string]Kind{
	// 整数
	"TINYINT": KindTinyInt, "INT1": KindTinyInt,
	"SMALLINT": KindSmallInt, "INT2": KindSmallInt, "SMALLSERIAL": KindSmallInt,
	"MEDIUMINT": KindInt, "INT": KindInt, "INTEGER": KindInt, "INT4": KindInt, "SERIAL": KindInt,
	"BIGINT": KindBigInt, "INT8": KindBigInt, "BIGSERIAL": KindBigInt,
	"YEAR": KindYear,
	// 小数
	"DECIMAL": KindDecimal, "NUMERIC": KindDecimal,
	"REAL": KindFloat, "FLOAT4": KindFloat, "FLOAT": KindFloat,
	"DOUBLE": KindDouble, "DOUBLE PRECISION": KindDouble, "FLOAT8": KindDouble,
	// 布尔/位
	"BOOLEAN": KindBool, "BOOL": KindBool, "LOGICAL": KindBool,
	"BIT": KindBit,
	// 字符串
	"CHAR": KindChar, "NCHAR": KindChar, "CHARACTER": KindChar, "BPCHAR": KindChar,
	"VARCHAR": KindVarChar, "NVARCHAR": KindVarChar, "CHARACTER VARYING": KindVarChar, "VARCHAR2": KindVarChar,
	"TINYTEXT": KindText, "TEXT": KindText, "MEDIUMTEXT": KindText, "LONGTEXT": KindText,
	"CLOB": KindText, "STRING": KindText,
	// 二进制
	"TINYBLOB": KindBlob, "BLOB": KindBlob, "MEDIUMBLOB": KindBlob, "LONGBLOB": KindBlob, "BYTEA": KindBlob,
	"BINARY": KindBinary, "VARBINARY": KindBinary, "RAW": KindBinary,
	// 日期时间
	"DATE": KindDate,
	"TIME": KindTime, "TIME WITHOUT TIME ZONE": KindTime,
	"DATETIME": KindDateTime,
	"TIMESTAMP": KindTimestamp, "TIMESTAMP WITHOUT TIME ZONE": KindTimestamp,
	"TIMESTAMPTZ": KindTimestamp, "TIMESTAMP WITH TIME ZONE": KindTimestamp,
	// 其他
	"JSON": KindJSON, "JSONB": KindJSON,
	"UUID": KindUUID,
	"ENUM": KindEnum, "SET": KindSet,
}

// Normalize 将源方言的基础类型名归一化为中立类型。
func Normalize(baseType string) Kind {
	if k, ok := kindAliases[baseType]; ok {
		return k
	}
	return KindUnknown
}

// toSize 生成带参数的类型名，如 VARCHAR(255)。
func toSize(name string, n *int, def int) string {
	if n != nil && *n > 0 {
		return fmt.Sprintf("%s(%d)", name, *n)
	}
	return fmt.Sprintf("%s(%d)", name, def)
}

// ToMySQL 中立类型 → MySQL/MariaDB/TiDB/OceanBase/DM(兼容模式) 类型
func ToMySQL(k Kind, col types.ColumnMeta) string {
	switch k {
	case KindTinyInt:
		return withUnsigned("TINYINT", col)
	case KindSmallInt:
		return withUnsigned("SMALLINT", col)
	case KindInt:
		return withUnsigned("INT", col)
	case KindBigInt:
		return withUnsigned("BIGINT", col)
	case KindYear:
		return "YEAR"
	case KindDecimal:
		if col.Precision != nil && col.Scale != nil {
			return fmt.Sprintf("DECIMAL(%d, %d)", *col.Precision, *col.Scale)
		}
		return "DECIMAL"
	case KindFloat:
		return withUnsigned("FLOAT", col)
	case KindDouble:
		return withUnsigned("DOUBLE", col)
	case KindBool:
		return "TINYINT(1)"
	case KindBit:
		return toSize("BIT", col.Length, 1)
	case KindChar:
		return toSize("CHAR", col.Length, 1)
	case KindVarChar:
		return toSize("VARCHAR", col.Length, 255)
	case KindText:
		return "TEXT"
	case KindBlob:
		return "BLOB"
	case KindBinary:
		return toSize("VARBINARY", col.Length, 255)
	case KindDate:
		return "DATE"
	case KindTime:
		return "TIME"
	case KindDateTime:
		return "DATETIME"
	case KindTimestamp:
		return "TIMESTAMP"
	case KindJSON:
		return "JSON"
	case KindUUID:
		return "VARCHAR(36)"
	case KindEnum:
		return "VARCHAR(50)"
	case KindSet:
		return "VARCHAR(200)"
	default:
		return fallback(col, "TEXT")
	}
}

// ToPostgres 中立类型 → PostgreSQL/openGauss/Kingbase 类型
func ToPostgres(k Kind, col types.ColumnMeta) string {
	switch k {
	case KindTinyInt, KindSmallInt, KindYear:
		return "SMALLINT"
	case KindInt:
		if col.AutoIncrement {
			return "SERIAL"
		}
		return "INTEGER"
	case KindBigInt:
		if col.AutoIncrement {
			return "BIGSERIAL"
		}
		return "BIGINT"
	case KindDecimal:
		if col.Precision != nil && col.Scale != nil {
			return fmt.Sprintf("NUMERIC(%d, %d)", *col.Precision, *col.Scale)
		}
		return "NUMERIC"
	case KindFloat:
		return "REAL"
	case KindDouble:
		return "DOUBLE PRECISION"
	case KindBool:
		return "BOOLEAN"
	case KindBit:
		if col.Length == nil || *col.Length == 1 {
			return "BOOLEAN"
		}
		return fmt.Sprintf("BIT(%d)", *col.Length)
	case KindChar:
		return toSize("CHAR", col.Length, 1)
	case KindVarChar:
		return toSize("VARCHAR", col.Length, 255)
	case KindText:
		return "TEXT"
	case KindBlob, KindBinary:
		return "BYTEA"
	case KindDate:
		return "DATE"
	case KindTime:
		return "TIME"
	case KindDateTime, KindTimestamp:
		return "TIMESTAMP"
	case KindJSON:
		return "JSONB"
	case KindUUID:
		return "UUID"
	case KindEnum:
		return "VARCHAR(50)"
	case KindSet:
		return "VARCHAR(200)"
	default:
		return fallback(col, "TEXT")
	}
}

// ToSQLite 中立类型 → SQLite 存储类型
func ToSQLite(k Kind, col types.ColumnMeta) string {
	switch k {
	case KindTinyInt, KindSmallInt, KindInt, KindBigInt, KindYear, KindBool, KindBit:
		return "INTEGER"
	case KindDecimal, KindFloat, KindDouble:
		return "REAL"
	case KindChar, KindVarChar, KindText, KindEnum, KindSet,
		KindDate, KindTime, KindDateTime, KindTimestamp, KindJSON, KindUUID:
		return "TEXT"
	case KindBlob, KindBinary:
		return "BLOB"
	default:
		return fallback(col, "TEXT")
	}
}

func withUnsigned(base string, col types.ColumnMeta) string {
	if col.Unsigned {
		return base + " UNSIGNED"
	}
	return base
}

// fallback 未知类型时的兜底：尽量保留原始类型声明，否则退到 def。
func fallback(col types.ColumnMeta, def string) string {
	if col.DataType != "" {
		return col.DataType
	}
	return def
}
