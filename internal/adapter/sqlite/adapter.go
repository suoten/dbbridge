package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	"dbbridge/pkg"

	_ "github.com/mattn/go-sqlite3"
)

// 确保实现了接口
var _ types.DatabaseAdapter = (*Adapter)(nil)

// Adapter SQLite 适配器
type Adapter struct {
	db *sql.DB
}

func init() {
	types.RegisterAdapter(types.SQLite, func() types.DatabaseAdapter {
		return &Adapter{}
	})
}

// Connect 连接 SQLite 数据库
// SQLite 是文件型数据库，Host 字段存储文件路径
func (a *Adapter) Connect(config types.ConnectionConfig) error {
	dsn := config.Database // 对于 SQLite，Database 字段就是文件路径
	if dsn == "" || dsn == ":memory:" {
		dsn = ":memory:"
	}
	// 添加 pragma 优化
	dsn = fmt.Sprintf("%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", dsn)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("sqlite: 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite 写入并发受限
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

// GetVersion 获取 SQLite 版本
func (a *Adapter) GetVersion() (string, error) {
	var version string
	err := a.db.QueryRow("SELECT sqlite_version()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("sqlite: 获取版本失败: %w", err)
	}
	return "SQLite " + version, nil
}

// GetTables 获取所有表
func (a *Adapter) GetTables() ([]types.TableMeta, error) {
	rows, err := a.db.Query(`
		SELECT name, COALESCE(sql, '') as sql_text
		FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_%' 
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name, sqlText string
		if err := rows.Scan(&name, &sqlText); err != nil {
			return nil, fmt.Errorf("sqlite: 读取表信息失败: %w", err)
		}
		// SQLite 表注释存储在 sql 文本中，这里简化处理
		tables = append(tables, types.TableMeta{
			Name: name,
		})
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Adapter) GetTableSchema(tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}

	// 获取列信息
	rows, err := a.db.Query(fmt.Sprintf("PRAGMA table_info(%q)", tableName))
	if err != nil {
		return schema, fmt.Errorf("sqlite: 获取表结构失败: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	var pkColumns []string

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int

		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return schema, fmt.Errorf("sqlite: 读取列信息失败: %w", err)
		}

		col := types.ColumnMeta{
			Name:      name,
			DataType:  ctype,
			BaseType:  strings.ToUpper(strings.SplitN(ctype, "(", 2)[0]),
			Nullable:  notnull == 0 && pk == 0,
			Comment:   "",
			IsPrimaryKey: pk > 0,
		}

		if dfltValue.Valid {
			val := dfltValue.String
			col.DefaultValue = &val
		}

		if pk > 0 {
			col.IsPrimaryKey = true
			pkColumns = append(pkColumns, name)
		}

		// 解析长度，如 VARCHAR(255)
		if openIdx := strings.Index(ctype, "("); openIdx >= 0 {
			closePart := ctype[openIdx+1:]
			if closeIdx := strings.Index(closePart, ")"); closeIdx >= 0 {
				lengthStr := closePart[:closeIdx]
				var length int
				if _, err := fmt.Sscanf(lengthStr, "%d", &length); err == nil {
					col.Length = &length
				}
			}
		}

		columns = append(columns, col)
	}

	schema.Columns = columns

	// 如果有主键列，创建主键索引
	if len(pkColumns) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:      "PRIMARY",
			Columns:   pkColumns,
			IsUnique:  true,
			IsPrimary: true,
		})
	}

	// 获取索引信息
	indexRows, err := a.db.Query(fmt.Sprintf("PRAGMA index_list(%q)", tableName))
	if err == nil {
		defer indexRows.Close()
		for indexRows.Next() {
			var seq int
			var name string
			var unique, partial int
			if err := indexRows.Scan(&seq, &name, &unique, &partial); err != nil {
				continue
			}
			if name == "PRIMARY" || strings.HasPrefix(name, "sqlite_") {
				continue
			}

			// 获取索引列
			idxColRows, err := a.db.Query(fmt.Sprintf("PRAGMA index_info(%q)", name))
			if err != nil {
				continue
			}
			var idxCols []string
			for idxColRows.Next() {
				var seqno, cid int
				var colName string
				if err := idxColRows.Scan(&seqno, &cid, &colName); err != nil {
					continue
				}
				idxCols = append(idxCols, colName)
			}
			idxColRows.Close()

			schema.Indexes = append(schema.Indexes, types.IndexMeta{
				Name:     name,
				Columns:  idxCols,
				IsUnique: unique == 1,
			})
		}
	}

	// 获取建表 SQL（用于获取表注释等额外信息）
	var sqlText sql.NullString
	err = a.db.QueryRow(`
		SELECT sql FROM sqlite_master WHERE type='table' AND name=?
	`, tableName).Scan(&sqlText)
	if err == nil && sqlText.Valid {
		// 可以从 SQL 中解析注释，这里简化处理
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", tableName)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: 获取行数失败: %w", err)
	}
	return count, nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %q (\n", table.Name))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf("%q ", col.Name))

		// 类型映射
		sb.WriteString(a.MapType(col))

		if !col.Nullable {
			sb.WriteString(" NOT NULL")
		}

		if col.DefaultValue != nil {
			sb.WriteString(fmt.Sprintf(" DEFAULT %s", *col.DefaultValue))
		}

		if col.AutoIncrement {
			sb.WriteString(" AUTOINCREMENT")
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
	// 也检查列上的 isPrimaryKey 标记
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
			sb.WriteString(fmt.Sprintf("%q", c))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")

	// SQLite 不支持表级索引、外键在 CREATE TABLE 中（除列级外）
	// 唯一索引需要单独创建
	for _, idx := range table.Indexes {
		if idx.IsPrimary {
			continue
		}
		sb.WriteString(";\n")
		if idx.IsUnique {
			sb.WriteString("CREATE UNIQUE INDEX ")
		} else {
			sb.WriteString("CREATE INDEX ")
		}
		sb.WriteString(fmt.Sprintf("IF NOT EXISTS %q ON %q (", idx.Name, table.Name))
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%q", c))
		}
		sb.WriteString(")")
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS %q", tableName), nil
}

// ReadData 分页读取数据
func (a *Adapter) ReadData(tableName string, offset, limit int) ([]types.Row, error) {
	// 先获取列名
	schema, err := a.GetTableSchema(tableName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 获取表结构失败: %w", err)
	}

	var cols []string
	for _, c := range schema.Columns {
		cols = append(cols, c.Name)
	}

	query := fmt.Sprintf("SELECT * FROM %q LIMIT %d OFFSET %d", tableName, limit, offset)
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 查询数据失败: %w", err)
	}
	defer rows.Close()

	var result []types.Row
	for rows.Next() {
		// 使用动态列扫描
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("sqlite: 读取数据失败: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			row[col] = values[i]
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

	// 构建参数化 INSERT
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}

	query := fmt.Sprintf(
		"INSERT INTO %q (%s) VALUES (%s)",
		tableName,
		quoteIdentifiers(columns),
		strings.Join(placeholders, ", "),
	)

	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: 开启事务失败: %w", err)
	}

	stmt, err := tx.Prepare(query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("sqlite: 预处理失败: %w", err)
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
			return fmt.Errorf("sqlite: 写入数据失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: 提交事务失败: %w", err)
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

// MapType 将源数据库的列类型映射为 SQLite 类型
func (a *Adapter) MapType(col types.ColumnMeta) string {
	base := strings.ToUpper(col.BaseType)
	switch base {
	// 整数类型
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT", "YEAR":
		return "INTEGER"
	// 浮点类型
	case "FLOAT", "DOUBLE", "REAL", "DECIMAL", "NUMERIC":
		return "REAL"
	// 布尔
	case "BIT", "BOOLEAN", "BOOL":
		return "INTEGER"
	// 字符串类型
	case "CHAR", "VARCHAR", "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT", "NCHAR", "NVARCHAR", "CLOB":
		return "TEXT"
	// 二进制类型
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY":
		return "BLOB"
	// 日期时间
	case "DATE", "DATETIME", "TIMESTAMP", "TIME":
		return "TEXT" // SQLite 没有原生日期类型，用 TEXT 存储 ISO 格式
	// JSON
	case "JSON", "JSONB":
		return "TEXT"
	// 枚举和集合
	case "ENUM", "SET":
		return "TEXT"
	default:
		// 未知类型，尝试保留原始类型声明
		if col.DataType != "" {
			return col.DataType
		}
		return "TEXT"
	}
}

// quoteIdentifiers 给列名加引号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	return strings.Join(quoted, ", ")
}
