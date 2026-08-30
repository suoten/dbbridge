package postgres

import (
	"database/sql"
	"fmt"
	"strings"

	"dbbridge/pkg"

	_ "github.com/lib/pq"
)

var _ types.DatabaseAdapter = (*Adapter)(nil)

// Adapter PostgreSQL 适配器
type Adapter struct {
	db *sql.DB
}

func init() {
	types.RegisterAdapter(types.PostgreSQL, func() types.DatabaseAdapter {
		return &Adapter{}
	})
}

// Connect 连接 PostgreSQL 数据库
func (a *Adapter) Connect(config types.ConnectionConfig) error {
	sslmode := config.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.Username, config.Password, config.Database, sslmode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("postgres: 打开数据库失败: %w", err)
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

// GetVersion 获取 PostgreSQL 版本
func (a *Adapter) GetVersion() (string, error) {
	var version string
	err := a.db.QueryRow("SELECT version()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("postgres: 获取版本失败: %w", err)
	}
	return version, nil
}

// GetTables 获取所有表
func (a *Adapter) GetTables() ([]types.TableMeta, error) {
	rows, err := a.db.Query(`
		SELECT table_name, 
			COALESCE(obj_description(quote_ident(table_name)::regclass, 'pg_class'), '')
		FROM information_schema.tables
		WHERE table_schema = 'public'
		AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`)
	if err != nil {
		return nil, fmt.Errorf("postgres: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name, comment string
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, fmt.Errorf("postgres: 读取表信息失败: %w", err)
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

	// 获取表注释
	var comment sql.NullString
	err := a.db.QueryRow(`
		SELECT obj_description(quote_ident($1)::regclass, 'pg_class')
	`, tableName).Scan(&comment)
	if err == nil && comment.Valid {
		schema.Comment = comment.String
	}

	// 获取列信息
	rows, err := a.db.Query(`
		SELECT column_name, data_type, character_maximum_length,
		       numeric_precision, numeric_scale, is_nullable,
		       column_default, ordinal_position
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		ORDER BY ordinal_position
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("postgres: 查询列信息失败: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	for rows.Next() {
		var name, dataType, nullable string
		var defVal sql.NullString
		var maxLen, numPrecision, numScale sql.NullInt64

		if err := rows.Scan(&name, &dataType, &maxLen, &numPrecision, &numScale, &nullable, &defVal); err != nil {
			return schema, fmt.Errorf("postgres: 读取列信息失败: %w", err)
		}

		col := types.ColumnMeta{
			Name:         name,
			DataType:     dataType,
			BaseType:     strings.ToUpper(dataType),
			Nullable:     nullable == "YES",
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

		// 判断自增（SERIAL 或 nextval）
		if defVal.Valid && strings.Contains(defVal.String, "nextval") {
			col.AutoIncrement = true
		}

		columns = append(columns, col)
	}
	schema.Columns = columns

	// 获取主键
	pkRows, err := a.db.Query(`
		SELECT kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		WHERE tc.table_schema = 'public'
		AND tc.table_name = $1
		AND tc.constraint_type = 'PRIMARY KEY'
		ORDER BY kcu.ordinal_position
	`, tableName)
	if err == nil {
		var pkCols []string
		for pkRows.Next() {
			var colName string
			pkRows.Scan(&colName)
			pkCols = append(pkCols, colName)
		}
		pkRows.Close()
		if len(pkCols) > 0 {
			schema.Indexes = append(schema.Indexes, types.IndexMeta{
				Name:      "PRIMARY",
				Columns:   pkCols,
				IsUnique:  true,
				IsPrimary: true,
			})
			// 标记列
			for i := range schema.Columns {
				for _, pk := range pkCols {
					if schema.Columns[i].Name == pk {
						schema.Columns[i].IsPrimaryKey = true
					}
				}
			}
		}
	}

	// 获取索引
	idxRows, err := a.db.Query(`
		SELECT i.relname as index_name,
		       array_agg(a.attname ORDER BY x.n),
		       ix.indisunique
		FROM pg_index ix
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		CROSS JOIN LATERAL (
			SELECT unnest(ix.indkey) AS attnum, generate_subscripts(ix.indkey, 1) AS n
		) x
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = x.attnum
		WHERE t.relname = $1 AND n.nspname = 'public'
		AND NOT ix.indisprimary
		GROUP BY i.relname, ix.indisunique
	`, tableName)
	if err == nil {
		defer idxRows.Close()
		for idxRows.Next() {
			var idxName string
			var colArray []string
			var isUnique bool
			if err := idxRows.Scan(&idxName, &colArray, &isUnique); err != nil {
				continue
			}
			schema.Indexes = append(schema.Indexes, types.IndexMeta{
				Name:     idxName,
				Columns:  colArray,
				IsUnique: isUnique,
			})
		}
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, escapeIdent(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres: 获取行数失败: %w", err)
	}
	return count, nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS \"%s\" (\n", table.Name))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf("\"%s\" ", col.Name))
		sb.WriteString(a.MapType(col))

		if !col.Nullable {
			sb.WriteString(" NOT NULL")
		}

		if col.DefaultValue != nil {
			sb.WriteString(fmt.Sprintf(" DEFAULT %s", *col.DefaultValue))
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
			sb.WriteString(fmt.Sprintf("\"%s\"", c))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")

	// 表注释
	if table.Comment != "" {
		sb.WriteString(fmt.Sprintf(";\nCOMMENT ON TABLE \"%s\" IS '%s'", table.Name, escapeSingleQuote(table.Comment)))
	}

	// 列注释
	for _, col := range table.Columns {
		if col.Comment != "" {
			sb.WriteString(fmt.Sprintf(";\nCOMMENT ON COLUMN \"%s\".\"%s\" IS '%s'",
				table.Name, col.Name, escapeSingleQuote(col.Comment)))
		}
	}

	// 非主键索引
	for _, idx := range table.Indexes {
		if idx.IsPrimary {
			continue
		}
		sb.WriteString(";\n")
		if idx.IsUnique {
			sb.WriteString(fmt.Sprintf("CREATE UNIQUE INDEX IF NOT EXISTS \"%s\" ON \"%s\" (", idx.Name, table.Name))
		} else {
			sb.WriteString(fmt.Sprintf("CREATE INDEX IF NOT EXISTS \"%s\" ON \"%s\" (", idx.Name, table.Name))
		}
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("\"%s\"", c))
		}
		sb.WriteString(")")
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS \"%s\"", escapeIdent(tableName)), nil
}

// ReadData 分页读取数据
func (a *Adapter) ReadData(tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(tableName)
	if err != nil {
		return nil, fmt.Errorf("postgres: 获取表结构失败: %w", err)
	}
	var cols []string
	for _, c := range schema.Columns {
		cols = append(cols, c.Name)
	}

	query := fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d OFFSET %d`, escapeIdent(tableName), limit, offset)
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("postgres: 查询数据失败: %w", err)
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
			return nil, fmt.Errorf("postgres: 读取数据失败: %w", err)
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

	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	query := fmt.Sprintf(
		`INSERT INTO "%s" (%s) VALUES (%s)`,
		escapeIdent(tableName),
		quoteIdentifiersPG(columns),
		strings.Join(placeholders, ", "),
	)

	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("postgres: 开启事务失败: %w", err)
	}

	stmt, err := tx.Prepare(query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("postgres: 预处理失败: %w", err)
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
			return fmt.Errorf("postgres: 写入数据失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: 提交事务失败: %w", err)
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

// MapType 将源数据库的列类型映射为 PostgreSQL 类型
func (a *Adapter) MapType(col types.ColumnMeta) string {
	base := strings.ToUpper(col.BaseType)
	switch base {
	// 整数
	case "TINYINT":
		return "SMALLINT"
	case "SMALLINT":
		return "SMALLINT"
	case "INT", "INTEGER", "MEDIUMINT":
		return "INTEGER"
	case "BIGINT":
		return "BIGINT"
	case "YEAR":
		return "SMALLINT"
	// 浮点
	case "FLOAT":
		return "REAL"
	case "DOUBLE":
		return "DOUBLE PRECISION"
	case "DECIMAL", "NUMERIC":
		if col.Precision != nil && col.Scale != nil {
			return fmt.Sprintf("NUMERIC(%d, %d)", *col.Precision, *col.Scale)
		}
		return "NUMERIC"
	// 布尔
	case "BIT":
		if col.Length != nil && *col.Length == 1 {
			return "BOOLEAN"
		}
		return fmt.Sprintf("BIT(%d)", *col.Length)
	case "BOOLEAN", "BOOL":
		return "BOOLEAN"
	// 字符串
	case "CHAR", "NCHAR":
		if col.Length != nil {
			return fmt.Sprintf("CHAR(%d)", *col.Length)
		}
		return "CHAR(1)"
	case "VARCHAR", "NVARCHAR":
		if col.Length != nil {
			return fmt.Sprintf("VARCHAR(%d)", *col.Length)
		}
		return "VARCHAR(255)"
	case "TINYTEXT", "TEXT", "MEDIUMTEXT", "LONGTEXT":
		return "TEXT"
	// 二进制
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB":
		return "BYTEA"
	case "BINARY", "VARBINARY":
		return "BYTEA"
	// 日期时间
	case "DATE":
		return "DATE"
	case "TIME":
		return "TIME"
	case "DATETIME":
		return "TIMESTAMP"
	case "TIMESTAMP":
		return "TIMESTAMP"
	// JSON
	case "JSON":
		return "JSONB"
	// 枚举
	case "ENUM":
		return "VARCHAR(50)"
	case "SET":
		return "VARCHAR(200)"
	default:
		return "TEXT"
	}
}

// escapeIdent 转义 PostgreSQL 标识符
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, "\"", "\"\"")
}

// escapeSingleQuote 转义单引号
func escapeSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// quoteIdentifiersPG 给列名加双引号
func quoteIdentifiersPG(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("\"%s\"", escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}
