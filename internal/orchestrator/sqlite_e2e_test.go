// Package orchestrator_test - SQLite↔SQLite 端到端互迁测试
//
// 测试策略：
//   - 使用 SQLite 内存数据库（:memory:），零依赖、零安装、CI 友好
//   - 验证完整的迁移链路：建表 → 写数据 → 读结构 → 生成 DDL → 建目标表 → 写数据 → 校验
//   - 覆盖多种数据类型、主键自增、索引、默认值、NULL 值、BLOB
//   - 验证 keyset 分页和 OFFSET 分页的正确性
//   - 验证触发器/存储过程接口不 panic
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	types "dbbridge/pkg"
	"dbbridge/internal/adapter/sqlite"
)

// TestSQLiteToSQLiteE2E SQLite→SQLite 完整迁移测试
// 验证核心迁移路径：建表 → 插入 → 读结构 → 生成 DDL → 建目标 → 迁数据 → 校验
func TestSQLiteToSQLiteE2E(t *testing.T) {
	ctx := context.Background()

	// === 1. 创建源库并填充数据 ===
	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 建表：覆盖多种类型
	ddl := `CREATE TABLE products (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  price REAL DEFAULT 0,
  stock INTEGER DEFAULT 0,
  description TEXT,
  image BLOB,
  created_at TEXT DEFAULT '2026-01-01 00:00:00',
  is_active INTEGER DEFAULT 1
);
CREATE UNIQUE INDEX idx_product_name ON products (name);
CREATE INDEX idx_product_price ON products (price);`

	for _, stmt := range splitSQL(ddl) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("源库建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 插入数据（包含 NULL、BLOB、空字符串等边界值）
	rows := make([]types.Row, 0, 50)
	for i := 1; i <= 50; i++ {
		var img []byte
		if i%5 != 0 {
			img = []byte(fmt.Sprintf("img%d", i))
		}
		var desc any
		if i%3 == 0 {
			desc = nil // NULL 描述
		} else {
			desc = fmt.Sprintf("Product %d description", i)
		}
		rows = append(rows, types.Row{
			"id":          int64(i),
			"name":        fmt.Sprintf("Product-%03d", i),
			"price":       float64(i) * 9.99,
			"stock":       int64(i * 10),
			"description": desc,
			"image":       img,
			"created_at":  "2026-01-15 10:30:00",
			"is_active":   int64(1),
		})
	}

	cols := []string{"id", "name", "price", "stock", "description", "image", "created_at", "is_active"}
	if err := src.WriteData(ctx, "products", cols, rows); err != nil {
		t.Fatalf("源库写入数据失败: %v", err)
	}

	// === 2. 读取源表结构 ===
	schema, err := src.GetTableSchema(ctx, "products")
	if err != nil {
		t.Fatalf("读取源表结构失败: %v", err)
	}
	if len(schema.Columns) != 8 {
		t.Fatalf("列数 = %d, want 8", len(schema.Columns))
	}

	// 验证主键识别
	pkFound := false
	for _, c := range schema.Columns {
		if c.Name == "id" && c.IsPrimaryKey && c.AutoIncrement {
			pkFound = true
		}
	}
	if !pkFound {
		t.Error("未正确识别 id 为自增主键")
	}

	// 验证索引读取
	if len(schema.Indexes) < 2 {
		t.Errorf("索引数 = %d, want >= 2", len(schema.Indexes))
	}

	// === 3. 创建目标库并迁移 ===
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标 SQLite 失败: %v", err)
	}
	defer dst.Close()

	// 生成目标 DDL
	dstDDL, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成目标 DDL 失败: %v", err)
	}

	for _, stmt := range splitSQL(dstDDL) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标库建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// === 4. 迁移数据（使用 keyset 分页）===
	totalMigrated := int64(0)
	lastKey := any(nil)
	batchSize := 20

	for {
		srcRows, err := src.ReadDataKeyset(ctx, "products", "id", lastKey, batchSize)
		if err != nil {
			t.Fatalf("读取源数据失败: %v", err)
		}
		if len(srcRows) == 0 {
			break
		}

		if err := dst.WriteData(ctx, "products", cols, srcRows); err != nil {
			t.Fatalf("写入目标库失败: %v", err)
		}

		totalMigrated += int64(len(srcRows))
		lastKey = srcRows[len(srcRows)-1]["id"]
	}

	if totalMigrated != 50 {
		t.Errorf("迁移行数 = %d, want 50", totalMigrated)
	}

	// === 5. 校验目标库数据 ===
	dstCount, err := dst.GetRowCount(ctx, "products")
	if err != nil {
		t.Fatalf("获取目标行数失败: %v", err)
	}
	if dstCount != 50 {
		t.Errorf("目标行数 = %d, want 50", dstCount)
	}

	// 验证数据完整性：抽样检查首行和末行
	firstRow, err := dst.ReadDataKeyset(ctx, "products", "id", nil, 1)
	if err != nil || len(firstRow) != 1 {
		t.Fatalf("读取目标首行失败: err=%v, len=%d", err, len(firstRow))
	}
	if firstRow[0]["name"] != "Product-001" {
		t.Errorf("首行 name = %v, want Product-001", firstRow[0]["name"])
	}

	lastRows, err := dst.ReadData(ctx, "products", 49, 1)
	if err != nil || len(lastRows) != 1 {
		t.Fatalf("读取目标末行失败: err=%v, len=%d", err, len(lastRows))
	}
	if lastRows[0]["name"] != "Product-050" {
		t.Errorf("末行 name = %v, want Product-050", lastRows[0]["name"])
	}

	// 验证 NULL 值正确迁移（第 3 行 description 应为 nil）
	nullRow, err := dst.ReadDataKeyset(ctx, "products", "id", int64(2), 1)
	if err != nil || len(nullRow) != 1 {
		t.Fatalf("读取第 3 行失败: err=%v, len=%d", err, len(nullRow))
	}
	if nullRow[0]["description"] != nil {
		t.Errorf("第 3 行 description 应为 NULL, got %v", nullRow[0]["description"])
	}

	// 验证 BLOB 值正确迁移（第 5 行 image 应为 nil）
	blobRow, err := dst.ReadDataKeyset(ctx, "products", "id", int64(4), 1)
	if err != nil || len(blobRow) != 1 {
		t.Fatalf("读取第 5 行失败: err=%v, len=%d", err, len(blobRow))
	}
	if blobRow[0]["image"] != nil {
		t.Errorf("第 5 行 image 应为 NULL, got %v", blobRow[0]["image"])
	}

	// 验证目标表结构
	dstSchema, err := dst.GetTableSchema(ctx, "products")
	if err != nil {
		t.Fatalf("读取目标表结构失败: %v", err)
	}
	if len(dstSchema.Columns) != 8 {
		t.Errorf("目标列数 = %d, want 8", len(dstSchema.Columns))
	}

	// === 6. 验证触发器/存储过程接口可用 ===
	triggers, err := src.GetTriggers(ctx)
	if err != nil {
		t.Errorf("GetTriggers 失败: %v", err)
	}
	// SQLite 内存库无触发器，返回空切片是正常的
	if triggers == nil {
		// nil 也是可接受的
	}

	routines, err := src.GetRoutines(ctx)
	if err != nil {
		t.Errorf("GetRoutines 失败: %v", err)
	}
	if routines == nil {
		// nil 也是可接受的
	}

	// 验证 GenerateTriggerDDL 不 panic
	triggerDDL, err := src.GenerateTriggerDDL(types.TriggerMeta{
		Name: "test_trg", Event: "INSERT", Timing: "AFTER", Table: "products",
		Body: "INSERT INTO log VALUES (1)",
	}, types.SQLite)
	if err != nil {
		t.Errorf("GenerateTriggerDDL 失败: %v", err)
	}
	if triggerDDL == "" {
		t.Error("GenerateTriggerDDL 返回空字符串")
	}
}

// TestSQLiteMultiTableE2E 多表迁移测试
// 验证多张表同时迁移时的正确性（外键关系、不同类型表）
func TestSQLiteMultiTableE2E(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	// 建立有外键关系的表结构
	ddl := `
CREATE TABLE departments (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);
CREATE TABLE employees (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  dept_id INTEGER,
  salary REAL,
  hire_date TEXT,
  FOREIGN KEY (dept_id) REFERENCES departments (id)
);
CREATE INDEX idx_emp_dept ON employees (dept_id);
CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY,
  table_name TEXT,
  record_id INTEGER,
  action TEXT,
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE config (
  key TEXT PRIMARY KEY,
  value TEXT
);
CREATE TABLE big_data (
  id INTEGER PRIMARY KEY,
  payload BLOB
);`

	for _, stmt := range splitSQL(ddl) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 填充数据
	// departments
	deptRows := make([]types.Row, 0, 5)
	for i := 1; i <= 5; i++ {
		deptRows = append(deptRows, types.Row{
			"id":   int64(i),
			"name": fmt.Sprintf("Dept-%c", 'A'+i-1),
		})
	}
	if err := src.WriteData(ctx, "departments", []string{"id", "name"}, deptRows); err != nil {
		t.Fatalf("写入 departments 失败: %v", err)
	}

	// employees
	empRows := make([]types.Row, 0, 100)
	for i := 1; i <= 100; i++ {
		empRows = append(empRows, types.Row{
			"id":        int64(i),
			"name":      fmt.Sprintf("Emp-%03d", i),
			"dept_id":   int64((i % 5) + 1),
			"salary":    float64(5000 + i*100),
			"hire_date": fmt.Sprintf("2026-%02d-%02d", (i%12)+1, (i%28)+1),
		})
	}
	if err := src.WriteData(ctx, "employees", []string{"id", "name", "dept_id", "salary", "hire_date"}, empRows); err != nil {
		t.Fatalf("写入 employees 失败: %v", err)
	}

	// config（无自增主键的 TEXT 主键表）
	cfgRows := []types.Row{
		{"key": "site_name", "value": "DBBridge"},
		{"key": "version", "value": "1.0.0"},
		{"key": "max_conn", "value": "100"},
	}
	if err := src.WriteData(ctx, "config", []string{"key", "value"}, cfgRows); err != nil {
		t.Fatalf("写入 config 失败: %v", err)
	}

	// big_data（含 BLOB）
	bigRows := []types.Row{
		{"id": int64(1), "payload": []byte("large payload data 1")},
		{"id": int64(2), "payload": nil},
		{"id": int64(3), "payload": []byte("large payload data 3")},
	}
	if err := src.WriteData(ctx, "big_data", []string{"id", "payload"}, bigRows); err != nil {
		t.Fatalf("写入 big_data 失败: %v", err)
	}

	// === 迁移到目标库 ===
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标库失败: %v", err)
	}
	defer dst.Close()

	tables := []string{"departments", "employees", "audit_log", "config", "big_data"}
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			migrateTableE2E(t, ctx, src, dst, table)
		})
	}
}

// migrateTableE2E 迁移单表并校验
func migrateTableE2E(t *testing.T, ctx context.Context, src, dst types.DatabaseAdapter, tableName string) {
	t.Helper()

	// 读源结构
	schema, err := src.GetTableSchema(ctx, tableName)
	if err != nil {
		t.Fatalf("读取 %s 结构失败: %v", tableName, err)
	}

	// 生成目标 DDL
	ddl, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 %s DDL 失败: %v", tableName, err)
	}
	for _, stmt := range splitSQL(ddl) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标库建 %s 失败: %v (SQL: %s)", tableName, err, stmt)
		}
	}

	// 读源数据
	srcCount, err := src.GetRowCount(ctx, tableName)
	if err != nil {
		t.Fatalf("获取 %s 源行数失败: %v", tableName, err)
	}

	// 生成列名
	cols := make([]string, len(schema.Columns))
	for i, c := range schema.Columns {
		cols[i] = c.Name
	}

	// 迁移数据（OFFSET 分页兜底）
	batchSize := 50
	offset := 0
	totalMigrated := int64(0)
	for {
		rows, err := src.ReadData(ctx, tableName, offset, batchSize)
		if err != nil {
			t.Fatalf("读取 %s 数据失败: %v", tableName, err)
		}
		if len(rows) == 0 {
			break
		}
		if err := dst.WriteData(ctx, tableName, cols, rows); err != nil {
			t.Fatalf("写入 %s 数据失败: %v", tableName, err)
		}
		totalMigrated += int64(len(rows))
		offset += len(rows)
		if int64(offset) >= srcCount {
			break
		}
	}

	// 校验行数
	dstCount, err := dst.GetRowCount(ctx, tableName)
	if err != nil {
		t.Fatalf("获取 %s 目标行数失败: %v", tableName, err)
	}
	if dstCount != srcCount {
		t.Errorf("%s: 目标行数 = %d, 源行数 = %d", tableName, dstCount, srcCount)
	}
	if totalMigrated != srcCount {
		t.Errorf("%s: 迁移行数 = %d, 源行数 = %d", tableName, totalMigrated, srcCount)
	}
}

// TestSQLiteEmptyTable 空表迁移不应报错
func TestSQLiteEmptyTable(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE empty (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标库失败: %v", err)
	}
	defer dst.Close()

	schema, err := src.GetTableSchema(ctx, "empty")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	ddl, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}
	for _, stmt := range splitSQL(ddl) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 写入空数据不应报错
	err = dst.WriteData(ctx, "empty", []string{"id", "name"}, []types.Row{})
	if err != nil {
		t.Errorf("写入空数据报错: %v", err)
	}

	count, err := dst.GetRowCount(ctx, "empty")
	if err != nil {
		t.Fatalf("读行数失败: %v", err)
	}
	if count != 0 {
		t.Errorf("空表行数 = %d, want 0", count)
	}
}

// TestSQLiteBackupRestore 备份和恢复
func TestSQLiteBackupRestore(t *testing.T) {
	ctx := context.Background()

	a := &sqlite.Adapter{}
	if err := a.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer a.Close()

	if err := a.ExecContext(ctx, `CREATE TABLE test_bak (id INTEGER PRIMARY KEY, val TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	rows := make([]types.Row, 0, 5)
	for i := 1; i <= 5; i++ {
		rows = append(rows, types.Row{"id": int64(i), "val": fmt.Sprintf("val%d", i)})
	}
	if err := a.WriteData(ctx, "test_bak", []string{"id", "val"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 备份
	backupName, err := a.BackupTable(ctx, "test_bak")
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if !strings.HasPrefix(backupName, "_bak_test_bak_") {
		t.Errorf("备份名格式错误: %s", backupName)
	}

	// 原表应不存在
	exists, _ := a.TableExists(ctx, "test_bak")
	if exists {
		t.Error("备份后原表应不存在")
	}

	// 恢复
	if err := a.RestoreFromBackup(ctx, backupName, "test_bak"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}

	count, _ := a.GetRowCount(ctx, "test_bak")
	if count != 5 {
		t.Errorf("恢复后行数 = %d, want 5", count)
	}

	// 清理
	if err := a.DropBackup(ctx, backupName); err != nil {
		// 备份表在恢复时已被改名，不存在是正常的
	}
}
