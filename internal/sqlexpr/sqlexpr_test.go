package sqlexpr

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

func TestRewriteMSSQLToMySQL(t *testing.T) {
	in := "SELECT TOP 10 id, name FROM users WHERE created_at > GETDATE() - 1 ORDER BY id;"
	out, changes := Rewrite(types.MSSQL, types.MySQL, in)
	if !strings.Contains(out, "LIMIT 10") {
		t.Errorf("TOP 未改写为 LIMIT: %s", out)
	}
	if strings.Contains(out, "TOP 10") {
		t.Errorf("TOP 子句未移除: %s", out)
	}
	if !strings.Contains(out, "NOW()") || strings.Contains(out, "GETDATE()") {
		t.Errorf("GETDATE 未改写: %s", out)
	}
	if !strings.Contains(out, "ORDER BY id LIMIT 10") {
		t.Errorf("LIMIT 应追加在语句末尾（ORDER BY 之后）: %s", out)
	}
	if len(changes) < 2 {
		t.Errorf("变更清单不完整: %v", changes)
	}
}

func TestRewriteMSSQLToPG(t *testing.T) {
	in := "SELECT ISNULL(status, 'unknown') AS s, [order].amount FROM [order] WHERE GETDATE() > created;"
	out, _ := Rewrite(types.MSSQL, types.PostgreSQL, in)
	if !strings.Contains(out, "COALESCE(status, 'unknown')") {
		t.Errorf("ISNULL 未改写: %s", out)
	}
	if !strings.Contains(out, `"order".amount`) || !strings.Contains(out, `FROM "order"`) {
		t.Errorf("方括号标识符未改写: %s", out)
	}
	if !strings.Contains(out, "CURRENT_TIMESTAMP") {
		t.Errorf("GETDATE 未改写为 CURRENT_TIMESTAMP: %s", out)
	}
}

func TestRewriteMySQLToPG(t *testing.T) {
	in := "SELECT `id`, IFNULL(name, 'x'), NVL(code, 0) FROM `users`;"
	out, changes := Rewrite(types.MySQL, types.PostgreSQL, in)
	if !strings.Contains(out, `"id"`) || strings.Contains(out, "`") {
		t.Errorf("反引号未改写: %s", out)
	}
	if !strings.Contains(out, "COALESCE(name, 'x')") {
		t.Errorf("IFNULL 未改写: %s", out)
	}
	if !strings.Contains(out, "COALESCE(code, 0)") {
		t.Errorf("NVL 未改写: %s", out)
	}
	foundIfNull, foundNvl := false, false
	for _, c := range changes {
		if strings.HasPrefix(c, "IFNULL") {
			foundIfNull = true
		}
		if strings.HasPrefix(c, "NVL") {
			foundNvl = true
		}
	}
	if !foundIfNull || !foundNvl {
		t.Errorf("变更清单缺项: %v", changes)
	}
}

// 字符串字面量与注释中的内容绝不能被改写
func TestRewriteSkipsStringsAndComments(t *testing.T) {
	in := "SELECT note, remark FROM t WHERE note = 'GETDATE() TOP 10' -- 注释 ISNULL(a,b)\n  AND remark = \"IFNULL(x,y)\";"
	out, changes := Rewrite(types.MSSQL, types.MySQL, in)
	if !strings.Contains(out, "'GETDATE() TOP 10'") {
		t.Errorf("字符串字面量被改写: %s", out)
	}
	if !strings.Contains(out, `"IFNULL(x,y)"`) {
		t.Errorf("双引号字符串被改写: %s", out)
	}
	if !strings.Contains(out, "-- 注释 ISNULL(a,b)") {
		t.Errorf("注释被改写: %s", out)
	}
	for _, c := range changes {
		if strings.HasPrefix(c, "ISNULL") || strings.HasPrefix(c, "IFNULL") {
			t.Errorf("不应改写字符串/注释中的函数: %v", changes)
		}
	}
}

// TOP n PERCENT / WITH TIES 无法安全改写，必须跳过
func TestRewriteSkipsUnsafeTop(t *testing.T) {
	in := "SELECT TOP 5 PERCENT id FROM t;"
	out, changes := Rewrite(types.MSSQL, types.MySQL, in)
	if !strings.Contains(out, "TOP 5 PERCENT") {
		t.Errorf("PERCENT 形式不应被改写: %s", out)
	}
	if len(changes) != 0 {
		t.Errorf("不应有变更: %v", changes)
	}
}

// 含分号的字符串字面量不能被切分语句
func TestSplitStatementsKeepsSemicolonInString(t *testing.T) {
	stmts := splitStatements("SELECT 'a;b' FROM t; SELECT 2;")
	if len(stmts) != 2 {
		t.Fatalf("语句切分错误: %v", stmts)
	}
	if !strings.Contains(stmts[0], "'a;b'") {
		t.Errorf("字符串内分号被切断: %q", stmts[0])
	}
}

// 多 TOP 子句（子查询分页）不做改写
func TestRewriteSkipsMultipleTop(t *testing.T) {
	in := "SELECT * FROM (SELECT TOP 5 id FROM a) x, (SELECT TOP 3 id FROM b) y;"
	out, changes := Rewrite(types.MSSQL, types.MySQL, in)
	if !strings.Contains(out, "TOP 5") || !strings.Contains(out, "TOP 3") {
		t.Errorf("多 TOP 语句不应被部分改写: %s", out)
	}
	if len(changes) != 0 {
		t.Errorf("不应有变更: %v", changes)
	}
}

func TestRewriteSameDialectNoop(t *testing.T) {
	in := "SELECT ISNULL(a, b) FROM [t];"
	out, changes := Rewrite(types.MSSQL, types.MSSQL, in)
	if out != in || len(changes) != 0 {
		t.Errorf("同方言不应改写: %s %v", out, changes)
	}
}

func TestRewriteGetDateToSQLite(t *testing.T) {
	out, _ := Rewrite(types.MSSQL, types.SQLite, "SELECT GETDATE();")
	if !strings.Contains(out, "CURRENT_TIMESTAMP") {
		t.Errorf("SQLite 目标应使用 CURRENT_TIMESTAMP: %s", out)
	}
}

// MySQL 目标的标识符应转反引号（MySQL 默认 sql_mode 不认双引号标识符）
func TestRewriteBacktickForMySQLTarget(t *testing.T) {
	out, _ := Rewrite(types.MSSQL, types.MySQL, "SELECT TOP 5 id FROM [users];")
	if !strings.Contains(out, "`users`") {
		t.Errorf("MySQL 目标应转反引号: %s", out)
	}
	if !strings.Contains(out, "LIMIT 5") {
		t.Errorf("TOP 未改写: %s", out)
	}
}
