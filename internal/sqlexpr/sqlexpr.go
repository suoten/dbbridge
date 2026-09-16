// Package sqlexpr 提供 SQL 表达式级的方言批量改写。
//
// 定位：与 sqllint（只报告不改写）互补。这里只做【语义等价且目标库确定支持】的
// 安全改写，例如 GETDATE()→NOW()、ISNULL(a,b)→COALESCE(a,b)、TOP n→LIMIT n、
// 标识符引号风格转换；无法安全自动改写的构造（取自增值、UPSERT、字符串拼接等）
// 一律不动，交给 sqllint 报告由人工确认。
//
// 所有替换都只作用于"代码区间"：字符串字面量（单/双引号，含 ”/"" 与反斜杠转义）
// 与注释（--、#、/* */）中的内容绝不会被改写，避免把业务数据
// （如备注里的 'GETDATE()'）改坏。
package sqlexpr

import (
	"fmt"
	"regexp"
	"strings"

	types "dbbridge/pkg"
)

// isPGFamily / isMySQLFamily / isMSSQL 方言分组
func isPGFamily(t types.DatabaseType) bool {
	switch t {
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB, types.TimescaleDB:
		return true
	}
	return false
}

func isMySQLFamily(t types.DatabaseType) bool {
	switch t {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase, types.PolarDB, types.Aurora:
		return true
	}
	return false
}

func isMSSQL(t types.DatabaseType) bool { return t == types.MSSQL }

// textState 全文扫描状态（跨语句/跨行维护）
type textState struct {
	inSingleQuote bool
	inDoubleQuote bool
	inLineComment bool
	inBlock       bool
}

// advance 处理 s[i] 处的字符并更新状态，返回 true 表示该字符属于代码区间。
// 按字节处理（SQL 关键字、引号、分号均为 ASCII；UTF-8 多字节序列的中间字节
// 高位为 1，不会命中任何 ASCII case，不会误判）。
func (st *textState) advance(s string, i int) bool {
	c := s[i]
	switch {
	case st.inLineComment:
		if c == '\n' {
			st.inLineComment = false
		}
		return false
	case st.inBlock:
		if c == '*' && i+1 < len(s) && s[i+1] == '/' {
			st.inBlock = false
			return false
		}
		return false
	case st.inSingleQuote:
		if c == '\\' && i+1 < len(s) { // MySQL 反斜杠转义
			return false
		}
		if c == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' { // '' 转义
				return false
			}
			st.inSingleQuote = false
		}
		return false
	case st.inDoubleQuote:
		if c == '"' {
			if i+1 < len(s) && s[i+1] == '"' {
				return false
			}
			st.inDoubleQuote = false
		}
		return false
	}

	switch c {
	case '\'':
		st.inSingleQuote = true
		return false
	case '"':
		st.inDoubleQuote = true
		return false
	case '-':
		if i+1 < len(s) && s[i+1] == '-' {
			st.inLineComment = true
			return false
		}
	case '#':
		st.inLineComment = true
		return false
	case '/':
		if i+1 < len(s) && s[i+1] == '*' {
			st.inBlock = true
			return false
		}
	}
	return true
}

// codeSpans 返回文本中所有代码区间的 [start, end) 列表（排除字符串与注释内部）。
func codeSpans(s string) [][2]int {
	var spans [][2]int
	st := &textState{}
	spanStart := -1
	flush := func(end int) {
		if spanStart >= 0 && end > spanStart {
			spans = append(spans, [2]int{spanStart, end})
		}
		spanStart = -1
	}
	i := 0
	for i < len(s) {
		if st.advance(s, i) {
			if spanStart < 0 {
				spanStart = i
			}
		} else {
			flush(i)
		}
		i++
	}
	flush(len(s))
	return spans
}

// inCode 判断 [start, end) 区间是否完整落在某个代码区间内
func inCode(spans [][2]int, start, end int) bool {
	for _, sp := range spans {
		if start >= sp[0] && end <= sp[1] {
			return true
		}
	}
	return false
}

// replaceInCode 只在代码区间内应用正则替换，返回新文本与替换次数。
// repl 接收完整匹配文本与子匹配文本列表，返回替换文本；返回 "" 表示本次跳过不改。
func replaceInCode(s string, re *regexp.Regexp, repl func(match string, groups []string) string) (string, int) {
	spans := codeSpans(s)
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
		if !inCode(spans, loc[0], loc[1]) {
			continue
		}
		var groups []string
		for g := 2; g < len(loc); g += 2 {
			if loc[g] >= 0 {
				groups = append(groups, s[loc[g]:loc[g+1]])
			}
		}
		text := repl(s[loc[0]:loc[1]], groups)
		if text == "" {
			continue
		}
		edits = append(edits, edit{start: loc[0], end: loc[1], text: text})
	}
	if len(edits) == 0 {
		return s, 0
	}
	var sb strings.Builder
	last := 0
	for _, e := range edits {
		sb.WriteString(s[last:e.start])
		sb.WriteString(e.text)
		last = e.end
	}
	sb.WriteString(s[last:])
	return sb.String(), len(edits)
}

// splitStatements 按顶层分号切分语句（引号/注释中的分号不切分）。
// 每条语句保留原文（不含末尾分号）。
func splitStatements(sql string) []string {
	var stmts []string
	var cur strings.Builder
	st := &textState{}
	i := 0
	for i < len(sql) {
		isCode := st.advance(sql, i)
		if isCode && sql[i] == ';' {
			stmts = append(stmts, cur.String())
			cur.Reset()
			i++
			continue
		}
		cur.WriteByte(sql[i])
		i++
	}
	if strings.TrimSpace(cur.String()) != "" {
		stmts = append(stmts, cur.String())
	}
	return stmts
}

var (
	backtickPairRe = regexp.MustCompile("`([^`\n]+)`")
	bracketPairRe  = regexp.MustCompile(`\[([A-Za-z_][^\[\]\n]*)\]`)
	getDateRe      = regexp.MustCompile(`\bGETDATE\s*\(\s*\)`)
	topUnsafeRe    = regexp.MustCompile(`\bTOP\s+\d+\s+(?:PERCENT|WITH\s+TIES)`)
	topNumRe       = regexp.MustCompile(`\bTOP\s+(\d+)\b`)
	isNullFnRe     = regexp.MustCompile(`\bISNULL\s*\(`)
	ifNullFnRe     = regexp.MustCompile(`\bIFNULL\s*\(`)
	nvlFnRe        = regexp.MustCompile(`\bNVL\s*\(`)
	spacesRe       = regexp.MustCompile(`[ \t]{2,}`)
)

// Rewrite 对 SQL 文本做安全的表达式级方言改写，返回改写后的文本与变更清单。
// 只改写语义等价的构造；改不了的留给 sqllint 报告。
func Rewrite(source, target types.DatabaseType, sqlText string) (string, []string) {
	if strings.TrimSpace(sqlText) == "" || source == target {
		return sqlText, nil
	}
	changeCount := map[string]int{}
	record := func(name string, n int) { changeCount[name] += n }

	stmts := splitStatements(sqlText)
	out := make([]string, 0, len(stmts))
	for _, stmt := range stmts {
		out = append(out, rewriteStatement(source, target, stmt, record))
	}
	result := strings.Join(out, ";\n")
	if strings.HasSuffix(strings.TrimRight(sqlText, " \t\r\n"), ";") {
		result += ";"
	}

	var changes []string
	for _, name := range []string{
		"TOP n → LIMIT n",
		"GETDATE() → " + nowFunc(target),
		"ISNULL(a, b) → COALESCE(a, b)",
		"IFNULL(a, b) → COALESCE(a, b)",
		"NVL(a, b) → COALESCE(a, b)",
		"标识符引号风格转换",
	} {
		if n := changeCount[name]; n > 0 {
			changes = append(changes, fmt.Sprintf("%s（%d 处）", name, n))
		}
	}
	return result, changes
}

// nowFunc 按目标方言返回"当前时间"函数
func nowFunc(target types.DatabaseType) string {
	switch {
	case isMySQLFamily(target):
		return "NOW()"
	case isPGFamily(target), target == types.SQLite:
		return "CURRENT_TIMESTAMP"
	case target == types.Dameng:
		return "SYSDATE"
	default:
		return "CURRENT_TIMESTAMP"
	}
}

// quoteFn 目标方言的标识符引号（PG 系/SQLite 双引号，MySQL 系反引号，MSSQL 方括号）
func quoteFn(target types.DatabaseType) string {
	if isMSSQL(target) {
		return "]"
	}
	if isMySQLFamily(target) {
		return "`"
	}
	return `"`
}

// rewriteStatement 对单条语句应用全部改写规则
func rewriteStatement(source, target types.DatabaseType, stmt string, record func(string, int)) string {
	// 1. MSSQL TOP n → LIMIT n（PERCENT/WITH TIES 无法安全改写，跳过）
	if isMSSQL(source) && !isMSSQL(target) && !topUnsafeRe.MatchString(stmt) {
		stmt = rewriteTop(stmt, target, record)
	}

	// 2. 当前时间函数（GETDATE 为 MSSQL 函数）
	if isMSSQL(source) && !isMSSQL(target) {
		repl := nowFunc(target)
		var n int
		stmt, n = replaceInCode(stmt, getDateRe, func(string, []string) string { return repl })
		record("GETDATE() → "+repl, n)
	}

	// 3. NULL 兜底函数 → COALESCE（所有库都支持，两参语义等价，仅改函数名）
	if (isMSSQL(source) || source == types.Dameng) && !isMSSQL(target) {
		stmt = renameFn(stmt, isNullFnRe, "COALESCE(", record, "ISNULL(a, b) → COALESCE(a, b)")
	}
	if isMySQLFamily(source) && !isMySQLFamily(target) {
		stmt = renameFn(stmt, ifNullFnRe, "COALESCE(", record, "IFNULL(a, b) → COALESCE(a, b)")
	}
	if isMySQLFamily(source) || source == types.Dameng || isMSSQL(source) {
		stmt = renameFn(stmt, nvlFnRe, "COALESCE(", record, "NVL(a, b) → COALESCE(a, b)")
	}

	// 4. 标识符引号风格
	if !isMySQLFamily(target) {
		quote := quoteFn(target)
		var n int
		stmt, n = replaceInCode(stmt, backtickPairRe, func(m string, groups []string) string {
			return quote + groups[0] + quote
		})
		record("标识符引号风格转换", n)
	}
	if isMSSQL(source) && !isMSSQL(target) {
		quote := quoteFn(target)
		var n int
		stmt, n = replaceInCode(stmt, bracketPairRe, func(m string, groups []string) string {
			return quote + groups[0] + quote
		})
		record("标识符引号风格转换", n)
	}

	return stmt
}

// renameFn 仅替换函数名本身（函数名后跟左括号），参数原样保留
func renameFn(stmt string, re *regexp.Regexp, replacement string, record func(string, int), name string) string {
	stmt, n := replaceInCode(stmt, re, func(string, []string) string { return replacement })
	record(name, n)
	return stmt
}

// rewriteTop 把语句中唯一的 "SELECT TOP n" 去掉并在语句末尾追加 " LIMIT n"。
// 追加在末尾对无 ORDER BY 与有 ORDER BY 的语句都成立（LIMIT 必须在 ORDER BY 之后）。
// 含多个 TOP（子查询分页）时不做改写，留给人工处理。
func rewriteTop(stmt string, target types.DatabaseType, record func(string, int)) string {
	spans := codeSpans(stmt)
	var hits [][]int
	for _, loc := range topNumRe.FindAllStringSubmatchIndex(stmt, -1) {
		if inCode(spans, loc[0], loc[1]) {
			hits = append(hits, loc)
		}
	}
	if len(hits) != 1 {
		return stmt
	}
	loc := hits[0]
	body := stmt[:loc[0]] + stmt[loc[1]:]
	body = spacesRe.ReplaceAllString(body, " ")
	body = strings.TrimRight(body, " \t\r\n")
	record("TOP n → LIMIT n", 1)
	return body + " LIMIT " + stmt[loc[2]:loc[3]]
}
