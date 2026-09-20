// Package db2 registers IBM Db2 adapter.
//
// IBM Db2 is an enterprise-grade relational database. Since there is no pure Go
// driver for Db2 (official go_ibm_db requires CGO + DB2 client libraries), this
// adapter uses the database/sql standard interface with driver name "go_ibm_db"
// loaded at runtime (users need to install the driver themselves).
// Type mapping uses the typeconv neutral type system.
package db2

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"
)

// Adapter Db2 database adapter
type Adapter struct {
	db         *sql.DB
	brand      string
	tablespace string // 目标表空间（CREATE TABLE ... IN "ts"）
}

// SetTablespace 设置目标表空间（orchestrator 在连接后调用一次，无并发竞争）
func (a *Adapter) SetTablespace(tablespace string) error {
	a.tablespace = tablespace
	return nil
}

func init() {
	types.RegisterAdapter(types.Db2, func() types.DatabaseAdapter {
		return &Adapter{brand: "IBM Db2"}
	})
}

func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 50000
	}
	dsn := fmt.Sprintf("DATABASE=%s;HOSTNAME=%s;PORT=%d;PROTOCOL=TCPIP;UID=%s;PWD=%s;",
		config.Database, config.Host, port, config.Username, config.Password)
	db, err := sql.Open("go_ibm_db", dsn)
	if err != nil {
		return fmt.Errorf("IBM Db2: open database failed: %w", err)
	}
	db.SetMaxOpenConns(10)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("IBM Db2: connect failed: %w", err)
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
	err := a.db.QueryRowContext(ctx, "SELECT SERVICE_LEVEL FROM TABLE(SYSPROC.ENV_GET_INST_INFO()) AS T").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("IBM Db2: get version failed: %w", err)
	}
	return "IBM Db2 " + version, nil
}

func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT TABNAME, REMARKS
		FROM SYSCAT.TABLES
		WHERE TABSCHEMA = CURRENT SCHEMA AND TYPE = 'T'
		ORDER BY TABNAME
	`)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: query tables failed: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name string
		var remarks sql.NullString
		if err := rows.Scan(&name, &remarks); err != nil {
			return nil, fmt.Errorf("IBM Db2: scan table info failed: %w", err)
		}
		tables = append(tables, types.TableMeta{Name: name, Comment: remarks.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("IBM Db2: iterate tables failed: %w", err)
	}
	return tables, nil
}

func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schemaName, table := types.SplitQualified(tableName)
	schema := types.TableSchema{Name: tableName}

	// schemaFilter：未指定 schema 时用当前模式，否则用指定模式（统一转大写比较）
	schemaFilter := "TABSCHEMA = CASE WHEN ? = '' THEN CURRENT SCHEMA ELSE UPPER(?) END"
	schemaArgs := []any{schemaName, schemaName}

	rows, err := a.db.QueryContext(ctx, `
		SELECT COLNAME, TYPENAME, LENGTH, SCALE, NULLS, DEFAULT, COLNO
		FROM SYSCAT.COLUMNS
		WHERE TABNAME = UPPER(?) AND `+schemaFilter+`
		ORDER BY COLNO
	`, append([]any{table}, schemaArgs...)...)
	if err != nil {
		return schema, fmt.Errorf("IBM Db2: query columns failed: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	for rows.Next() {
		var name, dataType, nullable string
		var length, scale sql.NullInt64
		var defVal sql.NullString
		var colNo sql.NullInt64
		if err := rows.Scan(&name, &dataType, &length, &scale, &nullable, &defVal, &colNo); err != nil {
			return schema, fmt.Errorf("IBM Db2: scan column failed: %w", err)
		}
		col := types.ColumnMeta{
			Name:     name,
			DataType: dataType,
			BaseType: strings.ToUpper(dataType),
			Nullable: nullable == "Y",
		}
		if length.Valid {
			l := int(length.Int64)
			col.Length = &l
		}
		if scale.Valid {
			s := int(scale.Int64)
			col.Scale = &s
		}
		if defVal.Valid && defVal.String != "" {
			val := strings.TrimSpace(defVal.String)
			col.DefaultValue = &val
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("IBM Db2: iterate columns failed: %w", err)
	}
	schema.Columns = columns

	// Primary key
	pkRows, err := a.db.QueryContext(ctx, `
		SELECT COLNAME
		FROM SYSCAT.KEYCOLUSE
		WHERE TABNAME = UPPER(?) AND `+schemaFilter+`
		ORDER BY COLSEQ
	`, append([]any{table}, schemaArgs...)...)
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
				Name: "PRIMARY", Columns: pkCols, IsUnique: true, IsPrimary: true,
			})
			for i := range schema.Columns {
				for _, pk := range pkCols {
					if strings.EqualFold(schema.Columns[i].Name, pk) {
						schema.Columns[i].IsPrimaryKey = true
					}
				}
			}
		}
	}

	return schema, nil
}

func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, qualifyTable(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("IBM Db2: get row count failed: %w", err)
	}
	return count, nil
}

func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	schemaName, table := types.SplitQualified(tableName)
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM SYSCAT.TABLES
		WHERE TABNAME = UPPER(?) AND TABSCHEMA = CASE WHEN ? = '' THEN CURRENT SCHEMA ELSE UPPER(?) END AND TYPE = 'T'
	`, table, schemaName, schemaName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("IBM Db2: check table exists failed: %w", err)
	}
	return count > 0, nil
}

func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	// 备份表名取裸表名（避免限定名中的点号混入标识符）；RENAME 后留在原 schema
	_, bareTable := types.SplitQualified(tableName)
	backupName := fmt.Sprintf("_bak_%s_%s", bareTable, time.Now().Format("20060102_150405"))
	// Db2 RENAME TABLE 目标侧不能带 schema（重命名后留在原 schema）
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`RENAME TABLE %s TO "%s"`, qualifyTable(tableName), escapeIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("IBM Db2: backup table failed: %w", err)
	}
	qualifiedBackup := tableName
	if schemaName, _ := types.SplitQualified(tableName); schemaName != "" {
		qualifiedBackup = schemaName + "." + backupName
	}
	return qualifiedBackup, nil
}

func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE %s`, qualifyTable(originalName)))
		if err != nil {
			return fmt.Errorf("IBM Db2: drop current table during restore failed: %w", err)
		}
	}
	// RENAME 目标侧用裸表名（留在原 schema）
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`RENAME TABLE %s TO "%s"`, qualifyTable(backupName), escapeIdent(qualifyBare(originalName))))
	if err != nil {
		return fmt.Errorf("IBM Db2: restore backup failed: %w", err)
	}
	return nil
}

func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE %s`, qualifyTable(backupName)))
	if err != nil {
		return fmt.Errorf("IBM Db2: drop backup table failed: %w", err)
	}
	return nil
}

func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`CREATE TABLE %s (
`, qualifyTable(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf(`"%s" `, escapeIdent(col.Name)))
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
			sb.WriteString(fmt.Sprintf(`"%s"`, escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")
	// 目标表空间（Db2: IN 子句，连接后由 SetTablespace 配置）
	if a.tablespace != "" {
		sb.WriteString(fmt.Sprintf(" IN \"%s\"", escapeIdent(a.tablespace)))
	}
	return sb.String(), nil
}

func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf(`DROP TABLE %s`, qualifyTable(tableName)), nil
}

func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: get schema failed: %w", err)
	}
	cols := schemaColumnNames(schema)
	// Db2 语法: OFFSET n ROWS FETCH NEXT m ROWS ONLY（OFFSET 在 FETCH 之前）
	query := fmt.Sprintf(`SELECT * FROM %s OFFSET %d ROWS FETCH FIRST %d ROWS ONLY`, qualifyTable(tableName), offset, limit)
	return a.scanRows(ctx, query, nil, cols)
}

func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: get schema failed: %w", err)
	}
	cols := schemaColumnNames(schema)
	query := fmt.Sprintf(`SELECT * FROM %s`, qualifyTable(tableName))
	var args []any
	if lastKey != nil {
		query += fmt.Sprintf(` WHERE "%s" > ?`, escapeIdent(keyColumn))
		args = append(args, lastKey)
	}
	query += fmt.Sprintf(` ORDER BY "%s" ASC FETCH FIRST %d ROWS ONLY`, escapeIdent(keyColumn), limit)
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
	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`,
		qualifyTable(tableName), quoteIdentifiers(columns), strings.Join(placeholders, ", "))

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("IBM Db2: begin tx failed: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("IBM Db2: prepare failed: %w", err)
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
			return fmt.Errorf("IBM Db2: write data failed: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("IBM Db2: commit tx failed: %w", err)
	}
	return nil
}

func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToDb2(typeconv.Normalize(col.BaseType), col)
}

func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT TRIGNAME, TRIGEVENT, TRIGTIME, TABNAME, TEXT
		FROM SYSCAT.TRIGGERS
		WHERE TABSCHEMA = CURRENT SCHEMA
		ORDER BY TRIGNAME
	`)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: query triggers failed: %w", err)
	}
	defer rows.Close()

	var triggers []types.TriggerMeta
	for rows.Next() {
		var name, event, timing, table string
		var body sql.NullString
		if err := rows.Scan(&name, &event, &timing, &table, &body); err != nil {
			return nil, fmt.Errorf("IBM Db2: scan trigger failed: %w", err)
		}
		triggers = append(triggers, types.TriggerMeta{
			Name: name, Event: event, Timing: timing, Table: table, Body: body.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("IBM Db2: iterate triggers failed: %w", err)
	}
	return triggers, nil
}

func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT ROUTINENAME, ROUTINETYPE
		FROM SYSCAT.ROUTINES
		WHERE ROUTINESCHEMA = CURRENT SCHEMA
		ORDER BY ROUTINENAME
	`)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: query routines failed: %w", err)
	}
	defer rows.Close()

	var routines []types.RoutineMeta
	for rows.Next() {
		var name, rType string
		if err := rows.Scan(&name, &rType); err != nil {
			return nil, fmt.Errorf("IBM Db2: scan routine failed: %w", err)
		}
		routines = append(routines, types.RoutineMeta{
			Name: name, Type: strings.ToLower(rType),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("IBM Db2: iterate routines failed: %w", err)
	}
	return routines, nil
}

func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.Db2:
		return trigger.Body, nil
	default:
		return "", fmt.Errorf("IBM Db2: trigger auto-conversion to %s not supported", targetDialect)
	}
}

func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.Db2:
		return routine.Body, nil
	default:
		return "", fmt.Errorf("IBM Db2: routine auto-conversion to %s not supported", targetDialect)
	}
}

func (a *Adapter) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: query data failed: %w", err)
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
			return nil, fmt.Errorf("IBM Db2: scan data failed: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("IBM Db2: iterate data failed: %w", err)
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
		quoted[i] = fmt.Sprintf(`"%s"`, escapeIdent(c))
	}
	return strings.Join(quoted, ", ")
}

// escapeIdent 转义 Db2 标识符中的双引号
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}

// ReadDataByPhysicalRowID 基于 Db2 RID() 的物理行游标分页。
// Db2 没有可直接查询的 ROWID 伪列，但 Db2 9.7+ 提供 RID(t) 函数，
// 返回 BIGINT 类型的物理行标识（即使无主键也可用）。
//
// 防回归：不能用 ROW_NUMBER() OVER (ORDER BY 常量) 造行号——常量排序
// 下行号分配由访问计划决定、跨批次不确定，第一批与第二批的行号可能
// 对应不同物理行，游标推进后造成重复/静默丢数据且误报迁移完成。
// RID() 返回 BIGINT，Go 侧 int64 直接回传比较参数，类型往返无损。
// 返回的每行包含 _physrowid 列，编排器用它推进游标并在写入前移除。
func (a *Adapter) ReadDataByPhysicalRowID(ctx context.Context, tableName string, lastRowID any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return nil, fmt.Errorf("IBM Db2: 表 %s 无可读列", tableName)
	}

	colList := quoteIdentifiers(cols)
	query := buildPhysicalRowIDQuery(colList, qualifyTable(tableName), lastRowID != nil)
	var args []any
	if lastRowID != nil {
		args = append(args, lastRowID, limit)
	} else {
		args = append(args, limit)
	}

	// 使用动态列扫描（查询返回 colList + "_physrowid"，别名加引号防 Db2 转大写）
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: query data failed: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("IBM Db2: get columns failed: %w", err)
	}

	var result []types.Row
	for rows.Next() {
		values := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("IBM Db2: scan data failed: %w", err)
		}
		row := make(types.Row)
		for i, col := range colNames {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("IBM Db2: iterate data failed: %w", err)
	}
	return result, nil
}

// buildPhysicalRowIDQuery 构建 Db2 物理行游标分页查询（纯函数，便于单测防回归）。
// RID(%s) 返回 BIGINT 物理行标识；WHERE 游标比较与 ORDER BY 使用同一
// 表达式，保证排序语义一致；_physrowid 别名加引号，
// 防止 Db2 将未加引号的别名转为大写导致编排器取不到游标值。
// escapedTable 由调用方传 qualifyTable 结果（格式串只留裸 %s 占位）。
func buildPhysicalRowIDQuery(colList, escapedTable string, hasLastRowID bool) string {
	rid := fmt.Sprintf(`RID(%s)`, escapedTable)
	if hasLastRowID {
		return fmt.Sprintf(
			`SELECT %s, %s AS "_physrowid" FROM %s WHERE %s > ? ORDER BY %s FETCH FIRST ? ROWS ONLY`,
			colList, rid, escapedTable, rid, rid)
	}
	return fmt.Sprintf(
		`SELECT %s, %s AS "_physrowid" FROM %s ORDER BY %s FETCH FIRST ? ROWS ONLY`,
		colList, rid, escapedTable, rid)
}

// qualifyTable 限定表名："s.t" → "s"."t"，裸表名 → "t"
func qualifyTable(name string) string {
	schema, table := types.SplitQualified(name)
	if schema == "" {
		return `"` + escapeIdent(table) + `"`
	}
	return `"` + escapeIdent(schema) + `"."` + escapeIdent(table) + `"`
}

// qualifyBare 取限定名的裸表部分（供 RENAME 目标侧使用）
func qualifyBare(name string) string {
	_, table := types.SplitQualified(name)
	return table
}
