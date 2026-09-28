// Package scylladb 注册 ScyllaDB 适配器（兼容 CQL，与 Cassandra 共用协议）。
// ScyllaDB 与 Cassandra 协议完全兼容，直接复用 CQL 驱动。
// 由于 Cassandra 适配器的字段未导出，ScyllaDB 使用独立实现（品牌名不同）。
package scylladb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	"github.com/gocql/gocql"
)

// Adapter ScyllaDB 适配器
type Adapter struct {
	session  *gocql.Session
	brand    string
	keyspace string
}

func init() {
	types.RegisterAdapter(types.ScyllaDB, func() types.DatabaseAdapter {
		return &Adapter{brand: "ScyllaDB"}
	})
}

func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 9042
	}
	cluster := gocql.NewCluster(config.Host)
	cluster.Port = port
	if config.Username != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: config.Username,
			Password: config.Password,
		}
	}
	// 先不指定 keyspace 连接，以便检查/创建目标 keyspace
	cluster.Timeout = 30 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("ScyllaDB: 连接失败: %w", err)
	}

	// 如果指定了 keyspace 但不存在，自动创建
	if config.Database != "" {
		var ksCount int
		if err := session.Query("SELECT COUNT(*) FROM system_schema.keyspaces WHERE keyspace_name = ?", config.Database).Consistency(gocql.One).Scan(&ksCount); err == nil && ksCount == 0 {
			// ScyllaDB 新版不支持 SimpleStrategy（tablet replication），用 NetworkTopologyStrategy
			createKS := fmt.Sprintf("CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'NetworkTopologyStrategy', 'datacenter1': 1}",
				config.Database)
			if err := session.Query(createKS).Exec(); err != nil {
				// 回退到 SimpleStrategy（Cassandra 兼容模式）
				createKS = fmt.Sprintf("CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}",
					config.Database)
				if err := session.Query(createKS).Exec(); err != nil {
					session.Close()
					return fmt.Errorf("ScyllaDB: 创建 keyspace %s 失败: %w", config.Database, err)
				}
			}
		}
		session.Close()
		cluster.Keyspace = config.Database
		session, err = cluster.CreateSession()
		if err != nil {
			return fmt.Errorf("ScyllaDB: 连接 keyspace %s 失败: %w", config.Database, err)
		}
	}

	a.session = session
	a.keyspace = config.Database
	return nil
}

func (a *Adapter) Close() error {
	if a.session != nil {
		a.session.Close()
	}
	return nil
}

func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var version string
	if err := a.session.Query("SELECT release_version FROM system.local").Consistency(gocql.One).Scan(&version); err != nil {
		return "", fmt.Errorf("ScyllaDB: 获取版本失败: %w", err)
	}
	return "ScyllaDB " + version, nil
}

func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	iter := a.session.Query(`
		SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?
	`, a.keyspace).Iter()
	defer iter.Close()

	var tables []types.TableMeta
	var name string
	for iter.Scan(&name) {
		tables = append(tables, types.TableMeta{Name: name})
	}
	return tables, nil
}

func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{Name: tableName}

	// ScyllaDB schema 变更需要时间传播到系统表，重试 3 次
	var columns []types.ColumnMeta
	var pkCols []string
	for attempt := 0; attempt < 3; attempt++ {
		actualName := a.resolveTableName(ctx, tableName)
		iter := a.session.Query(`
			SELECT column_name, kind, type, position
			FROM system_schema.columns
			WHERE keyspace_name = ? AND table_name = ?
			ORDER BY position
		`, a.keyspace, actualName).Iter()

		columns = nil
		pkCols = nil
		for {
			var colName, kind, dataType string
			var position int
			if !iter.Scan(&colName, &kind, &dataType, &position) {
				break
			}
			col := types.ColumnMeta{
				Name:     colName,
				DataType: dataType,
				BaseType: strings.ToUpper(dataType),
				Nullable: kind != "partition_key" && kind != "clustering",
			}
			if kind == "partition_key" || kind == "clustering" {
				col.IsPrimaryKey = true
				col.Nullable = false
				pkCols = append(pkCols, colName)
			}
			columns = append(columns, col)
		}
		iter.Close()
		if len(columns) > 0 {
			break
		}
		// 精确匹配失败，尝试大小写不敏感查询
		if attempt == 0 {
			lower := strings.ToLower(tableName)
			iter2 := a.session.Query(`
				SELECT table_name, column_name, kind, type, position
				FROM system_schema.columns
				WHERE keyspace_name = ?
			`, a.keyspace).Iter()
			for {
				var tbl, colName, kind, dataType string
				var position int
				if !iter2.Scan(&tbl, &colName, &kind, &dataType, &position) {
					break
				}
				if strings.ToLower(tbl) == lower {
					col := types.ColumnMeta{
						Name:     colName,
						DataType: dataType,
						BaseType: strings.ToUpper(dataType),
						Nullable: kind != "partition_key" && kind != "clustering",
					}
					if kind == "partition_key" || kind == "clustering" {
						col.IsPrimaryKey = true
						col.Nullable = false
						pkCols = append(pkCols, colName)
					}
					columns = append(columns, col)
				}
			}
			iter2.Close()
			if len(columns) > 0 {
				break
			}
		}
		if attempt < 2 {
			a.session.AwaitSchemaAgreement(ctx)
			time.Sleep(time.Second)
		}
	}
	schema.Columns = columns
	if len(pkCols) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name: "PRIMARY", Columns: pkCols, IsUnique: true, IsPrimary: true,
		})
	}
	if len(columns) == 0 {
		return schema, fmt.Errorf("ScyllaDB: 表 %s 在 keyspace %s 中未找到列信息（可能表名大小写不匹配）", tableName, a.keyspace)
	}
	return schema, nil
}

// resolveTableName 大小写不敏感地解析 ScyllaDB 中的实际表名。
func (a *Adapter) resolveTableName(ctx context.Context, tableName string) string {
	var count int
	if err := a.session.Query(`
		SELECT COUNT(*) FROM system_schema.tables
		WHERE keyspace_name = ? AND table_name = ?
	`, a.keyspace, tableName).Scan(&count); err == nil && count > 0 {
		return tableName
	}
	lower := strings.ToLower(tableName)
	iter := a.session.Query(`
		SELECT table_name FROM system_schema.tables
		WHERE keyspace_name = ?
	`, a.keyspace).Iter()
	defer iter.Close()
	var name string
	for iter.Scan(&name) {
		if strings.ToLower(name) == lower {
			return name
		}
	}
	return tableName
}

func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	actualName := a.resolveTableName(ctx, tableName)
	var count int64
	if err := a.session.Query(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, escapeIdent(actualName))).Scan(&count); err != nil {
		return 0, fmt.Errorf("ScyllaDB: 获取行数失败: %w", err)
	}
	return count, nil
}

func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	actual := a.resolveTableName(ctx, tableName)
	var count int
	if err := a.session.Query(`
		SELECT COUNT(*) FROM system_schema.tables WHERE keyspace_name = ? AND table_name = ?
	`, a.keyspace, actual).Scan(&count); err != nil {
		return false, fmt.Errorf("ScyllaDB: 检查表存在失败: %w", err)
	}
	return count > 0, nil
}

// BackupTable 创建备份表并复制数据
func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))

	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return "", fmt.Errorf("ScyllaDB: 备份时获取表结构失败: %w", err)
	}

	schema.Name = backupName
	ddl, err := a.GenerateCreateTableDDL(schema)
	if err != nil {
		return "", fmt.Errorf("ScyllaDB: 生成备份表DDL失败: %w", err)
	}
	if err := a.session.Query(ddl).Exec(); err != nil {
		return "", fmt.Errorf("ScyllaDB: 创建备份表失败: %w", err)
	}

	// 逐行复制数据
	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return backupName, nil
	}
	colList := quoteIdentifiers(cols)
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`,
		escapeIdent(backupName), colList, strings.Join(placeholders, ", "))

	iter := a.session.Query(fmt.Sprintf(`SELECT %s FROM "%s"`, colList, escapeIdent(tableName))).Iter()

	rowData := make(map[string]any)
	for iter.MapScan(rowData) {
		values := make([]any, len(cols))
		for i, c := range cols {
			values[i] = rowData[c]
		}
		if err := a.session.Query(insertSQL, values...).Exec(); err != nil {
			iter.Close()
			return "", fmt.Errorf("ScyllaDB: 备份写入数据失败: %w", err)
		}
		rowData = make(map[string]any)
	}
	if err := iter.Close(); err != nil {
		return "", fmt.Errorf("ScyllaDB: 备份读取数据失败: %w", err)
	}

	return backupName, nil
}

// RestoreFromBackup 从备份表恢复
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	schema, err := a.GetTableSchema(ctx, backupName)
	if err != nil {
		return fmt.Errorf("ScyllaDB: 恢复时获取备份表结构失败: %w", err)
	}

	a.session.Query(fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, escapeIdent(originalName))).Exec()

	schema.Name = originalName
	ddl, err := a.GenerateCreateTableDDL(schema)
	if err != nil {
		return fmt.Errorf("ScyllaDB: 生成恢复表DDL失败: %w", err)
	}
	if err := a.session.Query(ddl).Exec(); err != nil {
		return fmt.Errorf("ScyllaDB: 创建恢复表失败: %w", err)
	}

	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return nil
	}
	colList := quoteIdentifiers(cols)
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`,
		escapeIdent(originalName), colList, strings.Join(placeholders, ", "))

	iter := a.session.Query(fmt.Sprintf(`SELECT %s FROM "%s"`, colList, escapeIdent(backupName))).Iter()

	rowData := make(map[string]any)
	for iter.MapScan(rowData) {
		values := make([]any, len(cols))
		for i, c := range cols {
			values[i] = rowData[c]
		}
		if err := a.session.Query(insertSQL, values...).Exec(); err != nil {
			iter.Close()
			return fmt.Errorf("ScyllaDB: 恢复写入数据失败: %w", err)
		}
		rowData = make(map[string]any)
	}
	if err := iter.Close(); err != nil {
		return fmt.Errorf("ScyllaDB: 恢复读取数据失败: %w", err)
	}

	if err := a.session.Query(fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, escapeIdent(backupName))).Exec(); err != nil {
		return fmt.Errorf("ScyllaDB: 删除备份表失败: %w", err)
	}
	return nil
}

func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	if err := a.session.Query(fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, escapeIdent(backupName))).Exec(); err != nil {
		return fmt.Errorf("ScyllaDB: 删除备份表失败: %w", err)
	}
	return nil
}

func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (
`, escapeIdent(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf(`"%s" %s`, escapeIdent(col.Name), a.MapType(col)))
	}

	pkCols := []string{}
	for _, idx := range table.Indexes {
		if idx.IsPrimary {
			pkCols = idx.Columns
			break
		}
	}
	if len(pkCols) == 0 {
		for _, col := range table.Columns {
			if col.IsPrimaryKey {
				pkCols = append(pkCols, col.Name)
			}
		}
	}
	if len(pkCols) > 0 {
		sb.WriteString(",\n  PRIMARY KEY (")
		for i, c := range pkCols {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")
	return sb.String(), nil
}

func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, escapeIdent(tableName)), nil
}

// ReadData 读取数据（Cassandra/ScyllaDB 不支持 OFFSET，用 LIMIT + 内存跳过模拟）
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	actualName := a.resolveTableName(ctx, tableName)
	totalFetch := offset + limit
	if totalFetch <= 0 {
		totalFetch = limit
	}
	query := fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d`, escapeIdent(actualName), totalFetch)
	iter := a.session.Query(query).Iter()
	defer iter.Close()

	var result []types.Row
	rowData := make(map[string]any)
	skipped := 0
	for iter.MapScan(rowData) {
		if skipped < offset {
			skipped++
			rowData = make(map[string]any)
			continue
		}
		row := make(types.Row)
		for k, v := range rowData {
			row[k] = v
		}
		result = append(result, row)
		if len(result) >= limit {
			break
		}
		rowData = make(map[string]any)
	}
	return result, nil
}

func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	actualName := a.resolveTableName(ctx, tableName)
	var query string
	if lastKey != nil {
		query = fmt.Sprintf(`SELECT * FROM "%s" WHERE "%s" > ? LIMIT %d`, escapeIdent(actualName), escapeIdent(keyColumn), limit)
		iter := a.session.Query(query, lastKey).Iter()
		defer iter.Close()

		var result []types.Row
		rowData := make(map[string]any)
		for iter.MapScan(rowData) {
			row := make(types.Row)
			for k, v := range rowData {
				row[k] = v
			}
			result = append(result, row)
			rowData = make(map[string]any)
		}
		return result, nil
	}
	query = fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d`, escapeIdent(actualName), limit)
	iter := a.session.Query(query).Iter()
	defer iter.Close()

	var result []types.Row
	rowData := make(map[string]any)
	for iter.MapScan(rowData) {
		row := make(types.Row)
		for k, v := range rowData {
			row[k] = v
		}
		result = append(result, row)
		rowData = make(map[string]any)
	}
	return result, nil
}

func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	if len(rows) == 0 {
		return nil
	}
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	query := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`,
		escapeIdent(tableName), quoteIdentifiers(columns), strings.Join(placeholders, ", "))

	// 获取目标表 schema，用于值类型转换（gocql 要求值类型与列类型严格匹配）
	schema, _ := a.GetTableSchema(ctx, tableName)
	colTypes := make(map[string]string, len(schema.Columns))
	for _, c := range schema.Columns {
		colTypes[strings.ToLower(c.Name)] = strings.ToLower(c.DataType)
	}

	batch := a.session.NewBatch(gocql.UnloggedBatch)
	for _, row := range rows {
		values := make([]any, len(columns))
		for i, col := range columns {
			if val, ok := row[col]; ok {
				values[i] = convertScyllaValue(val, colTypes[strings.ToLower(col)])
			} else {
				values[i] = nil
			}
		}
		batch.Query(query, values...)
	}
	if err := a.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("ScyllaDB: 写入数据失败: %w", err)
	}
	return nil
}

// convertScyllaValue 将值转换为 ScyllaDB 列类型兼容的 Go 类型。
// gocql 驱动要求值类型与列类型严格匹配，string 无法写入 double/float/int 列。
func convertScyllaValue(val any, cassType string) any {
	if val == nil {
		return nil
	}
	var s string
	switch v := val.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return val
	}
	switch {
	case strings.Contains(cassType, "double") || strings.Contains(cassType, "float"):
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case strings.Contains(cassType, "int") || strings.Contains(cassType, "bigint") || strings.Contains(cassType, "smallint") || strings.Contains(cassType, "tinyint"):
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case strings.Contains(cassType, "boolean"):
		if s == "true" || s == "1" {
			return true
		} else if s == "false" || s == "0" {
			return false
		}
	}
	return val
}

func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	return a.session.Query(sqlText).Exec()
}

func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToCassandra(typeconv.Normalize(col.BaseType), col)
}

func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	return nil, nil
}

func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("ScyllaDB: 不支持触发器")
}

func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("ScyllaDB: 暂不支持将 UDF 自动转换为 %s 方言", targetDialect)
}

func schemaColumnNames(schema types.TableSchema) []string {
	var cols []string
	for _, c := range schema.Columns {
		cols = append(cols, c.Name)
	}
	return cols
}

func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf(`"%s"`, escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}

// escapeIdent 转义 CQL 标识符中的双引号
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}
