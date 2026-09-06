package types

// DatabaseType 数据库类型枚举
type DatabaseType string

const (
	MySQL       DatabaseType = "mysql"
	PostgreSQL  DatabaseType = "postgres"
	SQLite      DatabaseType = "sqlite"
	MariaDB     DatabaseType = "mariadb"
	OceanBase   DatabaseType = "oceanbase"
	TiDB        DatabaseType = "tidb"
	OpenGauss   DatabaseType = "opengauss"
	Dameng      DatabaseType = "dameng"
	KingbaseES  DatabaseType = "kingbase"
	CockroachDB DatabaseType = "cockroachdb"
)

// ConnectionConfig 数据库连接配置
type ConnectionConfig struct {
	Type     DatabaseType `json:"type"`
	Host     string       `json:"host"`
	Port     int          `json:"port"`
	Username string       `json:"username"`
	Password string       `json:"password"`
	Database string       `json:"database"`
	SSLMode  string       `json:"sslMode,omitempty"` // PostgreSQL 专用
	Charset  string       `json:"charset,omitempty"` // MySQL 专用
}

// TableMeta 表的元数据
type TableMeta struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
}

// ColumnMeta 列的元数据
type ColumnMeta struct {
	Name          string  `json:"name"`
	DataType      string  `json:"dataType"`           // 原始数据类型，如 VARCHAR(255)
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
	IsPrimary bool     `json:"isPrimary"` // 主键索引
	Type      string   `json:"type,omitempty"` // BTREE, HASH 等
}

// ForeignKeyMeta 外键的元数据
type ForeignKeyMeta struct {
	Name           string   `json:"name"`
	Columns        []string `json:"columns"`
	RefTable       string   `json:"refTable"`
	RefColumns     []string `json:"refColumns"`
	OnDelete       string   `json:"onDelete,omitempty"` // CASCADE, SET NULL, RESTRICT 等
	OnUpdate       string   `json:"onUpdate,omitempty"`
}

// TableSchema 表的完整结构定义
type TableSchema struct {
	Name         string          `json:"name"`
	Comment      string          `json:"comment,omitempty"`
	Columns      []ColumnMeta    `json:"columns"`
	Indexes      []IndexMeta     `json:"indexes,omitempty"`
	ForeignKeys  []ForeignKeyMeta `json:"foreignKeys,omitempty"`
	Engine       string          `json:"engine,omitempty"`       // MySQL 专用
	Charset      string          `json:"charset,omitempty"`      // MySQL 专用
	Collation    string          `json:"collation,omitempty"`    // MySQL 专用
}

// Row 一行数据，使用 map 表示列名到值的映射
type Row map[string]any

// MigrationConfig 迁移配置
type MigrationConfig struct {
	Source          ConnectionConfig `json:"source"`
	Target          ConnectionConfig `json:"target"`
	SQLFilePath     string           `json:"sqlFilePath,omitempty"`    // SQL 文件模式
	SourceDialect   string           `json:"sourceDialect,omitempty"`  // SQL 文件的源方言
	Tables          []string         `json:"tables,omitempty"`          // 指定迁移的表，空表示全部
	StructureOnly   bool             `json:"structureOnly"`            // 仅迁移结构
	DataOnly        bool             `json:"dataOnly"`                 // 仅迁移数据
	BatchSize       int              `json:"batchSize"`               // 批量写入大小
	Concurrency     int              `json:"concurrency"`              // 并发表迁移数
	IgnoreErrors    bool             `json:"ignoreErrors"`            // 单表失败是否跳过
	DropIfExists    bool             `json:"dropIfExists"`             // 目标表存在时是否先删除
	BackupBefore    bool             `json:"backupBefore"`             // 迁移前备份目标库同名表
	AutoRollback    bool             `json:"autoRollback"`             // 迁移失败时自动回滚到备份
}

// ProgressInfo 迁移进度信息
type ProgressInfo struct {
	Phase           string `json:"phase"`            // "parsing", "structure", "data", "verifying", "done"
	CurrentTable    string `json:"currentTable"`
	ProcessedRows   int64  `json:"processedRows"`
	TotalRows       int64  `json:"totalRows"`
	TablesCompleted int    `json:"tablesCompleted"`
	TablesTotal     int    `json:"tablesTotal"`
	Percent         float64 `json:"percent"`
	Elapsed         string `json:"elapsed"`
	Remaining       string `json:"remaining,omitempty"`
}

// LogEntry 日志条目
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`   // "INFO", "WARN", "ERROR"
	Table   string `json:"table,omitempty"`
	Message string `json:"message"`
}

// MigrationReport 迁移报告
type MigrationReport struct {
	StartTime      string            `json:"startTime"`
	EndTime        string            `json:"endTime"`
	Duration       string            `json:"duration"`
	TablesTotal    int               `json:"tablesTotal"`
	TablesSuccess  int               `json:"tablesSuccess"`
	TablesFailed   int               `json:"tablesFailed"`
	TablesCancelled int              `json:"tablesCancelled,omitempty"` // 用户取消时未完成的表数
	TotalRows      int64             `json:"totalRows"`
	FailedTables   []string          `json:"failedTables,omitempty"`
	TableDetails   []TableReport     `json:"tableDetails"`
	Backups        []BackupInfo      `json:"backups,omitempty"`     // 备份记录
	RollbackCount  int               `json:"rollbackCount,omitempty"` // 回滚的表数
	Error          string            `json:"error,omitempty"`        // 迁移失败原因（含取消）
}

// TableReport 单表迁移报告
type TableReport struct {
	TableName   string `json:"tableName"`
	Rows        int64  `json:"rows"`
	Status      string `json:"status"` // "success", "failed", "skipped", "rolled_back"
	Error       string `json:"error,omitempty"`
	Duration   string `json:"duration"`
	BackupTable string `json:"backupTable,omitempty"`  // 备份表名（如果有）
	RolledBack bool   `json:"rolledBack,omitempty"`  // 是否已回滚
}

// BackupInfo 单表备份信息
type BackupInfo struct {
	OriginalTable string `json:"originalTable"`
	BackupTable   string `json:"backupTable"`
	CreatedAt     string `json:"createdAt"`
	Restored      bool   `json:"restored,omitempty"` // 是否已恢复
}
