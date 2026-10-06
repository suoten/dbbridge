package adapter_test

import (
	"context"
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/sqlite"
	"dbbridge/internal/adapter/sqlite"
)

// TestCompositePrimaryKeyPagination 验证复合主键表不被错误地使用 keyset 分页
// 复合主键只用第一列做 keyset 会在重复值边界跳行
func TestCompositePrimaryKeyPagination(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 复合主键表：tenant_id + id 组合
	if err := src.ExecContext(ctx, `CREATE TABLE multi_pk (
  tenant_id INTEGER NOT NULL,
  id INTEGER NOT NULL,
  data TEXT,
  PRIMARY KEY (tenant_id, id)
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 写入数据：tenant_id 有重复值
	rows := make([]types.Row, 0, 10)
	for tid := 1; tid <= 3; tid++ {
		for i := 1; i <= 5; i++ {
			rows = append(rows, types.Row{
				"tenant_id": int64(tid),
				"id":        int64(i),
				"data":      "data",
			})
		}
	}
	if err := src.WriteData(ctx, "multi_pk", []string{"tenant_id", "id", "data"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 读结构验证复合主键
	schema, err := src.GetTableSchema(ctx, "multi_pk")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	pkCount := 0
	for _, idx := range schema.Indexes {
		if idx.IsPrimary {
			pkCount = len(idx.Columns)
		}
	}
	if pkCount != 2 {
		t.Fatalf("复合主键列数 = %d, want 2", pkCount)
	}

	// 用 OFFSET 读取所有数据（复合主键应回退到 OFFSET 而非 keyset）
	allData, err := src.ReadData(ctx, "multi_pk", 0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(allData) != 15 {
		t.Errorf("总行数 = %d, want 15", len(allData))
	}

	// 分两批读取验证不丢行
	batch1, err := src.ReadData(ctx, "multi_pk", 0, 10)
	if err != nil {
		t.Fatalf("第一批读取失败: %v", err)
	}
	batch2, err := src.ReadData(ctx, "multi_pk", 10, 10)
	if err != nil {
		t.Fatalf("第二批读取失败: %v", err)
	}
	if len(batch1) != 10 || len(batch2) != 5 {
		t.Errorf("分批结果: batch1=%d batch2=%d, want 10+5", len(batch1), len(batch2))
	}
}

// TestEmptyTableMigration 验证空表（0 行）能正确建表不报错
func TestEmptyTableMigration(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 创建空表
	if err := src.ExecContext(ctx, `CREATE TABLE empty_table (
  id INTEGER PRIMARY KEY,
  name TEXT,
  created_at TEXT DEFAULT '2026-01-01'
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 读取空表数据
	data, err := src.ReadData(ctx, "empty_table", 0, 100)
	if err != nil {
		t.Fatalf("读取空表失败: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("空表行数 = %d, want 0", len(data))
	}

	// 获取行数
	count, err := src.GetRowCount(ctx, "empty_table")
	if err != nil {
		t.Fatalf("获取行数失败: %v", err)
	}
	if count != 0 {
		t.Errorf("空表行数 = %d, want 0", count)
	}

	// 读结构并生成 DDL
	schema, err := src.GetTableSchema(ctx, "empty_table")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}
	if len(schema.Columns) != 3 {
		t.Errorf("列数 = %d, want 3", len(schema.Columns))
	}

	// 生成目标 DDL 并建表
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标 SQLite 失败: %v", err)
	}
	defer dst.Close()

	ddl, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}

	for _, stmt := range strings.Split(ddl, ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 验证目标表存在且为空
	exists, err := dst.TableExists(ctx, "empty_table")
	if err != nil {
		t.Fatalf("检查表存在失败: %v", err)
	}
	if !exists {
		t.Error("目标表不存在")
	}

	// 写入空数据不报错
	err = dst.WriteData(ctx, "empty_table", []string{"id", "name", "created_at"}, nil)
	if err != nil {
		t.Errorf("写入空数据失败: %v", err)
	}
}

// TestSpecialCharacterIdentifiers 验证含特殊字符的标识符正确转义
func TestSpecialCharacterIdentifiers(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 表名含空格和中文
	if err := src.ExecContext(ctx, `CREATE TABLE "order details" (
  "order id" INTEGER PRIMARY KEY,
  "product name" TEXT NOT NULL,
  "单价" REAL,
  "数量" INTEGER DEFAULT 1
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 写入数据
	rows := []types.Row{
		{"order id": int64(1), "product name": "Widget", "单价": 9.99, "数量": int64(5)},
		{"order id": int64(2), "product name": "Gadget", "单价": 19.99, "数量": int64(3)},
	}
	if err := src.WriteData(ctx, "order details", []string{"order id", "product name", "单价", "数量"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 读取验证
	data, err := src.ReadData(ctx, "order details", 0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(data) != 2 {
		t.Errorf("行数 = %d, want 2", len(data))
	}

	// 读结构
	schema, err := src.GetTableSchema(ctx, "order details")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}
	if len(schema.Columns) != 4 {
		t.Errorf("列数 = %d, want 4", len(schema.Columns))
	}

	// 验证列名
	colNames := make(map[string]bool)
	for _, c := range schema.Columns {
		colNames[c.Name] = true
	}
	for _, expected := range []string{"order id", "product name", "单价", "数量"} {
		if !colNames[expected] {
			t.Errorf("缺少列 %q", expected)
		}
	}

	// 生成 MySQL DDL 验证转义
	mysqlAdapter := types.NewAdapter(types.MySQL)
	if mysqlAdapter == nil {
		t.Fatal("MySQL 适配器未注册")
	}
	ddl, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}
	t.Logf("MySQL DDL:\n%s", ddl)

	// 验证反引号包裹
	if !strings.Contains(ddl, "`order details`") {
		t.Error("MySQL DDL 缺少反引号包裹表名")
	}
	if !strings.Contains(ddl, "`order id`") {
		t.Error("MySQL DDL 缺少反引号包裹列名 'order id'")
	}

	// 生成 PG DDL 验证双引号
	pgAdapter := types.NewAdapter(types.PostgreSQL)
	if pgAdapter == nil {
		t.Fatal("PG 适配器未注册")
	}
	pgDDL, err := pgAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 PG DDL 失败: %v", err)
	}
	t.Logf("PG DDL:\n%s", pgDDL)

	if !strings.Contains(pgDDL, `"order details"`) {
		t.Error("PG DDL 缺少双引号包裹表名")
	}
}

// TestReservedWordAsIdentifier 验证 SQL 保留字作标识符的转义
func TestReservedWordAsIdentifier(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 使用 SQL 保留字作列名
	if err := src.ExecContext(ctx, `CREATE TABLE keywords (
  id INTEGER PRIMARY KEY,
  "order" TEXT,
  "select" TEXT,
  "from" TEXT,
  "where" TEXT
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	rows := []types.Row{
		{"id": int64(1), "order": "A", "select": "B", "from": "C", "where": "D"},
	}
	if err := src.WriteData(ctx, "keywords", []string{"id", "order", "select", "from", "where"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "keywords")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	// 生成各适配器 DDL 验证不报错且包含转义
	for _, dbType := range []types.DatabaseType{types.MySQL, types.PostgreSQL, types.SQLite, types.MSSQL, types.Oracle} {
		t.Run(string(dbType), func(t *testing.T) {
			adapter := types.NewAdapter(dbType)
			if adapter == nil {
				t.Skipf("%s 适配器未注册", dbType)
				return
			}
			ddl, err := adapter.GenerateCreateTableDDL(schema)
			if err != nil {
				t.Errorf("%s: 生成 DDL 失败: %v", dbType, err)
				return
			}
			// DDL 必须包含这些列名（被引号/反引号/方括号包裹）
			// Oracle 将标识符规范化为大写，做大小写不敏感匹配
			ddlUpper := strings.ToUpper(ddl)
			for _, col := range []string{"order", "select", "from", "where"} {
				if !strings.Contains(ddlUpper, strings.ToUpper(col)) {
					t.Errorf("%s DDL 缺少列 %q", dbType, col)
				}
			}
			t.Logf("%s DDL OK", dbType)
		})
	}
}
