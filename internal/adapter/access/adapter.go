// Package access 注册 Microsoft Access 适配器。
//
// Microsoft Access 是桌面文件型数据库（.mdb/.accdb），使用 Jet/ACE 引擎。
// 本适配器通过 ODBC 接口连接 Access 数据库（Windows 需安装 Microsoft Access Database Engine）。
// Access 的 SQL 语法与 SQL Server 类似但有限制：
//   - 不支持存储过程（有 Query 但不是 SQL 存储过程）
//   - 不支持触发器
//   - 分页使用 TOP + 子查询（类似 MSSQL 2012 之前的方案）
//   - 标识符用方括号 [] 包裹
package access

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"
)

// 确保实现了接口
var _ types.DatabaseAdapter = (*Adapter)(nil)

// Adapter Access 适配器
type Adapter struct {
	db    *sql.DB
	brand string
}

func init() {
	types.RegisterAdapter(types.Access, func() types.DatabaseAdapter {
		return &Adapter{brand: "Microsoft Access"}
	})
}

// connectDB 由平台特定文件（access_windows.go / access_other.go）提供。
// 在 Windows 上通过 ODBC 连接，其他平台返回错误。

// Close 关闭连接
func (a *Adapter) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// GetVersion 获取 Access 版本
func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	// Access 通过 ODBC 获取版本信息有限，返回固定品牌标识
	var ver string
	err := a.db.QueryRowContext(ctx, "SELECT Val(GetOption(\"AccessVersion\"))").Scan(&ver)
	if err != nil {
		// 某些 Access 版本不支持 GetOption，降级返回品牌
		return a.brand, nil
	}
	return fmt.Sprintf("%s (%s)", a.brand, ver), nil
}

// GetTables 获取所有表
func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT Name, Type FROM MSysObjects
		WHERE Type IN (1, 4, 6)
		AND Name NOT LIKE '~%'
		AND Name NOT LIKE 'MSys%'
		AND Flags = 0
		ORDER BY Name
	`)
	if err != nil {
		return nil, fmt.Errorf("access: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, fmt.Errorf("access: 读取表信息失败: %w", err)
		}
		tables = append(tables, types.TableMeta{Name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("access: 遍历表列表失败: %w", err)
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{Name: tableName}

	// Access 通过 ODBC 的 ANSI_NULLS + INFORMATION_SCHEMA 不可用，
	// 使用 ADOX 风格的列信息查询
	// 方案：用 SELECT TOP 1 * 读取列信息（ODBC + database/sql 返回 ColumnTypes）
	rows, err := a.db.QueryContext(ctx,
		fmt.Sprintf("SELECT TOP 1 * FROM [%s]", escapeIdent(tableName)))
	if err != nil {
		return schema, fmt.Errorf("access: 获取表结构失败: %w", err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return schema, fmt.Errorf("access: 读取列类型失败: %w", err)
	}

	colNames, err := rows.Columns()
	if err != nil {
		return schema, fmt.Errorf("access: 读取列名失败: %w", err)
	}

	var pkColumns []string
	for i, ct := range colTypes {
		col := types.ColumnMeta{
			Name:     colNames[i],
			DataType: ct.DatabaseTypeName(),
			BaseType: strings.ToUpper(ct.DatabaseTypeName()),
			Nullable: true,
		}

		// 解析长度和精度
		if length, ok := ct.Length(); ok && length > 0 {
			l := int(length)
			col.Length = &l
		}
		if prec, scale, ok := ct.DecimalSize(); ok {
			p := int(prec)
			s := int(scale)
			col.Precision = &p
			col.Scale = &s
		}
		if nullable, ok := ct.Nullable(); ok {
			col.Nullable = nullable
		}

		// Access 中 AUTOINCREMENT/IDENTITY 列是自增
		typeName := strings.ToUpper(ct.DatabaseTypeName())
		if strings.Contains(typeName, "COUNTER") || strings.Contains(typeName, "AUTOINCREMENT") ||
			strings.Contains(typeName, "IDENTITY") {
			col.AutoIncrement = true
			col.IsPrimaryKey = true
			pkColumns = append(pkColumns, col.Name)
		}

		schema.Columns = append(schema.Columns, col)
	}

	// 主键索引
	if len(pkColumns) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:      "PrimaryKey",
			Columns:   pkColumns,
			IsUnique:  true,
			IsPrimary: true,
		})
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM [%s]", escapeIdent(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("access: 获取行数失败: %w", err)
	}
	return count, nil
}

// TableExists 检查表是否存在
func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	var cnt int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM MSysObjects WHERE Name=? AND Type IN (1,4,6) AND Flags=0`,
		tableName).Scan(&cnt)
	if err != nil {
		return false, fmt.Errorf("access: 检查表存在失败: %w", err)
	}
	return cnt > 0, nil
}

// BackupTable 将表重命名为备份表名
func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	// Access 不支持 RENAME，用 SELECT INTO 复制 + DROP
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("SELECT * INTO [%s] FROM [%s]", escapeIdent(backupName), escapeIdent(tableName)))
	if err != nil {
		return "", fmt.Errorf("access: 备份表失败 (%s→%s): %w", tableName, backupName, err)
	}
	_, err = a.db.ExecContext(ctx,
		fmt.Sprintf("DROP TABLE [%s]", escapeIdent(tableName)))
	if err != nil {
		return "", fmt.Errorf("access: 删除原表失败: %w", err)
	}
	return backupName, nil
}

// RestoreFromBackup 从备份表恢复
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	// 删除当前表（如果存在）
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx,
			fmt.Sprintf("DROP TABLE [%s]", escapeIdent(originalName)))
		if err != nil {
			return fmt.Errorf("access: 恢复时删除当前表失败: %w", err)
		}
	}
	// SELECT INTO 复制
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("SELECT * INTO [%s] FROM [%s]", escapeIdent(originalName), escapeIdent(backupName)))
	if err != nil {
		return fmt.Errorf("access: 恢复备份失败 (%s→%s): %w", backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("DROP TABLE [%s]", escapeIdent(backupName)))
	if err != nil {
		return fmt.Errorf("access: 删除备份表失败: %w", err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE [%s] (\n", escapeIdent(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  [")
		sb.WriteString(escapeIdent(col.Name))
		sb.WriteString("] ")
		sb.WriteString(a.MapType(col))

		if col.AutoIncrement {
			sb.WriteString(" AUTOINCREMENT")
		} else if !col.Nullable {
			sb.WriteString(" NOT NULL")
		}

		if col.DefaultValue != nil && *col.DefaultValue != "" && !col.AutoIncrement {
			if quoted := typeconv.FormatDefault(*col.DefaultValue); quoted != "" {
				sb.WriteString(" DEFAULT " + quoted)
			}
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
		sb.WriteString(",\n  CONSTRAINT [PK_")
		sb.WriteString(escapeIdent(table.Name))
		sb.WriteString("] PRIMARY KEY (")
		for i, c := range pkCols {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("[" + escapeIdent(c) + "]")
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")

	// 二级索引
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
		sb.WriteString("[" + escapeIdent(idx.Name) + "] ON [" + escapeIdent(table.Name) + "] (")
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("[" + escapeIdent(c) + "]")
		}
		sb.WriteString(")")
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS [%s]", escapeIdent(tableName)), nil
}

// ReadData 按偏移量分页读取数据
// Access 不支持 OFFSET，使用 TOP + 子查询方案
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("access: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)

	var query string
	if offset > 0 {
		// Access 分页：TOP N NOT IN (SELECT TOP M ...)
		// 需要排序列，用列名拼接
		colList := quoteIdentifiers(cols)
		query = fmt.Sprintf(
			"SELECT TOP %d %s FROM [%s] WHERE [%s] NOT IN (SELECT TOP %d [%s] FROM [%s])",
			limit, colList, escapeIdent(tableName),
			escapeIdent(cols[0]), offset, escapeIdent(cols[0]), escapeIdent(tableName),
		)
	} else {
		colList := quoteIdentifiers(cols)
		query = fmt.Sprintf("SELECT TOP %d %s FROM [%s]", limit, colList, escapeIdent(tableName))
	}

	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("access: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	colList := quoteIdentifiers(cols)

	query := fmt.Sprintf("SELECT TOP %d %s FROM [%s]", limit, colList, escapeIdent(tableName))
	args := []any{}
	if lastKey != nil {
		args = append(args, lastKey)
		query = fmt.Sprintf(
			"SELECT TOP %d %s FROM [%s] WHERE [%s] > ? ORDER BY [%s]",
			limit, colList, escapeIdent(tableName), escapeIdent(keyColumn), escapeIdent(keyColumn),
		)
	} else {
		query = fmt.Sprintf(
			"SELECT TOP %d %s FROM [%s] ORDER BY [%s]",
			limit, colList, escapeIdent(tableName), escapeIdent(keyColumn),
		)
	}
	return a.scanRows(ctx, query, args, cols)
}

// WriteData 批量写入数据
func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	if len(rows) == 0 {
		return nil
	}

	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}

	query := fmt.Sprintf(
		"INSERT INTO [%s] (%s) VALUES (%s)",
		escapeIdent(tableName),
		quoteIdentifiers(columns),
		strings.Join(placeholders, ", "),
	)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("access: 开启事务失败: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("access: 预处理失败: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		values := make([]any, len(columns))
		for i, col := range columns {
			if val, ok := row[col]; ok {
				if t, isTime := val.(time.Time); isTime {
					val = t.Format("2006-01-02 15:04:05.999")
				}
				values[i] = val
			} else {
				values[i] = nil
			}
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			tx.Rollback()
			return fmt.Errorf("access: 写入数据失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("access: 提交事务失败: %w", err)
	}
	return nil
}

// ExecContext 执行原始 SQL
func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

// MapType 将源数据库的列类型映射为 Access 类型
func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToAccess(typeconv.Normalize(col.BaseType), col)
}

// GetTriggers Access 不支持触发器
func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	return nil, nil
}

// GetRoutines Access 不支持存储过程
func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

// GenerateTriggerDDL Access 不支持触发器
func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("access: 不支持触发器迁移")
}

// GenerateRoutineDDL Access 不支持存储过程
func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("access: 不支持存储过程/函数迁移")
}

// scanRows 执行查询并按列名映射为 Row
func (a *Adapter) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("access: 查询数据失败: %w", err)
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
			return nil, fmt.Errorf("access: 读取数据失败: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("access: 遍历数据失败: %w", err)
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

// escapeIdent 转义 Access 标识符（方括号包裹，内部 ] 双写）
func escapeIdent(s string) string {
	return strings.ReplaceAll(s, "]", "]]")
}

// quoteIdentifiers 给列名加方括号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = "[" + escapeIdent(c) + "]"
	}
	return strings.Join(quoted, ", ")
}
