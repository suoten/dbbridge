package sqllint

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

func TestLintBackticksToPG(t *testing.T) {
	sql := "SELECT `name` FROM `users` WHERE `id` = 1"
	r := Lint(types.MySQL, types.PostgreSQL, sql)
	if r.Errors == 0 {
		t.Fatal("反引号在 PG 目标应报 error")
	}
	found := false
	for _, f := range r.Findings {
		if f.Category == "quote" && f.Line == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("应包含 quote 类发现, got: %+v", r.Findings)
	}
}

func TestLintLimitToMSSQL(t *testing.T) {
	sql := "SELECT * FROM orders ORDER BY id LIMIT 20"
	r := Lint(types.MySQL, types.MSSQL, sql)
	if r.Errors == 0 {
		t.Fatal("LIMIT 在 MSSQL 目标应报 error")
	}
	if !strings.Contains(r.Findings[0].Suggestion, "TOP") {
		t.Errorf("建议应包含 TOP, got: %s", r.Findings[0].Suggestion)
	}
}

func TestLintLimitOffsetCountToPG(t *testing.T) {
	sql := "SELECT * FROM t LIMIT 20, 10"
	r := Lint(types.MySQL, types.PostgreSQL, sql)
	found := false
	for _, f := range r.Findings {
		if f.Category == "pagination" && strings.Contains(f.Message, "双参数") {
			found = true
		}
	}
	if !found {
		t.Errorf("应检出 LIMIT offset,count 双参数, got: %+v", r.Findings)
	}
}

func TestLintOnDuplicateKeyToPG(t *testing.T) {
	sql := "INSERT INTO t (a, b) VALUES (1, 2) ON DUPLICATE KEY UPDATE b = b + 1"
	r := Lint(types.MySQL, types.PostgreSQL, sql)
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f.Message, "ON DUPLICATE KEY") {
			found = true
		}
	}
	if !found {
		t.Errorf("应检出 ON DUPLICATE KEY UPDATE, got: %+v", r.Findings)
	}
}

func TestLintOnConflictToMySQL(t *testing.T) {
	sql := "INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO NOTHING"
	r := Lint(types.PostgreSQL, types.MySQL, sql)
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f.Message, "ON CONFLICT") {
			found = true
		}
	}
	if !found {
		t.Errorf("反向应检出 ON CONFLICT, got: %+v", r.Findings)
	}
}

func TestLintFunctions(t *testing.T) {
	// GETDATE / SCOPE_IDENTITY → PG 目标应报错
	sql := "SELECT GETDATE(), SCOPE_IDENTITY(), IFNULL(a, 0) FROM t"
	r := Lint(types.MSSQL, types.PostgreSQL, sql)
	kinds := map[string]bool{}
	for _, f := range r.Findings {
		kinds[f.Category] = true
		if f.Category == "function" && strings.Contains(f.Message, "ISNULL") {
			t.Errorf("ISNULL 提示应只出现一次且为 warning, got severity=%s", f.Severity)
		}
	}
	if !kinds["function"] {
		t.Errorf("应检出 function 类问题, got: %+v", r.Findings)
	}
}

func TestLintStringLiteralNotFlagged(t *testing.T) {
	// 字符串字面量里的 LIMIT/GETDATE 不应误报
	sql := "SELECT * FROM t WHERE remark = 'please use LIMIT 10 and GETDATE() here'"
	r := Lint(types.MySQL, types.PostgreSQL, sql)
	for _, f := range r.Findings {
		if f.Category == "pagination" || f.Category == "function" {
			t.Errorf("字符串字面量被误报: %+v", f)
		}
	}
}

func TestLintCommentsSkipped(t *testing.T) {
	sql := "-- LIMIT 10 in comment\n/* GETDATE() in block */\nSELECT 1"
	r := Lint(types.MySQL, types.PostgreSQL, sql)
	if len(r.Findings) != 0 {
		t.Errorf("注释内容不应报, got: %+v", r.Findings)
	}
}

func TestLintLastInsertIdToMSSQL(t *testing.T) {
	sql := "INSERT INTO t (a) VALUES (1); SELECT LAST_INSERT_ID()"
	r := Lint(types.MySQL, types.MSSQL, sql)
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f.Message, "LAST_INSERT_ID") {
			found = true
		}
	}
	if !found {
		t.Errorf("应检出 LAST_INSERT_ID, got: %+v", r.Findings)
	}
}

func TestLintLineNumbers(t *testing.T) {
	sql := "SELECT 1\nSELECT `a` FROM `b`\nSELECT 3"
	r := Lint(types.MySQL, types.PostgreSQL, sql)
	if len(r.Findings) == 0 || r.Findings[0].Line != 2 {
		t.Errorf("发现的行号应为 2, got: %+v", r.Findings)
	}
}

func TestLintSameDialectStillFlags(t *testing.T) {
	// 即使方言相同（如用户复制了别处 SQL），规则基于目标方言仍然生效
	r := Lint(types.MySQL, types.MySQL, "SELECT `a` FROM `b`")
	if len(r.Findings) != 0 {
		t.Errorf("MySQL 目标不应报反引号, got: %+v", r.Findings)
	}
}

func TestLintEmptyInput(t *testing.T) {
	r := Lint(types.MySQL, types.PostgreSQL, "   \n  ")
	if len(r.Findings) != 0 {
		t.Errorf("空输入不应有发现, got: %+v", r.Findings)
	}
}

func TestSummarize(t *testing.T) {
	r := Lint(types.MySQL, types.PostgreSQL, "SELECT `a`, GETDATE() FROM t LIMIT 10")
	s := r.Summarize()
	if !strings.Contains(s, "方言体检报告") {
		t.Errorf("摘要格式异常: %s", s)
	}
}
