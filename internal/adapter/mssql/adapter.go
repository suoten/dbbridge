// Package mssql 注册 Microsoft SQL Server 适配器。
//
// 支持 MSSQL 作为源库和目标库，实现与其他数据库（MySQL、PostgreSQL 等）的数据互迁。
// 同时支持触发器、存储过程的读取和转换。
package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	_ "github.com/microsoft/go-mssqldb"
)

// 确保实现了接口
var _ types.DatabaseAdapter = (*Adapter)(nil)

// Adapter MSSQL 适配器
type Adapter struct {
	db *sql.DB

	tablespace string // 目标文件组（可选，CREATE TABLE 时追加 ON 子句）
}

// SetTablespace 设置目标文件组（TablespaceAware 接口）
func (a *Adapter) SetTablespace(tablespace string) error {
	a.tablespace = tablespace
	return nil
}

// qualifyTable 生成方括号包裹的表名片段，支持 "schema.table" 限定名
// （Schema 映射场景由 orchestrator 传入；未配置映射时为普通表名）
func qualifyTable(name string) string {
	schema, table := types.SplitQualified(name)
	if schema == "" {
		return "[" + escapeIdent(table) + "]"
	}
	return "[" + escapeIdent(schema) + "].[" + escapeIdent(table) + "]"
}

// qualifyBare 取限定名的表名部分（不带 Schema 前缀，供 sp_rename @newname 使用）
func qualifyBare(name string) string {
	_, table := types.SplitQualified(name)
	return table
}

func init() {
	types.RegisterAdapter(types.MSSQL, func() types.DatabaseAdapter {
		return &Adapter{}
	})
}

// Connect 连接 MSSQL 数据库
//
// 使用 URL 形式连接串（sqlserver://user:pass@host:port?database=db），
// url.UserPassword 会自动编码密码中的特殊字符（; ' @ 等），
// 避免旧拼接式 DSN 被特殊字符密码破坏。
// 命名实例：用户可填 Instance 字段（如 SQLEXPRESS），此时不带端口，
// 由 go-mssqldb 通过 SQL Server Browser 服务解析实例端口。
func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("MSSQL: 未指定数据库名")
	}

	dsn := &url.URL{Scheme: "sqlserver"}
	if config.Instance != "" {
		// 命名实例：sqlserver://user:pass@host/INSTANCE
		dsn.Host = config.Host
		dsn.Path = "/" + config.Instance
	} else if config.Port > 0 {
		dsn.Host = fmt.Sprintf("%s:%d", config.Host, config.Port)
	} else {
		dsn.Host = config.Host
	}
	dsn.User = url.UserPassword(config.Username, config.Password)
	q := url.Values{}
	q.Set("database", config.Database)
	q.Set("encrypt", "disable")
	dsn.RawQuery = q.Encode()

	db, err := sql.Open("sqlserver", dsn.String())
	if err != nil {
		return fmt.Errorf("MSSQL: 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(10)

	// sql.Open 不实际连接，用 Ping 验证连接可用
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("MSSQL: 连接失败: %w", err)
	}

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

// GetVersion 获取 MSSQL 版本
func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("MSSQL: 获取版本失败: %w", err)
	}
	// 截取第一行（完整版本信息很长）
	if idx := strings.Index(version, "\n"); idx > 0 {
		version = strings.TrimSpace(version[:idx])
	}
	return "MSSQL " + version, nil
}

// GetTables 获取所有表
func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT t.TABLE_NAME, CAST(ep.value AS NVARCHAR(MAX))
		FROM INFORMATION_SCHEMA.TABLES t
		LEFT JOIN sys.tables st ON st.name = t.TABLE_NAME
		LEFT JOIN sys.extended_properties ep ON ep.major_id = st.object_id AND ep.minor_id = 0 AND ep.name = 'MS_Description'
		WHERE t.TABLE_TYPE = 'BASE TABLE'
		AND t.TABLE_SCHEMA = 'dbo'
		ORDER BY t.TABLE_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name string
		var comment sql.NullString
		if err := rows.Scan(&name, &comment); err != nil {
			return nil, fmt.Errorf("MSSQL: 读取表信息失败: %w", err)
		}
		tables = append(tables, types.TableMeta{
			Name:    name,
			Comment: comment.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("MSSQL: 遍历表列表失败: %w", err)
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{
		Name: tableName,
	}
	// 支持限定名：schema 感知查询。注意 OBJECT_ID/QUOTENAME 逐段拼接，
	// 不能用 'dbo.' + name（限定名下既查不到也错报 identity）
	schemaName, table := types.SplitQualified(tableName)

	// 获取表注释
	var tableComment sql.NullString
	err := a.db.QueryRowContext(ctx, `
		SELECT CAST(ep.value AS NVARCHAR(MAX))
		FROM sys.tables st
		JOIN sys.extended_properties ep ON ep.major_id = st.object_id AND ep.minor_id = 0 AND ep.name = 'MS_Description'
		WHERE st.name = @p2 AND SCHEMA_NAME(st.schema_id) = COALESCE(NULLIF(@p1, ''), 'dbo')
	`, schemaName, table).Scan(&tableComment)
	if err == nil && tableComment.Valid {
		schema.Comment = tableComment.String
	}

	// 获取列信息
	rows, err := a.db.QueryContext(ctx, `
		SELECT c.COLUMN_NAME, c.DATA_TYPE, c.IS_NULLABLE, c.COLUMN_DEFAULT,
		       c.CHARACTER_MAXIMUM_LENGTH, c.NUMERIC_PRECISION, c.NUMERIC_SCALE,
		       c.ORDINAL_POSITION,
		       COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'IsIdentity') as is_identity,
		       CAST(ep.value AS NVARCHAR(MAX)) as comment
		FROM INFORMATION_SCHEMA.COLUMNS c
		LEFT JOIN sys.columns sc ON sc.object_id = OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)) AND sc.name = c.COLUMN_NAME
		LEFT JOIN sys.extended_properties ep ON ep.major_id = sc.object_id AND ep.minor_id = sc.column_id AND ep.name = 'MS_Description'
		WHERE c.TABLE_SCHEMA = COALESCE(NULLIF(@p2, ''), 'dbo') AND c.TABLE_NAME = @p1
		ORDER BY c.ORDINAL_POSITION
	`, table, schemaName)
	if err != nil {
		return schema, fmt.Errorf("MSSQL: 查询列信息失败: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	var pkColumns []string

	for rows.Next() {
		var name, dataType, nullable string
		var colDefault sql.NullString
		var maxLen, numPrecision, numScale, ordinal, isIdentity sql.NullInt64
		var comment sql.NullString

		if err := rows.Scan(&name, &dataType, &nullable, &colDefault, &maxLen, &numPrecision, &numScale, &ordinal, &isIdentity, &comment); err != nil {
			return schema, fmt.Errorf("MSSQL: 读取列信息失败: %w", err)
		}

		col := types.ColumnMeta{
			Name:     name,
			DataType: dataType,
			BaseType: strings.ToUpper(dataType),
			Nullable: nullable == "YES",
			Comment:  comment.String,
		}

		// MSSQL 的 timestamp 类型是 rowversion（8 字节行版本二进制），
		// 与其他数据库的 TIMESTAMP 语义完全不同；不纠正会被映射成目标库的
		// 时间类型（MySQL TIMESTAMP/PG timestamp），写入必然失败。
		if col.BaseType == "TIMESTAMP" || col.BaseType == "ROWVERSION" {
			col.BaseType = "VARBINARY"
			col.DataType = "rowversion"
			l := 8
			col.Length = &l
		}

		if colDefault.Valid && colDefault.String != "" {
			val := colDefault.String
			// MSSQL 默认值通常以多层括号包裹，如 ((0))、(('pending'))、(((0)))
			// 反复剥离直到不再以 ( 开头、) 结尾
			val = strings.TrimSpace(val)
			for strings.HasPrefix(val, "(") && strings.HasSuffix(val, ")") {
				val = val[1 : len(val)-1]
				val = strings.TrimSpace(val)
			}
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
		if isIdentity.Valid && isIdentity.Int64 == 1 {
			col.AutoIncrement = true
		}

		columns = append(columns, col)
	}

	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("MSSQL: 遍历列信息失败: %w", err)
	}

	schema.Columns = columns

	// 获取主键
	pkRows, err := a.db.QueryContext(ctx, `
		SELECT kcu.COLUMN_NAME
		FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
		JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
			ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
			AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA
		WHERE tc.TABLE_SCHEMA = COALESCE(NULLIF(@p2, ''), 'dbo')
		AND tc.TABLE_NAME = @p1
		AND tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
		ORDER BY kcu.ORDINAL_POSITION
	`, table, schemaName)
	if err != nil {
		return schema, fmt.Errorf("MSSQL: 查询主键信息失败: %w", err)
	}
	for pkRows.Next() {
		var colName string
		if err := pkRows.Scan(&colName); err != nil {
			pkRows.Close()
			return schema, fmt.Errorf("MSSQL: 读取主键信息失败: %w", err)
		}
		pkColumns = append(pkColumns, colName)
	}
	pkRows.Close()

	if len(pkColumns) > 0 {
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:      "PRIMARY",
			Columns:   pkColumns,
			IsUnique:  true,
			IsPrimary: true,
		})
		for i := range schema.Columns {
			for _, pk := range pkColumns {
				if schema.Columns[i].Name == pk {
					schema.Columns[i].IsPrimaryKey = true
				}
			}
		}
	}

	// 获取索引
	idxRows, err := a.db.QueryContext(ctx, `
		SELECT i.name AS index_name,
		       i.is_unique,
		       STUFF((
		           SELECT ', ' + c.name
		           FROM sys.index_columns ic2
		           JOIN sys.columns c ON c.object_id = ic2.object_id AND c.column_id = ic2.column_id
		           WHERE ic2.object_id = i.object_id AND ic2.index_id = i.index_id
		           AND ic2.is_included_column = 0
		           ORDER BY ic2.key_ordinal
		           FOR XML PATH(''), TYPE).value('.', 'NVARCHAR(MAX)'), 1, 2, '') AS columns
		FROM sys.indexes i
		JOIN sys.tables t ON t.object_id = i.object_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE t.name = @p1 AND s.name = COALESCE(NULLIF(@p2, ''), 'dbo') AND i.type > 0 AND i.is_primary_key = 0
		ORDER BY i.name
	`, table, schemaName)
	if err != nil {
		return schema, fmt.Errorf("MSSQL: 查询索引信息失败: %w", err)
	}
	defer idxRows.Close()
	for idxRows.Next() {
		var idxName, colsStr string
		var isUnique bool
		if err := idxRows.Scan(&idxName, &isUnique, &colsStr); err != nil {
			return schema, fmt.Errorf("MSSQL: 读取索引信息失败: %w", err)
		}
		var cols []string
		for _, c := range strings.Split(colsStr, ", ") {
			c = strings.TrimSpace(c)
			if c != "" {
				cols = append(cols, c)
			}
		}
		schema.Indexes = append(schema.Indexes, types.IndexMeta{
			Name:     idxName,
			Columns:  cols,
			IsUnique: isUnique,
		})
	}

	// 获取外键
	fkRows, err := a.db.QueryContext(ctx, `
		SELECT fk.name AS constraint_name,
		       STUFF((
		           SELECT ', ' + c_parent.name
		           FROM sys.foreign_key_columns fkc
		           JOIN sys.columns c_parent ON c_parent.object_id = fkc.parent_object_id AND c_parent.column_id = fkc.parent_column_id
		           WHERE fkc.constraint_object_id = fk.object_id
		           ORDER BY fkc.constraint_column_id
		           FOR XML PATH(''), TYPE).value('.', 'NVARCHAR(MAX)'), 1, 2, '') AS columns,
		       rt.name AS ref_table,
		       STUFF((
		           SELECT ', ' + c_ref.name
		           FROM sys.foreign_key_columns fkc
		           JOIN sys.columns c_ref ON c_ref.object_id = fkc.referenced_object_id AND c_ref.column_id = fkc.referenced_column_id
		           WHERE fkc.constraint_object_id = fk.object_id
		           ORDER BY fkc.constraint_column_id
		           FOR XML PATH(''), TYPE).value('.', 'NVARCHAR(MAX)'), 1, 2, '') AS ref_columns,
		       fk.delete_referential_action_desc,
		       fk.update_referential_action_desc
		FROM sys.foreign_keys fk
		JOIN sys.tables t ON t.object_id = fk.parent_object_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		JOIN sys.tables rt ON rt.object_id = fk.referenced_object_id
		WHERE t.name = @p1 AND s.name = COALESCE(NULLIF(@p2, ''), 'dbo')
		ORDER BY fk.name
	`, table, schemaName)
	if err != nil {
		return schema, fmt.Errorf("MSSQL: 查询外键信息失败: %w", err)
	}
	defer fkRows.Close()
	for fkRows.Next() {
		var constraintName, colsStr, refTable, refColsStr, onDelete, onUpdate string
		if err := fkRows.Scan(&constraintName, &colsStr, &refTable, &refColsStr, &onDelete, &onUpdate); err != nil {
			return schema, fmt.Errorf("MSSQL: 读取外键信息失败: %w", err)
		}
		fk := types.ForeignKeyMeta{
			Name:     constraintName,
			RefTable: refTable,
			OnDelete: strings.ToLower(strings.TrimSpace(onDelete)),
			OnUpdate: strings.ToLower(strings.TrimSpace(onUpdate)),
		}
		for _, c := range strings.Split(colsStr, ", ") {
			c = strings.TrimSpace(c)
			if c != "" {
				fk.Columns = append(fk.Columns, c)
			}
		}
		for _, c := range strings.Split(refColsStr, ", ") {
			c = strings.TrimSpace(c)
			if c != "" {
				fk.RefColumns = append(fk.RefColumns, c)
			}
		}
		schema.ForeignKeys = append(schema.ForeignKeys, fk)
	}

	// 获取 CHECK 约束
	checkRows, err := a.db.QueryContext(ctx, `
		SELECT cc.name, cc.definition
		FROM sys.check_constraints cc
		JOIN sys.tables t ON t.object_id = cc.parent_object_id
		WHERE t.name = @p1
		ORDER BY cc.name
	`, tableName)
	if err == nil {
		for checkRows.Next() {
			var name string
			var definition sql.NullString
			if err := checkRows.Scan(&name, &definition); err == nil && definition.Valid {
				schema.Checks = append(schema.Checks, types.CheckMeta{
					Name:       name,
					Definition: definition.String,
				})
			}
		}
		checkRows.Close()
	}

	// 获取表级触发器
	trigRows, err := a.db.QueryContext(ctx, `
		SELECT tr.name, tr.type_desc,
		       OBJECTPROPERTY(tr.object_id, 'ExecIsAfterTrigger'),
		       OBJECTPROPERTY(tr.object_id, 'ExecIsInsteadOfTrigger'),
		       OBJECTPROPERTY(tr.object_id, 'ExecIsInsertTrigger'),
		       OBJECTPROPERTY(tr.object_id, 'ExecIsUpdateTrigger'),
		       OBJECTPROPERTY(tr.object_id, 'ExecIsDeleteTrigger'),
		       OBJECT_DEFINITION(tr.object_id)
		FROM sys.triggers tr
		JOIN sys.tables t ON t.object_id = tr.parent_id
		WHERE t.name = @p1
		ORDER BY tr.name
	`, tableName)
	if err == nil {
		for trigRows.Next() {
			var name, typeDesc string
			var isAfter, isInstead, isInsert, isUpdate, isDelete int
			var body sql.NullString
			if err := trigRows.Scan(&name, &typeDesc, &isAfter, &isInstead, &isInsert, &isUpdate, &isDelete, &body); err == nil {
				timing := "AFTER"
				if isInstead == 1 {
					timing = "INSTEAD OF"
				}
				event := ""
				if isInsert == 1 {
					event = "INSERT"
				}
				if isUpdate == 1 {
					if event != "" {
						event += ", "
					}
					event += "UPDATE"
				}
				if isDelete == 1 {
					if event != "" {
						event += ", "
					}
					event += "DELETE"
				}
				trigger := types.TriggerMeta{
					Name:   name,
					Event:  event,
					Timing: timing,
					Table:  tableName,
				}
				if body.Valid {
					trigger.Body = body.String
				}
				schema.Triggers = append(schema.Triggers, trigger)
			}
		}
		trigRows.Close()
	}

	return schema, nil
}

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s", qualifyTable(tableName)),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("MSSQL: 获取行数失败: %w", err)
	}
	return count, nil
}

// TableExists 检查表是否存在（支持 "schema.table" 限定名；未配置时查 dbo）
func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	schemaName, table := types.SplitQualified(tableName)
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = COALESCE(NULLIF(@p1, ''), 'dbo') AND TABLE_NAME = @p2
	`, schemaName, table).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("MSSQL: 检查表存在失败: %w", err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名（支持 "schema.table" 限定名，
// sp_rename 的 @newname 不能带 Schema，重命名后留在原 Schema，返回限定名）
func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	schemaName, table := types.SplitQualified(tableName)
	backupName := fmt.Sprintf("_bak_%s_%s", table, time.Now().Format("20060102_150405"))
	qualifiedBackup := backupName
	if schemaName != "" {
		qualifiedBackup = schemaName + "." + backupName
	}
	// sp_rename 参数是字符串字面量，内层用方括号包裹标识符（escapeIdent 已双写 ]）。
	// 注意必须用 bracketQualified 逐段限定：'[ods].[orders]' 是 schema 限定，
	// 而 '[ods.orders]' 是带点号的单个标识符，会静默找不到对象或改错对象。
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("EXEC sp_rename '%s', '[%s]'", bracketQualified(tableName), escapeIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("MSSQL: 备份表失败 (%s→%s): %w", tableName, qualifiedBackup, err)
	}
	return qualifiedBackup, nil
}

// RestoreFromBackup 从备份表恢复
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx,
			fmt.Sprintf("DROP TABLE %s", qualifyTable(originalName)))
		if err != nil {
			return fmt.Errorf("MSSQL: 恢复备份时删除当前表失败: %w", err)
		}
	}
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("EXEC sp_rename '%s', '[%s]'", bracketQualified(backupName), escapeIdent(qualifyBare(originalName))))
	if err != nil {
		return fmt.Errorf("MSSQL: 恢复备份失败 (%s→%s): %w", backupName, originalName, err)
	}
	return nil
}

// DropBackup 删除备份表（qualifyTable 逐段限定，兼容带 Schema 的备份表名）
func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifyTable(backupName)))
	if err != nil {
		return fmt.Errorf("MSSQL: 删除备份表失败: %w", err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", qualifyTable(table.Name)))

	// 收集索引列
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
		sb.WriteString(fmt.Sprintf("[%s] ", escapeIdent(col.Name)))
		colType := a.MapType(col)
		sb.WriteString(colType)

		if col.AutoIncrement {
			sb.WriteString(" IDENTITY(1,1)")
		}

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
		sb.WriteString(",\n  CONSTRAINT [PK_")
		sb.WriteString(escapeIdent(qualifyBare(table.Name)))
		sb.WriteString("] PRIMARY KEY (")
		for i, c := range pkCols {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("[%s]", escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	// 外键
	for _, fk := range table.ForeignKeys {
		sb.WriteString(",\n")
		fkName := fk.Name
		if fkName == "" {
			fkName = fmt.Sprintf("FK_%s_%s", table.Name, fk.RefTable)
		}
		sb.WriteString(fmt.Sprintf("  CONSTRAINT [%s] FOREIGN KEY (", escapeIdent(fkName)))
		for i, c := range fk.Columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("[%s]", escapeIdent(c)))
		}
		sb.WriteString(fmt.Sprintf(") REFERENCES %s (", qualifyTable(fk.RefTable)))
		for i, c := range fk.RefColumns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("[%s]", escapeIdent(c)))
		}
		sb.WriteString(")")
		if fk.OnDelete != "" && !strings.EqualFold(fk.OnDelete, "no_action") {
			sb.WriteString(" ON DELETE " + strings.ToUpper(fk.OnDelete))
		}
		if fk.OnUpdate != "" && !strings.EqualFold(fk.OnUpdate, "no_action") {
			sb.WriteString(" ON UPDATE " + strings.ToUpper(fk.OnUpdate))
		}
	}

	// CHECK 约束
	for _, ck := range table.Checks {
		sb.WriteString(",\n")
		sb.WriteString(fmt.Sprintf("  CONSTRAINT [%s] CHECK %s", escapeIdent(ck.Name), ck.Definition))
	}

	sb.WriteString("\n)")

	// 目标文件组（可选；未配置时使用默认文件组）
	if a.tablespace != "" {
		sb.WriteString(fmt.Sprintf(" ON [%s]", escapeIdent(a.tablespace)))
	}

	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifyTable(tableName)), nil
}

// ReadData 按偏移量分页读取数据
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	// MSSQL 使用 OFFSET ... FETCH NEXT 分页
	query := fmt.Sprintf("SELECT * FROM %s ORDER BY (SELECT NULL) OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
		qualifyTable(tableName), offset, limit)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	query := fmt.Sprintf("SELECT * FROM %s", qualifyTable(tableName))
	args := []any{}
	if lastKey != nil {
		query += fmt.Sprintf(" WHERE [%s] > @p1", escapeIdent(keyColumn))
		args = append(args, lastKey)
	}
	query += fmt.Sprintf(" ORDER BY [%s] ASC OFFSET 0 ROWS FETCH NEXT %d ROWS ONLY", escapeIdent(keyColumn), limit)
	return a.scanRows(ctx, query, args, cols)
}

// WriteData 批量写入数据
func (a *Adapter) WriteData(ctx context.Context, tableName string, columns []string, rows []types.Row) error {
	if len(rows) == 0 {
		return nil
	}

	// 构建参数化 INSERT
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = fmt.Sprintf("@p%d", i+1)
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		qualifyTable(tableName),
		quoteIdentifiers(columns),
		strings.Join(placeholders, ", "),
	)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("MSSQL: 开启事务失败: %w", err)
	}

	// 迁移会显式插入 IDENTITY 列的值，默认 OFF 会直接报错
	// （Error 544: Cannot insert explicit value for identity column...）。
	// SET IDENTITY_INSERT 是会话级设置，必须与 INSERT 在同一事务（同一连接）内执行；
	// 且同一会话同时只能对一张表开启，结束后必须在同一连接上关闭，
	// 否则连接归还池后会污染后续其他表的写入。
	identityOn := false
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET IDENTITY_INSERT %s ON", qualifyTable(tableName))); err == nil {
		identityOn = true
	}
	// turnOff 在同一连接上关闭 IDENTITY_INSERT；必须在 Commit/Rollback 之前调用
	// （事务完成后 tx.Exec 会返回 ErrTxDone 不再执行）
	turnOffIdentity := func() {
		if identityOn {
			// 用独立 ctx 避免 ctx 已取消时漏关
			_, _ = tx.ExecContext(context.Background(), fmt.Sprintf("SET IDENTITY_INSERT %s OFF", qualifyTable(tableName)))
		}
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		turnOffIdentity()
		tx.Rollback()
		return fmt.Errorf("MSSQL: 预处理失败: %w", err)
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
			turnOffIdentity()
			tx.Rollback()
			return fmt.Errorf("MSSQL: 写入数据失败: %w", err)
		}
	}

	turnOffIdentity()
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("MSSQL: 提交事务失败: %w", err)
	}
	return nil
}

// ExecContext 执行原始 SQL 语句
func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

// MapType 将源数据库的列类型映射为 MSSQL 类型
func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToMSSQL(typeconv.Normalize(col.BaseType), col)
}

// GetTriggers 获取 MSSQL 所有触发器定义
func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT tr.name,
		       t.name AS table_name,
		       OBJECTPROPERTY(tr.object_id, 'ExecIsAfterTrigger') AS is_after,
		       OBJECTPROPERTY(tr.object_id, 'ExecIsInsteadOfTrigger') AS is_instead,
		       OBJECTPROPERTY(tr.object_id, 'ExecIsInsertTrigger') AS is_insert,
		       OBJECTPROPERTY(tr.object_id, 'ExecIsUpdateTrigger') AS is_update,
		       OBJECTPROPERTY(tr.object_id, 'ExecIsDeleteTrigger') AS is_delete,
		       OBJECT_DEFINITION(tr.object_id) AS body
		FROM sys.triggers tr
		JOIN sys.tables t ON t.object_id = tr.parent_id
		ORDER BY tr.name
	`)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 查询触发器失败: %w", err)
	}
	defer rows.Close()

	var triggers []types.TriggerMeta
	for rows.Next() {
		var name, tableName string
		var isAfter, isInstead, isInsert, isUpdate, isDelete int
		var body sql.NullString
		if err := rows.Scan(&name, &tableName, &isAfter, &isInstead, &isInsert, &isUpdate, &isDelete, &body); err != nil {
			return nil, fmt.Errorf("MSSQL: 读取触发器失败: %w", err)
		}

		timing := "AFTER"
		if isInstead == 1 {
			timing = "INSTEAD OF"
		}

		event := ""
		if isInsert == 1 {
			event = "INSERT"
		}
		if isUpdate == 1 {
			if event != "" {
				event += ", "
			}
			event += "UPDATE"
		}
		if isDelete == 1 {
			if event != "" {
				event += ", "
			}
			event += "DELETE"
		}

		trigger := types.TriggerMeta{
			Name:       name,
			Event:      event,
			Timing:     timing,
			Table:      tableName,
			ForEachRow: true, // MSSQL 触发器默认行级
		}
		if body.Valid {
			trigger.Body = body.String
		}
		triggers = append(triggers, trigger)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("MSSQL: 遍历触发器失败: %w", err)
	}
	return triggers, nil
}

// GetRoutines 获取 MSSQL 所有存储过程和函数定义
func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT o.name,
		       CASE o.type WHEN 'P' THEN 'procedure' WHEN 'FN' THEN 'function' WHEN 'IF' THEN 'function' WHEN 'TF' THEN 'function' END AS type,
		       OBJECT_DEFINITION(o.object_id) AS body
		FROM sys.objects o
		WHERE o.type IN ('P', 'FN', 'IF', 'TF')
		AND o.is_ms_shipped = 0
		ORDER BY o.name
	`)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 查询存储过程失败: %w", err)
	}
	defer rows.Close()

	var routines []types.RoutineMeta
	for rows.Next() {
		var name, rType string
		var body sql.NullString
		if err := rows.Scan(&name, &rType, &body); err != nil {
			return nil, fmt.Errorf("MSSQL: 读取存储过程失败: %w", err)
		}
		routine := types.RoutineMeta{
			Name: name,
			Type: rType,
		}
		if body.Valid {
			routine.Body = body.String
		}
		routines = append(routines, routine)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("MSSQL: 遍历存储过程失败: %w", err)
	}
	return routines, nil
}

// GenerateTriggerDDL 将 MSSQL 触发器转换为目标方言的 DDL
func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora, types.Dameng:
		// MSSQL → MySQL 触发器转换
		return convertMSSQLTriggerToMySQL(trigger), nil
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB:
		return convertMSSQLTriggerToPG(trigger), nil
	case types.SQLite:
		return convertMSSQLTriggerToSQLite(trigger), nil
	default:
		return "", fmt.Errorf("MSSQL: 不支持的目标方言 %s", targetDialect)
	}
}

// GenerateRoutineDDL 将 MSSQL 存储过程/函数转换为目标方言的 DDL
func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora, types.Dameng:
		return convertMSSQLRoutineToMySQL(routine), nil
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB:
		return convertMSSQLRoutineToPG(routine), nil
	default:
		return "", fmt.Errorf("MSSQL: 不支持的目标方言 %s", targetDialect)
	}
}

// scanRows 执行查询并按列名映射为 Row
func (a *Adapter) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 查询数据失败: %w", err)
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
			return nil, fmt.Errorf("MSSQL: 读取数据失败: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			// MSSQL 驱动返回 []byte 的场景统一转为 string
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("MSSQL: 遍历数据失败: %w", err)
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

// GenerateAddForeignKeyDDL 生成 "ALTER TABLE ... ADD CONSTRAINT ..."（外键延后添加用）
func (a *Adapter) GenerateAddForeignKeyDDL(tableName string, fk types.ForeignKeyMeta) (string, error) {
	fkName := fk.Name
	if fkName == "" {
		fkName = fmt.Sprintf("FK_%s_%s", tableName, fk.RefTable)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT [%s] FOREIGN KEY (",
		qualifyTable(tableName), escapeIdent(fkName)))
	for i, c := range fk.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("[%s]", escapeIdent(c)))
	}
	sb.WriteString(fmt.Sprintf(") REFERENCES [%s] (", escapeIdent(fk.RefTable)))
	for i, c := range fk.RefColumns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("[%s]", escapeIdent(c)))
	}
	sb.WriteString(")")
	if fk.OnDelete != "" && !strings.EqualFold(fk.OnDelete, "no_action") {
		sb.WriteString(" ON DELETE " + strings.ToUpper(fk.OnDelete))
	}
	if fk.OnUpdate != "" && !strings.EqualFold(fk.OnUpdate, "no_action") {
		sb.WriteString(" ON UPDATE " + strings.ToUpper(fk.OnUpdate))
	}
	return sb.String(), nil
}

// FixAutoIncrementSequences 将 IDENTITY 种子重置到当前最大值。
//
// 显式插入 IDENTITY 列的值不会推进种子，不修复的话目标库下一条自动
// INSERT 会与已迁入数据主键冲突（Error 2627）。
func (a *Adapter) FixAutoIncrementSequences(ctx context.Context, tableName string, columns []types.ColumnMeta) error {
	hasIdentity := false
	for _, c := range columns {
		if c.AutoIncrement {
			hasIdentity = true
			break
		}
	}
	if !hasIdentity {
		return nil
	}
	// RESEED 不带新值时：表非空则种子重置为该列当前最大值，空表重置为初始值
	_, err := a.db.ExecContext(ctx,
		fmt.Sprintf("DBCC CHECKIDENT ('%s', RESEED)", bracketQualified(tableName)))
	if err != nil {
		return fmt.Errorf("MSSQL: 重置 IDENTITY 种子失败: %w", err)
	}
	return nil
}

// escapeIdent 转义 MSSQL 标识符（方括号内右方括号双写）
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, "]", "]]")
}

// mssqlPhysLoc MSSQL 物理行定位符。
// 注意：该伪列的字面写法就是双百分号 %%physloc%%，若放在 fmt.Sprintf
// 的格式串里会被 %% 转义吃掉一半变成单百分号（SQL Server 报 syntax error 102）。
// 因此必须通过 %s 参数传入，绝不能直接写在格式串里。
const mssqlPhysLoc = "%%physloc%%"

// ReadDataByPhysicalRowID 基于 MSSQL %%physloc%% 的物理行游标分页。
// %%physloc%% 是 MSSQL 内置的物理行定位符（等价于 Oracle ROWID / PG ctid），
// 返回 varbinary(8)，转为 bigint 做比较。即使无主键也可做 O(1) 游标分页。
// 返回的每行包含 _physrowid 列，编排器用它推进游标并在写入前移除。
func (a *Adapter) ReadDataByPhysicalRowID(ctx context.Context, tableName string, lastRowID any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("MSSQL: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return nil, fmt.Errorf("MSSQL: 表 %s 无可读列", tableName)
	}

	colList := quoteIdentifiers(cols)
	query := buildPhysicalRowIDQuery(colList, qualifyTable(tableName), lastRowID != nil)
	var args []any
	if lastRowID != nil {
		args = append(args, lastRowID, limit)
	} else {
		args = append(args, limit)
	}

	allCols := append(append([]string{}, cols...), "_physrowid")
	return a.scanRows(ctx, query, args, allCols)
}

// quoteIdentifiers 给列名加方括号
func quoteIdentifiers(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("[%s]", escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}

// buildPhysicalRowIDQuery 构建物理行游标分页查询（纯函数，便于单测防回归）。
// 首批（无 lastRowID）用 @p1 占位 limit，续批用 @p1 比较 @p2 占位 limit。
//
// 关键约束：WHERE 游标比较与 ORDER BY 必须使用同一个表达式
// CONVERT(bigint, %%physloc%%)。
// 曾经的 Bug：ORDER BY 用原始 %%physloc%%（varbinary 按无符号二进制排序），
// 而 WHERE 用 CONVERT(bigint,...)（有符号解释）。高位为 1 的 physloc
// 转换后是负数，在二进制排序中却排在最后：续批 WHERE bigint > 负数游标
// 会重新捞出前面已迁移的正数行、跳过后面的负数行，造成重复+静默丢数据
// 且程序误报迁移完成。两个表达式排序语义一致后游标才能正确推进。
// bracketQualified 生成 "[schema].[table]" 字符串字面量（供 DBCC 等字符串参数使用；
// 未配置 Schema 时默认 dbo，与既往行为一致）
func bracketQualified(name string) string {
	schema, table := types.SplitQualified(name)
	if schema == "" {
		schema = "dbo"
	}
	return "[" + escapeIdent(schema) + "].[" + escapeIdent(table) + "]"
}

func buildPhysicalRowIDQuery(colList, escapedTable string, hasLastRowID bool) string {
	if hasLastRowID {
		return fmt.Sprintf(
			"SELECT %s, CONVERT(bigint, %s) AS _physrowid FROM %s WHERE CONVERT(bigint, %s) > @p1 ORDER BY CONVERT(bigint, %s) OFFSET 0 ROWS FETCH NEXT @p2 ROWS ONLY",
			colList, mssqlPhysLoc, escapedTable, mssqlPhysLoc, mssqlPhysLoc)
	}
	return fmt.Sprintf(
		"SELECT %s, CONVERT(bigint, %s) AS _physrowid FROM %s ORDER BY CONVERT(bigint, %s) OFFSET 0 ROWS FETCH NEXT @p1 ROWS ONLY",
		colList, mssqlPhysLoc, escapedTable, mssqlPhysLoc)
}
