package oracle

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// demoSchema 模拟 MySQL 源库读出的表结构（小写表名/列名 + 自增主键）
func demoSchema() types.TableSchema {
	ten := 10
	return types.TableSchema{
		Name: "customers",
		Columns: []types.ColumnMeta{
			{Name: "id", BaseType: "INT", DataType: "INT", Nullable: false, IsPrimaryKey: true, AutoIncrement: true},
			{Name: "name", BaseType: "VARCHAR", DataType: "VARCHAR(64)", Nullable: false, Length: &ten},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
		},
	}
}

// 防回归：标识符必须规范化为大写（Oracle 惯例），不得按源库大小写带引号输出。
// 此前输出 "customers" 导致表以小写存储，而 TableExists 等用 UPPER() 判断，
// 第二次迁移误判表不存在、跳过 DROP 直接 CREATE，报 ORA-00955。
func TestGenerateCreateTableDDLUppercaseIdentifiers(t *testing.T) {
	a := &Adapter{brand: "Oracle"}
	ddl, err := a.GenerateCreateTableDDL(demoSchema())
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}

	for _, want := range []string{
		"CREATE TABLE CUSTOMERS (",
		"ID NUMBER(10)",
		"NAME VARCHAR2(",
		"PRIMARY KEY (ID)",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL 缺少片段 %q\nDDL:\n%s", want, ddl)
		}
	}
	if strings.Contains(ddl, `"customers"`) || strings.Contains(ddl, `"id"`) {
		t.Errorf("DDL 不应包含小写引号标识符\nDDL:\n%s", ddl)
	}
}

func TestGenerateDropTableDDLUppercase(t *testing.T) {
	a := &Adapter{brand: "Oracle"}
	ddl, _ := a.GenerateDropTableDDL("customers")
	if ddl != `DROP TABLE CUSTOMERS PURGE` {
		t.Errorf("DROP DDL = %q, 期望大写无引号", ddl)
	}
}

// 含特殊字符的标识符才允许带引号，且内部大写化保持一致；
// Oracle 保留字（如 LEVEL）裸写会 ORA-00904，必须保持引号
func TestOraIdentSpecialChars(t *testing.T) {
	if got := oraIdent("order items"); got != `"ORDER ITEMS"` {
		t.Errorf("oraIdent(含空格) = %q", got)
	}
	if got := oraIdent(`a"b`); got != `"A""B"` {
		t.Errorf("oraIdent(含双引号) = %q", got)
	}
	if got := oraIdent("CUSTOMERS"); got != "CUSTOMERS" {
		t.Errorf("oraIdent(常规) = %q", got)
	}
	for _, w := range []string{"level", "order", "comment", "size", "rows", "date"} {
		if got := oraIdent(w); got != `"`+strings.ToUpper(w)+`"` {
			t.Errorf("oraIdent(保留字 %s) = %q, 期望带引号", w, got)
		}
	}
}

// INSERT 的表名列名同样必须大写化（迁移写入路径）
func TestWriteDataUppercaseIdentifiers(t *testing.T) {
	if got := quoteIdentifiers([]string{"id", "name"}); got != "ID, NAME" {
		t.Errorf("quoteIdentifiers = %q, 期望 %q", got, "ID, NAME")
	}
}
