package types

import "strings"

// DatabaseType 数据库类型枚举
type DatabaseType string

const (
	// 第一阶段：开源主流
	MySQL      DatabaseType = "mysql"
	PostgreSQL DatabaseType = "postgres"
	SQLite     DatabaseType = "sqlite"
	MariaDB    DatabaseType = "mariadb"
	OceanBase  DatabaseType = "oceanbase"

	// 第二阶段：云原生与国产化
	TiDB        DatabaseType = "tidb"
	PolarDB     DatabaseType = "polardb"
	OpenGauss   DatabaseType = "opengauss"
	Dameng      DatabaseType = "dameng"
	KingbaseES  DatabaseType = "kingbase"
	Aurora      DatabaseType = "aurora"
	CockroachDB DatabaseType = "cockroachdb"

	// 第三阶段：主流商业与 NoSQL
	Oracle      DatabaseType = "oracle"
	MSSQL       DatabaseType = "mssql"
	Db2         DatabaseType = "db2"
	MongoDB     DatabaseType = "mongodb"
	Redis       DatabaseType = "redis"
	Cassandra   DatabaseType = "cassandra"
	ScyllaDB    DatabaseType = "scylladb"
	InfluxDB    DatabaseType = "influxdb"
	TimescaleDB DatabaseType = "timescaledb"
	TDengine    DatabaseType = "tdengine"

	// 第四阶段：桌面/文件型数据库
	Access DatabaseType = "access"
)

// ConnectionConfig 数据库连接配置
type ConnectionConfig struct {
	Type     DatabaseType `json:"type"`
	Host     string       `json:"host"`
	Port     int          `json:"port"`
	Username string       `json:"username"`
	Password string       `json:"password"`
	Database string       `json:"database"`
	SSLMode  string       `json:"sslMode,omitempty"`  // PostgreSQL 专用
	Charset  string       `json:"charset,omitempty"`  // MySQL 专用
	Instance string       `json:"instance,omitempty"` // MSSQL 实例名（可选）
}

// TableMeta 表的元数据
type TableMeta struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
}

// ColumnMeta 列的元数据
type ColumnMeta struct {
	Name          string  `json:"name"`
	DataType      string  `json:"dataType"`            // 原始数据类型，如 VARCHAR(255)
	BaseType      string  `json:"baseType"`            // 基础类型，如 VARCHAR
	Length        *int    `json:"length,omitempty"`    // 字段长度
	Precision     *int    `json:"precision,omitempty"` // 精度（DECIMAL）
	Scale         *int    `json:"scale,omitempty"`     // 小数位数（DECIMAL）
	Nullable      bool    `json:"nullable"`
	DefaultValue  *string `json:"defaultValue,omitempty"`
	AutoIncrement bool    `json:"autoIncrement"`
	Unsigned      bool    `json:"unsigned,omitempty"` // MySQL 无符号
	Comment       string  `json:"comment,omitempty"`
	IsPrimaryKey  bool    `json:"isPrimaryKey"`
}

// IndexMeta 索引的元数据
type IndexMeta struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"isUnique"`
	IsPrimary bool     `json:"isPrimary"`      // 主键索引
	Type      string   `json:"type,omitempty"` // BTREE, HASH 等
}

// ForeignKeyMeta 外键的元数据
type ForeignKeyMeta struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnDelete   string   `json:"onDelete,omitempty"` // CASCADE, SET NULL, RESTRICT 等
	OnUpdate   string   `json:"onUpdate,omitempty"`
}

// TableSchema 表的完整结构定义
type TableSchema struct {
	Name        string           `json:"name"`
	Comment     string           `json:"comment,omitempty"`
	Columns     []ColumnMeta     `json:"columns"`
	Indexes     []IndexMeta      `json:"indexes,omitempty"`
	ForeignKeys []ForeignKeyMeta `json:"foreignKeys,omitempty"`
	Engine      string           `json:"engine,omitempty"`    // MySQL 专用
	Charset     string           `json:"charset,omitempty"`   // MySQL 专用
	Collation   string           `json:"collation,omitempty"` // MySQL 专用
	Checks      []CheckMeta      `json:"checks,omitempty"`    // CHECK 约束
	Triggers    []TriggerMeta    `json:"triggers,omitempty"`  // 触发器
}

// CheckMeta CHECK 约束元数据
type CheckMeta struct {
	Name       string `json:"name"`
	Definition string `json:"definition"` // 约束表达式，如 (age > 0)
}

// TriggerMeta 触发器元数据
type TriggerMeta struct {
	Name       string   `json:"name"`
	Event      string   `json:"event"`             // INSERT, UPDATE, DELETE
	Timing     string   `json:"timing"`            // BEFORE, AFTER, INSTEAD OF
	Table      string   `json:"table"`             // 关联的表
	Body       string   `json:"body"`              // 触发器主体代码（源方言）
	ForEachRow bool     `json:"forEachRow"`        // 是否行级触发
	Columns    []string `json:"columns,omitempty"` // UPDATE 触发器的列列表
}

// RoutineMeta 存储过程/函数元数据
type RoutineMeta struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"` // "procedure" 或 "function"
	Body       string         `json:"body"` // 主体代码（源方言）
	Parameters []RoutineParam `json:"parameters,omitempty"`
	Returns    string         `json:"returns,omitempty"` // 函数返回类型
	Language   string         `json:"language,omitempty"`
}

// RoutineParam 存储过程/函数参数
type RoutineParam struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	Mode     string `json:"mode"` // IN, OUT, INOUT
}

// Row 一行数据，使用 map 表示列名到值的映射
type Row map[string]any

// TablespaceAware 支持表空间/文件组配置的目标适配器。
// 表空间是全局配置（对所有表一致），在连接建立后设置一次即可，
// 与并发表迁移无竞争；Schema 映射因逐表不同，走无状态的限定名传递
// （orchestrator 把表名解析为 "schema.table" 传入适配器），不走接口状态。
type TablespaceAware interface {
	SetTablespace(tablespace string) error
}

// RunSuffixAware 支持注入本次迁移运行唯一后缀的适配器接口。
// 背景：MSSQL 约束/索引对象名在 schema 级唯一，先删后建/备份模式的
// 备份表（RENAME 而来）会携带旧约束名存活，重跑迁移建同名约束必然
// 冲突（真实库实测 Error 2714）。orchestrator 在备份/先删后建模式下
// 注入每次运行唯一的后缀，建表时 PK/CHECK/索引命名追加该后缀去重。
type RunSuffixAware interface {
	SetRunSuffix(suffix string)
}

// SplitQualified 将可能带 Schema 限定的表名拆分为 (schema, table)。
// "a.b" -> ("a", "b")；"b" -> ("", "b")。
// 约定：orchestrator 仅在配置了 Schema 映射时才生成限定名；
// 未配置映射时表名原样传递，不会引入点号。
func SplitQualified(name string) (schema, table string) {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// MigrationConfig 迁移配置
type MigrationConfig struct {
	Source          ConnectionConfig `json:"source"`
	Target          ConnectionConfig `json:"target"`
	SQLFilePath     string           `json:"sqlFilePath,omitempty"`   // SQL 文件模式
	SourceDialect   string           `json:"sourceDialect,omitempty"` // SQL 文件的源方言
	Tables          []string         `json:"tables,omitempty"`        // 指定迁移的表，空表示全部
	StructureOnly   bool             `json:"structureOnly"`           // 仅迁移结构
	DataOnly        bool             `json:"dataOnly"`                // 仅迁移数据
	BatchSize       int              `json:"batchSize"`               // 批量写入大小
	Concurrency     int              `json:"concurrency"`             // 并发表迁移数
	IgnoreErrors    bool             `json:"ignoreErrors"`            // 单表失败是否跳过
	DropIfExists    bool             `json:"dropIfExists"`            // 目标表存在时是否先删除
	BackupBefore    bool             `json:"backupBefore"`            // 迁移前备份目标库同名表
	AutoRollback    bool             `json:"autoRollback"`            // 迁移失败时自动回滚到备份
	MigrateTriggers bool             `json:"migrateTriggers"`         // 迁移触发器
	MigrateRoutines bool             `json:"migrateRoutines"`         // 迁移存储过程/函数

	// Schema 映射与表空间（不配置时行为与既往版本完全一致）
	SchemaDefault string            `json:"schemaDefault,omitempty"` // 全局默认目标 Schema（空=目标连接默认库）
	SchemaTables  map[string]string `json:"schemaTables,omitempty"`  // 按表覆盖：源表名 -> 目标 Schema
	SchemaRegex   []SchemaRule      `json:"schemaRegex,omitempty"`   // 正则规则（按顺序匹配，首个命中生效）
	Tablespace    string            `json:"tablespace,omitempty"`    // 目标表空间/文件组（支持的目标库才生效）
}

// SchemaRule 正则形式的 Schema 映射规则
// Target 支持 $1/$2 捕获组引用（regexp.ReplaceAllString 语义）
type SchemaRule struct {
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

// ProgressInfo 迁移进度信息
type ProgressInfo struct {
	Phase           string  `json:"phase"` // "parsing", "structure", "data", "verifying", "done"
	CurrentTable    string  `json:"currentTable"`
	ProcessedRows   int64   `json:"processedRows"`
	TotalRows       int64   `json:"totalRows"`
	TablesCompleted int     `json:"tablesCompleted"`
	TablesTotal     int     `json:"tablesTotal"`
	Percent         float64 `json:"percent"`
	Elapsed         string  `json:"elapsed"`
	Remaining       string  `json:"remaining,omitempty"`
}

// LogEntry 日志条目
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"` // "INFO", "WARN", "ERROR"
	Table   string `json:"table,omitempty"`
	Message string `json:"message"`
}

// MigrationReport 迁移报告
type MigrationReport struct {
	StartTime        string        `json:"startTime"`
	EndTime          string        `json:"endTime"`
	Duration         string        `json:"duration"`
	LogFile          string        `json:"logFile,omitempty"` // 本次迁移完整日志的本地文件路径
	TablesTotal      int           `json:"tablesTotal"`
	TablesSuccess    int           `json:"tablesSuccess"`
	TablesFailed     int           `json:"tablesFailed"`
	TablesCancelled  int           `json:"tablesCancelled,omitempty"` // 用户取消时未完成的表数
	TotalRows        int64         `json:"totalRows"`
	FailedTables     []string      `json:"failedTables,omitempty"`
	TableDetails     []TableReport `json:"tableDetails"`
	Backups          []BackupInfo  `json:"backups,omitempty"`          // 备份记录
	RollbackCount    int           `json:"rollbackCount,omitempty"`    // 回滚的表数
	Error            string        `json:"error,omitempty"`            // 迁移失败原因（含取消）
	TriggersMigrated int           `json:"triggersMigrated,omitempty"` // 迁移的触发器数
	RoutinesMigrated int           `json:"routinesMigrated,omitempty"` // 迁移的存储过程/函数数
	TriggerErrors    []string      `json:"triggerErrors,omitempty"`    // 触发器迁移失败列表
	RoutineErrors    []string      `json:"routineErrors,omitempty"`    // 存储过程迁移失败列表
	FKErrors         []string      `json:"fkErrors,omitempty"`         // 外键补建失败列表（不影响数据，需人工核对）
}

// TableReport 单表迁移报告
type TableReport struct {
	TableName   string `json:"tableName"`
	Rows        int64  `json:"rows"`
	Status      string `json:"status"` // "success", "failed", "skipped", "rolled_back"
	Error       string `json:"error,omitempty"`
	Duration    string `json:"duration"`
	BackupTable string `json:"backupTable,omitempty"` // 备份表名（如果有）
	RolledBack  bool   `json:"rolledBack,omitempty"`  // 是否已回滚
}

// BackupInfo 单表备份信息
type BackupInfo struct {
	OriginalTable string `json:"originalTable"`
	BackupTable   string `json:"backupTable"`
	CreatedAt     string `json:"createdAt"`
	Restored      bool   `json:"restored,omitempty"` // 是否已恢复
}
