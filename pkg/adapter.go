package types

import "context"

// DatabaseAdapter 数据库适配器接口
// 每种数据库（MySQL、PostgreSQL、SQLite 等）实现此接口。
//
// 设计约定：
//   - 所有涉及网络/IO 的方法都接收 context.Context，以支持取消与超时；
//   - 每次写入操作（WriteData）自身是原子的（内部事务），适配器不再暴露
//     跨调用的会话级事务（历史版本的裸 BEGIN/COMMIT 在连接池上不成立，已移除）；
//   - 适配器实例是无状态的（除连接本身），可安全并发使用。
type DatabaseAdapter interface {
	// 连接管理
	Connect(ctx context.Context, config ConnectionConfig) error
	Close() error
	GetVersion(ctx context.Context) (string, error)

	// 元数据读取（直连模式）
	GetTables(ctx context.Context) ([]TableMeta, error)
	GetTableSchema(ctx context.Context, tableName string) (TableSchema, error)
	GetRowCount(ctx context.Context, tableName string) (int64, error)
	TableExists(ctx context.Context, tableName string) (bool, error)

	// DDL 生成（纯函数，不触库）
	GenerateCreateTableDDL(table TableSchema) (string, error)
	GenerateDropTableDDL(tableName string) (string, error)

	// 备份与恢复
	// BackupTable 将 tableName 重命名为备份名，返回备份表名
	BackupTable(ctx context.Context, tableName string) (backupName string, err error)
	// RestoreFromBackup 将备份表恢复为原表名（会删除当前同名表）
	RestoreFromBackup(ctx context.Context, backupName, originalName string) error
	// DropBackup 删除备份表
	DropBackup(ctx context.Context, backupName string) error

	// 数据操作
	// ReadData 按偏移量分页读取（无主键表或复合主键表的兜底方案）
	ReadData(ctx context.Context, tableName string, offset, limit int) ([]Row, error)
	// ReadDataKeyset 基于单列主键的游标分页读取，lastKey 为上一批末行的主键值（nil 表示从头开始）
	ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]Row, error)
	// WriteData 批量写入（内部事务保证批次原子性）
	WriteData(ctx context.Context, tableName string, columns []string, rows []Row) error
	// ExecContext 执行原始 SQL（DDL 等）
	ExecContext(ctx context.Context, sql string) error

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
	dbs := make([]DatabaseType, 0, len(adapterRegistry))
	for t := range adapterRegistry {
		dbs = append(dbs, t)
	}
	return dbs
}
