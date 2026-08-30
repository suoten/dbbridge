package mysql

import (
	"database/sql"
	"fmt"
	"strings"

	"dbbridge/pkg"

	_ "github.com/go-sql-driver/mysql"
)

var _ types.DatabaseAdapter = (*Adapter)(nil)

// Adapter MySQL 适配器
type Adapter struct {
	db *sql.DB
}

func init() {
	types.RegisterAdapter(types.MySQL, func() types.DatabaseAdapter {
		return &Adapter{}
	})
}

// Connect 连接 MySQL 数据库
func (a *Adapter) Connect(config types.ConnectionConfig) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
	)
	if config.Charset != "" {
		dsn += "&charset=" + config.Charset
	} else {
		dsn += "&charset=utf8mb4"
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql: 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(10)
	a.db = db
	return nil
}

// Close 关闭连接
func (a *Adapter) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// GetVersion 获取 MySQL 版本
func (a *Adapter) GetVersion() (string, error) {
	var version string
	err := a.db.QueryRow("SELECT VERSION()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("mysql: 获取版本失败: %w", err)
	}
	return "MySQL " + version, nil
}

// GetTables 获取所有表
func (a *Adapter) GetTables() ([]types.TableMeta, error) {
	rows, err := a.db.Query(`
		SELECT TABLE_NAME, TABLE_COMMENT
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("mysql: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, fmt.Errorf("mysql: 读取表信息失败: %w", err)
		}
		tables = append(tables, types.TableMeta{
			Name:    name,
			Comment: comment,
		})
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Adapter) GetTableSchema(tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}

	// 获取表注释和引擎
	var tableComment, engine, charset, collation sql.NullString
	err := a.db.QueryRow(`
		SELECT TABLE_COMMENT, ENGINE, TABLE_COLLATION,
			(SELECT CHARACTER_SET_NAME FROM information_schema.COLLATION_CHARACTER_SET_APPLICABILITY WHERE COLLATION_NAME = TABLE_COLLATION LIMIT 1)
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
	`, tableName).Scan(&tableComment, &engine, &collation, &charset)
	if err != nil {
		return schema, fmt.Errorf("mysql: 获取表信息失败: %w", err)
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
	rows, err := a.db.Query(`
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
		       COLUMN_KEY, EXTRA, COLUMN_COMMENT, DATA_TYPE,
		       CHARACTER_MAXIMUM_LENGTH, NUMERIC_PRECISION, NUMERIC_SCALE
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("mysql: 查询列信息失败: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	var pkColumns []string

	for rows.Next() {
		var name, colType, nullable, colKey, extra, comment, dataType string
		var defVal sql.NullString
		var maxLen, numPrecision, numScale sql.NullInt64

		if err := rows.Scan(&name, &colType, &nullable, &defVal, &colKey, &extra, &comment, &dataType, &maxLen, &numPrecision, &numScale); err != nil {
			return schema, fmt.Errorf("mysql: 读取列信息失败: %w", err)
		}

		col := types.ColumnMeta{
			Name:          name,
			DataType:      colType,
			BaseType:      strings.ToUpper(dataType),
			Nullable:      nullable == "YES",
			Comment:       comment,
			IsPrimaryKey:  colKey == "PRI",
			AutoIncrement: strings.Contains(extra, "auto_increment"),
		}

		if defVal.Valid {
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

	schema.Columns = columns

	// 获取索引信息
	indexRows, err := a.db.Query(`
		SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE, SEQ_IN_INDEX
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("mysql: 查询索引信息失败: %w", err)
	}
	defer indexRows.Close()

	indexMap := make(map[string]*types.IndexMeta)
	for indexRows.Next() {
		var idxName, colName string
		var nonUnique, seqInIndex int
		if err := indexRows.Scan(&idxName, &colName, &nonUnique, &seqInIndex); err != nil {
			continue
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
		}
		indexMap[idxName].Columns = append(indexMap[idxName].Columns, colName)
	}

	for _, idx := range indexMap {
		schema.Indexes = append(schema.Indexes, *idx)
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
	fkRows, err := a.db.Query(`
		SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_NAME = ?
		AND REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION
	`, tableName)
	if err == nil {
		defer fkRows.Close()
		fkMap := make(map[string]*types.ForeignKeyMeta)
		for fkRows.Next() {
			var constraintName, columnName, refTable, refColumn string
			if err := fkRows.Scan(&constraintName, &columnName, &refTable, &refColumn); err != nil {
				continue
			}
			if _, ok := fkMap[constraintName]; !ok {
				fkMap[constraintName] = &types.ForeignKeyMeta{
					Name:     constraintName,
					RefTable: refTable,
				}
			}
			fkMap[constraintName].Columns = append(fkMap[constraintName].Columns, columnName)
			fkMap[constraintName].RefColumns = append(fkMap[constraintName].RefColumns, refColumn)
		}
		for _, fk := range fkMap {
			schema.ForeignKeys = append(schema.ForeignKeys, *fk)
		}
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(tableName string) (int64, error) {
	var count int64
	// 使用反引号包裹表名
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`", escapeIdent(tableName))
	err := a.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("mysql: 获取行数失败: %w", err)
	}
	return count, nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (\n", table.Name))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf("`%s` ", col.Name))
		sb.WriteString(col.DataType)

		if !col.Nullable {
			sb.WriteString(" NOT NULL")
		}

		if col.AutoIncrement {
			sb.WriteString(" AUTO_INCREMENT")
		}

		if col.DefaultValue != nil {
			sb.WriteString(fmt.Sprintf(" DEFAULT %s", *col.DefaultValue))
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
	if len(pkCols) > 0 {
		sb.WriteString(",\n  PRIMARY KEY (")
		for i, c := range pkCols {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("`%s`", c))
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
			sb.WriteString(fmt.Sprintf("  UNIQUE INDEX `%s` (", idx.Name))
		} else {
			sb.WriteString(fmt.Sprintf("  INDEX `%s` (", idx.Name))
		}
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("`%s`", c))
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
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(tableName)), nil
}

// ReadData 分页读取数据
func (a *Adapter) ReadData(tableName string, offset, limit int) ([]types.Row, error) {
	// 先获取列名
	schema, err := a.GetTableSchema(tableName)
	if err != nil {
		return nil, fmt.Errorf("mysql: 获取表结构失败: %w", err)
	}

	var cols []string
	for _, c := range schema.Columns {
		cols = append(cols, c.Name)
	}

	query := fmt.Sprintf("SELECT * FROM `%s` LIMIT %d OFFSET %d", escapeIdent(tableName), limit, offset)
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("mysql: 查询数据失败: %w", err)
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
			return nil, fmt.Errorf("mysql: 读取数据失败: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			// 处理 []byte 类型（MySQL 驱动返回 BLOB/TEXT 为 []byte）
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		result = append(result, row)
	}

	return result, nil
}

// WriteData 批量写入数据
func (a *Adapter) WriteData(tableName string, columns []string, rows []types.Row) error {
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
		quoteIdentifiersMySQL(columns),
		strings.Join(placeholders, ", "),
	)

	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("mysql: 开启事务失败: %w", err)
	}

	stmt, err := tx.Prepare(query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("mysql: 预处理失败: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		values := make([]any, len(columns))
		for i, col := range columns {
			val, ok := row[col]
			if !ok {
				values[i] = nil
			} else {
				values[i] = val
			}
		}
		if _, err := stmt.Exec(values...); err != nil {
			tx.Rollback()
			return fmt.Errorf("mysql: 写入数据失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mysql: 提交事务失败: %w", err)
	}

	return nil
}

// Exec 执行原始 SQL 语句
func (a *Adapter) Exec(sql string) error {
	_, err := a.db.Exec(sql)
	return err
}

// BeginTx 开启事务
func (a *Adapter) BeginTx() error {
	_, err := a.db.Exec("BEGIN")
	return err
}

// CommitTx 提交事务
func (a *Adapter) CommitTx() error {
	_, err := a.db.Exec("COMMIT")
	return err
}

// RollbackTx 回滚事务
func (a *Adapter) RollbackTx() error {
	_, err := a.db.Exec("ROLLBACK")
	return err
}

// MapType MySQL 到 MySQL 的类型映射（自身，保持原样）
func (a *Adapter) MapType(col types.ColumnMeta) string {
	return col.DataType
}

// escapeIdent 转义 MySQL 标识符，防止 SQL 注入
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, "`", "``")
}

// escapeSingleQuote 转义单引号
func escapeSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// quoteIdentifiersMySQL 给列名加反引号
func quoteIdentifiersMySQL(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("`%s`", escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}
