// Package mysqlcompat 提供基于 MySQL 线协议的共享适配器基座。
//
// MySQL、MariaDB、TiDB、OceanBase 以及达梦(MySQL 兼容模式)共用本实现，
// 各数据库只需覆写差异点（如版本查询、品牌名）。
package mysqlcompat

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	types "dbbridge/pkg"
	"dbbridge/internal/typeconv"

	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
)

// Base MySQL 兼容协议适配器基座。
// 实例除连接外无状态，可安全并发使用。
type Base struct {
	Brand string    // 品牌名，用于错误信息与版本展示
	db    *sql.DB
}

// New 创建基座实例
func New(brand string) *Base {
	return &Base{Brand: brand}
}

// DB 暴露底层连接，供子类覆写方法使用
func (a *Base) DB() *sql.DB { return a.db }

// Connect 连接数据库
func (a *Base) Connect(ctx context.Context, config types.ConnectionConfig) error {
	cfg := mysql.NewConfig()
	cfg.User = config.Username
	cfg.Passwd = config.Password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", config.Host, config.Port)
	cfg.DBName = config.Database
	cfg.ParseTime = true
	if config.Charset != "" {
		cfg.Params = map[string]string{"charset": config.Charset}
	} else {
		cfg.Params = map[string]string{"charset": "utf8mb4"}
	}

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("%s: 打开数据库失败: %w", a.brand(), err)
	}
	db.SetMaxOpenConns(10)
	a.db = db
	return nil
}

// Close 关闭连接
func (a *Base) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *Base) brand() string {
	if a.Brand == "" {
		return "MySQL"
	}
	return a.Brand
}

// GetVersion 获取数据库版本
func (a *Base) GetVersion(ctx context.Context) (string, error) {
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("%s: 获取版本失败: %w", a.brand(), err)
	}
	return a.brand() + " " + version, nil
}

// GetTables 获取所有表
func (a *Base) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT TABLE_NAME, TABLE_COMMENT
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: 查询表列表失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, fmt.Errorf("%s: 读取表信息失败: %w", a.brand(), err)
		}
		tables = append(tables, types.TableMeta{
			Name:    name,
			Comment: comment,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: 遍历表列表失败: %w", a.brand(), err)
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Base) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}

	// 获取表注释和引擎
	var tableComment, engine, charset, collation sql.NullString
	err := a.db.QueryRowContext(ctx, `
		SELECT TABLE_COMMENT, ENGINE, TABLE_COLLATION,
			(SELECT CHARACTER_SET_NAME FROM information_schema.COLLATION_CHARACTER_SET_APPLICABILITY WHERE COLLATION_NAME = TABLE_COLLATION LIMIT 1)
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
	`, tableName).Scan(&tableComment, &engine, &collation, &charset)
	if err != nil {
		return schema, fmt.Errorf("%s: 获取表信息失败: %w", a.brand(), err)
	}
	if tableComment.Valid {
		schema.Comment = tableComment.String
	}
	if engine.Valid {
		schema.Engine = engine.String
	}
	if charset.Valid {
		schema.Charset = charset.String
	}
	if collation.Valid {
		schema.Collation = collation.String
	}

	// 获取列信息
	rows, err := a.db.QueryContext(ctx, `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
		       COLUMN_KEY, EXTRA, COLUMN_COMMENT, DATA_TYPE,
		       CHARACTER_MAXIMUM_LENGTH, NUMERIC_PRECISION, NUMERIC_SCALE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("%s: 查询列信息失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	var pkColumns []string

	for rows.Next() {
		var name, colType, nullable, colKey, extra, comment, dataType string
		var defVal sql.NullString
		var maxLen, numPrecision, numScale sql.NullInt64

		if err := rows.Scan(&name, &colType, &nullable, &defVal, &colKey, &extra, &comment, &dataType, &maxLen, &numPrecision, &numScale); err != nil {
			return schema, fmt.Errorf("%s: 读取列信息失败: %w", a.brand(), err)
		}

		col := types.ColumnMeta{
			Name:          name,
			DataType:      colType,
			BaseType:      strings.ToUpper(dataType),
			Nullable:      nullable == "YES",
			Comment:       comment,
			IsPrimaryKey:  colKey == "PRI",
			AutoIncrement: strings.Contains(extra, "auto_increment"),
			Unsigned:      strings.Contains(strings.ToLower(colType), "unsigned"),
		}

		if defVal.Valid && defVal.String != "" {
			val := defVal.String
			col.DefaultValue = &val
		}

		if maxLen.Valid {
			l := int(maxLen.Int64)
			col.Length = &l
		}
		if numPrecision.Valid {
			p := int(numPrecision.Int64)
			col.Precision = &p
		}
		if numScale.Valid {
			s := int(numScale.Int64)
			col.Scale = &s
		}

		if col.IsPrimaryKey {
			pkColumns = append(pkColumns, name)
		}

		columns = append(columns, col)
	}

	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("%s: 遍历列信息失败: %w", a.brand(), err)
	}

	schema.Columns = columns

	// 获取索引信息
	indexRows, err := a.db.QueryContext(ctx, `
		SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE, SEQ_IN_INDEX
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("%s: 查询索引信息失败: %w", a.brand(), err)
	}
	defer indexRows.Close()

	indexMap := make(map[string]*types.IndexMeta)
	var indexOrder []string
	for indexRows.Next() {
		var idxName, colName string
		var nonUnique, seqInIndex int
		if err := indexRows.Scan(&idxName, &colName, &nonUnique, &seqInIndex); err != nil {
			return schema, fmt.Errorf("%s: 读取索引信息失败: %w", a.brand(), err)
		}
		isPrimary := idxName == "PRIMARY"
		if isPrimary {
			continue // 主键已在列信息中处理
		}
		if _, ok := indexMap[idxName]; !ok {
			indexMap[idxName] = &types.IndexMeta{
				Name:     idxName,
				IsUnique: nonUnique == 0,
			}
			indexOrder = append(indexOrder, idxName)
		}
		indexMap[idxName].Columns = append(indexMap[idxName].Columns, colName)
	}
	if err := indexRows.Err(); err != nil {
		return schema, fmt.Errorf("%s: 遍历索引信息失败: %w", a.brand(), err)
	}

	for _, name := range indexOrder {
		schema.Indexes = append(schema.Indexes, *indexMap[name])
	}

	if len(pkColumns) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:      "PRIMARY",
			Columns:   pkColumns,
			IsUnique:  true,
			IsPrimary: true,
		})
	}

	// 获取外键信息
	fkRows, err := a.db.QueryContext(ctx, `
		SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_NAME = ?
		AND REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("%s: 查询外键信息失败: %w", a.brand(), err)
	}
	defer fkRows.Close()
	fkMap := make(map[string]*types.ForeignKeyMeta)
	var fkOrder []string
	for fkRows.Next() {
		var constraintName, columnName, refTable, refColumn string
		if err := fkRows.Scan(&constraintName, &columnName, &refTable, &refColumn); err != nil {
			return schema, fmt.Errorf("%s: 读取外键信息失败: %w", a.brand(), err)
		}
		if _, ok := fkMap[constraintName]; !ok {
			fkMap[constraintName] = &types.ForeignKeyMeta{
				Name:     constraintName,
				RefTable: refTable,
			}
			fkOrder = append(fkOrder, constraintName)
		}
		fkMap[constraintName].Columns = append(fkMap[constraintName].Columns, columnName)
		fkMap[constraintName].RefColumns = append(fkMap[constraintName].RefColumns, refColumn)
	}
	if err := fkRows.Err(); err != nil {
		return schema, fmt.Errorf("%s: 遍历外键信息失败: %w", a.brand(), err)
	}
	for _, name := range fkOrder {
		schema.ForeignKeys = append(schema.ForeignKeys, *fkMap[name])
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Base) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`", escapeIdent(tableName))
	err := a.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("%s: 获取行数失败: %w", a.brand(), err)
	}
	return count, nil
}

// TableExists 检查表是否存在
func (a *Base) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
	`, tableName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("%s: 检查表存在失败: %w", a.brand(), err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名
func (a *Base) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("RENAME TABLE `%s` TO `%s`",
		escapeIdent(tableName), escapeIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("%s: 备份表失败 (%s→%s): %w", a.brand(), tableName, backupName, err)
	}
	return backupName, nil
}

// RestoreFromBackup 从备份表恢复（删除当前同名表，将备份表重命名回去）
func (a *Base) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(originalName)))
		if err != nil {
			return fmt.Errorf("%s: 恢复备份时删除当前表失败: %w", a.brand(), err)
		}
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("RENAME TABLE `%s` TO `%s`",
		escapeIdent(backupName), escapeIdent(originalName)))
	if err != nil {
		return fmt.Errorf("%s: 恢复备份失败 (%s→%s): %w", a.brand(), backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Base) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(backupName)))
	if err != nil {
		return fmt.Errorf("%s: 删除备份表失败: %w", a.brand(), err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL（类型经 typeconv 映射，支持异构迁移）
func (a *Base) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (\n", escapeIdent(table.Name)))

	// 收集参与索引（主键/唯一/普通）的列：MySQL 键列不允许 TEXT/BLOB/JSON 无长度（Error 1170）
	indexedCols := map[string]bool{}
	for _, idx := range table.Indexes {
		for _, c := range idx.Columns {
			indexedCols[c] = true
		}
	}
	for _, col := range table.Columns {
		if col.IsPrimaryKey {
			indexedCols[col.Name] = true
		}
	}

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf("`%s` ", escapeIdent(col.Name)))
		colType := a.MapType(col)
		// 键列上的 TEXT/BLOB/JSON 降级为 VARCHAR(191)（utf8mb4 下 764 字节，兼容所有 InnoDB 行格式）
		if indexedCols[col.Name] {
			switch colType {
			case "TEXT", "BLOB", "JSON", "LONGTEXT", "MEDIUMTEXT", "TINYTEXT":
				colType = "VARCHAR(191)"
			}
		}
		sb.WriteString(colType)

		if !col.Nullable {
			sb.WriteString(" NOT NULL")
		}

		if col.AutoIncrement {
			sb.WriteString(" AUTO_INCREMENT")
		}

		if col.DefaultValue != nil && *col.DefaultValue != "" && !col.AutoIncrement {
			if quoted := typeconv.FormatDefault(*col.DefaultValue); quoted != "" {
				sb.WriteString(" DEFAULT " + quoted)
			}
		}

		if col.Comment != "" {
			sb.WriteString(fmt.Sprintf(" COMMENT '%s'", escapeSingleQuote(col.Comment)))
		}
	}

	// 主键
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
			sb.WriteString(fmt.Sprintf("`%s`", escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	// 唯一索引和普通索引
	for _, idx := range table.Indexes {
		if idx.IsPrimary {
			continue
		}
		sb.WriteString(",\n")
		if idx.IsUnique {
			sb.WriteString(fmt.Sprintf("  UNIQUE INDEX `%s` (", escapeIdent(idx.Name)))
		} else {
			sb.WriteString(fmt.Sprintf("  INDEX `%s` (", escapeIdent(idx.Name)))
		}
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("`%s`", escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")

	// 表选项
	if table.Engine != "" {
		sb.WriteString(fmt.Sprintf(" ENGINE=%s", table.Engine))
	} else {
		sb.WriteString(" ENGINE=InnoDB")
	}
	sb.WriteString(" DEFAULT CHARSET=utf8mb4")

	if table.Comment != "" {
		sb.WriteString(fmt.Sprintf(" COMMENT='%s'", escapeSingleQuote(table.Comment)))
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Base) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(tableName)), nil
}

// ReadData 按偏移量分页读取数据
func (a *Base) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM `%s` LIMIT %d OFFSET %d", escapeIdent(tableName), limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取（深翻页 O(1)，替代 OFFSET）
func (a *Base) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM `%s`", escapeIdent(tableName))
	args := []any{}
	if lastKey != nil {
		query += fmt.Sprintf(" WHERE `%s` > ?", escapeIdent(keyColumn))
		args = append(args, lastKey)
	}
	query += fmt.Sprintf(" ORDER BY `%s` ASC LIMIT %d", escapeIdent(keyColumn), limit)
	return a.scanRows(ctx, query, args, cols)
}

// WriteData 批量写入数据（内部事务保证批次原子性）
func (a *Base) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	if len(rows) == 0 {
		return nil
	}

	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}

	query := fmt.Sprintf(
		"INSERT INTO `%s` (%s) VALUES (%s)",
		escapeIdent(tableName),
		quoteIdentifiers(columns),
		strings.Join(placeholders, ", "),
	)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: 开启事务失败: %w", a.brand(), err)
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("%s: 预处理失败: %w", a.brand(), err)
	}
	defer stmt.Close()

	for _, row := range rows {
		values := make([]any, len(columns))
		for i, col := range columns {
			if val, ok := row[col]; ok {
				values[i] = val
			} else {
				values[i] = nil
			}
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: 写入数据失败: %w", a.brand(), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: 提交事务失败: %w", a.brand(), err)
	}

	return nil
}

// ExecContext 执行原始 SQL 语句
func (a *Base) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

// MapType 将源数据库的列类型映射为 MySQL 类型
func (a *Base) MapType(col types.ColumnMeta) string {
	return typeconv.ToMySQL(typeconv.Normalize(col.BaseType), col)
}

// scanRows 执行查询并按列名映射为 Row（MySQL 驱动的 []byte 统一转为 string）
func (a *Base) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: 查询数据失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var result []types.Row
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("%s: 读取数据失败: %w", a.brand(), err)
		}
		row := make(types.Row)
		for i, col := range cols {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: 遍历数据失败: %w", a.brand(), err)
	}

	return result, nil
}

func schemaColumnNames(schema types.TableSchema) []string {
	var cols []string
	for _, c := range schema.Columns {
		cols = append(cols, c.Name)
	}
	return cols
}

// GetTriggers 获取 MySQL 触发器定义
func (a *Base) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT TRIGGER_NAME, EVENT_MANIPULATION, ACTION_TIMING, EVENT_OBJECT_TABLE,
		       ACTION_STATEMENT, ACTION_ORIENTATION
		FROM information_schema.TRIGGERS
		WHERE TRIGGER_SCHEMA = DATABASE()
		ORDER BY TRIGGER_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: 查询触发器失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var triggers []types.TriggerMeta
	for rows.Next() {
		var name, event, timing, table, body, orientation string
		if err := rows.Scan(&name, &event, &timing, &table, &body, &orientation); err != nil {
			return nil, fmt.Errorf("%s: 读取触发器失败: %w", a.brand(), err)
		}
		triggers = append(triggers, types.TriggerMeta{
			Name:       name,
			Event:      event,
			Timing:     timing,
			Table:      table,
			Body:       body,
			ForEachRow: orientation == "ROW",
		})
	}
	return triggers, nil
}

// GetRoutines 获取 MySQL 存储过程和函数定义
func (a *Base) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT ROUTINE_NAME, ROUTINE_TYPE, ROUTINE_DEFINITION, DTD_IDENTIFIER
		FROM information_schema.ROUTINES
		WHERE ROUTINE_SCHEMA = DATABASE()
		ORDER BY ROUTINE_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: 查询存储过程失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var routines []types.RoutineMeta
	for rows.Next() {
		var name, rType string
		var body, returns sql.NullString
		if err := rows.Scan(&name, &rType, &body, &returns); err != nil {
			return nil, fmt.Errorf("%s: 读取存储过程失败: %w", a.brand(), err)
		}
		routine := types.RoutineMeta{
			Name: name,
			Type: strings.ToLower(rType),
		}
		if body.Valid {
			routine.Body = body.String
		}
		if returns.Valid {
			routine.Returns = returns.String
		}
		routines = append(routines, routine)
	}
	return routines, nil
}

// GenerateTriggerDDL 将触发器转换为目标方言的 DDL
func (a *Base) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("CREATE TRIGGER `%s` %s %s ON `%s` FOR EACH ROW\n",
			escapeIdent(trigger.Name), trigger.Timing, trigger.Event, escapeIdent(trigger.Table)))
		sb.WriteString(trigger.Body)
		return sb.String(), nil
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("CREATE TRIGGER \"%s\" %s %s ON \"%s\" FOR EACH ROW\n",
			escapeIdent(trigger.Name), trigger.Timing, trigger.Event, escapeIdent(trigger.Table)))
		sb.WriteString("EXECUTE FUNCTION \"" + escapeIdent(trigger.Name) + "_fn\"()\n")
		sb.WriteString(fmt.Sprintf(";\nCREATE FUNCTION \"%s_fn\"() RETURNS TRIGGER AS $$\nBEGIN\n", escapeIdent(trigger.Name)))
		sb.WriteString(trigger.Body)
		sb.WriteString("\nRETURN NEW;\nEND;\n$$ LANGUAGE plpgsql;")
		return sb.String(), nil
	default:
		return "", fmt.Errorf("%s: 不支持的目标方言 %s", a.brand(), targetDialect)
	}
}

// GenerateRoutineDDL 将存储过程/函数转换为目标方言的 DDL
func (a *Base) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase:
		if routine.Type == "function" {
			return fmt.Sprintf("CREATE FUNCTION `%s`() RETURNS %s\nBEGIN\n%s\nEND",
				escapeIdent(routine.Name), routine.Returns, routine.Body), nil
		}
		return fmt.Sprintf("CREATE PROCEDURE `%s`()\nBEGIN\n%s\nEND",
			escapeIdent(routine.Name), routine.Body), nil
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		if routine.Type == "function" {
			return fmt.Sprintf("CREATE FUNCTION \"%s\"() RETURNS %s AS $$\nBEGIN\n%s\nEND;\n$$ LANGUAGE plpgsql;",
				escapeIdent(routine.Name), routine.Returns, routine.Body), nil
		}
		return fmt.Sprintf("CREATE PROCEDURE \"%s\"() AS $$\nBEGIN\n%s\nEND;\n$$ LANGUAGE plpgsql;",
			escapeIdent(routine.Name), routine.Body), nil
	default:
		return "", fmt.Errorf("%s: 不支持的目标方言 %s", a.brand(), targetDialect)
	}
}

// escapeIdent 转义 MySQL 标识符，防止 SQL 注入
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, "`", "``")
}

// escapeSingleQuote 转义单引号
func escapeSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// quoteIdentifiers 给列名加反引号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("`%s`", escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}
