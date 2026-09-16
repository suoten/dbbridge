// Package sqllint 提供 SQL 方言体检能力。
//
// 用途：数据迁移完成后，业务程序里写死的 SQL 往往不兼容目标库方言。
// 本包对 SQL 文本做静态扫描，输出按行号组织的不兼容清单（含改法建议），
// 帮助开发人员逐条核对改造——只做分析不改写，改写交给开发确认。
//
// 规则按"目标方言"组织：目标库不支持的构造标为 error（必然报错），
// 语义有差异的标为 warning（需人工确认），建议项标为 info。
package sqllint

import (
	"fmt"
	"regexp"
	"strings"

	types "dbbridge/pkg"
)

// Severity 严重程度
type Severity string

const (
	SeverityError   Severity = "error"   // 目标库必然语法/功能错误
	SeverityWarning Severity = "warning" // 语义有差异，需人工确认
	SeverityInfo    Severity = "info"    // 建议关注
)

// Finding 单条不兼容发现
type Finding struct {
	Line       int    `json:"line"`       // 1-based 行号
	Column     int    `json:"column"`     // 1-based 列号
	Severity   string `json:"severity"`   // error/warning/info
	Category   string `json:"category"`   // pagination/function/quote/upsert/identity/routine/placeholder
	Message    string `json:"message"`    // 问题描述
	Suggestion string `json:"suggestion"` // 改法建议
	Snippet    string `json:"snippet"`    // 命中的代码片段（截断）
}

// Report 体检报告
type Report struct {
	SourceDialect string    `json:"sourceDialect"`
	TargetDialect string    `json:"targetDialect"`
	Findings      []Finding `json:"findings"`
	Errors        int       `json:"errors"`
	Warnings      int       `json:"warnings"`
	Infos         int       `json:"infos"`
	TotalLines    int       `json:"totalLines"`
}

// rule 模式按目标方言匹配
type rule struct {
	category   string
	severity   Severity
	pattern    *regexp.Regexp
	message    string
	suggestion string
}

// isPGTarget / isMySQLTarget / isMSSQLTarget / isDamengTarget 方言分组
func isPGTarget(t types.DatabaseType) bool {
	switch t {
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB:
		return true
	}
	return false
}

func isMySQLTarget(t types.DatabaseType) bool {
	switch t {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora:
		return true
	}
	return false
}

func isDamengTarget(t types.DatabaseType) bool { return t == types.Dameng }

func isOracleTarget(t types.DatabaseType) bool { return t == types.Oracle }

func isDb2Target(t types.DatabaseType) bool { return t == types.Db2 }

func isMSSQLTarget(t types.DatabaseType) bool { return t == types.MSSQL }

// buildRules 按目标方言构建规则集
func buildRules(target types.DatabaseType) []rule {
	var rules []rule
	add := func(category string, severity Severity, pattern string, message, suggestion string) {
		rules = append(rules, rule{
			category: category, severity: severity,
			pattern: regexp.MustCompile(`(?i)` + pattern),
			message: message, suggestion: suggestion,
		})
	}

	// ---- 引号风格 ----
	if !isMySQLTarget(target) {
		add("quote", SeverityError,
			"`[^`\n]+`",
			"MySQL 反引号标识符不被目标库支持",
			"改为目标库的标识符引号（PG 双引号 / MSSQL 方括号），或改用合法裸标识符")
	}
	if !isMSSQLTarget(target) {
		add("quote", SeverityWarning,
			`\[[A-Za-z_][\w$#@ ]*\]`,
			"MSSQL 方括号标识符",
			"改为目标库的标识符引号（MySQL 反引号 / PG 双引号）")
	}

	// ---- 分页 ----
	if isMSSQLTarget(target) {
		add("pagination", SeverityError,
			`\bLIMIT\s+\d+`,
			"MSSQL 不支持 LIMIT 分页",
			"改为 SELECT TOP n ... 或 ORDER BY ... OFFSET n ROWS FETCH NEXT m ROWS ONLY")
	} else if isDamengTarget(target) {
		add("pagination", SeverityWarning,
			`\bLIMIT\s+\d+`,
			"达梦对 LIMIT 的支持依兼容模式而定，Oracle 模式下不支持",
			"改为 ROWNUM <= n 或 OFFSET ... FETCH NEXT ... ROWS ONLY")
	}
	if !isMSSQLTarget(target) && !isDamengTarget(target) {
		add("pagination", SeverityError,
			`\bTOP\s+\d+`,
			"目标库不支持 MSSQL TOP 分页",
			"改为 LIMIT n 或 LIMIT n OFFSET m")
	}
	if !isMySQLTarget(target) {
		// LIMIT offset, count 双参数形式仅 MySQL 系支持
		add("pagination", SeverityError,
			`\bLIMIT\s+\d+\s*,\s*\d+`,
			"LIMIT offset,count 双参数形式仅 MySQL 系支持",
			"改为 LIMIT count OFFSET offset")
	}

	// ---- 函数 ----
	if !isMSSQLTarget(target) {
		add("function", SeverityError,
			`\bGETDATE\s*\(\s*\)`,
			"GETDATE() 仅 MSSQL/达梦支持",
			"MySQL 系改为 NOW()；PG 系改为 CURRENT_TIMESTAMP 或 NOW()")
		add("function", SeverityError,
			`\bSCOPE_IDENTITY\s*\(\s*\)|@@IDENTITY\b|@@ROWCOUNT\b`,
			"MSSQL 全局变量/取自增函数不被目标库支持",
			"MySQL 系改为 LAST_INSERT_ID()；PG 系改为 INSERT ... RETURNING id")
		add("function", SeverityWarning,
			`\bDATEDIFF\s*\(`,
			"DATEDIFF 参数个数/语义各库不同（MSSQL 3 参带单位，MySQL 2 参只算天）",
			"确认目标库语义，必要时改用日期差计算表达式")
		add("function", SeverityWarning,
			`\bCHARINDEX\s*\(`,
			"CHARINDEX() 仅 MSSQL/达梦支持",
			"改为 LOCATE()（MySQL）或 POSITION()/STRPOS()（PG）")
		add("function", SeverityWarning,
			`\bISNULL\s*\(\s*[^)]+,`,
			"ISNULL(expr, val) 两参函数仅 MSSQL 支持（PG 中 IS NULL 是语法不是函数）",
			"统一改为 COALESCE(expr, val)，所有库都支持")
	}
	if !isMySQLTarget(target) {
		add("function", SeverityError,
			`\bLAST_INSERT_ID\s*\(\s*\)`,
			"LAST_INSERT_ID() 仅 MySQL 系支持",
			"MSSQL 改为 SCOPE_IDENTITY()；PG 系改为 INSERT ... RETURNING")
		add("function", SeverityError,
			`\bON\s+DUPLICATE\s+KEY\s+UPDATE\b`,
			"ON DUPLICATE KEY UPDATE 仅 MySQL 系支持",
			"PG 系改为 INSERT ... ON CONFLICT (...) DO UPDATE；MSSQL 改为 MERGE")
		add("function", SeverityError,
			`\bREPLACE\s+INTO\b`,
			"REPLACE INTO 仅 MySQL 系支持（且是删除重插语义）",
			"PG 系改为 INSERT ... ON CONFLICT DO UPDATE；注意 REPLACE 会删旧行触发级联")
		add("function", SeverityWarning,
			`\bGROUP_CONCAT\s*\(`,
			"GROUP_CONCAT() 仅 MySQL 系支持",
			"PG 系改为 STRING_AGG()；MSSQL 改为 STRING_AGG()（2017+）")
		add("function", SeverityWarning,
			`\bDATE_FORMAT\s*\(`,
			"DATE_FORMAT() 仅 MySQL 系支持",
			"PG 系改为 TO_CHAR(timestamp, format)；注意格式符也不同")
		add("function", SeverityWarning,
			`\bIF\s*\(\s*[^)]*,`,
			"IF(cond, a, b) 三参函数仅 MySQL 系支持",
			"统一改为 CASE WHEN cond THEN a ELSE b END")
		add("function", SeverityWarning,
			`\bCONCAT_WS\s*\(|\bIFNULL\s*\(`,
			"MySQL 系函数，PG/MSSQL 不支持 IFNULL/CONCAT_WS（PG 无 CONCAT_WS）",
			"IFNULL 改为 COALESCE；CONCAT_WS 改用 || 或 CONCAT 拼接")
	}
	if !isPGTarget(target) {
		add("function", SeverityError,
			`\bRETURNING\b`,
			"INSERT/UPDATE ... RETURNING 仅 PG 系支持",
			"MySQL 系改为插入后 LAST_INSERT_ID()；MSSQL 改为 OUTPUT 子句")
	}
	if isMSSQLTarget(target) {
		add("function", SeverityError,
			`\bON\s+CONFLICT\b`,
			"ON CONFLICT 仅 PG 系支持",
			"MSSQL 改为 MERGE INTO ... WHEN MATCHED")
	}
	if !isDamengTarget(target) {
		add("function", SeverityWarning,
			`\bNVL\s*\(`,
			"NVL() 为 Oracle/达梦函数",
			"统一改为 COALESCE()")
	}

	// ---- UPSERT/MERGE ----
	if !isMSSQLTarget(target) && !isDamengTarget(target) {
		add("upsert", SeverityWarning,
			`\bMERGE\s+(?:INTO\s+)?[`+"`"+`"\[\w.]+`,
			"MERGE 语句为 MSSQL/Oracle 语法",
			"MySQL 系改为 INSERT ... ON DUPLICATE KEY UPDATE；PG 系改为 INSERT ... ON CONFLICT（PG15+ 也支持 MERGE）")
	}
	if isMySQLTarget(target) {
		add("upsert", SeverityError,
			`\bON\s+CONFLICT\b`,
			"ON CONFLICT 仅 PG 系支持",
			"改为 INSERT ... ON DUPLICATE KEY UPDATE")
	}

	// ---- 自增取值 ----
	add("identity", SeverityWarning,
		`SELECT\s+@@IDENTITY|SELECT\s+SCOPE_IDENTITY`,
		"取自增值方式与数据库绑定",
		"切换数据库后必须改用目标库的取值方式，否则并发下会取错 ID")

	// ---- 存储过程调用 ----
	if !isMySQLTarget(target) {
		add("routine", SeverityError,
			`\bCALL\s+[`+"`"+`"\[\w]+`,
			"CALL 语句调用存储过程（MySQL 语法）",
			"MSSQL 改为 EXEC proc；PG 系函数用 SELECT func()，过程用 CALL（PG11+）")
	}
	if !isMSSQLTarget(target) {
		add("routine", SeverityError,
			`\bEXEC(?:UTE)?\s+(?:\[[^\]]+\]|(?:sp|usp|xp|dbo)_?\w+|dbo\.\w+)`,
			"EXEC/EXECUTE 调用存储过程（MSSQL 语法）",
			"MySQL 系改为 CALL proc()；PG 系改为 SELECT func()")
	}

	// ---- 占位符（info 级，是否兼容取决于驱动）----
	if isMySQLTarget(target) {
		add("placeholder", SeverityInfo,
			`\$\d+\b`,
			"PG 风格位置参数 $1/$2（MySQL 驱动不支持）",
			"MySQL 系驱动改为 ? 占位符")
	}
	if isPGTarget(target) {
		add("placeholder", SeverityInfo,
			`@p\d+`,
			"MSSQL 风格命名参数 @p1（PG 驱动通常用 $1）",
			"PG 系改为 $n 位置参数")
	}

	return rules
}

// segment 一行内的连续"代码段"（已剔除字符串字面量与注释）
type segment struct {
	start int // 段起始列（0-based）
	text  string
}

// splitLineSegments 把一行切分为代码段，跳过单引号/双引号字符串（支持 ”/"" 转义）、
// 行注释（--、#）与块注释（/* */，状态跨行由 inBlock 维护）
func splitLineSegments(line string, inBlock *bool) []segment {
	var segs []segment
	var cur strings.Builder
	curStart := -1
	flush := func() {
		if curStart >= 0 && cur.Len() > 0 {
			segs = append(segs, segment{start: curStart, text: cur.String()})
		}
		cur.Reset()
		curStart = -1
	}

	i := 0
	for i < len(line) {
		c := line[i]
		if *inBlock {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				*inBlock = false
				i += 2
				continue
			}
			i++
			continue
		}
		if c == '\'' || c == '"' {
			flush()
			quote := c
			i++
			for i < len(line) {
				if line[i] == quote {
					if i+1 < len(line) && line[i+1] == quote {
						i += 2 // 转义引号
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}
		if c == '-' && i+1 < len(line) && line[i+1] == '-' {
			flush()
			return segs // 行注释，本行结束
		}
		if c == '#' {
			flush()
			return segs
		}
		if c == '/' && i+1 < len(line) && line[i+1] == '*' {
			flush()
			*inBlock = true
			i += 2
			continue
		}
		if curStart < 0 {
			curStart = i
		}
		cur.WriteByte(c)
		i++
	}
	flush()
	return segs
}

// Lint 对 SQL 文本做目标方言体检
func Lint(source, target types.DatabaseType, sqlText string) *Report {
	report := &Report{
		SourceDialect: string(source),
		TargetDialect: string(target),
	}
	if strings.TrimSpace(sqlText) == "" {
		return report
	}

	rules := buildRules(target)
	lines := strings.Split(sqlText, "\n")

	inBlockComment := false
	for lineNo, line := range lines {
		for _, seg := range splitLineSegments(line, &inBlockComment) {
			for col := 0; col < len(seg.text); {
				rest := seg.text[col:]
				matchedAny := false
				for _, r := range rules {
					loc := r.pattern.FindStringIndex(rest)
					if loc == nil {
						continue
					}
					hit := rest[loc[0]:loc[1]]
					snippet := hit
					if len(snippet) > 60 {
						snippet = snippet[:57] + "..."
					}
					report.Findings = append(report.Findings, Finding{
						Line:       lineNo + 1,
						Column:     seg.start + col + loc[0] + 1,
						Severity:   string(r.severity),
						Category:   r.category,
						Message:    r.message,
						Suggestion: r.suggestion,
						Snippet:    strings.TrimSpace(snippet),
					})
					switch r.severity {
					case SeverityError:
						report.Errors++
					case SeverityWarning:
						report.Warnings++
					case SeverityInfo:
						report.Infos++
					}
					if loc[1] > 0 {
						col += loc[1]
					} else {
						col++
					}
					matchedAny = true
					break
				}
				if !matchedAny {
					col++
				}
			}
		}
	}
	report.TotalLines = len(lines)
	return report
}

// Summarize 生成人类可读的摘要
func (r *Report) Summarize() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SQL 方言体检报告: %s → %s\n", r.SourceDialect, r.TargetDialect))
	sb.WriteString(fmt.Sprintf("共 %d 行, 发现 %d 处问题 (错误 %d / 警告 %d / 建议 %d)\n\n",
		r.TotalLines, len(r.Findings), r.Errors, r.Warnings, r.Infos))
	for _, f := range r.Findings {
		sb.WriteString(fmt.Sprintf("[%s] 第 %d 行第 %d 列 (%s): %s\n  片段: %s\n  建议: %s\n\n",
			f.Severity, f.Line, f.Column, f.Category, f.Message, f.Snippet, f.Suggestion))
	}
	if len(r.Findings) == 0 {
		sb.WriteString("未发现不兼容构造 ✓\n")
	}
	return sb.String()
}
