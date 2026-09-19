package converter

import (
	"fmt"
	"regexp"
	"strings"

	"dbbridge/pkg"
)

// Converter DDL 转换器
// 将源方言的 DDL 转换为目标方言的 DDL
type Converter struct {
	sourceDialect types.DatabaseType
	targetDialect types.DatabaseType
	targetAdapter types.DatabaseAdapter
}

// NewConverter 创建 DDL 转换器
func NewConverter(source, target types.DatabaseType, targetAdapter types.DatabaseAdapter) *Converter {
	return &Converter{
		sourceDialect: source,
		targetDialect: target,
		targetAdapter: targetAdapter,
	}
}

// ConvertDDL 将一条 DDL 语句从源方言转换为目标方言
func (c *Converter) ConvertDDL(ddl string) (string, error) {
	upper := strings.ToUpper(strings.TrimSpace(ddl))

	switch {
	case strings.HasPrefix(upper, "CREATE TABLE"):
		return c.convertCreateTable(ddl)
	case strings.HasPrefix(upper, "CREATE INDEX"),
		strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
		return c.convertCreateIndex(ddl)
	case strings.HasPrefix(upper, "DROP TABLE"):
		return c.convertDropTable(ddl)
	case strings.HasPrefix(upper, "INSERT"):
		return c.convertInsert(ddl)
	case strings.HasPrefix(upper, "ALTER TABLE"):
		// ALTER TABLE 转换较复杂，暂原样返回
		return ddl, nil
	default:
		return ddl, nil
	}
}

// convertCreateTable 转换 CREATE TABLE 语句
func (c *Converter) convertCreateTable(ddl string) (string, error) {
	// 解析源 DDL 为 TableSchema
	schema, err := parseCreateTable(ddl, c.sourceDialect)
	if err != nil {
		return "", fmt.Errorf("解析 CREATE TABLE 失败: %w", err)
	}

	// 用目标适配器生成新 DDL
	return c.targetAdapter.GenerateCreateTableDDL(*schema)
}

// convertCreateIndex 转换 CREATE INDEX 语句
func (c *Converter) convertCreateIndex(ddl string) (string, error) {
	// 解析索引信息
	idx, table, err := parseCreateIndex(ddl, c.sourceDialect)
	if err != nil {
		return "", err
	}

	// 用目标适配器格式
	var sb strings.Builder
	if idx.IsUnique {
		sb.WriteString("CREATE UNIQUE INDEX ")
	} else {
		sb.WriteString("CREATE INDEX ")
	}

	switch c.targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora, types.Dameng:
		sb.WriteString(fmt.Sprintf("`%s` ON `%s` (", idx.Name, table))
	case types.PostgreSQL:
		sb.WriteString(fmt.Sprintf("\"%s\" ON \"%s\" (", idx.Name, table))
	case types.SQLite:
		sb.WriteString(fmt.Sprintf("\"%s\" ON \"%s\" (", idx.Name, table))
	default:
		sb.WriteString(fmt.Sprintf("%s ON %s (", idx.Name, table))
	}

	for i, col := range idx.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		switch c.targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora, types.Dameng:
		sb.WriteString(fmt.Sprintf("`%s`", col))
		case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB, types.SQLite:
			sb.WriteString(fmt.Sprintf("\"%s\"", col))
		default:
			sb.WriteString(col)
		}
	}
	sb.WriteString(")")

	return sb.String(), nil
}

// convertDropTable 转换 DROP TABLE 语句
func (c *Converter) convertDropTable(ddl string) (string, error) {
	// 提取表名
	re := regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?[\` + "`" + `""]?(.+?)[\` + "`" + `""]?\s*;?\s*$`)
	match := re.FindStringSubmatch(ddl)
	if len(match) < 2 {
		// 简单回退
		return ddl, nil
	}
	tableName := strings.Trim(match[1], "`\"")
	return c.targetAdapter.GenerateDropTableDDL(tableName)
}

// convertInsert 转换 INSERT 语句
// 主要是处理引号差异，比如 MySQL 的反引号在 PostgreSQL 中需要改成双引号
func (c *Converter) convertInsert(ddl string) (string, error) {
	switch c.targetDialect {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora, types.Dameng:
		return ddl, nil // MySQL 源和目标一致时不需要转换
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB, types.SQLite:
		// 将反引号标识符替换为双引号，但跳过单引号字符串字面量内部，
		// 避免改写数据内容（如 INSERT 值中的 `code`）
		converted := replaceBackticksOutsideStrings(ddl)
		// MySQL 的 \ 转义在 PG 中不适用，但这个复杂度较高，暂保留
		return converted, nil
	default:
		return ddl, nil
	}
}

// replaceBackticksOutsideStrings 将反引号替换为双引号，但跳过单引号字符串字面量内部。
// 兼容 MySQL 的反斜杠转义（\' 与 \\）。
func replaceBackticksOutsideStrings(s string) string {
	var sb strings.Builder
	inString := false
	escaped := false
	for _, r := range s {
		if inString {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '\'' {
				inString = false
			}
			sb.WriteRune(r)
			continue
		}
		switch r {
		case '\'':
			inString = true
		case '`':
			sb.WriteByte('"')
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// parseCreateTable 从 CREATE TABLE DDL 中解析出 TableSchema
// 这是一个简化的解析器，处理常见的 MySQL mysqldump 输出格式
func parseCreateTable(ddl string, dialect types.DatabaseType) (*types.TableSchema, error) {
	// 提取表名
	tableName := extractTableName(ddl)
	if tableName == "" {
		return nil, fmt.Errorf("无法解析表名")
	}

	schema := &types.TableSchema{
		Name: tableName,
	}

	// 提取括号内的列定义
	body := extractTableBody(ddl)
	if body == "" {
		return nil, fmt.Errorf("无法解析表定义体")
	}

	// 分割列定义
	parts := splitColumnDefs(body)

	var pkCols []string

	for _, part := range parts {
		upper := strings.ToUpper(strings.TrimSpace(part))

		// 检查是否是约束（PRIMARY KEY, UNIQUE KEY, KEY, CONSTRAINT）
		if isConstraint(upper) {
			// 解析主键
			if strings.HasPrefix(upper, "PRIMARY KEY") {
				cols := extractColumnList(part)
				pkCols = cols
				schema.Indexes = append(schema.Indexes, types.IndexMeta{
					Name:      "PRIMARY",
					Columns:   cols,
					IsUnique:  true,
					IsPrimary: true,
				})
			} else if strings.HasPrefix(upper, "UNIQUE KEY") || strings.HasPrefix(upper, "UNIQUE INDEX") || strings.HasPrefix(upper, "UNIQUE") {
				idxName := extractIndexName(part, dialect)
				cols := extractColumnList(part)
				schema.Indexes = append(schema.Indexes, types.IndexMeta{
					Name:     idxName,
					Columns:  cols,
					IsUnique: true,
				})
			} else if strings.HasPrefix(upper, "KEY") || strings.HasPrefix(upper, "INDEX") {
				idxName := extractIndexName(part, dialect)
				cols := extractColumnList(part)
				schema.Indexes = append(schema.Indexes, types.IndexMeta{
					Name:    idxName,
					Columns: cols,
				})
			} else if strings.HasPrefix(upper, "CONSTRAINT") || strings.HasPrefix(upper, "FOREIGN KEY") {
				// 外键解析
				fk := parseForeignKey(part)
				if fk != nil {
					schema.ForeignKeys = append(schema.ForeignKeys, *fk)
				}
			}
			continue
		}

		// 解析列定义
		col := parseColumnDef(part, dialect)
		if col != nil {
			schema.Columns = append(schema.Columns, *col)
		}
	}

	// 标记主键列
	if len(pkCols) > 0 {
		for i := range schema.Columns {
			for _, pk := range pkCols {
				if schema.Columns[i].Name == pk {
					schema.Columns[i].IsPrimaryKey = true
				}
			}
		}
	}

	// 提取表选项（ENGINE, CHARSET, COMMENT）
	schema.Engine = extractTableOption(ddl, "ENGINE")
	schema.Charset = extractTableOption(ddl, "CHARSET")
	schema.Collation = extractTableOption(ddl, "COLLATE")
	schema.Comment = extractTableComment(ddl)

	return schema, nil
}

// extractTableName 提取表名
func extractTableName(ddl string) string {
	// CREATE TABLE [IF NOT EXISTS] `name` 或 "name" 或 name
	re := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[\` + "`" + `""]?(.+?)[\` + "`" + `""]?\s*\(`)
	match := re.FindStringSubmatch(ddl)
	if len(match) >= 2 {
		return strings.Trim(match[1], "`\"")
	}
	return ""
}

// extractTableBody 提取括号内的定义体
func extractTableBody(ddl string) string {
	start := strings.Index(ddl, "(")
	if start < 0 {
		return ""
	}

	// 找到匹配的右括号
	depth := 0
	for i := start; i < len(ddl); i++ {
		switch ddl[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return ddl[start+1 : i]
			}
		}
	}
	return ""
}

// splitColumnDefs 分割列定义（按逗号，但忽略括号内的逗号）
func splitColumnDefs(body string) []string {
	var parts []string
	depth := 0
	start := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false

	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch ch {
		case '\'':
			if i > 0 && body[i-1] == '\\' {
				continue
			}
			if !inDoubleQuote && !inBacktick {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote && !inBacktick {
				inDoubleQuote = !inDoubleQuote
			}
		case '`':
			if !inSingleQuote && !inDoubleQuote {
				inBacktick = !inBacktick
			}
		case '(':
			if !inSingleQuote && !inDoubleQuote && !inBacktick {
				depth++
			}
		case ')':
			if !inSingleQuote && !inDoubleQuote && !inBacktick {
				depth--
			}
		case ',':
			if depth == 0 && !inSingleQuote && !inDoubleQuote && !inBacktick {
				parts = append(parts, strings.TrimSpace(body[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(body) {
		last := strings.TrimSpace(body[start:])
		if last != "" {
			parts = append(parts, last)
		}
	}
	return parts
}

// isConstraint 判断是否是约束定义
func isConstraint(upper string) bool {
	return strings.HasPrefix(upper, "PRIMARY KEY") ||
		strings.HasPrefix(upper, "UNIQUE KEY") ||
		strings.HasPrefix(upper, "UNIQUE INDEX") ||
		strings.HasPrefix(upper, "UNIQUE") ||
		strings.HasPrefix(upper, "KEY ") ||
		strings.HasPrefix(upper, "INDEX ") ||
		strings.HasPrefix(upper, "CONSTRAINT") ||
		strings.HasPrefix(upper, "FOREIGN KEY")
}

// extractColumnList 从约束中提取列名列表
func extractColumnList(def string) []string {
	start := strings.Index(def, "(")
	if start < 0 {
		return nil
	}
	end := strings.LastIndex(def, ")")
	if end < 0 || end <= start {
		return nil
	}
	inner := def[start+1 : end]
	// 分割逗号
	cols := strings.Split(inner, ",")
	var result []string
	for _, c := range cols {
		c = strings.TrimSpace(c)
		// 去掉引号
		c = strings.Trim(c, "`\"")
		// 去掉长度限定（如 column_name(10)）
		if idx := strings.Index(c, "("); idx >= 0 {
			c = c[:idx]
		}
		c = strings.TrimSpace(c)
		if c != "" {
			result = append(result, c)
		}
	}
	return result
}

// extractIndexName 提取索引名
func extractIndexName(def string, dialect types.DatabaseType) string {
	upper := strings.ToUpper(strings.TrimSpace(def))
	// 去掉 KEY/UNIQUE KEY/INDEX 前缀
	for _, prefix := range []string{"UNIQUE KEY ", "UNIQUE INDEX ", "UNIQUE ", "KEY ", "INDEX "} {
		if strings.HasPrefix(upper, prefix) {
			rest := strings.TrimSpace(def[len(prefix):])
			// 提取第一个标识符
			rest = strings.TrimLeft(rest, "`\"")
			end := 0
			for end < len(rest) && rest[end] != ' ' && rest[end] != '`' && rest[end] != '"' && rest[end] != '(' {
				end++
			}
			name := rest[:end]
			name = strings.Trim(name, "`\"")
			return name
		}
	}
	return ""
}

// parseColumnDef 解析列定义
func parseColumnDef(def string, dialect types.DatabaseType) *types.ColumnMeta {
	def = strings.TrimSpace(def)
	if def == "" {
		return nil
	}

	// 提取列名（第一个标识符，可能是 `name` 或 "name" 或 name）
	col := &types.ColumnMeta{}

	// 处理反引号/双引号包裹的列名
	if strings.HasPrefix(def, "`") {
		end := strings.Index(def[1:], "`")
		if end >= 0 {
			col.Name = def[1 : 1+end]
			def = strings.TrimSpace(def[2+end:])
		}
	} else if strings.HasPrefix(def, "\"") {
		end := strings.Index(def[1:], "\"")
		if end >= 0 {
			col.Name = def[1 : 1+end]
			def = strings.TrimSpace(def[2+end:])
		}
	} else {
		// 无引号的列名
		end := 0
		for end < len(def) && def[end] != ' ' && def[end] != '\t' {
			end++
		}
		col.Name = def[:end]
		def = strings.TrimSpace(def[end:])
	}

	if col.Name == "" {
		return nil
	}

	// 解析类型
	col.BaseType, col.DataType, def = extractColumnType(def)
	if col.DataType == "" {
		return nil
	}

	upperDef := strings.ToUpper(def)

	// NOT NULL
	if strings.Contains(upperDef, "NOT NULL") {
		col.Nullable = false
	} else {
		col.Nullable = true
	}

	// AUTO_INCREMENT
	if strings.Contains(upperDef, "AUTO_INCREMENT") || strings.Contains(upperDef, "AUTOINCREMENT") {
		col.AutoIncrement = true
	}

	// 列级 PRIMARY KEY（MySQL 允许写在列定义内，
	// 如 `id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'xx'`）。
	// 先截掉 COMMENT 段，防止注释文案里出现 "primary key" 误判。
	scanPart := upperDef
	if cIdx := strings.Index(scanPart, "COMMENT"); cIdx >= 0 {
		scanPart = scanPart[:cIdx]
	}
	if strings.Contains(scanPart, "PRIMARY KEY") {
		col.IsPrimaryKey = true
	}

	// DEFAULT
	if idx := strings.Index(upperDef, "DEFAULT"); idx >= 0 {
		rest := strings.TrimSpace(def[idx+7:])
		// 提取默认值
		if strings.HasPrefix(rest, "'") {
			end := strings.Index(rest[1:], "'")
			if end >= 0 {
				val := rest[:2+end]
				col.DefaultValue = &val
			}
		} else {
			end := 0
			for end < len(rest) && rest[end] != ' ' && rest[end] != ',' && rest[end] != '\n' {
				end++
			}
			val := rest[:end]
			if val != "" {
				col.DefaultValue = &val
			}
		}
	}

	// COMMENT
	if idx := strings.Index(upperDef, "COMMENT"); idx >= 0 {
		rest := strings.TrimSpace(def[idx+7:])
		if strings.HasPrefix(rest, "'") {
			end := strings.Index(rest[1:], "'")
			if end >= 0 {
				col.Comment = rest[1 : 1+end]
			}
		}
	}

	// 解析长度
	if openIdx := strings.Index(col.DataType, "("); openIdx >= 0 {
		closePart := col.DataType[openIdx+1:]
		if closeIdx := strings.Index(closePart, ")"); closeIdx >= 0 {
			params := closePart[:closeIdx]
			// VARCHAR(255) -> length=255
			// DECIMAL(10,2) -> precision=10, scale=2
			nums := strings.Split(params, ",")
			if len(nums) >= 1 {
				var n int
				if _, err := fmt.Sscanf(strings.TrimSpace(nums[0]), "%d", &n); err == nil {
					col.Length = &n
				}
			}
			if len(nums) >= 2 {
				var p, s int
				fmt.Sscanf(strings.TrimSpace(nums[0]), "%d", &p)
				fmt.Sscanf(strings.TrimSpace(nums[1]), "%d", &s)
				col.Precision = &p
				col.Scale = &s
			}
		}
	}

	return col
}

// extractColumnType 提取列类型
func extractColumnType(def string) (baseType, fullType, rest string) {
	def = strings.TrimSpace(def)
	if def == "" {
		return "", "", ""
	}

	// 类型可能包含括号参数，如 VARCHAR(255) 或 DECIMAL(10,2)
	end := 0
	depth := 0
	for end < len(def) {
		ch := def[end]
		if ch == '(' {
			depth++
		} else if ch == ')' {
			depth--
		} else if (ch == ' ' || ch == '\t') && depth == 0 {
			break
		}
		end++
	}

	fullType = def[:end]
	rest = strings.TrimSpace(def[end:])

	// 提取基础类型（不含括号参数）
	if openIdx := strings.Index(fullType, "("); openIdx >= 0 {
		baseType = strings.ToUpper(fullType[:openIdx])
	} else {
		baseType = strings.ToUpper(fullType)
	}

	return baseType, fullType, rest
}

// extractTableOption 提取表选项
func extractTableOption(ddl, option string) string {
	re := regexp.MustCompile("(?i)" + option + "=([\\w]+)")
	match := re.FindStringSubmatch(ddl)
	if len(match) >= 2 {
		return match[1]
	}
	return ""
}

// extractTableComment 提取表注释
func extractTableComment(ddl string) string {
	re := regexp.MustCompile("(?i)COMMENT\\s*=\\s*'([^']*)'")
	match := re.FindStringSubmatch(ddl)
	if len(match) >= 2 {
		return match[1]
	}
	return ""
}

// parseForeignKey 解析外键定义
func parseForeignKey(def string) *types.ForeignKeyMeta {
	// CONSTRAINT `name` FOREIGN KEY (`col`) REFERENCES `table` (`col`)
	re := regexp.MustCompile("(?i)(?:CONSTRAINT\\s+[\\`\"]?([\\w]+)[\\`\"]?\\s+)?FOREIGN\\s+KEY\\s*\\(([^)]+)\\)\\s*REFERENCES\\s+[\\`\"]?([\\w]+)[\\`\"]?\\s*\\(([^)]+)\\)")
	match := re.FindStringSubmatch(def)
	if len(match) < 4 {
		return nil
	}

	fk := &types.ForeignKeyMeta{}

	if match[1] != "" {
		fk.Name = match[1]
	}

	// 解析列
	for _, c := range strings.Split(match[2], ",") {
		c = strings.TrimSpace(strings.Trim(c, "`\""))
		if c != "" {
			fk.Columns = append(fk.Columns, c)
		}
	}

	fk.RefTable = strings.Trim(match[3], "`\"")

	for _, c := range strings.Split(match[4], ",") {
		c = strings.TrimSpace(strings.Trim(c, "`\""))
		if c != "" {
			fk.RefColumns = append(fk.RefColumns, c)
		}
	}

	// ON DELETE / ON UPDATE
	if idx := strings.Index(strings.ToUpper(def), "ON DELETE"); idx >= 0 {
		action := strings.TrimSpace(def[idx+10:])
		end := strings.IndexAny(action, " \n")
		if end > 0 {
			fk.OnDelete = action[:end]
		}
	}
	if idx := strings.Index(strings.ToUpper(def), "ON UPDATE"); idx >= 0 {
		action := strings.TrimSpace(def[idx+10:])
		end := strings.IndexAny(action, " \n")
		if end > 0 {
			fk.OnUpdate = action[:end]
		}
	}

	return fk
}

// parseCreateIndex 从 CREATE INDEX DDL 中解析索引信息
func parseCreateIndex(ddl string, dialect types.DatabaseType) (*types.IndexMeta, string, error) {
	upper := strings.ToUpper(strings.TrimSpace(ddl))

	isUnique := strings.Contains(upper, "UNIQUE")

	// 提取索引名和表名
	// CREATE [UNIQUE] INDEX [IF NOT EXISTS] `index_name` ON `table_name` (col1, col2)
	re := regexp.MustCompile("(?i)CREATE\\s+(?:UNIQUE\\s+)?INDEX\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?[`\"\\[]?(\\w+)[`\"\\]]?\\s+ON\\s+[`\"\\[]?(\\w+)[`\"\\]]?\\s*\\(")
	match := re.FindStringSubmatch(ddl)
	if len(match) < 3 {
		return nil, "", fmt.Errorf("无法解析索引名和表名")
	}

	idxName := match[1]
	tableName := match[2]

	// 提取列名
	cols := extractColumnList(ddl)
	if cols == nil {
		cols = []string{}
	}

	return &types.IndexMeta{
		Name:     idxName,
		Columns:  cols,
		IsUnique: isUnique,
	}, tableName, nil
}
