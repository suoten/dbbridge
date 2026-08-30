// Package sqlite 注册 SQLite 适配器（纯 Go 驱动，无需 CGO）。
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	types "dbbridge/pkg"
	"dbbridge/internal/typeconv"

	_ "modernc.org/sqlite"
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
// SQLite 是文件型数据库，Database 字段存储文件路径
func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	dsn := config.Database
	if dsn == "" || dsn == ":memory:" {
		dsn = ":memory:"
	}
	// 添加 pragma 优化
	dsn = fmt.Sprintf("%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", dsn)

	db, err := sql.Open("sqlite", dsn)
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
func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("sqlite: 获取版本失败: %w", err)
	}
	return "SQLite " + version, nil
}

// GetTables 获取所有表
func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '\_%' ESCAPE '\'
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("sqlite: 读取表信息失败: %w", err)
		}
		tables = append(tables, types.TableMeta{Name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: 遍历表列表失败: %w", err)
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}

	// 获取列信息
	rows, err := a.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", tableName))
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
			Name:         name,
			DataType:     ctype,
			BaseType:     strings.ToUpper(strings.SplitN(ctype, "(", 2)[0]),
			Nullable:     notnull == 0 && pk == 0,
			IsPrimaryKey: pk > 0,
		}

		if dfltValue.Valid && dfltValue.String != "" {
			val := dfltValue.String
			col.DefaultValue = &val
		}

		if pk > 0 {
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
	indexRows, err := a.db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%q)", tableName))
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
			idxColRows, err := a.db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%q)", name))
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

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %q", tableName)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: 获取行数失败: %w", err)
	}
	return count, nil
}

// TableExists 检查表是否存在
func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name=?
	`, tableName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("sqlite: 检查表存在失败: %w", err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名
func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q RENAME TO %q", tableName, backupName))
	if err != nil {
		return "", fmt.Errorf("sqlite: 备份表失败 (%s→%s): %w", tableName, backupName, err)
	}
	return backupName, nil
}

// RestoreFromBackup 从备份表恢复
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %q", originalName))
		if err != nil {
			return fmt.Errorf("sqlite: 恢复备份时删除当前表失败: %w", err)
		}
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %q RENAME TO %q", backupName, originalName))
	if err != nil {
		return fmt.Errorf("sqlite: 恢复备份失败 (%s→%s): %w", backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %q", backupName))
	if err != nil {
		return fmt.Errorf("sqlite: 删除备份表失败: %w", err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL（类型经 typeconv 映射，支持异构迁移）
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

		if col.DefaultValue != nil && *col.DefaultValue != "" {
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

	// 索引需要单独创建
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

// ReadData 按偏移量分页读取数据
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 获取表结构失败: %w", err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM %q LIMIT %d OFFSET %d", tableName, limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 获取表结构失败: %w", err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM %q", tableName)
	args := []any{}
	if lastKey != nil {
		args = append(args, lastKey)
		query += fmt.Sprintf(" WHERE %q > ?", keyColumn)
	}
	query += fmt.Sprintf(" ORDER BY %q ASC LIMIT %d", keyColumn, limit)
	return a.scanRows(ctx, query, args, cols)
}

// WriteData 批量写入数据（内部事务保证批次原子性）
func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
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

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: 开启事务失败: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("sqlite: 预处理失败: %w", err)
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
			return fmt.Errorf("sqlite: 写入数据失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: 提交事务失败: %w", err)
	}

	return nil
}

// ExecContext 执行原始 SQL 语句
func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

// MapType 将源数据库的列类型映射为 SQLite 类型
func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToSQLite(typeconv.Normalize(col.BaseType), col)
}

// scanRows 执行查询并按列名映射为 Row
func (a *Adapter) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 查询数据失败: %w", err)
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
			return nil, fmt.Errorf("sqlite: 读取数据失败: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: 遍历数据失败: %w", err)
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

// quoteIdentifiers 给列名加引号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	return strings.Join(quoted, ", ")
}
