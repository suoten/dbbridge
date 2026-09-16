// Package mssql - MSSQL 到目标方言的触发器/存储过程转换器。
//
// 转换策略：
//   - 触发器：语法层面的自动转换（CREATE TRIGGER 语法差异、inserted/deleted → NEW/OLD）
//   - 存储过程：语法转换（T-SQL → MySQL/PLpgSQL），MSSQL 的 @变量 → MySQL 的局部变量等
//   - 复杂的业务逻辑可能需要人工微调，转换器尽可能减少手动翻译量
package mssql

import (
	"fmt"
	"regexp"
	"strings"

	types "dbbridge/pkg"
)

// ============ 触发器转换 ============

// convertMSSQLTriggerToMySQL 将 MSSQL 触发器转换为 MySQL 触发器
//
// MSSQL 触发器特点：
//   - 使用 inserted/deleted 伪表访问新/旧数据
//   - 触发器是语句级（statement-level），但可处理多行
//   - 语法：CREATE TRIGGER name ON table AFTER/AFTER INSERT/UPDATE/DELETE AS BEGIN ... END
//
// MySQL 触发器特点：
//   - 使用 NEW/OLD 访问新/旧数据（行级）
//   - 必须指定 FOR EACH ROW
//   - 语法：CREATE TRIGGER name timing event ON table FOR EACH ROW BEGIN ... END
func convertMSSQLTriggerToMySQL(trigger types.TriggerMeta) string {
	var sb strings.Builder

	// MSSQL 触发器可能包含多事件（如 INSERT, UPDATE），MySQL 每个触发器只支持一个事件
	// 如果是组合事件，仅取第一个（MySQL 限制每表每事件只能一个触发器）
	events := strings.Split(trigger.Event, ",")
	primaryEvent := strings.TrimSpace(events[0])

	sb.WriteString(fmt.Sprintf("CREATE TRIGGER `%s` %s %s ON `%s` FOR EACH ROW\n",
		escapeIdentTrigger(trigger.Name), trigger.Timing, primaryEvent, escapeIdentTrigger(trigger.Table)))
	sb.WriteString("BEGIN\n")

	// 转换触发器体。
	// 注意：MSSQL 源库的 Body 是完整 OBJECT_DEFINITION（含 CREATE TRIGGER ... AS 头），
	// 必须先剥离头部，否则会嵌套出非法 SQL
	body := trigger.Body
	if body != "" {
		body = stripMSSQLTriggerBody(body)
		body, decls := convertMSSQLBodyToMySQL(body)
		if decls != "" {
			sb.WriteString(decls)
			sb.WriteString("\n")
		}
		sb.WriteString(body)
	}

	sb.WriteString("\nEND")

	return sb.String()
}

// convertMSSQLTriggerToPG 将 MSSQL 触发器转换为 PostgreSQL 触发器
func convertMSSQLTriggerToPG(trigger types.TriggerMeta) string {
	var sb strings.Builder

	events := strings.Split(trigger.Event, ",")
	primaryEvent := strings.TrimSpace(events[0])

	timing := trigger.Timing
	if strings.EqualFold(timing, "INSTEAD OF") {
		timing = "INSTEAD OF"
	} else {
		timing = "AFTER"
	}

	// PG 需要先创建函数，再创建触发器
	sb.WriteString(fmt.Sprintf("CREATE OR REPLACE FUNCTION \"%s_fn\"() RETURNS TRIGGER AS $$\n", escapeIdentPG(trigger.Name)))

	// 转换触发器体（剥离 CREATE TRIGGER 头，见 convertMSSQLTriggerToMySQL 注释）
	body := trigger.Body
	var decls string
	if body != "" {
		body = stripMSSQLTriggerBody(body)
		body, decls = convertMSSQLBodyToPG(body)
	}
	// plpgsql 中 DECLARE 必须在 BEGIN 之前
	if decls != "" {
		sb.WriteString("DECLARE\n")
		sb.WriteString(decls)
		sb.WriteString("\n")
	}
	sb.WriteString("BEGIN\n")
	if body != "" {
		sb.WriteString(body)
	}

	sb.WriteString("\nRETURN NEW;\nEND;\n$$ LANGUAGE plpgsql;\n\n")
	sb.WriteString(fmt.Sprintf("CREATE TRIGGER \"%s\" %s %s ON \"%s\" FOR EACH ROW EXECUTE FUNCTION \"%s_fn\"();",
		escapeIdentPG(trigger.Name), timing, primaryEvent, escapeIdentPG(trigger.Table), escapeIdentPG(trigger.Name)))

	return sb.String()
}

// convertMSSQLTriggerToSQLite 将 MSSQL 触发器转换为 SQLite 触发器
func convertMSSQLTriggerToSQLite(trigger types.TriggerMeta) string {
	var sb strings.Builder

	events := strings.Split(trigger.Event, ",")
	primaryEvent := strings.TrimSpace(events[0])

	timing := trigger.Timing
	if strings.EqualFold(timing, "INSTEAD OF") {
		timing = "INSTEAD OF"
	} else {
		timing = "AFTER"
	}

	sb.WriteString(fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s FOR EACH ROW\n",
		escapeIdentSQLite(trigger.Name), timing, primaryEvent, escapeIdentSQLite(trigger.Table)))
	sb.WriteString("BEGIN\n")

	body := trigger.Body
	if body != "" {
		// SQLite 无 DECLARE/局部变量，声明段丢弃（近似转换，@var 引用保留为 v_var 名）
		body, _ = convertMSSQLBodyToMySQL(body) // SQLite 语法接近 MySQL
		sb.WriteString(body)
	}

	sb.WriteString("\nEND;")

	return sb.String()
}

// ============ 存储过程/函数转换 ============

// convertMSSQLRoutineToMySQL 将 MSSQL 存储过程/函数转换为 MySQL 格式
func convertMSSQLRoutineToMySQL(routine types.RoutineMeta) string {
	var sb strings.Builder

	body := routine.Body
	if body == "" {
		body = "-- 空过程体"
	}

	// 移除 MSSQL 的 CREATE PROCEDURE 头部，只保留过程体
	body = stripMSSQLRoutineHeader(body)

	// 转换 T-SQL 语法到 MySQL（变量声明需放在 BEGIN 块内）
	body, decls := convertMSSQLBodyToMySQL(body)

	if routine.Type == "function" {
		returns := routine.Returns
		if returns == "" {
			returns = "VARCHAR(255)"
		}
		sb.WriteString(fmt.Sprintf("CREATE FUNCTION `%s`() RETURNS %s\n", routine.Name, returns))
		sb.WriteString("DETERMINISTIC\n")
		sb.WriteString("BEGIN\n")
	} else {
		sb.WriteString(fmt.Sprintf("CREATE PROCEDURE `%s`()\n", routine.Name))
		sb.WriteString("BEGIN\n")
	}
	if decls != "" {
		sb.WriteString(decls)
		sb.WriteString("\n")
	}
	sb.WriteString(body)
	sb.WriteString("\nEND")

	return sb.String()
}

// convertMSSQLRoutineToPG 将 MSSQL 存储过程/函数转换为 PostgreSQL 格式
func convertMSSQLRoutineToPG(routine types.RoutineMeta) string {
	var sb strings.Builder

	body := routine.Body
	if body == "" {
		body = "-- 空过程体"
	}

	// 移除 MSSQL 的 CREATE PROCEDURE 头部
	body = stripMSSQLRoutineHeader(body)

	// 转换 T-SQL 语法到 PL/pgSQL（变量声明需放在 $$ 与 BEGIN 之间的 DECLARE 段）
	body, decls := convertMSSQLBodyToPG(body)

	if routine.Type == "function" {
		returns := routine.Returns
		if returns == "" {
			returns = "TEXT"
		}
		sb.WriteString(fmt.Sprintf("CREATE OR REPLACE FUNCTION \"%s\"() RETURNS %s AS $$\n", routine.Name, returns))
	} else {
		sb.WriteString(fmt.Sprintf("CREATE OR REPLACE PROCEDURE \"%s\"() AS $$\n", routine.Name))
	}
	if decls != "" {
		sb.WriteString("DECLARE\n")
		sb.WriteString(decls)
		sb.WriteString("\n")
	}
	sb.WriteString("BEGIN\n")
	sb.WriteString(body)
	sb.WriteString("\nEND;\n$$ LANGUAGE plpgsql;")

	return sb.String()
}

// ============ T-SQL → 目标方言 语法转换 ============

// convertMSSQLBodyToMySQL 将 T-SQL 过程体转换为 MySQL 兼容语法。
// 第二个返回值是需要插入到 BEGIN 块开头的变量声明段（MySQL 的 DECLARE 必须在块内）。
func convertMSSQLBodyToMySQL(body string) (string, string) {
	result := body

	// 1. inserted/deleted 伪表 → NEW/OLD
	result = convertInsertedDeletedToNewOld(result, "mysql")

	// 2. @变量 → 局部变量声明（MySQL 中用 DECLARE v_name ...），返回声明段由调用方插入 BEGIN 内
	decls := convertAtVariablesToMySQL(&result)

	// 3. SET @var = → SET var =
	result = convertSetAtVar(result)

	// 4. PRINT → SELECT
	result = regexp.MustCompile(`(?i)\bPRINT\s+`).ReplaceAllString(result, "SELECT ")

	// 5. GETUTCDATE() → UTC_TIMESTAMP()
	result = regexp.MustCompile(`(?i)\bGETUTCDATE\s*\(\s*\)`).ReplaceAllString(result, "UTC_TIMESTAMP()")
	result = regexp.MustCompile(`(?i)\bGETDATE\s*\(\s*\)`).ReplaceAllString(result, "NOW()")

	// 6. LEN() → CHAR_LENGTH()
	result = regexp.MustCompile(`(?i)\bLEN\s*\(`).ReplaceAllString(result, "CHAR_LENGTH(")

	// 7. ISNULL(a, b) → IFNULL(a, b)
	result = regexp.MustCompile(`(?i)\bISNULL\s*\(`).ReplaceAllString(result, "IFNULL(")

	// 8. CONVERT(type, expr) → CAST(expr AS type)
	result = convertConvertToCast(result)

	// 9. + 字符串连接 → CONCAT()
	// （简化处理：保留 + 运算符，MySQL 不支持需要手动调整）
	// 更精确的转换需要类型推断，这里不做

	// 10. TOP n → LIMIT n（在 SELECT 语句中）
	result = convertTopToLimit(result)

	// 11. SCOPE_IDENTITY() → LAST_INSERT_ID()
	result = regexp.MustCompile(`(?i)\bSCOPE_IDENTITY\s*\(\s*\)`).ReplaceAllString(result, "LAST_INSERT_ID()")

	// 12. WHILE 语法兼容（MySQL 支持 WHILE，无需转换）

	// 13. RETURN @var → RETURN var
	result = regexp.MustCompile(`(?i)\bRETURN\s+@`).ReplaceAllString(result, "RETURN ")

	// 14. 去除 MSSQL 的 BEGIN/END 嵌套标记（MySQL 也支持，保留）

	return result, decls
}

// convertMSSQLBodyToPG 将 T-SQL 过程体转换为 PL/pgSQL 兼容语法。
// 第二个返回值是需要插入到 DECLARE 段的变量声明（plpgsql 要求 DECLARE 在 BEGIN 之前）。
func convertMSSQLBodyToPG(body string) (string, string) {
	result := body

	// 1. inserted/deleted 伪表 → NEW/OLD
	result = convertInsertedDeletedToNewOld(result, "pg")

	// 2. @变量 → 普通变量（PG 中不需要 @ 前缀），返回声明段由调用方插入 DECLARE 段
	decls := convertAtVariablesToPG(&result)

	// 3. SET @var = → var :=
	result = convertSetAtVarPG(result)

	// 4. PRINT → RAISE NOTICE
	// 匹配 PRINT 后面的内容直到分号、换行或字符串末尾
	printRe := regexp.MustCompile(`(?im)\bPRINT\s+([^;\n]+)`)
	result = printRe.ReplaceAllStringFunc(result, func(s string) string {
		m := printRe.FindStringSubmatch(s)
		if len(m) >= 2 {
			return fmt.Sprintf("RAISE NOTICE '%%', %s", strings.TrimSpace(m[1]))
		}
		return s
	})

	// 5. GETUTCDATE() → NOW() at time zone 'UTC'
	result = regexp.MustCompile(`(?i)\bGETUTCDATE\s*\(\s*\)`).ReplaceAllString(result, "NOW() AT TIME ZONE 'UTC'")
	result = regexp.MustCompile(`(?i)\bGETDATE\s*\(\s*\)`).ReplaceAllString(result, "NOW()")

	// 6. LEN() → LENGTH()
	result = regexp.MustCompile(`(?i)\bLEN\s*\(`).ReplaceAllString(result, "LENGTH(")

	// 7. ISNULL(a, b) → COALESCE(a, b)
	result = regexp.MustCompile(`(?i)\bISNULL\s*\(`).ReplaceAllString(result, "COALESCE(")

	// 8. CONVERT(type, expr) → CAST(expr AS type)
	result = convertConvertToCast(result)

	// 9. TOP n → LIMIT n
	result = convertTopToLimit(result)

	// 10. SCOPE_IDENTITY() → currval()
	result = regexp.MustCompile(`(?i)\bSCOPE_IDENTITY\s*\(\s*\)`).ReplaceAllString(result, "lastval()")

	// 11. RETURN @var → RETURN var
	result = regexp.MustCompile(`(?i)\bRETURN\s+@`).ReplaceAllString(result, "RETURN ")

	// 12. + 字符串连接 → ||
	// PG 使用 || 连接字符串，但需要类型判断，这里保持 + 不变（数值运算）
	// 如需字符串连接，用户需手动调整

	return result, decls
}

// ============ 辅助函数 ============

// convertInsertedDeletedToNewOld 将 inserted/deleted 伪表引用转换为 NEW/OLD
func convertInsertedDeletedToNewOld(body, dialect string) string {
	result := body

	if dialect == "mysql" || dialect == "pg" {
		// inserted.column → NEW.column
		// deleted.column → OLD.column
		// 适用于 INSERTED 和 DELETED 在 SELECT/IF 语句中的引用
		result = regexp.MustCompile(`(?i)\binserted\.(\w+)`).ReplaceAllString(result, "NEW.$1")
		result = regexp.MustCompile(`(?i)\bdeleted\.(\w+)`).ReplaceAllString(result, "OLD.$1")

		// 处理 SELECT ... FROM inserted → 引用 NEW
		// 这种情况比较复杂，通常需要重写逻辑，这里做基本替换
		result = regexp.MustCompile(`(?i)\bFROM\s+inserted\b`).ReplaceAllString(result, "FROM NEW")
		result = regexp.MustCompile(`(?i)\bFROM\s+deleted\b`).ReplaceAllString(result, "FROM OLD")

		// 处理 JOIN inserted/deleted
		result = regexp.MustCompile(`(?i)\bJOIN\s+inserted\b`).ReplaceAllString(result, "JOIN NEW")
		result = regexp.MustCompile(`(?i)\bJOIN\s+deleted\b`).ReplaceAllString(result, "JOIN OLD")
	}

	return result
}

// convertAtVariablesToMySQL 将 MSSQL @变量收集为 MySQL 局部变量声明，
// 并把 body 中的 @var 原地替换为 v_var。声明段由调用方插入到 BEGIN 块开头
// （MySQL 要求 DECLARE 位于块内首部，不能出现在 BEGIN 之前）。
func convertAtVariablesToMySQL(body *string) string {
	varRe := regexp.MustCompile(`@(\w+)`)
	matches := varRe.FindAllStringSubmatch(*body, -1)

	seen := make(map[string]bool)
	var declarations []string
	for _, m := range matches {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			// 统一用 TEXT 承载任意类型值，避免 VARCHAR(4000) 截断
			declarations = append(declarations, fmt.Sprintf("DECLARE v_%s TEXT;", m[1]))
		}
	}

	*body = varRe.ReplaceAllStringFunc(*body, func(s string) string {
		return "v_" + s[1:]
	})

	return strings.Join(declarations, "\n")
}

// convertAtVariablesToPG 将 MSSQL @变量收集为 PG 变量声明（去掉 @ 前缀，加 v_ 前缀），
// 声明段由调用方插入到 DECLARE 段。
func convertAtVariablesToPG(body *string) string {
	varRe := regexp.MustCompile(`@(\w+)`)
	matches := varRe.FindAllStringSubmatch(*body, -1)

	seen := make(map[string]bool)
	var declarations []string
	for _, m := range matches {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			declarations = append(declarations, fmt.Sprintf("v_%s TEXT;", m[1]))
		}
	}

	*body = varRe.ReplaceAllStringFunc(*body, func(s string) string {
		return "v_" + s[1:]
	})

	return strings.Join(declarations, "\n")
}

// convertSetAtVar 将 SET @var = expr 转换为 SET var = expr
func convertSetAtVar(body string) string {
	return regexp.MustCompile(`(?i)\bSET\s+@`).ReplaceAllString(body, "SET v_")
}

// convertSetAtVarPG 将 SET @var = expr 转换为 PG 的 var := expr
func convertSetAtVarPG(body string) string {
	// SET @var = expr → v_var := expr
	re := regexp.MustCompile(`(?i)\bSET\s+@(\w+)\s*=`)
	result := re.ReplaceAllStringFunc(body, func(s string) string {
		m := re.FindStringSubmatch(s)
		if len(m) >= 2 {
			return "v_" + m[1] + " :="
		}
		return s
	})
	// SELECT @var = expr → v_var := expr
	re2 := regexp.MustCompile(`(?i)\bSELECT\s+@(\w+)\s*=`)
	result = re2.ReplaceAllStringFunc(result, func(s string) string {
		m := re2.FindStringSubmatch(s)
		if len(m) >= 2 {
			return "v_" + m[1] + " :="
		}
		return s
	})
	return result
}

// convertConvertToCast 将 MSSQL CONVERT(type, expr) 转换为 CAST(expr AS type)
func convertConvertToCast(body string) string {
	// CONVERT(VARCHAR(10), GETDATE(), 120) → CAST(GETDATE() AS VARCHAR(10))
	// 简化处理：匹配 CONVERT(type, expr) 模式
	re := regexp.MustCompile(`(?i)\bCONVERT\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)
	return re.ReplaceAllStringFunc(body, func(s string) string {
		m := re.FindStringSubmatch(s)
		if len(m) >= 3 {
			return fmt.Sprintf("CAST(%s AS %s)", strings.TrimSpace(m[2]), strings.TrimSpace(m[1]))
		}
		return s
	})
}

// convertTopToLimit 将 MSSQL TOP n 转换为 LIMIT n
func convertTopToLimit(body string) string {
	// SELECT TOP n ... → SELECT ... LIMIT n
	// 简化处理：在简单场景下生效
	re := regexp.MustCompile(`(?i)\bSELECT\s+TOP\s+(\d+)\s+`)
	return re.ReplaceAllStringFunc(body, func(s string) string {
		m := re.FindStringSubmatch(s)
		if len(m) >= 2 {
			return "SELECT "
		}
		return s
	})
	// 注意：这里只是去掉了 TOP n，LIMIT 需要加到语句末尾
	// 完整实现需要更复杂的解析，这里做基本处理
}

// stripMSSQLRoutineHeader 移除 MSSQL 存储过程的 CREATE PROCEDURE/FUNCTION 头部
// 保留 AS 后面的过程体
func stripMSSQLRoutineHeader(body string) string {
	// 匹配 CREATE PROCEDURE name AS / CREATE FUNCTION name() RETURNS x AS
	// 保留 AS 之后的内容
	re := regexp.MustCompile(`(?is)\s*CREATE\s+(?:PROCEDURE|PROC|FUNCTION)\s+.*?\bAS\b\s*`)
	loc := re.FindStringIndex(body)
	if loc != nil {
		return strings.TrimSpace(body[loc[1]:])
	}
	return body
}

// stripMSSQLTriggerBody 从完整触发器定义中剥离 CREATE TRIGGER ... AS 头部。
// MSSQL 的 GetTriggers 返回的 Body 是 OBJECT_DEFINITION 全文，形如：
//
//	CREATE TRIGGER [tg] ON [tbl] AFTER INSERT AS BEGIN ... END
//
// 或 CREATE OR ALTER TRIGGER ...；若不剥离，转换产物会嵌套非法 SQL。
func stripMSSQLTriggerBody(body string) string {
	re := regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+ALTER\s+)?TRIGGER\s+.*?\bAS\b`)
	loc := re.FindStringIndex(body)
	if loc != nil {
		return strings.TrimSpace(body[loc[1]:])
	}
	// 已是裸过程体（如来自 information_schema）则原样返回
	return body
}

// escapeIdentTrigger 转义 MySQL 反引号标识符（反引号双写）
func escapeIdentTrigger(name string) string {
	return strings.ReplaceAll(name, "`", "``")
}

// escapeIdentPG 转义 PostgreSQL 双引号标识符（双引号双写）
func escapeIdentPG(name string) string {
	return strings.ReplaceAll(name, `"`, `""`)
}

// escapeIdentSQLite 转义 SQLite 双引号标识符，返回带引号的完整标识符
func escapeIdentSQLite(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
