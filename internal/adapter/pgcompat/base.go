// Package pgcompat 提供基于 PostgreSQL 线协议的共享适配器基座。
//
// PostgreSQL、openGauss、KingbaseES、CockroachDB 共用本实现，
// 通过 Option 表达差异（注释支持、二进制类型等）。
package pgcompat

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	"github.com/lib/pq"
)

// Option 基座行为选项
type Option func(*Base)

// WithNoComments 禁用注释读写与 COMMENT ON 生成（如 CockroachDB）
func WithNoComments() Option {
	return func(b *Base) { b.noComments = true }
}

// WithBinaryType 自定义二进制类型名（默认 BYTEA，CockroachDB 为 BYTES）
func WithBinaryType(t string) Option {
	return func(b *Base) { b.binaryType = t }
}

// Base PostgreSQL 兼容协议适配器基座。
type Base struct {
	Brand      string // 品牌名，用于错误信息
	db         *sql.DB
	noComments bool
	binaryType string
}

// New 创建基座实例
func New(brand string, opts ...Option) *Base {
	b := &Base{Brand: brand, binaryType: "BYTEA"}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Connect 连接 PostgreSQL 数据库
func (a *Base) Connect(ctx context.Context, config types.ConnectionConfig) error {
	sslmode := config.SSLMode
	if sslmode == "" {
		sslmode = "prefer"
	}
	// 通过 URL 形式构造连接串，url.UserPassword 会自动编码密码中的特殊字符
	dsn := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(config.Username, config.Password),
		Host:   fmt.Sprintf("%s:%d", config.Host, config.Port),
		Path:   config.Database,
		RawQuery: url.Values{
			"sslmode": []string{sslmode},
		}.Encode(),
	}
	connector, err := pq.NewConnector(dsn.String())
	if err != nil {
		return fmt.Errorf("%s: 构造连接串失败: %w", a.brand(), err)
	}
	db := sql.OpenDB(connector)
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
		return "postgres"
	}
	return a.Brand
}

// GetVersion 获取数据库版本
func (a *Base) GetVersion(ctx context.Context) (string, error) {
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("%s: 获取版本失败: %w", a.brand(), err)
	}
	return version, nil
}

// GetTables 获取所有表
func (a *Base) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	commentExpr := `COALESCE(obj_description(quote_ident(table_name)::regclass, 'pg_class'), '')`
	if a.noComments {
		commentExpr = `''`
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT table_name, `+commentExpr+`
		FROM information_schema.tables
		WHERE table_schema = 'public'
		AND table_type = 'BASE TABLE'
		ORDER BY table_name
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

// normalizePGDefault 规范化 information_schema.columns.column_default：
// PG 会在默认值后附加 ::类型 转换（如 'pending'::character varying、NULL::text、'0'::numeric），
// 原样迁移到 MySQL 等库会语法错误；这里仅剥离字符串/字面量尾部的 ::类型 后缀。
func normalizePGDefault(def string) string {
	v := strings.TrimSpace(def)
	i := strings.Index(v, "::")
	if i < 0 {
		return v
	}
	left := strings.TrimSpace(v[:i])
	if strings.HasPrefix(left, "'") || strings.EqualFold(left, "NULL") || pgNumericPattern.MatchString(left) {
		return left
	}
	// 函数式默认值（如 now()::timestamp）保留原样
	return v
}

var pgNumericPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// GetTableSchema 获取表结构
func (a *Base) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}

	// 获取表注释
	if !a.noComments {
		var comment sql.NullString
		err := a.db.QueryRowContext(ctx, `
			SELECT obj_description(quote_ident($1)::regclass, 'pg_class')
		`, tableName).Scan(&comment)
		if err == nil && comment.Valid {
			schema.Comment = comment.String
		}
	}

	// 获取列信息
	rows, err := a.db.QueryContext(ctx, `
		SELECT column_name, data_type, character_maximum_length,
		       numeric_precision, numeric_scale, is_nullable,
		       column_default, ordinal_position
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		ORDER BY ordinal_position
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("%s: 查询列信息失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	for rows.Next() {
		var name, dataType, nullable string
		var defVal sql.NullString
		var maxLen, numPrecision, numScale, ordinal sql.NullInt64

		if err := rows.Scan(&name, &dataType, &maxLen, &numPrecision, &numScale, &nullable, &defVal, &ordinal); err != nil {
			return schema, fmt.Errorf("%s: 读取列信息失败: %w", a.brand(), err)
		}

		col := types.ColumnMeta{
			Name:     name,
			DataType: dataType,
			BaseType: strings.ToUpper(dataType),
			Nullable: nullable == "YES",
		}

		if defVal.Valid && defVal.String != "" {
			val := normalizePGDefault(defVal.String)
			if val != "" {
				col.DefaultValue = &val
			}
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
	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("%s: 遍历列信息失败: %w", a.brand(), err)
	}
	schema.Columns = columns

	// 获取主键
	pkRows, err := a.db.QueryContext(ctx, `
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
	if err != nil {
		return schema, fmt.Errorf("%s: 查询主键信息失败: %w", a.brand(), err)
	}
	var pkCols []string
	for pkRows.Next() {
		var colName string
		if err := pkRows.Scan(&colName); err != nil {
			pkRows.Close()
			return schema, fmt.Errorf("%s: 读取主键信息失败: %w", a.brand(), err)
		}
		pkCols = append(pkCols, colName)
	}
	if err := pkRows.Err(); err != nil {
		pkRows.Close()
		return schema, fmt.Errorf("%s: 遍历主键信息失败: %w", a.brand(), err)
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

	// 获取索引
	idxRows, err := a.db.QueryContext(ctx, `
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
	if err != nil {
		return schema, fmt.Errorf("%s: 查询索引信息失败: %w", a.brand(), err)
	}
	defer idxRows.Close()
	for idxRows.Next() {
		var idxName string
		var colArray []string
		var isUnique bool
		// lib/pq 不能直接 Scan text[] 到 []string，需 pq.Array 包装，否则索引静默丢失
		if err := idxRows.Scan(&idxName, pq.Array(&colArray), &isUnique); err != nil {
			return schema, fmt.Errorf("%s: 读取索引信息失败: %w", a.brand(), err)
		}
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:     idxName,
			Columns:  colArray,
			IsUnique: isUnique,
		})
	}
	if err := idxRows.Err(); err != nil {
		return schema, fmt.Errorf("%s: 遍历索引信息失败: %w", a.brand(), err)
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Base) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, escapeIdent(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("%s: 获取行数失败: %w", a.brand(), err)
	}
	return count, nil
}

// TableExists 检查表是否存在
func (a *Base) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = $1
	`, tableName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("%s: 检查表存在失败: %w", a.brand(), err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名
func (a *Base) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE "%s" RENAME TO "%s"`,
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
		_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s" CASCADE`, escapeIdent(originalName)))
		if err != nil {
			return fmt.Errorf("%s: 恢复备份时删除当前表失败: %w", a.brand(), err)
		}
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE "%s" RENAME TO "%s"`,
		escapeIdent(backupName), escapeIdent(originalName)))
	if err != nil {
		return fmt.Errorf("%s: 恢复备份失败 (%s→%s): %w", a.brand(), backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Base) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"`, escapeIdent(backupName)))
	if err != nil {
		return fmt.Errorf("%s: 删除备份表失败: %w", a.brand(), err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL（类型经 typeconv 映射，支持异构迁移）
func (a *Base) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS \"%s\" (\n", escapeIdent(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf("\"%s\" ", escapeIdent(col.Name)))
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
			sb.WriteString(fmt.Sprintf("\"%s\"", escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")

	// 表注释与列注释
	if !a.noComments {
		if table.Comment != "" {
			sb.WriteString(fmt.Sprintf(";\nCOMMENT ON TABLE \"%s\" IS '%s'", escapeIdent(table.Name), escapeSingleQuote(table.Comment)))
		}
		for _, col := range table.Columns {
			if col.Comment != "" {
				sb.WriteString(fmt.Sprintf(";\nCOMMENT ON COLUMN \"%s\".\"%s\" IS '%s'",
					escapeIdent(table.Name), escapeIdent(col.Name), escapeSingleQuote(col.Comment)))
			}
		}
	}

	// 非主键索引
	for _, idx := range table.Indexes {
		if idx.IsPrimary {
			continue
		}
		sb.WriteString(";\n")
		if idx.IsUnique {
			sb.WriteString(fmt.Sprintf("CREATE UNIQUE INDEX IF NOT EXISTS \"%s\" ON \"%s\" (", escapeIdent(idx.Name), escapeIdent(table.Name)))
		} else {
			sb.WriteString(fmt.Sprintf("CREATE INDEX IF NOT EXISTS \"%s\" ON \"%s\" (", escapeIdent(idx.Name), escapeIdent(table.Name)))
		}
		for i, c := range idx.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("\"%s\"", escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Base) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS \"%s\"", escapeIdent(tableName)), nil
}

// ReadData 按偏移量分页读取数据
func (a *Base) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d OFFSET %d`, escapeIdent(tableName), limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
func (a *Base) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf(`SELECT * FROM "%s"`, escapeIdent(tableName))
	args := []any{}
	if lastKey != nil {
		args = append(args, lastKey)
		query += fmt.Sprintf(` WHERE "%s" > $1`, escapeIdent(keyColumn))
	}
	query += fmt.Sprintf(` ORDER BY "%s" ASC LIMIT %d`, escapeIdent(keyColumn), limit)
	return a.scanRows(ctx, query, args, cols)
}

// WriteData 批量写入数据（内部事务保证批次原子性）
func (a *Base) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
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

// MapType 将源数据库的列类型映射为 PostgreSQL 类型
func (a *Base) MapType(col types.ColumnMeta) string {
	mapped := typeconv.ToPostgres(typeconv.Normalize(col.BaseType), col)
	// CockroachDB 等方言的二进制类型差异
	if a.binaryType != "BYTEA" && (mapped == "BYTEA") {
		return a.binaryType
	}
	return mapped
}

// scanRows 执行查询并按列名映射为 Row
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
			row[col] = values[i]
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

// GetTriggers 获取 PostgreSQL 触发器定义
func (a *Base) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT t.tgname, 
		       CASE WHEN (t.tgtype & 4) != 0 THEN 'INSERT'
		            WHEN (t.tgtype & 16) != 0 THEN 'DELETE'
		            WHEN (t.tgtype & 8) != 0 THEN 'UPDATE'
		            END as event,
		       CASE WHEN (t.tgtype & 1) != 0 THEN 'BEFORE'
		            WHEN (t.tgtype & 2) != 0 THEN 'AFTER'
		            WHEN (t.tgtype & 64) != 0 THEN 'INSTEAD OF'
		            END as timing,
		       c.relname as table_name,
		       pg_get_triggerdef(t.oid) as body,
		       (t.tgtype & 16) != 0 as for_each_row
		FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		AND NOT t.tgisinternal
		ORDER BY t.tgname
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: 查询触发器失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var triggers []types.TriggerMeta
	for rows.Next() {
		var name, event, timing, table, body string
		var forEachRow bool
		if err := rows.Scan(&name, &event, &timing, &table, &body, &forEachRow); err != nil {
			return nil, fmt.Errorf("%s: 读取触发器失败: %w", a.brand(), err)
		}
		triggers = append(triggers, types.TriggerMeta{
			Name:       name,
			Event:      event,
			Timing:     timing,
			Table:      table,
			Body:       body,
			ForEachRow: forEachRow,
		})
	}
	return triggers, nil
}

// GetRoutines 获取 PostgreSQL 存储过程和函数定义
func (a *Base) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.proname, 
		       CASE WHEN p.prokind = 'p' THEN 'procedure' ELSE 'function' END as type,
		       pg_get_functiondef(p.oid) as body,
		       pg_get_function_result(p.oid) as returns
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public'
		ORDER BY p.proname
	`)
	if err != nil {
		return nil, fmt.Errorf("%s: 查询存储过程失败: %w", a.brand(), err)
	}
	defer rows.Close()

	var routines []types.RoutineMeta
	for rows.Next() {
		var name, rType, body string
		var returns sql.NullString
		if err := rows.Scan(&name, &rType, &body, &returns); err != nil {
			return nil, fmt.Errorf("%s: 读取存储过程失败: %w", a.brand(), err)
		}
		routine := types.RoutineMeta{
			Name: name,
			Type: rType,
			Body: body,
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
		// PG 触发器体一般是 EXECUTE FUNCTION ... 转到 MySQL 需提取逻辑
		sb.WriteString(trigger.Body)
		return sb.String(), nil
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		return trigger.Body, nil // PG 原样输出
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
		return routine.Body, nil // PG 原样输出
	default:
		return "", fmt.Errorf("%s: 不支持的目标方言 %s", a.brand(), targetDialect)
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

// quoteIdentifiers 给列名加双引号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("\"%s\"", escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}
