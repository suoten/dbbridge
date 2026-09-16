// Package oracle 注册 Oracle 数据库适配器。
//
// Oracle 是全球顶级商业关系型数据库，本适配器基于纯 Go 的 go-ora 驱动（无 CGO 依赖）。
// 类型映射使用 typeconv 中立类型体系，支持与其他数据库互转。
package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	_ "github.com/sijms/go-ora/v2"
)

// Adapter Oracle 数据库适配器
type Adapter struct {
	db    *sql.DB
	brand string
}

func init() {
	types.RegisterAdapter(types.Oracle, func() types.DatabaseAdapter {
		return &Adapter{brand: "Oracle"}
	})
}

// Connect 连接 Oracle 数据库
func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	port := config.Port
	if port == 0 {
		port = 1521
	}
	// go-ora DSN 格式: oracle://user:pass@host:port/service_name
	dsn := fmt.Sprintf("oracle://%s:%s@%s:%d/%s",
		config.Username, config.Password, config.Host, port, config.Database)
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return fmt.Errorf("Oracle: 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(10)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("Oracle: 连接失败: %w", err)
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

// GetVersion 获取数据库版本
func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var version string
	err := a.db.QueryRowContext(ctx, "SELECT BANNER FROM v$version WHERE ROWNUM = 1").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("Oracle: 获取版本失败: %w", err)
	}
	return "Oracle " + version, nil
}

// GetTables 获取所有表（当前用户 schema）
func (a *Adapter) GetTables(ctx context.Context) ([]types.TableMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT t.TABLE_NAME, c.COMMENTS
		FROM USER_TABLES t
		LEFT JOIN USER_TAB_COMMENTS c ON t.TABLE_NAME = c.TABLE_NAME
		ORDER BY t.TABLE_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 查询表列表失败: %w", err)
	}
	defer rows.Close()

	var tables []types.TableMeta
	for rows.Next() {
		var name string
		var comments sql.NullString
		if err := rows.Scan(&name, &comments); err != nil {
			return nil, fmt.Errorf("Oracle: 读取表信息失败: %w", err)
		}
		tables = append(tables, types.TableMeta{Name: name, Comment: comments.String})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Oracle: 遍历表列表失败: %w", err)
	}
	return tables, nil
}

// GetTableSchema 获取表结构
func (a *Adapter) GetTableSchema(ctx context.Context, tableName string) (types.TableSchema, error) {
	schema := types.TableSchema{Name: tableName}

	// 获取列信息
	rows, err := a.db.QueryContext(ctx, `
		SELECT COLUMN_NAME, DATA_TYPE, DATA_LENGTH, DATA_PRECISION, DATA_SCALE,
		       NULLABLE, DATA_DEFAULT, COLUMN_ID
		FROM USER_TAB_COLUMNS
		WHERE TABLE_NAME = UPPER(:1)
		ORDER BY COLUMN_ID
	`, tableName)
	if err != nil {
		return schema, fmt.Errorf("Oracle: 查询列信息失败: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	for rows.Next() {
		var name, dataType, nullable string
		var dataLen, dataPrec, dataScale, colID sql.NullInt64
		var defVal sql.NullString
		if err := rows.Scan(&name, &dataType, &dataLen, &dataPrec, &dataScale, &nullable, &defVal, &colID); err != nil {
			return schema, fmt.Errorf("Oracle: 读取列信息失败: %w", err)
		}
		col := types.ColumnMeta{
			Name:     name,
			DataType: dataType,
			BaseType: strings.ToUpper(dataType),
			Nullable: nullable == "Y",
		}
		if dataLen.Valid {
			l := int(dataLen.Int64)
			col.Length = &l
		}
		if dataPrec.Valid {
			p := int(dataPrec.Int64)
			col.Precision = &p
		}
		if dataScale.Valid {
			s := int(dataScale.Int64)
			col.Scale = &s
		}
		if defVal.Valid && defVal.String != "" {
			val := strings.TrimSpace(defVal.String)
			col.DefaultValue = &val
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		return schema, fmt.Errorf("Oracle: 遍历列信息失败: %w", err)
	}
	schema.Columns = columns

	// 获取主键
	pkRows, err := a.db.QueryContext(ctx, `
		SELECT cc.COLUMN_NAME
		FROM USER_CONSTRAINTS c
		JOIN USER_CONS_COLUMNS cc ON c.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
		WHERE c.TABLE_NAME = UPPER(:1) AND c.CONSTRAINT_TYPE = 'P'
		ORDER BY cc.POSITION
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

// GetRowCount 获取表的行数
func (a *Adapter) GetRowCount(ctx context.Context, tableName string) (int64, error) {
	var count int64
	err := a.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, escapeIdent(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("Oracle: 获取行数失败: %w", err)
	}
	return count, nil
}

// TableExists 检查表是否存在
func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM USER_TABLES WHERE TABLE_NAME = UPPER(:1)
	`, tableName).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("Oracle: 检查表存在失败: %w", err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名
func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	backupName := fmt.Sprintf("_bak_%s_%s", tableName, time.Now().Format("20060102_150405"))
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`RENAME "%s" TO "%s"`, escapeIdent(tableName), escapeIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("Oracle: 备份表失败: %w", err)
	}
	return backupName, nil
}

// RestoreFromBackup 从备份表恢复
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE "%s" PURGE`, escapeIdent(originalName)))
		if err != nil {
			return fmt.Errorf("Oracle: 恢复备份时删除当前表失败: %w", err)
		}
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`RENAME "%s" TO "%s"`, escapeIdent(backupName), escapeIdent(originalName)))
	if err != nil {
		return fmt.Errorf("Oracle: 恢复备份失败: %w", err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE "%s" PURGE`, escapeIdent(backupName)))
	if err != nil {
		return fmt.Errorf("Oracle: 删除备份表失败: %w", err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`CREATE TABLE "%s" (
`, escapeIdent(table.Name)))

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
			sb.WriteString(fmt.Sprintf(`"%s"`, escapeIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")
	return sb.String(), nil
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf(`DROP TABLE "%s" PURGE`, escapeIdent(tableName)), nil
}

// ReadData 按偏移量分页读取数据
// Oracle 11g 不支持 OFFSET/FETCH，使用 ROWNUM 子查询分页。
// 三层嵌套：最内层取数据 + ROWNUM，中间层限制上限，最外层过滤偏移并去掉 rn 列。
// 最外层显式列出列名（不含 rn），确保 rows.Scan 列数与 schema 一致。
func (a *Adapter) ReadData(ctx context.Context, tableName string, offset, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return nil, fmt.Errorf("Oracle: 表 %s 无可读列", tableName)
	}
	// 构造最外层 SELECT 的列列表（显式列出，排除 rn）
	outerCols := make([]string, len(cols))
	for i, c := range cols {
		outerCols[i] = fmt.Sprintf(`"%s"`, escapeIdent(c))
	}
	query := fmt.Sprintf(`SELECT %s FROM (
		SELECT t.*, ROWNUM rn FROM (
			SELECT * FROM "%s" ORDER BY 1
		) t WHERE ROWNUM <= %d
	) WHERE rn > %d`,
		strings.Join(outerCols, ", "), escapeIdent(tableName), offset+limit, offset)
	return a.scanRows(ctx, query, nil, cols)
}

// ReadDataKeyset 基于单列主键的游标分页读取
// Oracle 11g 不支持 FETCH FIRST，使用 ROWNUM 限制行数。
func (a *Adapter) ReadDataKeyset(ctx context.Context, tableName, keyColumn string, lastKey any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	// 使用 ROWNUM 替代 FETCH FIRST（兼容 11g）
	var query string
	var args []any
	if lastKey != nil {
		query = fmt.Sprintf(`SELECT * FROM (
			SELECT * FROM "%s" WHERE "%s" > :1 ORDER BY "%s" ASC
		) WHERE ROWNUM <= %d`, escapeIdent(tableName), escapeIdent(keyColumn), escapeIdent(keyColumn), limit)
		args = append(args, lastKey)
	} else {
		query = fmt.Sprintf(`SELECT * FROM (
			SELECT * FROM "%s" ORDER BY "%s" ASC
		) WHERE ROWNUM <= %d`, escapeIdent(tableName), escapeIdent(keyColumn), limit)
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
		placeholders[i] = fmt.Sprintf(":%d", i+1)
	}
	query := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`,
		escapeIdent(tableName), quoteIdentifiers(columns), strings.Join(placeholders, ", "))

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("Oracle: 开启事务失败: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("Oracle: 预处理失败: %w", err)
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
			return fmt.Errorf("Oracle: 写入数据失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("Oracle: 提交事务失败: %w", err)
	}
	return nil
}

// ExecContext 执行原始 SQL
func (a *Adapter) ExecContext(ctx context.Context, sqlText string) error {
	_, err := a.db.ExecContext(ctx, sqlText)
	return err
}

// MapType 将列类型映射为 Oracle 类型
func (a *Adapter) MapType(col types.ColumnMeta) string {
	return typeconv.ToOracle(typeconv.Normalize(col.BaseType), col)
}

// GetTriggers 获取触发器定义
func (a *Adapter) GetTriggers(ctx context.Context) ([]types.TriggerMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT TRIGGER_NAME, TRIGGERING_EVENT, TRIGGER_TYPE, TABLE_NAME, TRIGGER_BODY
		FROM USER_TRIGGERS
		ORDER BY TRIGGER_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 查询触发器失败: %w", err)
	}
	defer rows.Close()

	var triggers []types.TriggerMeta
	for rows.Next() {
		var name, event, timing, table string
		var body sql.NullString
		if err := rows.Scan(&name, &event, &timing, &table, &body); err != nil {
			return nil, fmt.Errorf("Oracle: 读取触发器失败: %w", err)
		}
		triggers = append(triggers, types.TriggerMeta{
			Name: name, Event: event, Timing: timing, Table: table, Body: body.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Oracle: 遍历触发器失败: %w", err)
	}
	return triggers, nil
}

// GetRoutines 获取存储过程和函数定义
func (a *Adapter) GetRoutines(ctx context.Context) ([]types.RoutineMeta, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT OBJECT_NAME, OBJECT_TYPE
		FROM USER_OBJECTS
		WHERE OBJECT_TYPE IN ('PROCEDURE', 'FUNCTION')
		ORDER BY OBJECT_NAME
	`)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 查询存储过程失败: %w", err)
	}
	defer rows.Close()

	var routines []types.RoutineMeta
	for rows.Next() {
		var name, objType string
		if err := rows.Scan(&name, &objType); err != nil {
			return nil, fmt.Errorf("Oracle: 读取存储过程失败: %w", err)
		}
		routines = append(routines, types.RoutineMeta{
			Name: name, Type: strings.ToLower(objType),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Oracle: 遍历存储过程失败: %w", err)
	}
	return routines, nil
}

// GenerateTriggerDDL 将触发器转换为目标方言的 DDL
func (a *Adapter) GenerateTriggerDDL(trigger types.TriggerMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.Oracle:
		return trigger.Body, nil
	default:
		return "", fmt.Errorf("Oracle: 暂不支持将触发器自动转换为 %s 方言，请手动迁移", targetDialect)
	}
}

// GenerateRoutineDDL 将存储过程转换为目标方言的 DDL
func (a *Adapter) GenerateRoutineDDL(routine types.RoutineMeta, targetDialect types.DatabaseType) (string, error) {
	switch targetDialect {
	case types.Oracle:
		return routine.Body, nil
	default:
		return "", fmt.Errorf("Oracle: 暂不支持将存储过程自动转换为 %s 方言，请手动迁移", targetDialect)
	}
}

// FixAutoIncrementSequences 修复自增序列（Oracle 使用 SEQUENCE + 触发器）
func (a *Adapter) FixAutoIncrementSequences(ctx context.Context, tableName string, columns []types.ColumnMeta) error {
	for _, col := range columns {
		if !col.AutoIncrement {
			continue
		}
		seqName := strings.ToUpper(tableName) + "_SEQ"
		query := fmt.Sprintf(`SELECT "%s".NEXTVAL FROM DUAL`, escapeIdent(seqName))
		var curVal int64
		if err := a.db.QueryRowContext(ctx, query).Scan(&curVal); err != nil {
			continue
		}
		maxQuery := fmt.Sprintf(`SELECT COALESCE(MAX("%s"), 0) FROM "%s"`, escapeIdent(col.Name), escapeIdent(tableName))
		var maxVal int64
		if err := a.db.QueryRowContext(ctx, maxQuery).Scan(&maxVal); err != nil {
			continue
		}
		if maxVal > 0 {
	a.db.ExecContext(ctx, fmt.Sprintf(`ALTER SEQUENCE "%s" INCREMENT BY 1 MINVALUE 0`, escapeIdent(seqName)))
	a.db.ExecContext(ctx, fmt.Sprintf(`ALTER SEQUENCE "%s" RESTART START WITH %d`, escapeIdent(seqName), maxVal+1))
		}
	}
	return nil
}

func (a *Adapter) scanRows(ctx context.Context, query string, args []any, cols []string) ([]types.Row, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 查询数据失败: %w", err)
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
			return nil, fmt.Errorf("Oracle: 读取数据失败: %w", err)
		}
		row := make(types.Row)
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Oracle: 遍历数据失败: %w", err)
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

// escapeIdent 转义 Oracle 标识符中的双引号，防止 SQL 注入和语法错误
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}

// ReadDataByPhysicalRowID 基于 Oracle ROWID 的物理行游标分页。
// ROWID 是 Oracle 内置的物理行标识符，即使无主键也可做 O(1) 游标分页。
// 返回的每行包含 _physrowid 列存储 ROWID 字符串，编排器用它推进游标并在写入前移除。
func (a *Adapter) ReadDataByPhysicalRowID(ctx context.Context, tableName string, lastRowID any, limit int) ([]types.Row, error) {
	schema, err := a.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, fmt.Errorf("Oracle: 获取表结构失败: %w", err)
	}
	cols := schemaColumnNames(schema)
	if len(cols) == 0 {
		return nil, fmt.Errorf("Oracle: 表 %s 无可读列", tableName)
	}

	outerCols := make([]string, len(cols))
	for i, c := range cols {
		outerCols[i] = fmt.Sprintf(`"%s"`, escapeIdent(c))
	}
	var query string
	var args []any
	if lastRowID != nil {
		query = fmt.Sprintf(`SELECT %s, ROWIDTOCHAR(rowid) AS _physrowid FROM (
			SELECT * FROM "%s" WHERE ROWID > CHARTOROWID(:1) ORDER BY ROWID
		) WHERE ROWNUM <= %d`,
			strings.Join(outerCols, ", "), escapeIdent(tableName), limit)
		args = append(args, lastRowID)
	} else {
		query = fmt.Sprintf(`SELECT %s, ROWIDTOCHAR(rowid) AS _physrowid FROM (
			SELECT * FROM "%s" ORDER BY ROWID
		) WHERE ROWNUM <= %d`,
			strings.Join(outerCols, ", "), escapeIdent(tableName), limit)
	}

	allCols := append(append([]string{}, cols...), "_physrowid")
	return a.scanRows(ctx, query, args, allCols)
}
