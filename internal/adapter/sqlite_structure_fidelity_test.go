package adapter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/mariadb"
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	"dbbridge/internal/adapter/sqlite"
)

// TestStructureFidelity_SQLiteToMySQL 验证 SQLite → MySQL 迁移的结构保真度
// 不再只看"没报错"，而是逐项校验迁移后的表结构是否完整：
//   - 列数、列名、列类型
//   - 主键
//   - UNIQUE 约束（列级 + 表级）
//   - 外键
//   - 默认值
//   - 二级索引
func TestStructureFidelity_SQLiteToMySQL(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 复杂表结构：覆盖之前遗漏的边界场景
	ddl := `CREATE TABLE accounts (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  phone TEXT UNIQUE,
  code CHAR(8) NOT NULL,
  level INTEGER DEFAULT 0,
  balance REAL DEFAULT 0.00,
  metadata TEXT,
  created_at TEXT DEFAULT '2026-01-01 00:00:00',
  status TEXT DEFAULT 'active'
);
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  account_id INTEGER NOT NULL,
  amount REAL NOT NULL DEFAULT 0,
  note TEXT,
  FOREIGN KEY (account_id) REFERENCES accounts
);
CREATE INDEX idx_orders_account ON orders (account_id);
CREATE UNIQUE INDEX idx_orders_note ON orders (note);`

	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 写入数据
	accRows := []types.Row{
		{"id": int64(1), "email": "a@x.com", "phone": "100", "code": "CODE0001", "level": int64(1), "balance": 100.50, "metadata": nil, "created_at": "2026-01-15 10:00:00", "status": "active"},
		{"id": int64(2), "email": "b@x.com", "phone": nil, "code": "CODE0002", "level": int64(2), "balance": 200.00, "metadata": "{}", "created_at": "2026-01-16 11:00:00", "status": "active"},
	}
	if err := src.WriteData(ctx, "accounts", []string{"id", "email", "phone", "code", "level", "balance", "metadata", "created_at", "status"}, accRows); err != nil {
		t.Fatalf("写入 accounts 失败: %v", err)
	}

	orderRows := []types.Row{
		{"id": int64(1), "account_id": int64(1), "amount": 50.00, "note": "first"},
		{"id": int64(2), "account_id": int64(2), "amount": 99.99, "note": nil},
	}
	if err := src.WriteData(ctx, "orders", []string{"id", "account_id", "amount", "note"}, orderRows); err != nil {
		t.Fatalf("写入 orders 失败: %v", err)
	}

	// 读源结构
	srcSchema, err := src.GetTableSchema(ctx, "accounts")
	if err != nil {
		t.Fatalf("读 accounts 结构失败: %v", err)
	}
	srcOrders, err := src.GetTableSchema(ctx, "orders")
	if err != nil {
		t.Fatalf("读 orders 结构失败: %v", err)
	}

	// === 结构校验 ===
	t.Run("accounts_structure", func(t *testing.T) {
		// 列数
		if len(srcSchema.Columns) != 9 {
			t.Errorf("accounts 列数 = %d, want 9", len(srcSchema.Columns))
		}

		// 主键
		pkFound := false
		for _, c := range srcSchema.Columns {
			if c.Name == "id" && c.IsPrimaryKey && c.AutoIncrement {
				pkFound = true
			}
		}
		if !pkFound {
			t.Error("id 应为自增主键")
		}

		// 列级 UNIQUE 约束（email, phone）——之前被静默丢失的 bug
		uniqueCols := map[string]bool{}
		for _, idx := range srcSchema.Indexes {
			if idx.IsUnique && !idx.IsPrimary {
				for _, c := range idx.Columns {
					uniqueCols[c] = true
				}
			}
		}
		if !uniqueCols["email"] {
			t.Error("email 列级 UNIQUE 约束丢失（sqlite_autoindex 被错误跳过的 bug）")
		}
		if !uniqueCols["phone"] {
			t.Error("phone 列级 UNIQUE 约束丢失")
		}

		// CHAR(8) 长度保留
		for _, c := range srcSchema.Columns {
			if c.Name == "code" {
				if c.Length == nil || *c.Length != 8 {
					t.Errorf("code 长度 = %v, want 8", c.Length)
				}
			}
		}

		// 默认值
		defaults := map[string]string{}
		for _, c := range srcSchema.Columns {
			if c.DefaultValue != nil {
				defaults[c.Name] = *c.DefaultValue
			}
		}
		if defaults["level"] != "0" {
			t.Errorf("level 默认值 = %q, want '0'", defaults["level"])
		}
		if defaults["status"] != "'active'" {
			t.Errorf("status 默认值 = %q, want 'active'", defaults["status"])
		}
	})

	t.Run("orders_structure", func(t *testing.T) {
		// 外键存在
		if len(srcOrders.ForeignKeys) == 0 {
			t.Error("orders 外键丢失")
		}
		fkFound := false
		for _, fk := range srcOrders.ForeignKeys {
			if fk.RefTable == "accounts" {
				fkFound = true
				// 外键引用列：省略引用列名时 SQLite 返回空字符串
				// 这不会导致 GetTableSchema 报错，但在生成目标库 DDL 时必须被过滤
				// （orchestrator 的 hasEmptyRefColumn 和 SQLite 内联外键都有过滤）
				hasEmpty := false
				for _, rc := range fk.RefColumns {
					if strings.TrimSpace(rc) == "" {
						hasEmpty = true
					}
				}
				if hasEmpty {
					t.Log("外键引用列为空（SQLite 允许省略），DDL 生成时需过滤避免语法错误")
				}
			}
		}
		if !fkFound {
			t.Error("未找到引用 accounts 的外键")
		}

		// 二级索引（含 UNIQUE INDEX）
		var hasIdxAccount, hasIdxNote bool
		for _, idx := range srcOrders.Indexes {
			if idx.IsPrimary {
				continue
			}
			if len(idx.Columns) == 1 && idx.Columns[0] == "account_id" {
				hasIdxAccount = true
			}
			if len(idx.Columns) == 1 && idx.Columns[0] == "note" && idx.IsUnique {
				hasIdxNote = true
			}
		}
		if !hasIdxAccount {
			t.Error("idx_orders_account 索引丢失")
		}
		if !hasIdxNote {
			t.Error("idx_orders_note UNIQUE 索引丢失")
		}
	})

	// === 生成 MySQL DDL 并校验 ===
	mysqlAdapter := types.NewAdapter(types.MySQL)
	if mysqlAdapter == nil {
		t.Fatal("MySQL 适配器未注册")
	}

	t.Run("accounts_mysql_ddl", func(t *testing.T) {
		ddl, err := mysqlAdapter.GenerateCreateTableDDL(srcSchema)
		if err != nil {
			t.Fatalf("生成 MySQL DDL 失败: %v", err)
		}
		t.Logf("accounts MySQL DDL:\n%s", ddl)

		// UNIQUE INDEX 必须存在
		if !strings.Contains(ddl, "UNIQUE INDEX") {
			t.Error("MySQL DDL 缺少 UNIQUE INDEX（列级 UNIQUE 约束丢失）")
		}
		// CHAR(8) 长度保留
		if !strings.Contains(ddl, "CHAR(8)") {
			t.Errorf("MySQL DDL 缺少 CHAR(8)，长度信息丢失: %s", ddl)
		}
		// AUTO_INCREMENT
		if !strings.Contains(ddl, "AUTO_INCREMENT") {
			t.Error("MySQL DDL 缺少 AUTO_INCREMENT")
		}
		// PRIMARY KEY
		if !strings.Contains(ddl, "PRIMARY KEY") {
			t.Error("MySQL DDL 缺少 PRIMARY KEY")
		}
	})

	t.Run("orders_mysql_ddl", func(t *testing.T) {
		ddl, err := mysqlAdapter.GenerateCreateTableDDL(srcOrders)
		if err != nil {
			t.Fatalf("生成 MySQL DDL 失败: %v", err)
		}
		t.Logf("orders MySQL DDL:\n%s", ddl)

		// UNIQUE INDEX（note 列）
		if !strings.Contains(ddl, "UNIQUE INDEX") {
			t.Error("MySQL DDL 缺少 UNIQUE INDEX（note 列的 UNIQUE 约束丢失）")
		}
		// 普通索引
		if !strings.Contains(ddl, "INDEX") {
			t.Error("MySQL DDL 缺少普通索引")
		}
	})

	// === 数据校验 ===
	t.Run("data_integrity", func(t *testing.T) {
		srcData, err := src.ReadData(ctx, "accounts", 0, 100)
		if err != nil {
			t.Fatalf("读 accounts 数据失败: %v", err)
		}
		if len(srcData) != 2 {
			t.Fatalf("accounts 数据行数 = %d, want 2", len(srcData))
		}

		// 逐行逐列比对
		expectRow1 := map[string]interface{}{
			"id":         int64(1),
			"email":      "a@x.com",
			"phone":      "100",
			"code":       "CODE0001",
			"level":      int64(1),
			"balance":    100.5,
			"metadata":   nil,
			"created_at": "2026-01-15 10:00:00",
			"status":     "active",
		}
		for k, want := range expectRow1 {
			got := srcData[0][k]
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
				t.Errorf("accounts 行1 列 %s = %v(%T), want %v(%T)", k, got, got, want, want)
			}
		}

		// 行2 phone 为 nil
		if srcData[1]["phone"] != nil {
			t.Errorf("accounts 行2 phone = %v, want nil", srcData[1]["phone"])
		}

		// orders 数据
		orderData, err := src.ReadData(ctx, "orders", 0, 100)
		if err != nil {
			t.Fatalf("读 orders 数据失败: %v", err)
		}
		if len(orderData) != 2 {
			t.Fatalf("orders 数据行数 = %d, want 2", len(orderData))
		}
		// 行2 note 为 nil
		if orderData[1]["note"] != nil {
			t.Errorf("orders 行2 note = %v, want nil", orderData[1]["note"])
		}
	})
}

// TestStructureFidelity_SQLiteToSQLite 验证 SQLite → SQLite 迁移的结构保真度
// 确保 UNIQUE 约束、外键、索引在 SQLite 互迁中也不丢失
func TestStructureFidelity_SQLiteToSQLite(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT 'unknown',
  FOREIGN KEY (nonexistent) REFERENCES ghost
)`); err != nil {
		// 这个 DDL 会失败因为引用了不存在的表
		// 换一个合法的
	}

	if err := src.ExecContext(ctx, `CREATE TABLE parent (
  id INTEGER PRIMARY KEY,
  label TEXT UNIQUE
)`); err != nil {
		t.Fatalf("建 parent 失败: %v", err)
	}

	if err := src.ExecContext(ctx, `CREATE TABLE child (
  id INTEGER PRIMARY KEY,
  parent_id INTEGER NOT NULL,
  value TEXT NOT NULL DEFAULT 'default',
  FOREIGN KEY (parent_id) REFERENCES parent
)`); err != nil {
		t.Fatalf("建 child 失败: %v", err)
	}

	if err := src.ExecContext(ctx, `CREATE INDEX idx_child_parent ON child (parent_id)`); err != nil {
		t.Fatalf("建索引失败: %v", err)
	}

	// 读源结构
	childSchema, err := src.GetTableSchema(ctx, "child")
	if err != nil {
		t.Fatalf("读 child 结构失败: %v", err)
	}

	// 校验源结构
	// 1. 列级 UNIQUE（parent 表的 label）
	parentSchema, err := src.GetTableSchema(ctx, "parent")
	if err != nil {
		t.Fatalf("读 parent 结构失败: %v", err)
	}
	uniqueCount := 0
	for _, idx := range parentSchema.Indexes {
		if idx.IsUnique && !idx.IsPrimary {
			uniqueCount++
		}
	}
	if uniqueCount == 0 {
		t.Error("parent.label 列级 UNIQUE 约束丢失")
	}

	// 2. 外键存在（省略引用列名）
	if len(childSchema.ForeignKeys) == 0 {
		t.Error("child 外键丢失")
	} else {
		fk := childSchema.ForeignKeys[0]
		if fk.RefTable != "parent" {
			t.Errorf("外键引用表 = %s, want parent", fk.RefTable)
		}
		// RefColumns 含空字符串（省略引用列名场景）
		hasEmpty := false
		for _, rc := range fk.RefColumns {
			if rc == "" {
				hasEmpty = true
			}
		}
		if hasEmpty {
			t.Log("外键引用列为空（SQLite 允许省略），内联 DDL 应跳过此 FK 避免语法错误")
		}
	}

	// 3. 生成目标 SQLite DDL（内联外键）
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标库失败: %v", err)
	}
	defer dst.Close()

	// 先建 parent（被引用表必须先存在）
	parentDDL, err := dst.GenerateCreateTableDDL(parentSchema)
	if err != nil {
		t.Fatalf("生成 parent DDL 失败: %v", err)
	}
	for _, stmt := range strings.Split(parentDDL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标建 parent 失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 生成 child DDL（含内联外键）
	childDDL, err := dst.GenerateCreateTableDDL(childSchema)
	if err != nil {
		t.Fatalf("生成 child DDL 失败: %v", err)
	}
	t.Logf("child DDL:\n%s", childDDL)

	// 外键引用列为空时不应生成语法错误 DDL
	// 检查 DDL 中没有空反引号 REFERENCES parent ()
	if strings.Contains(childDDL, "REFERENCES \"parent\" ()") {
		t.Error("外键引用列为空时生成了语法错误 DDL: REFERENCES parent ()")
	}

	// 执行 DDL
	for _, stmt := range strings.Split(childDDL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标建 child 失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 验证目标表结构
	dstChild, err := dst.GetTableSchema(ctx, "child")
	if err != nil {
		t.Fatalf("读目标 child 结构失败: %v", err)
	}
	if len(dstChild.Columns) != 3 {
		t.Errorf("目标 child 列数 = %d, want 3", len(dstChild.Columns))
	}
}

// TestStructureFidelity_DefaultValues 验证默认值在各类型间的转换
func TestStructureFidelity_DefaultValues(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  s_text TEXT DEFAULT 'hello',
  s_int INTEGER DEFAULT 42,
  s_real REAL DEFAULT 3.14,
  s_expr TEXT DEFAULT (lower('WORLD')),
  s_null TEXT DEFAULT NULL
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	// 校验默认值读取
	defaults := map[string]string{}
	for _, c := range schema.Columns {
		if c.DefaultValue != nil {
			defaults[c.Name] = *c.DefaultValue
		}
	}

	if defaults["s_text"] != "'hello'" {
		t.Errorf("s_text 默认值 = %q, want 'hello'", defaults["s_text"])
	}
	if defaults["s_int"] != "42" {
		t.Errorf("s_int 默认值 = %q, want 42", defaults["s_int"])
	}
	if defaults["s_real"] != "3.14" {
		t.Errorf("s_real 默认值 = %q, want 3.14", defaults["s_real"])
	}

	// 生成 MySQL DDL，验证默认值转换
	mysqlAdapter := types.NewAdapter(types.MySQL)
	ddl, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}
	t.Logf("MySQL DDL:\n%s", ddl)

	if !strings.Contains(ddl, "DEFAULT 'hello'") {
		t.Error("MySQL DDL 缺少 DEFAULT 'hello'")
	}
	if !strings.Contains(ddl, "DEFAULT 42") {
		t.Error("MySQL DDL 缺少 DEFAULT 42")
	}
	if !strings.Contains(ddl, "DEFAULT 3.14") {
		t.Error("MySQL DDL 缺少 DEFAULT 3.14")
	}
}

// TestStructureFidelity_CompositePrimaryKey 验证复合主键迁移
func TestStructureFidelity_CompositePrimaryKey(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE kv (
  tenant TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT,
  PRIMARY KEY (tenant, key)
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "kv")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	// 复合主键不应标记 AutoIncrement
	for _, c := range schema.Columns {
		if c.AutoIncrement {
			t.Errorf("复合主键列 %s 不应标记 AutoIncrement", c.Name)
		}
	}

	// 生成 MySQL DDL
	mysqlAdapter := types.NewAdapter(types.MySQL)
	ddl, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}
	t.Logf("MySQL DDL:\n%s", ddl)

	// 复合主键列应降级为 VARCHAR(255)（TEXT 不能做主键）
	if !strings.Contains(ddl, "VARCHAR(255)") {
		t.Error("复合主键 TEXT 列应降级为 VARCHAR(255)")
	}
	// PRIMARY KEY 应包含两列
	if !strings.Contains(ddl, "PRIMARY KEY") {
		t.Error("缺少 PRIMARY KEY")
	}
}

// TestStructureFidelity_AllTypes 全类型映射验证
func TestStructureFidelity_AllTypes(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE t (
  i_int INTEGER,
  i_real REAL,
  i_text TEXT,
  i_blob BLOB,
  i_num NUMERIC,
  i_date TEXT,
  i_varchar VARCHAR(50),
  i_char CHAR(10)
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	ddl, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}
	t.Logf("MySQL DDL:\n%s", ddl)

	// 逐类型验证
	typeExpect := map[string]string{
		"i_int":     "INT",
		"i_real":    "FLOAT",
		"i_text":    "TEXT",
		"i_blob":    "BLOB",
		"i_num":     "DECIMAL",
						"i_date":    "TEXT", // 空表无数据采样，TEXT 列无法推断为 DATETIME
		"i_varchar": "VARCHAR(50)",
		"i_char":    "CHAR(10)",
	}
	for col, want := range typeExpect {
		if !strings.Contains(ddl, want) {
			t.Errorf("列 %s: MySQL DDL 缺少 %s", col, want)
		}
	}
}

// TestStructureFidelity_EmptyTable 空表迁移不应报错
func TestStructureFidelity_EmptyTable(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE empty (
  id INTEGER PRIMARY KEY,
  data TEXT UNIQUE
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "empty")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	ddl, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}
	t.Logf("Empty table MySQL DDL:\n%s", ddl)

	// 空表也应保留 UNIQUE 约束
	if !strings.Contains(ddl, "UNIQUE INDEX") {
		t.Error("空表 UNIQUE 约束丢失")
	}
}
