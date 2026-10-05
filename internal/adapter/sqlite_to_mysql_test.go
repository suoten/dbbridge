package adapter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/sqlite"
	"dbbridge/internal/adapter/sqlite"
)

// TestSQLiteToMySQLComplexSchema 测试 SQLite → MySQL 复杂表结构迁移
// 复现用户反馈的"SQLite 转 MySQL 转换失败"问题
func TestSQLiteToMySQLComplexSchema(t *testing.T) {
	ctx := context.Background()

	// 创建 SQLite 源库
	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 创建复杂表结构（模拟真实场景）
	ddl := `CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  order_no TEXT NOT NULL UNIQUE,
  customer_name TEXT NOT NULL DEFAULT 'anonymous',
  total_amount REAL DEFAULT 0,
  status TEXT DEFAULT 'pending',
  note TEXT,
  raw_data BLOB,
  created_at TEXT DEFAULT '2026-01-01 00:00:00',
  updated_at TEXT
);
CREATE TABLE products (
  id INTEGER PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  price REAL NOT NULL DEFAULT 0,
  stock INTEGER DEFAULT 0,
  description TEXT,
  image BLOB,
  tags TEXT,
  is_active INTEGER DEFAULT 1,
  created_at TEXT DEFAULT '2026-01-01 00:00:00'
);
CREATE TABLE order_items (
  id INTEGER PRIMARY KEY,
  order_id INTEGER NOT NULL,
  product_id INTEGER NOT NULL,
  quantity INTEGER DEFAULT 1,
  unit_price REAL NOT NULL,
  FOREIGN KEY (order_id) REFERENCES orders(id),
  FOREIGN KEY (product_id) REFERENCES products(id)
);
CREATE INDEX idx_order_items_order ON order_items (order_id);
CREATE INDEX idx_order_items_product ON order_items (product_id);`

	// 执行 DDL
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 写入测试数据
	orderRows := []types.Row{
		{"id": int64(1), "order_no": "ORD-001", "customer_name": "张三", "total_amount": 99.50, "status": "paid", "note": "加急", "raw_data": []byte("binary1"), "created_at": "2026-01-15 10:30:00", "updated_at": "2026-01-16 08:00:00"},
		{"id": int64(2), "order_no": "ORD-002", "customer_name": "李四", "total_amount": 150.00, "status": "shipped", "note": nil, "raw_data": nil, "created_at": "2026-01-16 14:20:00", "updated_at": nil},
	}
	if err := src.WriteData(ctx, "orders", []string{"id", "order_no", "customer_name", "total_amount", "status", "note", "raw_data", "created_at", "updated_at"}, orderRows); err != nil {
		t.Fatalf("写入 orders 失败: %v", err)
	}

	productRows := []types.Row{
		{"id": int64(1), "sku": "SKU-001", "name": "产品A", "price": 29.99, "stock": int64(100), "description": "描述A", "image": []byte("img1"), "tags": "电子,配件", "is_active": int64(1), "created_at": "2026-01-10 00:00:00"},
		{"id": int64(2), "sku": "SKU-002", "name": "产品B", "price": 59.99, "stock": int64(50), "description": nil, "image": nil, "tags": "服装", "is_active": int64(0), "created_at": "2026-01-11 00:00:00"},
	}
	if err := src.WriteData(ctx, "products", []string{"id", "sku", "name", "price", "stock", "description", "image", "tags", "is_active", "created_at"}, productRows); err != nil {
		t.Fatalf("写入 products 失败: %v", err)
	}

	itemRows := []types.Row{
		{"id": int64(1), "order_id": int64(1), "product_id": int64(1), "quantity": int64(2), "unit_price": 29.99},
		{"id": int64(2), "order_id": int64(1), "product_id": int64(2), "quantity": int64(1), "unit_price": 59.99},
		{"id": int64(3), "order_id": int64(2), "product_id": int64(1), "quantity": int64(3), "unit_price": 29.99},
	}
	if err := src.WriteData(ctx, "order_items", []string{"id", "order_id", "product_id", "quantity", "unit_price"}, itemRows); err != nil {
		t.Fatalf("写入 order_items 失败: %v", err)
	}

	// 获取 MySQL 适配器（用于生成 MySQL DDL）
	mysqlAdapter := types.NewAdapter(types.MySQL)
	if mysqlAdapter == nil {
		t.Fatal("MySQL 适配器未注册")
	}

	// 逐表测试：读 SQLite 结构 → 生成 MySQL DDL → 验证 DDL 正确性
	tables := []string{"orders", "products", "order_items"}
	for _, tableName := range tables {
		t.Run(tableName, func(t *testing.T) {
			schema, err := src.GetTableSchema(ctx, tableName)
			if err != nil {
				t.Fatalf("读取 %s 结构失败: %v", tableName, err)
			}

			t.Logf("表 %s 列信息:", tableName)
			for _, c := range schema.Columns {
				t.Logf("  列 %s: DataType=%s BaseType=%s Nullable=%v Default=%v PK=%v AutoInc=%v",
					c.Name, c.DataType, c.BaseType, c.Nullable, c.DefaultValue, c.IsPrimaryKey, c.AutoIncrement)
			}

			// 生成 MySQL DDL
			mysqlDDL, err := mysqlAdapter.GenerateCreateTableDDL(schema)
			if err != nil {
				t.Fatalf("生成 MySQL DDL 失败: %v", err)
			}

			t.Logf("MySQL DDL:\n%s", mysqlDDL)

			// 验证 DDL 包含表名
			if !strings.Contains(mysqlDDL, tableName) {
				t.Errorf("DDL 未包含表名 %s", tableName)
			}

			// 验证 DDL 包含 ENGINE=InnoDB
			if !strings.Contains(mysqlDDL, "ENGINE=InnoDB") {
				t.Errorf("DDL 未包含 ENGINE=InnoDB")
			}

			// 验证 DDL 包含 PRIMARY KEY
			if !strings.Contains(mysqlDDL, "PRIMARY KEY") {
				t.Errorf("DDL 未包含 PRIMARY KEY")
			}

			// 验证自增列
			if !strings.Contains(mysqlDDL, "AUTO_INCREMENT") {
				t.Errorf("DDL 未包含 AUTO_INCREMENT（SQLite INTEGER PRIMARY KEY 应映射为自增）")
			}

			// 验证 TEXT 列不是主键（SQLite TEXT 列如果做了 UNIQUE 索引应该用前缀索引或降级）
			// order_no 是 UNIQUE TEXT 列，不应做主键但应有唯一索引
			if tableName == "orders" {
				if !strings.Contains(mysqlDDL, "UNIQUE INDEX") {
					t.Errorf("orders DDL 未包含 UNIQUE INDEX（order_no 应有唯一索引）")
				}
			}
		})
	}

	// 验证数据读取
	for _, tableName := range tables {
		t.Run(tableName+"_data", func(t *testing.T) {
			rows, err := src.ReadData(ctx, tableName, 0, 100)
			if err != nil {
				t.Fatalf("读取 %s 数据失败: %v", tableName, err)
			}
			t.Logf("表 %s: %d 行数据", tableName, len(rows))
			for i, row := range rows {
				t.Logf("  行 %d: %v", i+1, row)
			}
		})
	}
}

// TestSQLiteToMySQLDefaultValues 测试 SQLite → MySQL 默认值转换
func TestSQLiteToMySQLDefaultValues(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// SQLite 的默认值可能带单引号（字符串）或不带（表达式/数字）
	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL DEFAULT 'unknown',
  count INTEGER DEFAULT 0,
  price REAL DEFAULT 0.00,
  flag INTEGER DEFAULT 1,
  label TEXT DEFAULT 'active'
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	mysqlDDL, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}

	t.Logf("MySQL DDL:\n%s", mysqlDDL)

	// 验证默认值正确转换
	for _, want := range []string{"DEFAULT 'unknown'", "DEFAULT 0", "DEFAULT 0.00", "DEFAULT 1", "DEFAULT 'active'"} {
		if !strings.Contains(mysqlDDL, want) {
			t.Errorf("DDL 缺少 %s", want)
		}
	}
}

// TestSQLiteToMySQLWithoutPrimaryKey 测试无主键表迁移
func TestSQLiteToMySQLWithoutPrimaryKey(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// 无主键表
	if err := src.ExecContext(ctx, `CREATE TABLE log (
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TEXT DEFAULT '2026-01-01 00:00:00'
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 写入数据
	rows := []types.Row{
		{"level": "INFO", "message": "系统启动", "created_at": "2026-01-15 10:00:00"},
		{"level": "WARN", "message": "内存不足", "created_at": "2026-01-15 11:00:00"},
	}
	if err := src.WriteData(ctx, "log", []string{"level", "message", "created_at"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "log")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	mysqlDDL, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}

	t.Logf("MySQL DDL:\n%s", mysqlDDL)

	// 无主键表不应包含 PRIMARY KEY
	if strings.Contains(mysqlDDL, "PRIMARY KEY") {
		t.Errorf("无主键表不应包含 PRIMARY KEY")
	}

	// 验证数据可读
	data, err := src.ReadData(ctx, "log", 0, 100)
	if err != nil {
		t.Fatalf("读取数据失败: %v", err)
	}
	if len(data) != 2 {
		t.Errorf("数据行数 = %d, want 2", len(data))
	}
}

// TestSQLiteToMySQLTextPrimaryKey 测试 TEXT 列做主键的转换（Cassandra/ScyllaDB 源场景）
func TestSQLiteToMySQLTextPrimaryKey(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	// TEXT 列做主键（SQLite 允许，MySQL 不允许 TEXT 做主键 Error 1170）
	if err := src.ExecContext(ctx, `CREATE TABLE kv (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "kv")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	mysqlDDL, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}

	t.Logf("MySQL DDL:\n%s", mysqlDDL)

	// TEXT 主键列应降级为 VARCHAR(255)
	if !strings.Contains(mysqlDDL, "VARCHAR(255)") {
		t.Errorf("TEXT 主键列应降级为 VARCHAR(255): %s", mysqlDDL)
	}
	if strings.Contains(mysqlDDL, "TEXT") && strings.Contains(mysqlDDL, "PRIMARY KEY") {
		// 检查是否是主键列本身是 TEXT（不是其他 TEXT 列）
		// 简单验证：VARCHAR(255) 应出现在 PRIMARY KEY 列定义中
		if !strings.Contains(mysqlDDL, "`key` VARCHAR(255)") {
			t.Errorf("TEXT 主键列 key 应降级为 VARCHAR(255): %s", mysqlDDL)
		}
	}
}

// TestSQLiteToMySQLAllColumnTypes 测试 SQLite 所有类型到 MySQL 的映射
func TestSQLiteToMySQLAllColumnTypes(t *testing.T) {
	cases := []struct {
		sqliteType string
		wantMySQL  string
	}{
		{"INTEGER", "INT"},
		{"TEXT", "TEXT"},
		{"REAL", "FLOAT"},
		{"BLOB", "BLOB"},
		{"NUMERIC", "DECIMAL"},
		{"DATETIME", "DATETIME"},
		{"DATE", "DATE"},
		{"BOOLEAN", "TINYINT(1)"},
		{"VARCHAR(255)", "VARCHAR(255)"},
		{"CHAR(10)", "CHAR(10)"},
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	for _, c := range cases {
		t.Run(c.sqliteType, func(t *testing.T) {
			col := types.ColumnMeta{
				DataType: c.sqliteType,
				BaseType: strings.ToUpper(strings.SplitN(c.sqliteType, "(", 2)[0]),
			}
			// 模拟 GetTableSchema 解析长度：CHAR(10) → Length=10
			if openIdx := strings.Index(c.sqliteType, "("); openIdx >= 0 {
				closePart := c.sqliteType[openIdx+1:]
				if closeIdx := strings.Index(closePart, ")"); closeIdx >= 0 {
					var n int
					if _, err := fmt.Sscanf(closePart[:closeIdx], "%d", &n); err == nil {
						col.Length = &n
					}
				}
			}
			got := mysqlAdapter.MapType(col)
			if !strings.HasPrefix(got, c.wantMySQL) {
				t.Errorf("MapType(%s) = %s, want prefix %s", c.sqliteType, got, c.wantMySQL)
			}
		})
	}
}

// TestSQLiteToMySQLDataTypes 测试数据值在 SQLite → MySQL 之间的类型兼容性
func TestSQLiteToMySQLDataTypes(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  int_val INTEGER,
  real_val REAL,
  text_val TEXT,
  blob_val BLOB,
  date_val TEXT
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 写入各种数据类型
	rows := []types.Row{
		{"id": int64(1), "int_val": int64(42), "real_val": 3.14, "text_val": "hello", "blob_val": []byte("binary"), "date_val": "2026-01-15 10:30:00"},
		{"id": int64(2), "int_val": nil, "real_val": nil, "text_val": nil, "blob_val": nil, "date_val": nil},
	}
	if err := src.WriteData(ctx, "t", []string{"id", "int_val", "real_val", "text_val", "blob_val", "date_val"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 读取并验证数据类型
	data, err := src.ReadData(ctx, "t", 0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}

	if len(data) != 2 {
		t.Fatalf("数据行数 = %d, want 2", len(data))
	}

	// 第一行应有值
	row1 := data[0]
	t.Logf("行1: id=%T(%v) int=%T(%v) real=%T(%v) text=%T(%v) blob=%T(%v) date=%T(%v)",
		row1["id"], row1["id"],
		row1["int_val"], row1["int_val"],
		row1["real_val"], row1["real_val"],
		row1["text_val"], row1["text_val"],
		row1["blob_val"], row1["blob_val"],
		row1["date_val"], row1["date_val"])

	// 第二行应全 NULL（除 id）
	row2 := data[1]
	for k, v := range row2 {
		if k != "id" && v != nil {
			t.Errorf("行2 列 %s 应为 nil, got %T(%v)", k, v, v)
		}
	}

	// 验证生成的 MySQL DDL
	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	mysqlAdapter := types.NewAdapter(types.MySQL)
	mysqlDDL, err := mysqlAdapter.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 MySQL DDL 失败: %v", err)
	}

	t.Logf("MySQL DDL:\n%s", mysqlDDL)

	// 验证列类型映射
	for _, want := range []string{"INT", "FLOAT", "TEXT", "BLOB", "DATETIME"} {
		if !strings.Contains(mysqlDDL, want) {
			t.Errorf("DDL 缺少类型 %s", want)
		}
	}

	// 验证列数（应有 6 列）
	colCount := strings.Count(mysqlDDL, "`")
	// 每列至少 2 个反引号（列名），加上 PRIMARY KEY 中的 2 个，加上表名 2 个
	if colCount < 12 {
		t.Errorf("DDL 反引号数 = %d, 列数可能不足", colCount)
	}
}

func init() {
	// 确保输出格式
	_ = fmt.Sprintf
}
