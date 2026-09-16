// Package tdengine 注册 TDengine 适配器（国产开源物联网时序数据库）。
//
// TDengine 是面向物联网大数据平台设计的时序数据库，使用 SQL-like 语法。
// 本适配器将 TDengine 的超级表(STable)/子表映射为"表"，标签和列映射为"列"。
// 使用 database/sql 标准接口，驱动名 "taosJson" 在运行时加载（REST API，不依赖 CGO）。
package tdengine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"
)

// Adapter TDengine 适配器
type Adapter struct {
	db    *sql.DB
	brand string
}

func init() {
	types.RegisterAdapter(types.TDengine, func() types.DatabaseAdapter {
		return &Adapter{brand: "TDengine"}
	})
}

func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 6030
	}
	// TDengine 使用 REST API 接口（不依赖 CGO）
	// DSN 格式: taosJson://user:pass@host:port/db
	dsn := fmt.Sprintf("taosJson://%s:%s@%s:%d/%s",
		config.Username, config.Password, config.Host, port, config.Database)
	db, err := sql.Open("taosJson", dsn)
	if err != nil {
		return fmt.Errorf("TDengine: open database failed: %w", err)
	}
	db.SetMaxOpenConns(10)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("TDengine: connect failed: %w", err)
	}
	a.db = db
	return nil
}

func (a *Adapter) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("TDengine: get version failed: %w", err)
	}
	return "TDengine " + version, nil
}

func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT TABLE_NAME, TABLE_TYPE
		FROM INFORMATION_SCHEMA.INS_TABLES
		WHERE DB_NAME = DATABASE()
		ORDER BY TABLE_NAME
	`)
	if err != nil {
		// fallback to SHOW TABLES
		rows, err = a.db.QueryContext(ctx, "SHOW TABLES")
		if err != nil {
			return nil, fmt.Errorf("TDengine: query tables failed: %w", err)
		}
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name string
		var tableType sql.NullString
		if err := rows.Scan(&name, &tableType); err != nil {
			tables = append(tables, types.TableMeta{Name: name})
		} else {
			tables = append(tables, types.TableMeta{Name: name, Comment: tableType.String})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("TDengine: iterate tables failed: %w", err)
	}
	return tables, nil
}

func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{Name: tableName}

	// TDengine DESCRIBE 返回: field | type | length | note
	// 不同版本可能返回不同列数，使用动态列扫描提高兼容性
	rows, err := a.db.QueryContext(ctx, fmt.Sprintf("DESCRIBE `%s`", escapeIdent(tableName)))
	if err != nil {
		return schema, fmt.Errorf("TDengine: describe table failed: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return schema, fmt.Errorf("TDengine: get column names failed: %w", err)
	}

	var columns []types.ColumnMeta
	var pkCols []string
	for rows.Next() {
		values := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return schema, fmt.Errorf("TDengine: scan column failed: %w", err)
		}

		colName, _ := values[0].(string)
		dataType, _ := values[1].(string)
		if colName == "" || dataType == "" {
			continue
		}

		col := types.ColumnMeta{
			Name:     colName,
			DataType: dataType,
			BaseType: strings.ToUpper(dataType),
			Nullable: true,
		}

		// 第三列可能是 length，第四列可能是 note
		if len(values) > 3 {
			if note, ok := values[3].(string); ok && note == "TAG" {
				col.Comment = "TAG"
			}
		}

		if strings.Contains(strings.ToLower(dataType), "timestamp") {
			col.IsPrimaryKey = true
			col.Nullable = false
			pkCols = append(pkCols, colName)
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("TDengine: iterate columns failed: %w", err)
	}
	schema.Columns = columns
	if len(pkCols) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name: "PRIMARY", Columns: pkCols, IsUnique: true, IsPrimary: true,
		})
	}
	return schema, nil
}

func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM `%s`", escapeIdent(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("TDengine: get row count failed: %w", err)
	}
	return count, nil
}

func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM INFORMATION_SCHEMA.INS_TABLES
		WHERE DB_NAME = DATABASE() AND TABLE_NAME = ?
	`, tableName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("TDengine: check table exists failed: %w", err)
	}
	return count > 0, nil
}

func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	a.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE `%s` AS SELECT * FROM `%s`", escapeIdent(backupName), escapeIdent(tableName)))
	return backupName, nil
}

func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(originalName)))
	a.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE `%s` AS SELECT * FROM `%s`", escapeIdent(originalName), escapeIdent(backupName)))
	return nil
}

func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(backupName)))
	return err
}

func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (\n", escapeIdent(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf("`%s` %s", escapeIdent(col.Name), a.MapType(col)))
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
			sb.WriteString(fmt.Sprintf("`%s`", escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")
	return sb.String(), nil
}

func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS `%s`", escapeIdent(tableName)), nil
}

func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("TDengine: get schema failed: %w", err)
	}
	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM `%s` LIMIT %d OFFSET %d", escapeIdent(tableName), limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("TDengine: get schema failed: %w", err)
	}
	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM `%s`", escapeIdent(tableName))
	var args []any
	if lastKey != nil {
		query += fmt.Sprintf(" WHERE `%s` > ?", escapeIdent(keyColumn))
		args = append(args, lastKey)
	}
	query += fmt.Sprintf(" ORDER BY `%s` ASC LIMIT %d", escapeIdent(keyColumn), limit)
	return a.scanRows(ctx, query, args, cols)
}

func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	if len(rows) == 0 {
		return nil
	}
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	query := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)",
		escapeIdent(tableName), quoteIdentifiers(columns), strings.Join(placeholders, ", "))

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("TDengine: begin tx failed: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("TDengine: prepare failed: %w", err)
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
			return fmt.Errorf("TDengine: write data failed: %w", err)
		}
	}
	return tx.Commit()
}

func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToTDengine(typeconv.Normalize(col.BaseType), col)
}

func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	return nil, nil
}

func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("TDengine: triggers not supported")
}

func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("TDengine: routines not supported")
}

func (a *Adapter) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("TDengine: query data failed: %w", err)
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
			return nil, fmt.Errorf("TDengine: scan data failed: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("TDengine: iterate data failed: %w", err)
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

func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("`%s`", escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}

// escapeIdent 转义 TDengine 标识符中的反引号
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, "`", "``")
}
