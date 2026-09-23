// Package oracle 注册 Oracle 数据库适配器。
//
// Oracle 是全球顶级商业关系型数据库，本适配器基于纯 Go 的 go-ora 驱动（无 CGO 依赖）。
// 类型映射使用 typeconv 中立类型体系，支持与其他数据库互转。
package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"

	_ "github.com/sijms/go-ora/v2"
)

// Adapter Oracle 数据库适配器
type Adapter struct {
	db    *sql.DB
	brand string

	tablespace string // 目标表空间（可选，CREATE TABLE 时追加 TABLESPACE 子句）

	identityOnce sync.Once
	identityOK   bool // 目标库支持 IDENTITY 列（Oracle 12c+）；否则自增回退 SEQUENCE+触发器
}

// SetTablespace 设置目标表空间（TablespaceAware 接口）
func (a *Adapter) SetTablespace(tablespace string) error {
	a.tablespace = tablespace
	return nil
}

// qualifyTable 生成表名片段，支持 "schema.table" 限定名
// （Schema 映射场景由 orchestrator 传入；未配置映射时为普通表名）
func qualifyTable(name string) string {
	schema, table := types.SplitQualified(name)
	if schema == "" {
		return oraIdent(table)
	}
	return oraIdent(schema) + "." + oraIdent(table)
}

// qualifyBare 取限定名的裸表部分（供 RENAME 目标侧使用，Oracle 不允许带 schema）
func qualifyBare(name string) string {
	_, table := types.SplitQualified(name)
	return table
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

// versionBannerRe 从 BANNER 提取主版本号（避免服务端 REGEXP_SUBSTR/TO_NUMBER
// 在旧版本上的兼容问题）
var versionBannerRe = regexp.MustCompile(`\d+`)

// ensureIdentityDetected 惰性检测目标库是否支持 IDENTITY（12c 引入）。
// 刻意不在 Connect 时做：连接路径保持与历史版本完全一致（连接失败时只
// 报连接错误，不叠加任何额外查询）。检测推迟到首次建表前，自带短超时，
// 失败时保守回退到 SEQUENCE+触发器方案（所有版本均可用）。
func (a *Adapter) ensureIdentityDetected() {
	a.identityOnce.Do(func() {
		if a.db == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var banner string
		err := a.db.QueryRowContext(ctx,
			`SELECT BANNER FROM v$version WHERE ROWNUM = 1`).Scan(&banner)
		if err != nil {
			return // 检测失败 → 回退序列方案
		}
		m := versionBannerRe.FindString(banner)
		if m == "" {
			return
		}
		var major int
		if _, err := fmt.Sscanf(m, "%d", &major); err != nil {
			return
		}
		a.identityOK = major >= 12
	})
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
	schemaName, table := types.SplitQualified(tableName)

	// 获取列信息（IDENTITY_COLUMN 为 12c+ 新增列，11g 上查询失败则回退不含该列的版本）
	rows, err := a.db.QueryContext(ctx, `
	SELECT COLUMN_NAME, DATA_TYPE, DATA_LENGTH, DATA_PRECISION, DATA_SCALE,
	       NULLABLE, DATA_DEFAULT, COLUMN_ID, IDENTITY_COLUMN
	FROM ALL_TAB_COLUMNS
	WHERE OWNER = NVL(:1, USER) AND TABLE_NAME = UPPER(:2)
	ORDER BY COLUMN_ID
`, schemaName, table)
	if err != nil {
		rows, err = a.db.QueryContext(ctx, `
	SELECT COLUMN_NAME, DATA_TYPE, DATA_LENGTH, DATA_PRECISION, DATA_SCALE,
	       NULLABLE, DATA_DEFAULT, COLUMN_ID, 'NO'
	FROM ALL_TAB_COLUMNS
	WHERE OWNER = NVL(:1, USER) AND TABLE_NAME = UPPER(:2)
	ORDER BY COLUMN_ID
`, schemaName, table)
	}
	if err != nil {
		return schema, fmt.Errorf("Oracle: 查询列信息失败: %w", err)
	}
	defer rows.Close()

	var columns []types.ColumnMeta
	for rows.Next() {
		var name, dataType, nullable, identityCol string
		var dataLen, dataPrec, dataScale, colID sql.NullInt64
		var defVal sql.NullString
		if err := rows.Scan(&name, &dataType, &dataLen, &dataPrec, &dataScale, &nullable, &defVal, &colID, &identityCol); err != nil {
			return schema, fmt.Errorf("Oracle: 读取列信息失败: %w", err)
		}
		col := types.ColumnMeta{
			Name:     name,
			DataType: dataType,
			BaseType: strings.ToUpper(dataType),
			Nullable: nullable == "Y",
			// IDENTITY 列映射为通用 AutoIncrement 语义（迁移到其他目标库时
			// 由各自适配器生成对应自增 DDL）
			AutoIncrement: strings.EqualFold(identityCol, "YES"),
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
	FROM ALL_CONSTRAINTS c
	JOIN ALL_CONS_COLUMNS cc ON c.CONSTRAINT_NAME = cc.CONSTRAINT_NAME AND c.OWNER = cc.OWNER
	WHERE c.OWNER = NVL(:1, USER) AND c.TABLE_NAME = UPPER(:2) AND c.CONSTRAINT_TYPE = 'P'
	ORDER BY cc.POSITION
`, schemaName, table)
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
	err := a.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, qualifyTable(tableName))).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("Oracle: 获取行数失败: %w", err)
	}
	return count, nil
}

// TableExists 检查表是否存在（支持 "schema.table" 限定名；未配置时查当前用户 Schema）
func (a *Adapter) TableExists(ctx context.Context, tableName string) (bool, error) {
	var count int
	schemaName, table := types.SplitQualified(tableName)
	err := a.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM ALL_TABLES WHERE OWNER = NVL(:1, USER) AND TABLE_NAME = UPPER(:2)
`, schemaName, table).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("Oracle: 检查表存在失败: %w", err)
	}
	return count > 0, nil
}

// BackupTable 将表重命名为备份表名（支持 "schema.table" 限定名）。
// Oracle 的 RENAME 只能在当前 Schema 下操作，跨 Schema 统一用
// ALTER TABLE ... RENAME TO（重命名后留在原 Schema），返回限定名
// 便于后续恢复/删除定位。
func (a *Adapter) BackupTable(ctx context.Context, tableName string) (string, error) {
	schemaName, table := types.SplitQualified(tableName)
	backupName := fmt.Sprintf("_bak_%s_%s", table, time.Now().Format("20060102_150405"))
	qualifiedBackup := backupName
	if schemaName != "" {
		qualifiedBackup = schemaName + "." + backupName
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`,
		qualifyTable(tableName), oraIdent(backupName)))
	if err != nil {
		return "", fmt.Errorf("Oracle: 备份表失败 (%s→%s): %w", tableName, qualifiedBackup, err)
	}
	return qualifiedBackup, nil
}

// RestoreFromBackup 从备份表恢复
// 注意：RENAME TO 目标侧不能带 schema（Oracle 语法限制），
// 重命名后留在备份表所在 schema（与原表同 schema，语义正确）
func (a *Adapter) RestoreFromBackup(ctx context.Context, backupName, originalName string) error {
	exists, _ := a.TableExists(ctx, originalName)
	if exists {
		_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE %s PURGE`, qualifyTable(originalName)))
		if err != nil {
			return fmt.Errorf("Oracle: 恢复备份时删除当前表失败: %w", err)
		}
	}
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`,
		qualifyTable(backupName), oraIdent(qualifyBare(originalName))))
	if err != nil {
		return fmt.Errorf("Oracle: 恢复备份失败: %w", err)
	}
	return nil
}

// DropBackup 删除备份表
func (a *Adapter) DropBackup(ctx context.Context, backupName string) error {
	_, err := a.db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE %s PURGE`, qualifyTable(backupName)))
	if err != nil {
		return fmt.Errorf("Oracle: 删除备份表失败: %w", err)
	}
	return nil
}

// GenerateCreateTableDDL 生成建表 SQL
func (a *Adapter) GenerateCreateTableDDL(table types.TableSchema) (string, error) {
	a.ensureIdentityDetected()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`CREATE TABLE %s (
`, qualifyTable(table.Name)))

	for i, col := range table.Columns {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString("  ")
		sb.WriteString(fmt.Sprintf(`%s `, oraIdent(col.Name)))
		sb.WriteString(a.MapType(col))
		// 自增：12c+ 优先用 IDENTITY 列（内联、无额外对象，语义等同 MySQL
		// AUTO_INCREMENT）。BY DEFAULT ON NULL 允许迁移期显式插入已有 id 值，
		// 应用后续不传该列时仍自动递增。
		if col.AutoIncrement && a.identityOK {
			sb.WriteString(" GENERATED BY DEFAULT ON NULL AS IDENTITY")
		}
		if !col.Nullable {
			sb.WriteString(" NOT NULL")
		}
		if col.DefaultValue != nil && *col.DefaultValue != "" && !col.AutoIncrement {
			if d := a.formatOracleDefault(col, *col.DefaultValue); d != "" {
				sb.WriteString(" DEFAULT " + d)
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
			sb.WriteString(fmt.Sprintf(`%s`, oraIdent(c)))
		}
		sb.WriteString(")")
	}

	sb.WriteString("\n)")

	// 表空间（可选；未配置时使用目标库默认值）
	if a.tablespace != "" {
		sb.WriteString(fmt.Sprintf("\nTABLESPACE %s", oraIdent(a.tablespace)))
	}

	return sb.String(), nil
}

// formatOracleDefault 将源库默认值转换为 Oracle 合法的 DEFAULT 字面量。
// Oracle 在 CREATE TABLE 时即校验日期类默认值字面量的隐式格式
// （NLS_DATE_FORMAT 通常为 'DD-MON-RR'），MySQL 风格 '2024-01-01 00:00:00'
// 直接嵌入会报 ORA-01858，故日期类字符串默认值必须显式 TO_DATE/TO_TIMESTAMP；
// 日期零值（'0000-00-00'）Oracle 无对应表示，丢弃默认值。
func (a *Adapter) formatOracleDefault(col types.ColumnMeta, val string) string {
	quoted := typeconv.FormatDefault(val)
	if quoted == "" {
		return ""
	}
	// 布尔关键字：Oracle NUMBER(1) 无 TRUE/FALSE 关键字
	switch strings.ToLower(quoted) {
	case "true":
		return "1"
	case "false":
		return "0"
	}
	// 非字符串字面量（数值/函数/关键字如 CURRENT_TIMESTAMP）原样返回
	if !strings.HasPrefix(quoted, "'") {
		return quoted
	}
	lit := strings.Trim(quoted, "'")
	if isZeroDate(lit) {
		return ""
	}
	// 根据列的中立类型选择显式转换函数与格式掩码
	fmtMask := "SYYYY-MM-DD"
	if strings.Contains(lit, " ") {
		fmtMask += " HH24:MI:SS"
		if strings.Contains(lit, ".") {
			fmtMask += ".FF6"
		}
	}
	switch typeconv.Normalize(col.BaseType) {
	case typeconv.KindDate:
		return fmt.Sprintf("TO_DATE('%s', 'SYYYY-MM-DD')", lit)
	case typeconv.KindDateTime, typeconv.KindTimestamp, typeconv.KindTime:
		return fmt.Sprintf("TO_TIMESTAMP('%s', '%s')", lit, fmtMask)
	}
	return quoted
}

// isZeroDate 判断是否为日期零值（'0000-00-00' / '0000-00-00 00:00:00' 等）
func isZeroDate(lit string) bool {
	re := regexp.MustCompile(`\d+`)
	all := re.FindAllString(lit, -1)
	if len(all) == 0 {
		return false
	}
	for _, seg := range all {
		if seg != "0000" && strings.TrimLeft(seg, "0") != "" {
			return false
		}
	}
	return true
}

// GenerateDropTableDDL 生成删表 SQL
func (a *Adapter) GenerateDropTableDDL(tableName string) (string, error) {
	return fmt.Sprintf(`DROP TABLE %s PURGE`, qualifyTable(tableName)), nil
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
		outerCols[i] = fmt.Sprintf(`%s`, oraIdent(c))
	}
	query := fmt.Sprintf(`SELECT %s FROM (
		SELECT t.*, ROWNUM rn FROM (
			SELECT * FROM %s ORDER BY 1
		) t WHERE ROWNUM <= %d
	) WHERE rn > %d`,
		strings.Join(outerCols, ", "), qualifyTable(tableName), offset+limit, offset)
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
			SELECT * FROM %s WHERE %s > :1 ORDER BY %s ASC
		) WHERE ROWNUM <= %d`, qualifyTable(tableName), oraIdent(keyColumn), oraIdent(keyColumn), limit)
		args = append(args, lastKey)
	} else {
		query = fmt.Sprintf(`SELECT * FROM (
			SELECT * FROM %s ORDER BY %s ASC
		) WHERE ROWNUM <= %d`, qualifyTable(tableName), oraIdent(keyColumn), limit)
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
	query := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`,
		qualifyTable(tableName), quoteIdentifiers(columns), strings.Join(placeholders, ", "))

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

// FixAutoIncrementSequences 修复自增种子。
//
// 目标列由 GenerateCreateTableDDL 生成为 IDENTITY（Oracle 12c+）。迁移期显式
// 插入已有 id 值后，IDENTITY 内部序列滞后于 MAX(id)，后续应用插入不传该列
// 会触发主键冲突，需用 START WITH LIMIT VALUE 将内部序列推进到 MAX(id)+1。
//
// 若目标库不支持 IDENTITY（11g）或列尚未是 IDENTITY（历史版本建表），
// 回退为 SEQUENCE + BEFORE INSERT 触发器方案（所有版本可用）。
func (a *Adapter) FixAutoIncrementSequences(ctx context.Context, tableName string, columns []types.ColumnMeta) error {
	for _, col := range columns {
		if !col.AutoIncrement {
			continue
		}
		alter := fmt.Sprintf(`ALTER TABLE %s MODIFY (%s GENERATED BY DEFAULT ON NULL AS IDENTITY (START WITH LIMIT VALUE))`,
			qualifyTable(tableName), oraIdent(col.Name))
		if _, err := a.db.ExecContext(ctx, alter); err == nil {
			continue
		}
		if err := a.legacySequenceFallback(ctx, tableName, col); err != nil {
			return fmt.Errorf("Oracle: 修复自增列 %s.%s 失败: %w", tableName, col.Name, err)
		}
	}
	return nil
}

// legacySequenceFallback SEQUENCE + 触发器自增方案（Oracle 11g 回退路径）：
// 创建 表名_SEQ 序列（START WITH 当前 MAX(id)+1）和 BEFORE INSERT 触发器，
// 仅在自增列为 NULL 时取 NEXTVAL，不干扰迁移期显式插入。
func (a *Adapter) legacySequenceFallback(ctx context.Context, tableName string, col types.ColumnMeta) error {
	_, table := types.SplitQualified(tableName)
	// 11g 标识符上限 30 字节
	seqName := truncateIdent(strings.ToUpper(table), 26) + "_SEQ"
	trgName := "BI_" + truncateIdent(strings.ToUpper(table), 22) + "_" + truncateIdent(strings.ToUpper(col.Name), 4)

	// 序列不存在则创建（START WITH 为当前最大值+1，空表为 1）
	var seqCount int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_sequences WHERE sequence_name = :1`, seqName).Scan(&seqCount); err != nil {
		return fmt.Errorf("查询序列失败: %w", err)
	}
	if seqCount == 0 {
		var maxVal sql.NullInt64
		if err := a.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT MAX(%s) FROM %s`, oraIdent(col.Name), qualifyTable(tableName))).Scan(&maxVal); err != nil {
			return fmt.Errorf("查询当前最大值失败: %w", err)
		}
		start := maxVal.Int64 + 1
		if !maxVal.Valid {
			start = 1
		}
		if _, err := a.db.ExecContext(ctx, fmt.Sprintf(`CREATE SEQUENCE %s START WITH %d CACHE 20`, oraIdent(seqName), start)); err != nil {
			return fmt.Errorf("创建序列失败: %w", err)
		}
	}

	// 触发器不存在则创建（CREATE OR REPLACE 保证幂等）
	var trgCount int
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_triggers WHERE trigger_name = :1`, trgName).Scan(&trgCount); err != nil {
		return fmt.Errorf("查询触发器失败: %w", err)
	}
	if trgCount == 0 {
		body := fmt.Sprintf(`CREATE OR REPLACE TRIGGER %s
BEFORE INSERT ON %s
FOR EACH ROW
WHEN (NEW.%s IS NULL)
BEGIN
  :NEW.%s := %s.NEXTVAL;
END;`,
			oraIdent(trgName), qualifyTable(tableName), oraIdent(col.Name), oraIdent(col.Name), oraIdent(seqName))
		if _, err := a.db.ExecContext(ctx, body); err != nil {
			return fmt.Errorf("创建自增触发器失败: %w", err)
		}
	}
	return nil
}

// truncateIdent 截断标识符到指定字节长度（Oracle 标识符按字节计）
func truncateIdent(name string, max int) string {
	if len(name) <= max {
		return name
	}
	return name[:max]
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
		quoted[i] = oraIdent(c)
	}
	return strings.Join(quoted, ", ")
}

// escapeIdent 转义 Oracle 标识符中的双引号，防止 SQL 注入和语法错误
func escapeIdent(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}

// safeIdentRe 匹配无需引号即可安全使用的 Oracle 标识符
var safeIdentRe = regexp.MustCompile(`^[A-Z][A-Z0-9_$#]*$`)

// oracleReservedWords Oracle 保留字（经典集）。保留字裸写做列名/表名会报
// ORA-00904/ORA-00971 等，带引号则允许（如 "LEVEL"），因此命中时保持引号。
var oracleReservedWords = map[string]bool{
	"ACCESS": true, "ADD": true, "ALL": true, "ALTER": true, "AND": true, "ANY": true,
	"AS": true, "ASC": true, "AUDIT": true, "BETWEEN": true, "BY": true, "CHAR": true,
	"CHECK": true, "CLUSTER": true, "COLUMN": true, "COMMENT": true, "COMPRESS": true,
	"CONNECT": true, "CREATE": true, "CURRENT": true, "DATE": true, "DECIMAL": true,
	"DEFAULT": true, "DELETE": true, "DESC": true, "DISTINCT": true, "DROP": true,
	"ELSE": true, "EXCLUSIVE": true, "EXISTS": true, "FILE": true, "FLOAT": true,
	"FOR": true, "FROM": true, "GRANT": true, "GROUP": true, "HAVING": true,
	"IDENTIFIED": true, "IMMEDIATE": true, "IN": true, "INCREMENT": true, "INDEX": true,
	"INITIAL": true, "INSERT": true, "INTEGER": true, "INTERSECT": true, "INTO": true,
	"IS": true, "LEVEL": true, "LIKE": true, "LOCK": true, "LONG": true, "MAXEXTENTS": true,
	"MINUS": true, "MLSLABEL": true, "MODE": true, "MODIFY": true, "NOAUDIT": true,
	"NOCOMPRESS": true, "NOT": true, "NOWAIT": true, "NULL": true, "NUMBER": true,
	"OF": true, "OFFLINE": true, "ON": true, "ONLINE": true, "OPTION": true, "OR": true,
	"ORDER": true, "PCTFREE": true, "PRIOR": true, "PUBLIC": true, "RAW": true,
	"RENAME": true, "RESOURCE": true, "REVOKE": true, "ROW": true, "ROWID": true,
	"ROWNUM": true, "ROWS": true, "SELECT": true, "SESSION": true, "SET": true,
	"SHARE": true, "SIZE": true, "SMALLINT": true, "START": true, "SUCCESSFUL": true,
	"SYNONYM": true, "SYSDATE": true, "TABLE": true, "THEN": true, "TO": true,
	"TRIGGER": true, "UID": true, "UNION": true, "UNIQUE": true, "UPDATE": true,
	"USER": true, "VALIDATE": true, "VALUES": true, "VARCHAR": true, "VARCHAR2": true,
	"VIEW": true, "WHENEVER": true, "WHERE": true, "WITH": true,
}

// oraIdent 将标识符规范化为 Oracle 惯例（大写，安全时不加引号）。
//
// 此前所有 DDL/查询都输出带双引号的原始大小写标识符（如 "customers"），
// Oracle 会按字面小写存储，而存在性判断（USER_TABLES 里 UPPER(:1)）
// 和后续读写用的都是大写预期，两边对不上：第二次迁移时 TableExists 误判
// 为不存在，不执行 DROP 直接 CREATE，报 ORA-00955 name is already used，
// 建表失败且报错里的 SQL 被截断难定位。
// 统一规范化为大写后，存储/判断/读写三处一致。
// 保留字（如 LEVEL、ORDER、COMMENT）带引号使用是合法的，命中时保持引号。
func oraIdent(name string) string {
	upper := strings.ToUpper(name)
	if safeIdentRe.MatchString(upper) && !oracleReservedWords[upper] {
		return upper
	}
	return `"` + escapeIdent(upper) + `"`
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
		outerCols[i] = fmt.Sprintf(`%s`, oraIdent(c))
	}
	var query string
	var args []any
	if lastRowID != nil {
		query = fmt.Sprintf(`SELECT %s, ROWIDTOCHAR(rowid) AS _physrowid FROM (
SELECT * FROM %s WHERE ROWID > CHARTOROWID(:1) ORDER BY ROWID
) WHERE ROWNUM <= %d`,
			strings.Join(outerCols, ", "), qualifyTable(tableName), limit)
		args = append(args, lastRowID)
	} else {
		query = fmt.Sprintf(`SELECT %s, ROWIDTOCHAR(rowid) AS _physrowid FROM (
			SELECT * FROM %s ORDER BY ROWID
		) WHERE ROWNUM <= %d`,
			strings.Join(outerCols, ", "), qualifyTable(tableName), limit)
	}

	allCols := append(append([]string{}, cols...), "_physrowid")
	return a.scanRows(ctx, query, args, allCols)
}
