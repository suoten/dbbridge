package sqlite

import (
	"context"
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// newTestAdapter 创建内存库适配器并连接
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := &Adapter{}
	err := a.Connect(context.Background(), types.ConnectionConfig{Database: ":memory:"})
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func testSchema() types.TableSchema {
	return types.TableSchema{
		Name: "users",
		Columns: []types.ColumnMeta{
			// 模拟从 MySQL 迁来的异构类型：应映射为 SQLite 类型
			{Name: "id", DataType: "INT UNSIGNED", BaseType: "INT", AutoIncrement: true, IsPrimaryKey: true},
			{Name: "name", DataType: "VARCHAR(128)", BaseType: "VARCHAR", Length: intPtr(128)},
			{Name: "score", DataType: "DECIMAL(10,2)", BaseType: "DECIMAL", Precision: intPtr(10), Scale: intPtr(2)},
			{Name: "active", DataType: "TINYINT(1)", BaseType: "TINYINT"},
			{Name: "created_at", DataType: "DATETIME", BaseType: "DATETIME"},
		},
		Indexes: []types.IndexMeta{
			{Name: "PRIMARY", Columns: []string{"id"}, IsUnique: true, IsPrimary: true},
		},
	}
}

func intPtr(i int) *int { return &i }

// TestEndToEnd 覆盖：DDL 生成(异构类型映射) → 建表 → 写入 → 读取(含 keyset) → 备份 → 恢复
func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	// 1. DDL 生成：验证类型经过 typeconv 映射
	ddl, err := a.GenerateCreateTableDDL(testSchema())
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}
	if !strings.Contains(ddl, `"id" INTEGER`) {
		t.Errorf("id 应映射为 INTEGER, DDL: %s", ddl)
	}
	if !strings.Contains(ddl, `"name" TEXT`) {
		t.Errorf("name 应映射为 TEXT, DDL: %s", ddl)
	}
	if !strings.Contains(ddl, `"score" REAL`) {
		t.Errorf("score 应映射为 REAL, DDL: %s", ddl)
	}
	if strings.Contains(ddl, "INT UNSIGNED") || strings.Contains(ddl, "DECIMAL(10,2)") {
		t.Errorf("异构类型未被映射, DDL: %s", ddl)
	}

	// 2. 建表（DDL 含索引语句，逐条执行）
	for _, stmt := range strings.Split(ddl, ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := a.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("执行建表 SQL 失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 3. 写入数据
	rows := make([]types.Row, 0, 10)
	for i := 1; i <= 10; i++ {
		rows = append(rows, types.Row{
			"id": int64(i), "name": "user" + string(rune('0'+i%10)),
			"score": 1.5, "active": int64(1), "created_at": "2026-08-30 12:00:00",
		})
	}
	if err := a.WriteData(ctx, "users", []string{"id", "name", "score", "active", "created_at"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	count, err := a.GetRowCount(ctx, "users")
	if err != nil || count != 10 {
		t.Fatalf("行数 = %d, err = %v, want 10", count, err)
	}

	// 4. keyset 分页：两批读完，且无重复/遗漏
	batch1, err := a.ReadDataKeyset(ctx, "users", "id", nil, 6)
	if err != nil {
		t.Fatalf("keyset 第一批失败: %v", err)
	}
	if len(batch1) != 6 {
		t.Fatalf("第一批应 6 行, got %d", len(batch1))
	}
	lastKey := batch1[len(batch1)-1]["id"]
	batch2, err := a.ReadDataKeyset(ctx, "users", "id", lastKey, 6)
	if err != nil {
		t.Fatalf("keyset 第二批失败: %v", err)
	}
	if len(batch2) != 4 {
		t.Fatalf("第二批应 4 行, got %d", len(batch2))
	}
	if batch1[0]["id"] != int64(1) || batch2[3]["id"] != int64(10) {
		t.Errorf("keyset 顺序错误: first=%v last=%v", batch1[0]["id"], batch2[3]["id"])
	}

	// 5. OFFSET 分页兜底
	offRows, err := a.ReadData(ctx, "users", 8, 5)
	if err != nil {
		t.Fatalf("OFFSET 读取失败: %v", err)
	}
	if len(offRows) != 2 {
		t.Errorf("OFFSET(8,5) 应返回 2 行, got %d", len(offRows))
	}

	// 6. 备份 → 表名变化 → 恢复
	backupName, err := a.BackupTable(ctx, "users")
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if !strings.HasPrefix(backupName, "_bak_users_") {
		t.Errorf("备份名格式错误: %s", backupName)
	}
	exists, _ := a.TableExists(ctx, "users")
	if exists {
		t.Error("备份后原表应不存在")
	}
	if err := a.RestoreFromBackup(ctx, backupName, "users"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	count, _ = a.GetRowCount(ctx, "users")
	if count != 10 {
		t.Errorf("恢复后行数 = %d, want 10", count)
	}

	// 7. 清理备份
	if err := a.DropBackup(ctx, backupName); err != nil {
		t.Fatalf("删除备份失败: %v", err)
	}
}

// TestCancelContext 取消的 context 应让查询快速失败
func TestCancelContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := newTestAdapter(t)
	_, err := a.GetRowCount(ctx, "users")
	if err == nil {
		t.Error("取消后的查询应返回错误")
	}
}

// TestRowidAliasAutoIncrement 单列 INTEGER PRIMARY KEY 是 rowid 别名，
// GetTableSchema 应标记 AutoIncrement（否则 SQLite→MySQL 丢失自增）
func TestRowidAliasAutoIncrement(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	// ① 列约束写法
	if err := a.ExecContext(ctx,
		`CREATE TABLE t1 (id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT 'x')`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	s1, err := a.GetTableSchema(ctx, "t1")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}
	if !s1.Columns[0].AutoIncrement {
		t.Error("INTEGER PRIMARY KEY 应识别为 AutoIncrement")
	}
	if s1.Columns[0].Nullable {
		t.Error("rowid 别名列应为 NOT NULL")
	}

	// ② 表约束写法（单列 PK 同样是 rowid 别名）
	if err := a.ExecContext(ctx,
		`CREATE TABLE t2 (id INTEGER, code TEXT, PRIMARY KEY (id))`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	s2, err := a.GetTableSchema(ctx, "t2")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}
	if !s2.Columns[0].AutoIncrement {
		t.Error("单列 INTEGER 表约束主键也应识别为 AutoIncrement")
	}

	// ③ 非 INTEGER 主键不是 rowid 别名
	if err := a.ExecContext(ctx,
		`CREATE TABLE t3 (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	s3, err := a.GetTableSchema(ctx, "t3")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}
	if s3.Columns[0].AutoIncrement {
		t.Error("TEXT PRIMARY KEY 不应标记 AutoIncrement")
	}

	// ④ 复合主键不是 rowid 别名
	if err := a.ExecContext(ctx,
		`CREATE TABLE t4 (sku TEXT, wh TEXT, qty INTEGER, PRIMARY KEY (sku, wh))`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	s4, err := a.GetTableSchema(ctx, "t4")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}
	for _, c := range s4.Columns {
		if c.AutoIncrement {
			t.Errorf("复合主键列 %s 不应标记 AutoIncrement", c.Name)
		}
	}
}

// TestSecondaryIndexesAndTemporalInference 二级索引读取（PRAGMA index_list 5 列兼容）
// 与 TEXT 列时间类型采样推断
func TestSecondaryIndexesAndTemporalInference(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	if err := a.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, email TEXT, created_at TEXT, note TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := a.ExecContext(ctx, `CREATE UNIQUE INDEX email ON t (email)`); err != nil {
		t.Fatalf("建索引失败: %v", err)
	}
	if err := a.ExecContext(ctx, `INSERT INTO t (email, created_at, note) VALUES ('a@x.com', '2026-08-30 10:00:00', 'text'), ('b@x.com', '2026-08-30 11:00:00', NULL)`); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	s, err := a.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	// 二级索引：应读到 email UNIQUE（列数不匹配曾导致全部静默丢失）
	var foundEmail bool
	for _, idx := range s.Indexes {
		if idx.Name == "email" {
			foundEmail = true
			if !idx.IsUnique {
				t.Error("email 索引应为 UNIQUE")
			}
			if len(idx.Columns) != 1 || idx.Columns[0] != "email" {
				t.Errorf("email 索引列错误: %v", idx.Columns)
			}
		}
	}
	if !foundEmail {
		t.Error("未读到二级索引 email")
	}

	// 时间采样：created_at 全部样本都是 datetime → 推断为 DATETIME；email/note 保持 TEXT
	for _, c := range s.Columns {
		switch c.Name {
		case "created_at":
			if c.BaseType != "DATETIME" {
				t.Errorf("created_at 应推断为 DATETIME, got %s", c.BaseType)
			}
		case "email", "note":
			if c.BaseType != "TEXT" {
				t.Errorf("%s 应保持 TEXT, got %s", c.Name, c.BaseType)
			}
		}
	}
}
