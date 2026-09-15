// Package typeconv 提供跨数据库的列类型映射。
//
// 迁移路径：源方言原生类型 --Normalize--> 中立类型(Kind) --ToXxx--> 目标方言原生类型。
// 所有映射均为纯函数、表驱动，可被单元测试全覆盖。
package typeconv

import (
	"fmt"
	"regexp"
	"strings"

	types "dbbridge/pkg"
)

// defaultNumericPattern 匹配纯数值字面量（整数/小数/负数），可直接嵌入 DEFAULT 子句
var defaultNumericPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// FormatDefault 将 information_schema 中读出的列默认值格式化为可直接嵌入 DDL 的字面量。
// 规则：数值字面量与常见 SQL 函数/关键字（CURRENT_TIMESTAMP、NULL 等）原样返回；
// 已带引号的值原样返回（PG/SQLite 元数据可能带 ::type 后缀）；
// 其余一律按字符串字面量加单引号并转义，避免生成 "DEFAULT pending" 这类非法 SQL。
func FormatDefault(val string) string {
	v := strings.TrimSpace(val)
	if v == "" {
		return ""
	}
	if defaultNumericPattern.MatchString(v) {
		return v
	}
	key := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(v, "()")))
	switch key {
	case "current_timestamp", "current_date", "current_time", "localtime", "localtimestamp",
		"now", "uuid", "gen_random_uuid", "sysdate", "curdate", "curtime", "rand", "random",
		"null", "true", "false":
		return v
	}
	if strings.HasPrefix(v, "'") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

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
// 覆盖 MySQL/MariaDB、PostgreSQL/openGauss/Kingbase、SQLite、MSSQL 的常见类型名。
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
	"CLOB": KindText, "STRING": KindText, "NTEXT": KindText,
	// 二进制
	"TINYBLOB": KindBlob, "BLOB": KindBlob, "MEDIUMBLOB": KindBlob, "LONGBLOB": KindBlob, "BYTEA": KindBlob,
	"BINARY": KindBinary, "VARBINARY": KindBinary, "RAW": KindBinary, "IMAGE": KindBlob,
	// 日期时间
	"DATE": KindDate,
	"TIME": KindTime, "TIME WITHOUT TIME ZONE": KindTime,
	"DATETIME": KindDateTime, "DATETIME2": KindDateTime, "SMALLDATETIME": KindDateTime,
	"TIMESTAMP": KindTimestamp, "TIMESTAMP WITHOUT TIME ZONE": KindTimestamp,
	"TIMESTAMPTZ": KindTimestamp, "TIMESTAMP WITH TIME ZONE": KindTimestamp,
	// 其他
	"JSON": KindJSON, "JSONB": KindJSON,
	"UUID": KindUUID,
	"ENUM": KindEnum, "SET": KindSet,

	// MSSQL 专有类型（缺失会导致 fallback 把原始类型名写进目标 DDL，直接语法错误）
	"UNIQUEIDENTIFIER": KindUUID,
	"MONEY":            KindDecimal,
	"SMALLMONEY":       KindDecimal,
	"DATETIMEOFFSET":   KindTimestamp,
	"SQL_VARIANT":      KindVarChar,
	"SYSNAME":          KindVarChar,
	"XML":              KindText,
	"ROWVERSION":       KindBinary,
	"GUID":             KindUUID,
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

// goTimePattern 匹配 Go time.Time.String() 形式的字符串：
// 如 "2026-08-30 16:35:31 +0000 UTC"、"2026-08-30 16:35:31.123 +0800 CST m=+0.001"。
// SQLite 驱动直写 time.Time 时会以此形式落入 TEXT 列，目标库无法解析。
var goTimePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) [+-]\d{4} [A-Z]+(?: m=[+-][\d.]+)?$`)

// datetimeOffsetPattern 匹配带时区偏移的 datetime 字符串（MSSQL datetimeoffset、
// 驱动输出的 "2026-09-15 10:00:00 +08:00" 等）。MySQL/SQLite 等目标库不接受
// 时区后缀，需剥离后写入（时区语义由连接时区决定，迁移工具不换算时间值）。
var datetimeOffsetPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+[+-]\d{2}:?\d{2}$`)

// sqlDatetimePattern 匹配标准 SQL datetime 字符串（如 2026-08-30 16:35:31）
var sqlDatetimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d+)?$`)

// LooksLikeDateTime 判断字符串是否为常见 datetime 形态
// （标准 SQL 格式、带时区偏移格式或 Go time.Time 字符串形式），用于从无类型信息的数据源推断时间列
func LooksLikeDateTime(v string) bool {
	return sqlDatetimePattern.MatchString(v) || goTimePattern.MatchString(v) || datetimeOffsetPattern.MatchString(v)
}

// NormalizeTimeValue 将 Go 时间字符串形式与带时区偏移形式规范化为标准 SQL datetime
// 格式（如 "2026-08-30 16:35:31 +0000 UTC" → "2026-08-30 16:35:31"、
// "2026-09-15 10:00:00 +08:00" → "2026-09-15 10:00:00"）。
// 第二个返回值标识是否发生了转换；非时间字符串原样返回。
func NormalizeTimeValue(v string) (string, bool) {
	if m := datetimeOffsetPattern.FindStringSubmatch(v); m != nil {
		return strings.ReplaceAll(m[1], "T", " "), true
	}
	m := goTimePattern.FindStringSubmatch(v)
	if m == nil {
		return v, false
	}
	return m[1], true
}

// temporalDefaultKeywords 时间类默认关键字：SQLite 将日期时间存为 TEXT，
// 反向迁移时无法从存储类型还原，只能根据默认值语义推断原类型，
// 避免 VARCHAR DEFAULT CURRENT_TIMESTAMP（Error 1067）。
var temporalDefaultKeywords = map[string]bool{
	"current_timestamp": true, "current_date": true, "current_time": true,
	"localtime": true, "localtimestamp": true,
	"now": true, "sysdate": true, "curdate": true, "curtime": true,
}

// needsDegrade 判断默认值是否会让 MySQL 拒绝 TEXT/BLOB/JSON 类型：
// 无默认值、空串或 DEFAULT NULL 均合法（MySQL 允许 TEXT DEFAULT NULL），无需降级。
func needsDegrade(col types.ColumnMeta) bool {
	if col.DefaultValue == nil {
		return false
	}
	v := strings.TrimSpace(*col.DefaultValue)
	return v != "" && !strings.EqualFold(v, "NULL")
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
		// MySQL 禁止 TEXT 列带非常量默认值（Error 1101/1067）；
		// 时间类关键字默认值说明原列是日期时间（SQLite 存为 TEXT），还原为 DATETIME；
		// 其余非 NULL 默认值降级为可带默认值的 VARCHAR；DEFAULT NULL 合法保留 TEXT
		if needsDegrade(col) {
			d := strings.TrimSpace(*col.DefaultValue)
			if temporalDefaultKeywords[strings.ToLower(strings.TrimSuffix(d, "()"))] {
				return "DATETIME"
			}
			return toSize("VARCHAR", col.Length, 255)
		}
		return "TEXT"
	case KindBlob:
		if needsDegrade(col) {
			return toSize("VARBINARY", col.Length, 255)
		}
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
		// 同上：JSON 列不能有非 NULL 默认值，有则降级为 VARCHAR
		if needsDegrade(col) {
			return toSize("VARCHAR", col.Length, 255)
		}
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

// ToMSSQL 中立类型 → MSSQL (SQL Server) 类型
func ToMSSQL(k Kind, col types.ColumnMeta) string {
	switch k {
	case KindTinyInt:
		return "TINYINT"
	case KindSmallInt:
		return "SMALLINT"
	case KindInt:
		return "INT"
	case KindBigInt:
		return "BIGINT"
	case KindYear:
		return "SMALLINT"
	case KindDecimal:
		if col.Precision != nil && col.Scale != nil {
			return fmt.Sprintf("DECIMAL(%d, %d)", *col.Precision, *col.Scale)
		}
		return "DECIMAL(18,0)"
	case KindFloat:
		return "REAL"
	case KindDouble:
		return "FLOAT(53)"
	case KindBool:
		return "BIT"
	case KindBit:
		if col.Length != nil && *col.Length > 1 {
			return fmt.Sprintf("BINARY(%d)", *col.Length)
		}
		return "BIT"
	case KindChar:
		return toSizeNVarChar("NCHAR", col.Length, 1)
	case KindVarChar:
		return toSizeNVarChar("NVARCHAR", col.Length, 255)
	case KindText:
		return "NVARCHAR(MAX)"
	case KindBlob, KindBinary:
		if col.Length != nil && *col.Length > 0 {
			return fmt.Sprintf("VARBINARY(%d)", *col.Length)
		}
		return "VARBINARY(MAX)"
	case KindDate:
		return "DATE"
	case KindTime:
		return "TIME"
	case KindDateTime:
		return "DATETIME2"
	case KindTimestamp:
		return "DATETIME2"
	case KindJSON:
		return "NVARCHAR(MAX)"
	case KindUUID:
		return "UNIQUEIDENTIFIER"
	case KindEnum:
		return "NVARCHAR(50)"
	case KindSet:
		return "NVARCHAR(200)"
	default:
		return fallback(col, "NVARCHAR(MAX)")
	}
}

// toSizeNVarChar 生成带参数的类型名，MSSQL NVARCHAR 默认上限 4000，超出用 MAX
func toSizeNVarChar(name string, n *int, def int) string {
	if n != nil && *n > 0 {
		if *n > 4000 {
			return name + "(MAX)"
		}
		return fmt.Sprintf("%s(%d)", name, *n)
	}
	return fmt.Sprintf("%s(%d)", name, def)
}

// fallback 未知类型时的兜底：尽量保留原始类型声明，否则退到 def。
func fallback(col types.ColumnMeta, def string) string {
	if col.DataType != "" {
		return col.DataType
	}
	return def
}
