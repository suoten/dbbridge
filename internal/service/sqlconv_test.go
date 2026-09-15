package service

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

func TestConvertSQLMySQLToPG(t *testing.T) {
	s := NewSQLService()
	sql := `SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ----------------------------
-- Table structure for users
-- ----------------------------
DROP TABLE IF EXISTS ` + "`users`" + `;
CREATE TABLE ` + "`users`" + ` (
  ` + "`id`" + ` int NOT NULL AUTO_INCREMENT,
  ` + "`name`" + ` varchar(255) NOT NULL,
  PRIMARY KEY (` + "`id`" + `)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO ` + "`users`" + ` (` + "`id`" + `, ` + "`name`" + `) VALUES (1, '张三');`

	result := s.ConvertSQL(ConvertSQLRequest{
		SourceDialect: "mysql", TargetDialect: "postgres", SQL: sql,
	})
	if !result.Success {
		t.Fatalf("转换失败: %s", result.Error)
	}
	conv := result.Converted

	// DROP TABLE 反引号 → 双引号
	if !strings.Contains(conv, `DROP TABLE IF EXISTS "users"`) {
		t.Errorf("DROP TABLE 未转换: %s", conv)
	}
	// CREATE TABLE 应产出 PG 语法（转换器带 IF NOT EXISTS，列名反引号→双引号）
	if !strings.Contains(conv, `CREATE TABLE IF NOT EXISTS "users"`) {
		t.Errorf("CREATE TABLE 未转换: %s", conv)
	}
	// INSERT 反引号 → 双引号
	if !strings.Contains(conv, `INSERT INTO "users"`) {
		t.Errorf("INSERT 未转换: %s", conv)
	}
	// MySQL 专属的 ENGINE/CHARSET 不应残留在 PG DDL 中
	if strings.Contains(conv, "ENGINE=InnoDB") {
		t.Errorf("ENGINE=InnoDB 残留: %s", conv)
	}
	// SET/USE 应被跳过
	if strings.Contains(conv, "SET NAMES") {
		t.Errorf("SET 语句应跳过: %s", conv)
	}
	if result.SkippedCount < 1 {
		t.Errorf("SkippedCount 应 >= 1, got %d", result.SkippedCount)
	}
	if result.ConvertedCount < 3 {
		t.Errorf("ConvertedCount 应 >= 3 (DROP/CREATE/INSERT), got %d", result.ConvertedCount)
	}
}

func TestConvertSQLSameDialect(t *testing.T) {
	s := NewSQLService()
	result := s.ConvertSQL(ConvertSQLRequest{
		SourceDialect: "mysql", TargetDialect: "mysql", SQL: "SELECT 1",
	})
	if !result.Success {
		t.Fatalf("同方言应直接成功: %s", result.Error)
	}
}

func TestConvertSQLBadDialect(t *testing.T) {
	s := NewSQLService()
	result := s.ConvertSQL(ConvertSQLRequest{
		SourceDialect: "oracle123", TargetDialect: "mysql", SQL: "SELECT 1",
	})
	if result.Success || result.Error == "" {
		t.Fatal("不支持的方言应报错")
	}
}

func TestConvertSQLEmpty(t *testing.T) {
	s := NewSQLService()
	result := s.ConvertSQL(ConvertSQLRequest{
		SourceDialect: "mysql", TargetDialect: "postgres", SQL: "  ",
	})
	if result.Success || result.Error == "" {
		t.Fatal("空内容应报错")
	}
}

func TestConvertSQLInsertOnlyNonMySQLSource(t *testing.T) {
	// 非 MySQL 源：INSERT 引号转换依然有效
	s := NewSQLService()
	result := s.ConvertSQL(ConvertSQLRequest{
		SourceDialect: "mssql", TargetDialect: "postgres",
		SQL: "INSERT INTO [users] ([id]) VALUES (1);",
	})
	if !result.Success {
		t.Fatalf("转换失败: %s", result.Error)
	}
	// MSSQL 方括号 INSERT 转换器只处理反引号——原样透传，但不应丢语句
	if !strings.Contains(result.Converted, "INSERT INTO") {
		t.Errorf("INSERT 语句不应丢失: %s", result.Converted)
	}
}

func TestParseDialect(t *testing.T) {
	d, err := ParseDialect("MySQL")
	if err != nil || d != types.MySQL {
		t.Errorf("ParseDialect(MySQL) = %v, %v", d, err)
	}
	if _, err := ParseDialect("nope"); err == nil {
		t.Error("未知方言应报错")
	}
}
