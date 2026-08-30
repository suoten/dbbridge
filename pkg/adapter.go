package types

// DatabaseAdapter 数据库适配器接口
// 每种数据库（MySQL、PostgreSQL、SQLite 等）实现此接口
type DatabaseAdapter interface {
	// 连接管理
	Connect(config ConnectionConfig) error
	Close() error
	GetVersion() (string, error)

	// 元数据读取（直连模式）
	GetTables() ([]TableMeta, error)
	GetTableSchema(tableName string) (TableSchema, error)
	GetRowCount(tableName string) (int64, error)

	// DDL 生成
	GenerateCreateTableDDL(table TableSchema) (string, error)
	GenerateDropTableDDL(tableName string) (string, error)

	// 数据操作（直连模式）
	ReadData(tableName string, offset, limit int) ([]Row, error)
	WriteData(tableName string, columns []string, rows []Row) error

	// 事务支持
	BeginTx() error
	CommitTx() error
	RollbackTx() error

	// 类型映射支持
	// MapType 将源数据库的列类型映射为目标数据库的类型
	MapType(col ColumnMeta) string
}

// AdapterFactory 适配器工厂函数类型
type AdapterFactory func() DatabaseAdapter

// adapterRegistry 适配器注册表
var adapterRegistry = make(map[DatabaseType]AdapterFactory)

// RegisterAdapter 注册一个数据库适配器
func RegisterAdapter(dbType DatabaseType, factory AdapterFactory) {
	adapterRegistry[dbType] = factory
}

// NewAdapter 根据数据库类型创建适配器实例
func NewAdapter(dbType DatabaseType) DatabaseAdapter {
	if factory, ok := adapterRegistry[dbType]; ok {
		return factory()
	}
	return nil
}

// SupportedDatabases 返回已注册的数据库类型列表
func SupportedDatabases() []DatabaseType {
	types := make([]DatabaseType, 0, len(adapterRegistry))
	for t := range adapterRegistry {
		types = append(types, t)
	}
	return types
}
