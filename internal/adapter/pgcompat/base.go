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
	tablespace string // 目标表空间（可选，CREATE TABLE 时追加 TABLESPACE 子句）
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
// PG 会在默认值后附加 ::类型 转换（如 'pending'::character varying、NULL::text、'0'::numeric、
// nextval('t_id_seq'::regclass)），原样迁移到 MySQL 等库会语法错误。
// 注意：字面量本身也可能含 ::（如 'a::b'::text），所以从最后一个 :: 剥离，
// 且只有剥离后是完整的引号字面量/NULL/函数调用才接受，否则保守保留原值。
func normalizePGDefault(def string) string {
	v := strings.TrimSpace(def)
	// 找位于顶层（括号深度 0 且不在字符串字面量内）的最后一个 "::<类型>"。
	// 不能简单 LastIndex：nextval('seq'::regclass) 的 :: 在括号内，
	// 剥掉会把函数调用切碎，只能整体保留（该列会同时被标记为 AutoIncrement，
	// DEFAULT 不会出现在生成的 DDL 中，所以保留是安全的）。
	i := -1
	depth := 0
	inStr := false
	for j := 0; j < len(v)-1; j++ {
		c := v[j]
		if inStr {
			if c == '\'' {
				inStr = false
			}
			continue
		}
		switch c {
		case '\'':
			inStr = true
		case '(':
			depth++
		case ')':
			depth--
		case ':':
			if v[j+1] == ':' && depth == 0 {
				i = j
			}
		}
	}
	if i < 0 {
		return v
	}
	left := strings.TrimSpace(v[:i])
	if strings.HasPrefix(left, "'") && strings.HasSuffix(left, "'") {
		return left
	}
	if strings.EqualFold(left, "NULL") || pgNumericPattern.MatchString(left) {
		return left
	}
	// 函数式默认值（如 now()::timestamp）剥掉尾部 cast 保留函数调用
	if strings.HasSuffix(left, ")") && strings.Contains(left, "(") {
		return left
	}
	// :: 语义不明（非常规形态），保守保留原值
	return v
}

var pgNumericPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// GetTableSchema 获取表结构（支持 "schema.table" 限定名；未配置映射时查 public schema）
func (a *Base) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}
	schemaName, table := types.SplitQualified(tableName)
	schemaFilter := "table_schema = COALESCE(NULLIF($2, ''), 'public')"

	// 获取表注释（限定名需逐段拼出 regclass 引用；未配置 schema 时走 search_path）
	if !a.noComments {
		var comment sql.NullString
		ref := `"` + escapeIdent(table) + `"`
		if schemaName != "" {
			ref = `"` + escapeIdent(schemaName) + `".` + ref
		}
		err := a.db.QueryRowContext(ctx,
			`SELECT obj_description(`+ref+`::regclass, 'pg_class')`).Scan(&comment)
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
		WHERE `+schemaFilter+` AND table_name = $1
		ORDER BY ordinal_position
	`, table, schemaName)
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
WHERE tc.table_schema = COALESCE(NULLIF($2, ''), 'public')
AND tc.table_name = $1
AND tc.constraint_type = 'PRIMARY KEY'
ORDER BY kcu.ordinal_position
`, table, schemaName)
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

	// 获取外键（此前 PG 源库外键被完全丢弃，MySQL→PG 有延后添加而 PG 源无数据可延后）
	fkRows, err := a.db.QueryContext(ctx, `
		SELECT tc.constraint_name,
		       kcu.column_name,
		       ccu.table_name,
		       ccu.column_name,
		       COALESCE(rc.delete_rule, ''),
		       COALESCE(rc.update_rule, '')
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage ccu
			ON ccu.constraint_name = tc.constraint_name
			AND ccu.table_schema = tc.table_schema
		LEFT JOIN information_schema.referential_constraints rc
			ON rc.constraint_name = tc.constraint_name
			AND rc.constraint_schema = tc.table_schema
		WHERE tc.table_schema = COALESCE(NULLIF($2, ''), 'public')
		AND tc.table_name = $1
		AND tc.constraint_type = 'FOREIGN KEY'
		ORDER BY tc.constraint_name, kcu.ordinal_position
	`, table, schemaName)
	if err != nil {
		return schema, fmt.Errorf("%s: 查询外键信息失败: %w", a.brand(), err)
	}
	fkIndex := map[string]*types.ForeignKeyMeta{}
	var fkOrder []string
	for fkRows.Next() {
		var cname, colName, refTable, refCol, onDel, onUpd string
		if err := fkRows.Scan(&cname, &colName, &refTable, &refCol, &onDel, &onUpd); err != nil {
			fkRows.Close()
			return schema, fmt.Errorf("%s: 读取外键信息失败: %w", a.brand(), err)
		}
		fk, ok := fkIndex[cname]
		if !ok {
			fk = &types.ForeignKeyMeta{Name: cname, RefTable: refTable, OnDelete: onDel, OnUpdate: onUpd}
			fkIndex[cname] = fk
			fkOrder = append(fkOrder, cname)
		}
		fk.Columns = append(fk.Columns, colName)
		fk.RefColumns = append(fk.RefColumns, refCol)
	}
	if err := fkRows.Err(); err != nil {
		fkRows.Close()
		return schema, fmt.Errorf("%s: 遍历外键信息失败: %w", a.brand(), err)
	}
	fkRows.Close()
	for _, name := range fkOrder {
		schema.ForeignKeys = append(schema.ForeignKeys, *fkIndex[name])
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
	err := a.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, qualifyTable(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("%s: 获取行数失败: %w", a.brand(), err)
	}
	return count, nil
}

// TableExists 检查表是否存在（支持 "schema.table" 限定名；未配置时查 public）
func (a *Base) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	schemaName, table := types.SplitQualified(tableName)
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = COALESCE(NULLIF($1, ''), 'public') AND table_name = $2
	`, schemaName, table).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("%s: 检查表存在失败: %w", a.brand(), err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名（支持 "schema.table" 限定名，
// ALTER TABLE ... RENAME TO 的目标不带 schema，重命名后留在原 Schema，
// 返回限定名便于后续恢复/删除定位）
func (a *Base) BackupTable(ctx context.Context, tableName string) (string, error) {
	schemaName, table := types.SplitQualified(tableName)
	backupName := fmt.Sprintf("_bak_%s_%s", table, time.Now().Format("20060102_150405"))
	qualifiedBackup := backupName
	if schemaName != "" {
		qualifiedBackup = schemaName + "." + backupName
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO "%s"`,
		qualifyTable(tableName), escapeIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("%s: 备份表失败 (%s→%s): %w", a.brand(), tableName, qualifiedBackup, err)
	}
	return qualifiedBackup, nil
}

// RestoreFromBackup 从备份表恢复（删除当前同名表，将备份表重命名回原表名）。
// 注意：RENAME TO 目标侧不能带 schema（PG 语法限制），
// 重命名后留在备份表所在 schema（与原表同 schema，语义正确）
func (a *Base) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		// 不用 CASCADE：级联会静默删除依赖视图/外键，把“恢复备份”变成数据破坏。
		// 有依赖时 DROP 报错，用户可显式处理依赖后再恢复。
		_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, qualifyTable(originalName)))
		if err != nil {
			return fmt.Errorf("%s: 恢复备份时删除当前表失败（可能存在依赖视图/外键，请先处理依赖）: %w", a.brand(), err)
		}
	}
	_, bare := types.SplitQualified(originalName)
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO "%s"`,
		qualifyTable(backupName), escapeIdent(bare)))
	if err != nil {
		return fmt.Errorf("%s: 恢复备份失败 (%s→%s): %w", a.brand(), backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Base) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, qualifyTable(backupName)))
	if err != nil {
		return fmt.Errorf("%s: 删除备份表失败: %w", a.brand(), err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL（类型经 typeconv 映射，支持异构迁移）
// 注意：禁止使用 CREATE TABLE IF NOT EXISTS —— 目标表已存在时建表会被
// 数据库静默跳过，掩盖前置的备份/删除判断失效，导致数据重复追加。
// 表存在性由 orchestrator 通过 TableExists 显式判断并执行备份/删除/报错。
func (a *Base) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", qualifyTable(table.Name)))

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

	// 表空间（可选；未配置时使用目标库默认值）
	if a.tablespace != "" {
		sb.WriteString(fmt.Sprintf("\nTABLESPACE \"%s\"", escapeIdent(a.tablespace)))
	}

	// 表注释与列注释
	if !a.noComments {
		if table.Comment != "" {
			sb.WriteString(fmt.Sprintf(";\nCOMMENT ON TABLE %s IS '%s'", qualifyTable(table.Name), escapeSingleQuote(table.Comment)))
		}
		for _, col := range table.Columns {
			if col.Comment != "" {
				sb.WriteString(fmt.Sprintf(";\nCOMMENT ON COLUMN %s.\"%s\" IS '%s'",
					qualifyTable(table.Name), escapeIdent(col.Name), escapeSingleQuote(col.Comment)))
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
			sb.WriteString(fmt.Sprintf("CREATE UNIQUE INDEX \"%s\" ON %s (", escapeIdent(idx.Name), qualifyTable(table.Name)))
		} else {
			sb.WriteString(fmt.Sprintf("CREATE INDEX \"%s\" ON %s (", escapeIdent(idx.Name), qualifyTable(table.Name)))
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
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifyTable(tableName)), nil
}

// ReadData 按偏移量分页读取数据
func (a *Base) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf(`SELECT * FROM %s LIMIT %d OFFSET %d`, qualifyTable(tableName), limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
func (a *Base) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}

	cols := schemaColumnNames(schema)
	query := fmt.Sprintf(`SELECT * FROM %s`, qualifyTable(tableName))
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
		`INSERT INTO %s (%s) VALUES (%s)`,
		qualifyTable(tableName),
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
		       -- pg_trigger.tgtype 位定义（pg_trigger.h）：ROW=1, BEFORE=2, AFTER=4,
		       -- INSERT=8, DELETE=16, UPDATE=32, TRUNCATE=64, INSTEAD=128。
		       -- 旧实现位值全部错位，导致 INSERT 触发器被标成 UPDATE、BEFORE 被标成 AFTER。
		       COALESCE(NULLIF(BTRIM(
		           CASE WHEN (t.tgtype & 8) != 0 THEN 'INSERT ' ELSE '' END ||
		           CASE WHEN (t.tgtype & 16) != 0 THEN 'DELETE ' ELSE '' END ||
		           CASE WHEN (t.tgtype & 32) != 0 THEN 'UPDATE ' ELSE '' END ||
		           CASE WHEN (t.tgtype & 64) != 0 THEN 'TRUNCATE' ELSE '' END), ''), 'INSERT') AS event,
		       CASE WHEN (t.tgtype & 128) != 0 THEN 'INSTEAD OF'
		            WHEN (t.tgtype & 2) != 0 THEN 'BEFORE'
		            ELSE 'AFTER' END AS timing,
		       c.relname AS table_name,
		       pg_get_triggerdef(t.oid) AS body,
		       (t.tgtype & 1) != 0 AS for_each_row
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: 遍历触发器失败: %w", a.brand(), err)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: 遍历存储过程失败: %w", a.brand(), err)
	}
	return routines, nil
}

// GenerateTriggerDDL 将触发器转换为目标方言的 DDL
func (a *Base) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB:
		return trigger.Body, nil // PG 系内原样输出
	default:
		// trigger.Body 是 pg_get_triggerdef 生成的完整 CREATE TRIGGER 语句，
		// 指向 PG 函数且函数体是 PL/pgSQL。旧实现直接在其前面拼接 MySQL 头部，
		// 产出必然语法错误的垃圾 SQL 还可能部分执行。跨方言触发器语义转换复杂，
		// 显式报错让用户手动迁移，绝不写入非法 SQL 污染目标库。
		return "", fmt.Errorf("%s: 暂不支持将触发器自动转换为 %s 方言（触发器体为 PL/pgSQL），请在目标库手动创建",
			a.brand(), targetDialect)
	}
}

// GenerateRoutineDDL 将存储过程/函数转换为目标方言的 DDL
func (a *Base) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB:
		return routine.Body, nil // PG 系内原样输出
	default:
		// routine.Body 是 pg_get_functiondef 生成的完整 CREATE FUNCTION 语句，
		// 旧实现包装成 MySQL 的 CREATE FUNCTION/PROCEDURE 会产出垃圾 SQL。显式报错。
		return "", fmt.Errorf("%s: 暂不支持将存储过程/函数自动转换为 %s 方言（函数体为 PL/pgSQL），请在目标库手动创建",
			a.brand(), targetDialect)
	}
}

// GenerateAddForeignKeyDDL 生成 "ALTER TABLE ... ADD CONSTRAINT ..."（外键延后添加用）
func (a *Base) GenerateAddForeignKeyDDL(tableName string, fk types.ForeignKeyMeta) (string, error) {
	fkName := fk.Name
	if fkName == "" {
		fkName = fmt.Sprintf("FK_%s_%s", tableName, fk.RefTable)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`ALTER TABLE %s ADD CONSTRAINT "%s" FOREIGN KEY (`,
		qualifyTable(tableName), escapeIdent(fkName)))
	for i, c := range fk.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf(`"%s"`, escapeIdent(c)))
	}
	sb.WriteString(fmt.Sprintf(`) REFERENCES %s (`, qualifyTable(fk.RefTable)))
	for i, c := range fk.RefColumns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf(`"%s"`, escapeIdent(c)))
	}
	sb.WriteString(")")
	if fk.OnDelete != "" && !strings.EqualFold(fk.OnDelete, "NO ACTION") {
		sb.WriteString(" ON DELETE " + strings.ToUpper(fk.OnDelete))
	}
	if fk.OnUpdate != "" && !strings.EqualFold(fk.OnUpdate, "NO ACTION") {
		sb.WriteString(" ON UPDATE " + strings.ToUpper(fk.OnUpdate))
	}
	return sb.String(), nil
}

// FixAutoIncrementSequences 修复自增序列：显式插入 SERIAL/IDENTITY 列的值后，
// 序列不会自动推进，不 setval 的话目标库下一条自动 INSERT 会主键冲突。
func (a *Base) FixAutoIncrementSequences(ctx context.Context, tableName string, columns []types.ColumnMeta) error {
	for _, col := range columns {
		if !col.AutoIncrement {
			continue
		}
		// setval(seq, MAX(id), true)：有行则序列=MAX(id)，空表则=1
		query := fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE(MAX("%s"), 1), MAX("%s") IS NOT NULL) FROM %s`,
			escapeSingleQuote(tableName), escapeSingleQuote(col.Name), escapeIdent(col.Name), escapeIdent(col.Name), qualifyTable(tableName))
		if _, err := a.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("%s: 修复序列失败 (%s.%s): %w", a.brand(), tableName, col.Name, err)
		}
	}
	return nil
}

// escapeIdent 转义 PostgreSQL 标识符
// qualifyTable 生成双引号包裹的表名片段，支持 "schema.table" 限定名
// （Schema 映射场景由 orchestrator 传入；未配置映射时为普通表名）
func qualifyTable(name string) string {
	schema, table := types.SplitQualified(name)
	if schema == "" {
		return `"` + escapeIdent(table) + `"`
	}
	return `"` + escapeIdent(schema) + `".` + `"` + escapeIdent(table) + `"`
}

// SetTablespace 设置目标表空间（生成 CREATE TABLE 时追加 TABLESPACE 子句，
// 索引不指定、跟随表所在表空间）
func (a *Base) SetTablespace(tablespace string) error {
	a.tablespace = tablespace
	return nil
}

func escapeIdent(name string) string {
	return strings.ReplaceAll(name, "\"", "\"\"")
}

// ReadDataByPhysicalRowID 基于 PostgreSQL ctid 的物理行游标分页。
// ctid 是 PostgreSQL 内置的物理行标识符（page号, 行号），即使无主键也可做 O(1) 游标分页。
// 返回的每行包含 _physrowid 列存储 ctid 字符串，编排器用它推进游标并在写入前移除。
func (a *Base) ReadDataByPhysicalRowID(ctx context.Context, tableName string, lastRowID any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("%s: 获取表结构失败: %w", a.brand(), err)
	}
	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return nil, fmt.Errorf("%s: 表 %s 无可读列", a.brand(), tableName)
	}

	// 构造 SELECT 列列表 + ctid AS _physrowid
	colList := quoteIdentifiers(cols)
	var query string
	var args []any
	if lastRowID != nil {
		// ctid 是复合类型 (page,offset)，比较用 tid > '(page,offset)'::tid
		query = fmt.Sprintf(`SELECT %s, ctid::text AS _physrowid FROM %s WHERE ctid > $1::tid ORDER BY ctid LIMIT $2`,
			colList, qualifyTable(tableName))
		args = append(args, lastRowID, limit)
	} else {
		query = fmt.Sprintf(`SELECT %s, ctid::text AS _physrowid FROM %s ORDER BY ctid LIMIT $1`,
			colList, qualifyTable(tableName))
		args = append(args, limit)
	}

	// scanRows 需要包含 _physrowid 列
	allCols := append(append([]string{}, cols...), "_physrowid")
	return a.scanRows(ctx, query, args, allCols)
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
