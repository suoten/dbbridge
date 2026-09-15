package mssql

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// ============ 触发器转换测试 ============

func TestConvertMSSQLTriggerToMySQL(t *testing.T) {
	trigger := types.TriggerMeta{
		Name:   "trg_audit",
		Event:  "INSERT, UPDATE",
		Timing: "AFTER",
		Table:  "users",
		Body: `BEGIN
    INSERT INTO audit_log (user_id, action) VALUES (inserted.id, 'INSERT')
END`,
	}

	result := convertMSSQLTriggerToMySQL(trigger)

	// 应生成 MySQL 语法
	if !strings.Contains(result, "CREATE TRIGGER") {
		t.Errorf("缺少 CREATE TRIGGER: %s", result)
	}
	if !strings.Contains(result, "FOR EACH ROW") {
		t.Errorf("缺少 FOR EACH ROW: %s", result)
	}
	// 多事件取第一个
	if !strings.Contains(result, "AFTER INSERT") {
		t.Errorf("未取第一个事件 INSERT: %s", result)
	}
	// inserted → NEW
	if !strings.Contains(result, "NEW.id") {
		t.Errorf("inserted.id 未转换为 NEW.id: %s", result)
	}
	if strings.Contains(result, "inserted.") {
		t.Errorf("仍包含 inserted.: %s", result)
	}
}

func TestConvertMSSQLTriggerToPG(t *testing.T) {
	trigger := types.TriggerMeta{
		Name:   "trg_audit",
		Event:  "DELETE",
		Timing: "AFTER",
		Table:  "users",
		Body: `BEGIN
    INSERT INTO audit_log (user_id, action) VALUES (deleted.id, 'DELETE')
END`,
	}

	result := convertMSSQLTriggerToPG(trigger)

	// PG 需要先建函数再建触发器
	if !strings.Contains(result, "CREATE OR REPLACE FUNCTION") {
		t.Errorf("缺少 CREATE FUNCTION: %s", result)
	}
	if !strings.Contains(result, "LANGUAGE plpgsql") {
		t.Errorf("缺少 LANGUAGE plpgsql: %s", result)
	}
	if !strings.Contains(result, "EXECUTE FUNCTION") {
		t.Errorf("缺少 EXECUTE FUNCTION: %s", result)
	}
	// deleted → OLD
	if !strings.Contains(result, "OLD.id") {
		t.Errorf("deleted.id 未转换为 OLD.id: %s", result)
	}
}

func TestConvertMSSQLTriggerToSQLite(t *testing.T) {
	trigger := types.TriggerMeta{
		Name:   "trg_log",
		Event:  "INSERT",
		Timing: "AFTER",
		Table:  "orders",
		Body:   "INSERT INTO log VALUES (inserted.id, 'new')",
	}

	result := convertMSSQLTriggerToSQLite(trigger)

	if !strings.Contains(result, "CREATE TRIGGER") {
		t.Errorf("缺少 CREATE TRIGGER: %s", result)
	}
	if !strings.Contains(result, "FOR EACH ROW") {
		t.Errorf("缺少 FOR EACH ROW: %s", result)
	}
	if !strings.Contains(result, "NEW.id") {
		t.Errorf("inserted.id 未转换为 NEW.id: %s", result)
	}
}

func TestConvertMSSQLTriggerInsteadOf(t *testing.T) {
	trigger := types.TriggerMeta{
		Name:   "trg_view",
		Event:  "INSERT",
		Timing: "INSTEAD OF",
		Table:  "v_users",
		Body:   "INSERT INTO real_table VALUES (inserted.id)",
	}

	// MySQL
	mysqlDDL := convertMSSQLTriggerToMySQL(trigger)
	if !strings.Contains(mysqlDDL, "INSTEAD OF") {
		t.Errorf("MySQL: INSTEAD OF 未保留: %s", mysqlDDL)
	}

	// PG
	pgDDL := convertMSSQLTriggerToPG(trigger)
	if !strings.Contains(pgDDL, "INSTEAD OF") {
		t.Errorf("PG: INSTEAD OF 未保留: %s", pgDDL)
	}

	// SQLite
	sqliteDDL := convertMSSQLTriggerToSQLite(trigger)
	if !strings.Contains(sqliteDDL, "INSTEAD OF") {
		t.Errorf("SQLite: INSTEAD OF 未保留: %s", sqliteDDL)
	}
}

// ============ 存储过程转换测试 ============

func TestConvertMSSQLRoutineToMySQL(t *testing.T) {
	routine := types.RoutineMeta{
		Name: "sp_update_score",
		Type: "procedure",
		Body: `CREATE PROCEDURE sp_update_score AS
BEGIN
    DECLARE @score INT
    SET @score = 100
    UPDATE users SET score = @score WHERE id = 1
    PRINT 'Updated'
END`,
	}

	result := convertMSSQLRoutineToMySQL(routine)

	// 应生成 MySQL 存储过程
	if !strings.Contains(result, "CREATE PROCEDURE") {
		t.Errorf("缺少 CREATE PROCEDURE: %s", result)
	}
	// @变量应被转换
	if strings.Contains(result, "@score") {
		t.Errorf("仍包含 @score: %s", result)
	}
	if !strings.Contains(result, "v_score") {
		t.Errorf("未转换为 v_score: %s", result)
	}
	// PRINT → SELECT
	if strings.Contains(result, "PRINT") {
		t.Errorf("PRINT 未转换: %s", result)
	}
}

func TestConvertMSSQLRoutineToPG(t *testing.T) {
	routine := types.RoutineMeta{
		Name: "sp_update_score",
		Type: "procedure",
		Body: `CREATE PROCEDURE sp_update_score AS
BEGIN
    DECLARE @score INT
    SET @score = 100
    UPDATE users SET score = @score WHERE id = 1
END`,
	}

	result := convertMSSQLRoutineToPG(routine)

	if !strings.Contains(result, "CREATE OR REPLACE PROCEDURE") {
		t.Errorf("缺少 CREATE PROCEDURE: %s", result)
	}
	if !strings.Contains(result, "LANGUAGE plpgsql") {
		t.Errorf("缺少 LANGUAGE plpgsql: %s", result)
	}
	// @变量应被转换
	if strings.Contains(result, "@score") {
		t.Errorf("仍包含 @score: %s", result)
	}
}

func TestConvertMSSQLRoutineFunctionToMySQL(t *testing.T) {
	routine := types.RoutineMeta{
		Name:    "fn_get_count",
		Type:    "function",
		Returns: "INT",
		Body: `CREATE FUNCTION fn_get_count() RETURNS INT AS
BEGIN
    DECLARE @cnt INT
    SELECT @cnt = COUNT(*) FROM users
    RETURN @cnt
END`,
	}

	result := convertMSSQLRoutineToMySQL(routine)

	if !strings.Contains(result, "CREATE FUNCTION") {
		t.Errorf("缺少 CREATE FUNCTION: %s", result)
	}
	if !strings.Contains(result, "RETURNS INT") {
		t.Errorf("缺少 RETURNS: %s", result)
	}
	// RETURN @cnt → RETURN v_cnt
	if strings.Contains(result, "RETURN @") {
		t.Errorf("RETURN @ 未转换: %s", result)
	}
}

// ============ T-SQL 语法转换测试 ============

func TestConvertInsertedDeletedToNewOld(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantNew bool // 应包含 NEW.
		wantOld bool // 应包含 OLD.
		noIns   bool // 不应包含 inserted.
		noDel   bool // 不应包含 deleted.
	}{
		{
			name:    "inserted 列引用",
			input:   "SELECT inserted.id, inserted.name FROM inserted",
			wantNew: true,
			noIns:   true,
		},
		{
			name:    "deleted 列引用",
			input:   "SELECT deleted.id FROM deleted",
			wantOld: true,
			noDel:   true,
		},
		{
			name:    "JOIN inserted",
			input:   "SELECT * FROM t JOIN inserted ON t.id = inserted.id",
			wantNew: true,
			noIns:   true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := convertInsertedDeletedToNewOld(c.input, "mysql")
			if c.wantNew && !strings.Contains(result, "NEW.") {
				t.Errorf("未包含 NEW.: %s", result)
			}
			if c.wantOld && !strings.Contains(result, "OLD.") {
				t.Errorf("未包含 OLD.: %s", result)
			}
			if c.noIns && strings.Contains(strings.ToLower(result), "inserted.") {
				t.Errorf("仍包含 inserted.: %s", result)
			}
			if c.noDel && strings.Contains(strings.ToLower(result), "deleted.") {
				t.Errorf("仍包含 deleted.: %s", result)
			}
		})
	}
}

func TestConvertAtVariablesToMySQL(t *testing.T) {
	input := "SET @count = 10; SELECT @count"
	result := convertAtVariablesToMySQL(input)

	// @count → v_count
	if strings.Contains(result, "@count") {
		t.Errorf("仍包含 @count: %s", result)
	}
	if !strings.Contains(result, "v_count") {
		t.Errorf("未转换为 v_count: %s", result)
	}
	// 应包含 DECLARE 语句
	if !strings.Contains(result, "DECLARE v_count") {
		t.Errorf("未生成 DECLARE: %s", result)
	}
}

func TestConvertConvertToCast(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: "CONVERT(VARCHAR(10), GETDATE())",
			want:  "CAST(GETDATE() AS VARCHAR(10))",
		},
		{
			input: "CONVERT(INT, '123')",
			want:  "CAST('123' AS INT)",
		},
	}

	for _, c := range cases {
		result := convertConvertToCast(c.input)
		if !strings.Contains(result, "CAST(") {
			t.Errorf("CONVERT 未转为 CAST: input=%s, got=%s", c.input, result)
		}
		if !strings.Contains(result, " AS ") {
			t.Errorf("缺少 AS 关键字: input=%s, got=%s", c.input, result)
		}
	}
}

func TestConvertTopToLimit(t *testing.T) {
	input := "SELECT TOP 10 * FROM users"
	result := convertTopToLimit(input)

	// TOP n 应被移除
	if strings.Contains(strings.ToUpper(result), "TOP") {
		t.Errorf("TOP 未被移除: %s", result)
	}
}

func TestStripMSSQLRoutineHeader(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "CREATE PROCEDURE",
			input: "CREATE PROCEDURE sp_test AS SELECT 1",
			want:  "SELECT 1",
		},
		{
			name:  "CREATE PROC",
			input: "CREATE PROC sp_test AS BEGIN SELECT 1 END",
			want:  "BEGIN SELECT 1 END",
		},
		{
			name:  "CREATE FUNCTION",
			input: "CREATE FUNCTION fn_test() RETURNS INT AS BEGIN RETURN 1 END",
			want:  "BEGIN RETURN 1 END",
		},
		{
			name:  "无头部",
			input: "SELECT 1",
			want:  "SELECT 1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := stripMSSQLRoutineHeader(c.input)
			if strings.TrimSpace(result) != strings.TrimSpace(c.want) {
				t.Errorf("stripMSSQLRoutineHeader(%q) = %q, want %q", c.input, result, c.want)
			}
		})
	}
}

func TestConvertMSSQLBodyToMySQL(t *testing.T) {
	input := `SET @count = 1
PRINT 'hello'
SELECT GETUTCDATE()
SELECT LEN('abc')
SELECT ISNULL(NULL, 0)
SELECT SCOPE_IDENTITY()`

	result := convertMSSQLBodyToMySQL(input)

	// GETUTCDATE → UTC_TIMESTAMP
	if strings.Contains(strings.ToUpper(result), "GETUTCDATE") {
		t.Errorf("GETUTCDATE 未转换: %s", result)
	}
	if !strings.Contains(result, "UTC_TIMESTAMP") {
		t.Errorf("未转换为 UTC_TIMESTAMP: %s", result)
	}
	// LEN → CHAR_LENGTH
	if strings.Contains(strings.ToUpper(result), "LEN(") {
		t.Errorf("LEN 未转换: %s", result)
	}
	// ISNULL → IFNULL
	if strings.Contains(strings.ToUpper(result), "ISNULL(") {
		t.Errorf("ISNULL 未转换: %s", result)
	}
	// SCOPE_IDENTITY → LAST_INSERT_ID
	if strings.Contains(strings.ToUpper(result), "SCOPE_IDENTITY") {
		t.Errorf("SCOPE_IDENTITY 未转换: %s", result)
	}
	if !strings.Contains(result, "LAST_INSERT_ID") {
		t.Errorf("未转换为 LAST_INSERT_ID: %s", result)
	}
}

func TestConvertMSSQLBodyToPG(t *testing.T) {
	input := `SET @count = 1
PRINT 'hello'
SELECT GETUTCDATE()
SELECT LEN('abc')
SELECT ISNULL(NULL, 0)`

	result := convertMSSQLBodyToPG(input)

	// GETUTCDATE → NOW() AT TIME ZONE 'UTC'
	if strings.Contains(strings.ToUpper(result), "GETUTCDATE") {
		t.Errorf("GETUTCDATE 未转换: %s", result)
	}
	if !strings.Contains(result, "NOW() AT TIME ZONE 'UTC'") {
		t.Errorf("未转换为 NOW() AT TIME ZONE: %s", result)
	}
	// LEN → LENGTH
	if strings.Contains(strings.ToUpper(result), "LEN(") {
		t.Errorf("LEN 未转换: %s", result)
	}
	if !strings.Contains(result, "LENGTH(") {
		t.Errorf("未转换为 LENGTH: %s", result)
	}
	// ISNULL → COALESCE
	if strings.Contains(strings.ToUpper(result), "ISNULL(") {
		t.Errorf("ISNULL 未转换: %s", result)
	}
	if !strings.Contains(result, "COALESCE(") {
		t.Errorf("未转换为 COALESCE: %s", result)
	}
	// PRINT → RAISE NOTICE
	if strings.Contains(strings.ToUpper(result), "PRINT") {
		t.Errorf("PRINT 未转换: %s", result)
	}
	if !strings.Contains(result, "RAISE NOTICE") {
		t.Errorf("未转换为 RAISE NOTICE: %s", result)
	}
}
