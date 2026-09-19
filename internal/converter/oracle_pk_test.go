package converter

import (
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/oracle"
)

// 防回归：MySQL 列级主键写法（`id INT AUTO_INCREMENT PRIMARY KEY COMMENT 'xx'`）
// 必须被解析为主键，此前只认表级 PRIMARY KEY (...) 约束行，转换后主键丢失。
func TestParseColumnLevelPrimaryKey(t *testing.T) {
	ddl := "CREATE TABLE `customers` (\n" +
		"  `id` INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT '客户ID',\n" +
		"  `name` VARCHAR(64) NOT NULL COMMENT '客户名称'\n" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	schema, err := parseCreateTable(ddl, "mysql")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	found := false
	for _, c := range schema.Columns {
		if c.Name == "id" {
			found = true
			if !c.IsPrimaryKey {
				t.Fatalf("列级 PRIMARY KEY 未被识别为表主键")
			}
			if !c.AutoIncrement {
				t.Fatalf("AUTO_INCREMENT 未被识别")
			}
		}
	}
	if !found {
		t.Fatalf("未解析到 id 列")
	}
}

// 注释文案里出现 "primary key" 不应误判为主键
func TestParseCommentMentionsPrimaryKeyNotConfused(t *testing.T) {
	ddl := "CREATE TABLE `t1` (\n" +
		"  `note` VARCHAR(255) COMMENT 'this is the primary key of mapping table',\n" +
		"  `id` INT NOT NULL\n" +
		")"
	schema, err := parseCreateTable(ddl, "mysql")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, c := range schema.Columns {
		if c.Name == "note" && c.IsPrimaryKey {
			t.Fatalf("注释中的 primary key 被误判为列主键")
		}
	}
}

// 端到端：MySQL（列级主键）→ Oracle 转换输出必须包含 PRIMARY KEY
func TestConvertDDLMySQLToOracleKeepsPrimaryKey(t *testing.T) {
	tgt := types.NewAdapter(types.Oracle)
	conv := NewConverter(types.MySQL, types.Oracle, tgt)
	ddl := "CREATE TABLE `customers` (\n" +
		"  `id` INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT '客户ID',\n" +
		"  `phone` VARCHAR(20) NOT NULL\n" +
		")"
	out, err := conv.ConvertDDL(ddl)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if !strings.Contains(out, "PRIMARY KEY") {
		t.Fatalf("转换后的 Oracle DDL 丢失主键:\n%s", out)
	}
}
