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
	// 添加 pragma 优化；用户可能传入带参数的 DSN（如 file:x.db?mode=ro），此时用 & 追加
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn = fmt.Sprintf("%s%s_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", dsn, sep)

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
	rows, err := a.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", escapeIdent(tableName)))
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

	// 识别 rowid 别名：SQLite 中单列 INTEGER PRIMARY KEY（无论写成列约束还是表约束）
	// 等价于自增主键（MySQL AUTO_INCREMENT / PG SERIAL），迁移时需标记 AutoIncrement，
	// 否则反向迁移（SQLite→MySQL）会丢失自增属性。
	if len(pkColumns) == 1 {
		var ddl sql.NullString
		err := a.db.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type='table' AND name = ?`, tableName,
		).Scan(&ddl)
		if err == nil && ddl.Valid {
			for i := range schema.Columns {
				c := &schema.Columns[i]
				if c.IsPrimaryKey && strings.EqualFold(strings.TrimSpace(c.DataType), "INTEGER") {
					c.AutoIncrement = true
					// MySQL 中自增列必须非空，显式补 NOT NULL
					c.Nullable = false
					break
				}
			}
		}
	}

	// TEXT 列值采样：SQLite 无原生日期时间类型，MySQL/PG 迁来时时间列都变成 TEXT，
	// 通过采样还原时间语义（全部非空样本都是 datetime 形态才推断），避免反向迁移丢失时间类型
	for i := range schema.Columns {
		col := &schema.Columns[i]
		if col.BaseType != "TEXT" || col.IsPrimaryKey {
			continue
		}
		srows, err := a.db.QueryContext(ctx, fmt.Sprintf(
			`SELECT %s FROM %s WHERE %s IS NOT NULL AND %s != '' LIMIT 50`,
			escapeIdent(col.Name), escapeIdent(tableName), escapeIdent(col.Name), escapeIdent(col.Name)))
		if err != nil {
			continue
		}
		total, temporal := 0, 0
		for srows.Next() {
			var v string
			if srows.Scan(&v) != nil {
				break
			}
			total++
			if typeconv.LooksLikeDateTime(v) {
				temporal++
			}
		}
		srows.Close()
		if total > 0 && temporal == total {
			col.BaseType = "DATETIME"
		}
	}

	// 如果有主键列，创建主键索引
	if len(pkColumns) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:      "PRIMARY",
			Columns:   pkColumns,
			IsUnique:  true,
			IsPrimary: true,
		})
	}

	// 获取索引信息：先收集索引名并关闭结果集，
	// 再逐个查询索引列（连接池有限，遍历时嵌套查询会死锁）
	type idxInfo struct {
		name   string
		unique bool
	}
	var idxList []idxInfo
	indexRows, err := a.db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%s)", escapeIdent(tableName)))
	if err == nil {
		for indexRows.Next() {
			// 现代 SQLite 返回 5 列 (seq,name,unique,origin,partial)，老版本 3 列；
			// 列数不匹配会导致 Scan 失败，此处做兼容，避免静默丢失全部二级索引
			var seq, unique, partial int
			var name, origin string
			if err := indexRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				if err := indexRows.Scan(&seq, &name, &unique); err != nil {
					continue
				}
			}
			if name == "PRIMARY" || strings.HasPrefix(name, "sqlite_") {
				continue
			}
			idxList = append(idxList, idxInfo{name: name, unique: unique == 1})
		}
		indexRows.Close()
	}

	for _, it := range idxList {
		// 获取索引列
		idxColRows, err := a.db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%s)", escapeIdent(it.name)))
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
			Name:     it.name,
			Columns:  idxCols,
			IsUnique: it.unique,
		})
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", escapeIdent(tableName))).Scan(&count)
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
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", escapeIdent(tableName), escapeIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("sqlite: 备份表失败 (%s→%s): %w", tableName, backupName, err)
	}
	return backupName, nil
}

// RestoreFromBackup 从备份表恢复
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", escapeIdent(originalName)))
		if err != nil {
			return fmt.Errorf("sqlite: 恢复备份时删除当前表失败: %w", err)
		}
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", escapeIdent(backupName), escapeIdent(originalName)))
	if err != nil {
		return fmt.Errorf("sqlite: 恢复备份失败 (%s→%s): %w", backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", escapeIdent(backupName)))
	if err != nil {
		return fmt.Errorf("sqlite: 删除备份表失败: %w", err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL（类型经 typeconv 映射，支持异构迁移）
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", escapeIdent(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(escapeIdent(col.Name) + " ")

		// 类型映射
		sb.WriteString(a.MapType(col))

		if !col.Nullable {
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
			sb.WriteString(escapeIdent(c))
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
		sb.WriteString(fmt.Sprintf("IF NOT EXISTS %s ON %s (", escapeIdent(idx.Name), escapeIdent(table.Name)))
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(escapeIdent(c))
		}
		sb.WriteString(")")
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", escapeIdent(tableName)), nil
}

// ReadData 按偏移量分页读取数据
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 获取表结构失败: %w", err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", escapeIdent(tableName), limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 获取表结构失败: %w", err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM %s", escapeIdent(tableName))
	args := []any{}
	if lastKey != nil {
		args = append(args, lastKey)
		query += fmt.Sprintf(" WHERE %s > ?", escapeIdent(keyColumn))
	}
	query += fmt.Sprintf(" ORDER BY %s ASC LIMIT %d", escapeIdent(keyColumn), limit)
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
		"INSERT INTO %s (%s) VALUES (%s)",
		escapeIdent(tableName),
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
				// time.Time 直写会以 Go 字符串形式落入 TEXT 列，统一转为标准 datetime 格式
				if t, isTime := val.(time.Time); isTime {
					val = t.Format("2006-01-02 15:04:05")
				}
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

// GetTriggers 获取 SQLite 触发器定义
func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT name, sql FROM sqlite_master
		WHERE type='trigger' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 查询触发器失败: %w", err)
	}
	defer rows.Close()

	var triggers []types.TriggerMeta
	for rows.Next() {
		var name, sqlText sql.NullString
		if err := rows.Scan(&name, &sqlText); err != nil {
			return nil, fmt.Errorf("sqlite: 读取触发器失败: %w", err)
		}
		if !name.Valid || !sqlText.Valid {
			continue
		}
		trigger := types.TriggerMeta{
			Name: name.String,
			Body: sqlText.String,
		}
		triggers = append(triggers, trigger)
	}
	return triggers, nil
}

// GetRoutines SQLite 不支持存储过程，返回空切片
func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	return nil, nil
}

// GenerateTriggerDDL 将触发器转换为目标方言的 DDL
func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("CREATE TRIGGER `%s` %s %s ON `%s` FOR EACH ROW\n",
			escapeIdent(trigger.Name), trigger.Timing, trigger.Event, escapeIdent(trigger.Table)))
		sb.WriteString(trigger.Body)
		return sb.String(), nil
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		return trigger.Body, nil
	case types.SQLite:
		return trigger.Body, nil // SQLite 原样输出
	default:
		return "", fmt.Errorf("sqlite: 不支持的目标方言 %s", targetDialect)
	}
}

// GenerateRoutineDDL SQLite 不支持存储过程，直接返回错误
func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	return "", fmt.Errorf("sqlite: 不支持存储过程/函数迁移")
}

// escapeIdent 以 SQL 方式转义标识符（双写引号），不能用 Go 的 %q——
// 那会对 " 产生 \" 转义，在 SQL 里是字面反斜杠
func escapeIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteIdentifiers 给列名加引号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = escapeIdent(c)
	}
	return strings.Join(quoted, ", ")
}
