package adapter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/access"
	_ "dbbridge/internal/adapter/aurora"
	_ "dbbridge/internal/adapter/cockroachdb"
	_ "dbbridge/internal/adapter/dameng"
	_ "dbbridge/internal/adapter/kingbase"
	_ "dbbridge/internal/adapter/mariadb"
	_ "dbbridge/internal/adapter/mongodb"
	_ "dbbridge/internal/adapter/mssql"
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/oceanbase"
	_ "dbbridge/internal/adapter/opengauss"
	_ "dbbridge/internal/adapter/oracle"
	_ "dbbridge/internal/adapter/polardb"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	_ "dbbridge/internal/adapter/tidb"
	_ "dbbridge/internal/adapter/timescaledb"
	"dbbridge/internal/adapter/sqlite"
)

// TestDDLRoundTrip_SQLiteToSQLite SQLite→SQLite 完整往返测试
// 建源表(含复杂约束) → 读结构 → 生成目标DDL → 执行 → 读回结构 → 逐项比对
// 这是不需要 Docker 的最严格结构保真度测试
func TestDDLRoundTrip_SQLiteToSQLite(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		ddl  string
		data []types.Row
		cols []string
	}{
		{
			name: "all_constraints",
			ddl: `CREATE TABLE t1 (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  code CHAR(8) NOT NULL DEFAULT 'XXXXXXXX',
  price REAL DEFAULT 0.00,
  count INTEGER DEFAULT 0,
  note TEXT,
  flag INTEGER DEFAULT 1,
  created_at TEXT DEFAULT '2026-01-01 00:00:00'
)`,
			cols: []string{"id", "email", "code", "price", "count", "note", "flag", "created_at"},
			data: []types.Row{
				{"id": int64(1), "email": "a@x.com", "code": "CODE0001", "price": 10.5, "count": int64(5), "note": "first", "flag": int64(1), "created_at": "2026-01-15 10:00:00"},
				{"id": int64(2), "email": "b@x.com", "code": "CODE0002", "price": 20.0, "count": int64(0), "note": nil, "flag": int64(0), "created_at": "2026-01-16 11:00:00"},
			},
		},
		{
			name: "composite_pk",
			ddl: `CREATE TABLE t2 (
  tenant TEXT NOT NULL,
  k TEXT NOT NULL,
  v TEXT,
  PRIMARY KEY (tenant, k)
)`,
			cols: []string{"tenant", "k", "v"},
			data: []types.Row{
				{"tenant": "t1", "k": "key1", "v": "val1"},
				{"tenant": "t1", "k": "key2", "v": nil},
				{"tenant": "t2", "k": "key1", "v": "val3"},
			},
		},
		{
			name: "no_pk",
			ddl: `CREATE TABLE t3 (
  level TEXT NOT NULL,
  msg TEXT NOT NULL,
  ts TEXT DEFAULT '2026-01-01 00:00:00'
)`,
			cols: []string{"level", "msg", "ts"},
			data: []types.Row{
				{"level": "INFO", "msg": "hello", "ts": "2026-01-15 10:00:00"},
				{"level": "ERROR", "msg": "fail", "ts": "2026-01-15 11:00:00"},
			},
		},
		{
			name: "with_fk",
			ddl: `CREATE TABLE parent (
  id INTEGER PRIMARY KEY,
  label TEXT UNIQUE
);
CREATE TABLE child (
  id INTEGER PRIMARY KEY,
  parent_id INTEGER NOT NULL,
  val TEXT DEFAULT 'x',
  FOREIGN KEY (parent_id) REFERENCES parent(id)
)`,
			cols: []string{"id", "parent_id", "val"},
			data: []types.Row{
				{"id": int64(1), "parent_id": int64(1), "val": "a"},
				{"id": int64(2), "parent_id": int64(1), "val": "b"},
			},
		},
		{
			name: "multiple_indexes",
			ddl: `CREATE TABLE t5 (
  id INTEGER PRIMARY KEY,
  a TEXT NOT NULL,
  b TEXT,
  c INTEGER
);
CREATE UNIQUE INDEX idx_t5_a ON t5 (a);
CREATE INDEX idx_t5_b ON t5 (b);
CREATE INDEX idx_t5_bc ON t5 (b, c)`,
			cols: []string{"id", "a", "b", "c"},
			data: []types.Row{
				{"id": int64(1), "a": "aa", "b": "bb", "c": int64(1)},
				{"id": int64(2), "a": "aa2", "b": nil, "c": int64(2)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建源库
			src := &sqlite.Adapter{}
			if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
				t.Fatalf("连接源库失败: %v", err)
			}
			defer src.Close()

			// 执行源 DDL
			for _, stmt := range strings.Split(tt.ddl, ";") {
				stmt = strings.TrimSpace(stmt)
				if stmt == "" {
					continue
				}
				if err := src.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("源库建表失败: %v (SQL: %s)", err, stmt)
				}
			}

			// 写入数据
			if tt.data != nil {
				tableName := "t1"
				if tt.name == "composite_pk" {
					tableName = "t2"
				} else if tt.name == "no_pk" {
					tableName = "t3"
				} else if tt.name == "with_fk" {
					// 先写 parent 再写 child
					parentRows := []types.Row{{"id": int64(1), "label": "p1"}}
					if err := src.WriteData(ctx, "parent", []string{"id", "label"}, parentRows); err != nil {
						t.Fatalf("写入 parent 失败: %v", err)
					}
					tableName = "child"
				} else if tt.name == "multiple_indexes" {
					tableName = "t5"
				}
				if err := src.WriteData(ctx, tableName, tt.cols, tt.data); err != nil {
					t.Fatalf("写入数据失败: %v", err)
				}
			}

			// 读源结构
			tableName := "t1"
			if tt.name == "composite_pk" {
				tableName = "t2"
			} else if tt.name == "no_pk" {
				tableName = "t3"
			} else if tt.name == "with_fk" {
				tableName = "child"
			} else if tt.name == "multiple_indexes" {
				tableName = "t5"
			}

			srcSchema, err := src.GetTableSchema(ctx, tableName)
			if err != nil {
				t.Fatalf("读源结构失败: %v", err)
			}

			// 创建目标库
			dst := &sqlite.Adapter{}
			if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
				t.Fatalf("连接目标库失败: %v", err)
			}
			defer dst.Close()

			// with_fk 需要先建 parent
			if tt.name == "with_fk" {
				parentSchema, err := src.GetTableSchema(ctx, "parent")
				if err != nil {
					t.Fatalf("读 parent 结构失败: %v", err)
				}
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
			}

			// 生成目标 DDL
			dstDDL, err := dst.GenerateCreateTableDDL(srcSchema)
			if err != nil {
				t.Fatalf("生成目标 DDL 失败: %v", err)
			}
			t.Logf("目标 DDL:\n%s", dstDDL)

			// 执行目标 DDL
			for _, stmt := range strings.Split(dstDDL, ";") {
				stmt = strings.TrimSpace(stmt)
				if stmt == "" {
					continue
				}
				if err := dst.ExecContext(ctx, stmt); err != nil {
					t.Fatalf("目标库建表失败: %v (SQL: %s)", err, stmt)
				}
			}

			// 迁移数据
			srcData, err := src.ReadData(ctx, tableName, 0, 100)
			if err != nil {
				t.Fatalf("读源数据失败: %v", err)
			}
			if len(srcData) > 0 {
				// with_fk 需要先迁移 parent 数据
				if tt.name == "with_fk" {
					parentData, err := src.ReadData(ctx, "parent", 0, 100)
					if err != nil {
						t.Fatalf("读 parent 数据失败: %v", err)
					}
					parentCols := []string{"id", "label"}
					if err := dst.WriteData(ctx, "parent", parentCols, parentData); err != nil {
						t.Fatalf("写入 parent 数据失败: %v", err)
					}
				}
				if err := dst.WriteData(ctx, tableName, tt.cols, srcData); err != nil {
					t.Fatalf("写入目标数据失败: %v", err)
				}
			}

			// 读回目标结构
			dstSchema, err := dst.GetTableSchema(ctx, tableName)
			if err != nil {
				t.Fatalf("读目标结构失败: %v", err)
			}

			// === 逐项结构比对 ===

			// 1. 列数
			if len(srcSchema.Columns) != len(dstSchema.Columns) {
				t.Errorf("列数不匹配: 源=%d 目标=%d", len(srcSchema.Columns), len(dstSchema.Columns))
			}

			// 2. 列名和类型
			for i := range srcSchema.Columns {
				if i >= len(dstSchema.Columns) {
					break
				}
				sc := srcSchema.Columns[i]
				dc := dstSchema.Columns[i]
				if sc.Name != dc.Name {
					t.Errorf("列 %d 名称不匹配: 源=%s 目标=%s", i, sc.Name, dc.Name)
				}
				if sc.BaseType != dc.BaseType {
					// SQLite 类型亲和性：CHAR/VARCHAR 迁后可能变成 TEXT
					if normalizeSQLiteType(sc.BaseType) == normalizeSQLiteType(dc.BaseType) {
						// 类型亲和性一致，可接受
					} else {
						t.Errorf("列 %s BaseType 不匹配: 源=%s 目标=%s", sc.Name, sc.BaseType, dc.BaseType)
					}
				}
				if sc.Nullable != dc.Nullable {
					t.Errorf("列 %s Nullable 不匹配: 源=%v 目标=%v", sc.Name, sc.Nullable, dc.Nullable)
				}
				if sc.IsPrimaryKey != dc.IsPrimaryKey {
					t.Errorf("列 %s IsPrimaryKey 不匹配: 源=%v 目标=%v", sc.Name, sc.IsPrimaryKey, dc.IsPrimaryKey)
				}
				if sc.AutoIncrement != dc.AutoIncrement {
					t.Errorf("列 %s AutoIncrement 不匹配: 源=%v 目标=%v", sc.Name, sc.AutoIncrement, dc.AutoIncrement)
				}
			}

			// 3. 索引数（含主键、UNIQUE、普通）
			srcIdx := countNonPrimaryIndexes(srcSchema.Indexes)
			dstIdx := countNonPrimaryIndexes(dstSchema.Indexes)
			if srcIdx != dstIdx {
				t.Errorf("非主键索引数不匹配: 源=%d 目标=%d", srcIdx, dstIdx)
				t.Logf("源索引: %v", srcSchema.Indexes)
				t.Logf("目标索引: %v", dstSchema.Indexes)
			}

			// 4. UNIQUE 约束数
			srcUnique := countUniqueIndexes(srcSchema.Indexes)
			dstUnique := countUniqueIndexes(dstSchema.Indexes)
			if srcUnique != dstUnique {
				t.Errorf("UNIQUE 索引数不匹配: 源=%d 目标=%d", srcUnique, dstUnique)
			}

			// 5. 数据行数
			srcCount, _ := src.GetRowCount(ctx, tableName)
			dstCount, _ := dst.GetRowCount(ctx, tableName)
			if srcCount != dstCount {
				t.Errorf("数据行数不匹配: 源=%d 目标=%d", srcCount, dstCount)
			}

			// 6. 数据值抽样比对
			if srcCount > 0 && dstCount > 0 {
				dstData, err := dst.ReadData(ctx, tableName, 0, 100)
				if err != nil {
					t.Fatalf("读目标数据失败: %v", err)
				}
				for i := range srcData {
					if i >= len(dstData) {
						break
					}
					for _, col := range tt.cols {
						sv := fmt.Sprintf("%v", srcData[i][col])
						dv := fmt.Sprintf("%v", dstData[i][col])
						if sv != dv {
							t.Errorf("行 %d 列 %s 值不匹配: 源=%s 目标=%s", i, col, sv, dv)
						}
					}
				}
			}
		})
	}
}

// normalizeSQLiteType 将 SQLite 类型归一化为亲和性类型
// SQLite 类型亲和性：CHAR/VARCHAR/TEXT 都归为 TEXT 亲和性
func normalizeSQLiteType(t string) string {
	switch t {
	case "CHAR", "VARCHAR", "NCHAR", "NVARCHAR", "TEXT", "CLOB":
		return "TEXT"
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT":
		return "INTEGER"
	case "FLOAT", "DOUBLE", "DOUBLE PRECISION", "REAL":
		return "REAL"
	case "DECIMAL", "NUMERIC":
		return "NUMERIC"
	case "DATETIME":
		return "DATETIME" // 采样推断的类型
	}
	return t
}

func countNonPrimaryIndexes(idxs []types.IndexMeta) int {
	c := 0
	for _, idx := range idxs {
		if !idx.IsPrimary {
			c++
		}
	}
	return c
}

func countUniqueIndexes(idxs []types.IndexMeta) int {
	c := 0
	for _, idx := range idxs {
		if idx.IsUnique && !idx.IsPrimary {
			c++
		}
	}
	return c
}

// TestDDLRoundTrip_DataPrecision 数据精度验证
func TestDDLRoundTrip_DataPrecision(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  big_val INTEGER,
  dec_val REAL,
  text_val TEXT,
  empty_str TEXT,
  null_val TEXT,
  special TEXT,
  bool_val INTEGER
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	rows := []types.Row{
		{
			"id":        int64(1),
			"big_val":   int64(9223372036854775807), // max int64
			"dec_val":   3.141592653589793,
			"text_val":  "Hello世界🎉",
			"empty_str": "",
			"null_val":  nil,
			"special":   "O'Brien's \"quoted\" value",
			"bool_val":  int64(1),
		},
		{
			"id":        int64(2),
			"big_val":   int64(-9223372036854775808), // min int64
			"dec_val":   0.0,
			"text_val":  "",
			"empty_str": "",
			"null_val":  nil,
			"special":   "line1\nline2\ttab",
			"bool_val":  int64(0),
		},
	}
	cols := []string{"id", "big_val", "dec_val", "text_val", "empty_str", "null_val", "special", "bool_val"}
	if err := src.WriteData(ctx, "t", cols, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// SQLite → SQLite 往返
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标库失败: %v", err)
	}
	defer dst.Close()

	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	ddl, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	srcData, _ := src.ReadData(ctx, "t", 0, 100)
	if err := dst.WriteData(ctx, "t", cols, srcData); err != nil {
		t.Fatalf("写入目标失败: %v", err)
	}
	dstData, _ := dst.ReadData(ctx, "t", 0, 100)

	// 逐行逐列比对
	for i := range srcData {
		for _, col := range cols {
			sv := fmt.Sprintf("%v", srcData[i][col])
			dv := fmt.Sprintf("%v", dstData[i][col])
			if sv != dv {
				t.Errorf("行 %d 列 %s: 源=%q 目标=%q", i, col, sv, dv)
			}
		}
	}

	// 特别检查
	if dstData[0]["big_val"] != int64(9223372036854775807) {
		t.Errorf("大整数精度丢失: %v", dstData[0]["big_val"])
	}
	if dstData[0]["empty_str"] != "" {
		t.Errorf("空字符串应为空，got %v", dstData[0]["empty_str"])
	}
	if dstData[0]["null_val"] != nil {
		t.Errorf("NULL 值应为 nil，got %v", dstData[0]["null_val"])
	}
	if dstData[0]["special"] != "O'Brien's \"quoted\" value" {
		t.Errorf("特殊字符丢失: %v", dstData[0]["special"])
	}
}

// TestDDLRoundTrip_DefaultValuePreservation 默认值保留验证
func TestDDLRoundTrip_DefaultValuePreservation(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源库失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE t (
  id INTEGER PRIMARY KEY,
  str_def TEXT DEFAULT 'hello world',
  int_def INTEGER DEFAULT 42,
  real_def REAL DEFAULT 3.14,
  str_quoted TEXT DEFAULT 'it''s ok',
  zero_def INTEGER DEFAULT 0,
  neg_def INTEGER DEFAULT -1
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	schema, err := src.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读结构失败: %v", err)
	}

	// 验证默认值读取
	expectedDefaults := map[string]string{
		"str_def":    "'hello world'",
		"int_def":    "42",
		"real_def":   "3.14",
		"str_quoted": "'it''s ok'",
		"zero_def":   "0",
		"neg_def":    "-1",
	}
	for _, c := range schema.Columns {
		if c.DefaultValue == nil {
			if exp, ok := expectedDefaults[c.Name]; ok {
				t.Errorf("列 %s 默认值为 nil，期望 %s", c.Name, exp)
			}
			continue
		}
		exp, ok := expectedDefaults[c.Name]
		if !ok {
			continue
		}
		if *c.DefaultValue != exp {
			t.Errorf("列 %s 默认值 = %q，期望 %q", c.Name, *c.DefaultValue, exp)
		}
	}

	// SQLite → SQLite 往返
	dst := &sqlite.Adapter{}
	if err := dst.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接目标库失败: %v", err)
	}
	defer dst.Close()

	ddl, err := dst.GenerateCreateTableDDL(schema)
	if err != nil {
		t.Fatalf("生成 DDL 失败: %v", err)
	}
	t.Logf("DDL:\n%s", ddl)

	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := dst.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("目标建表失败: %v (SQL: %s)", err, stmt)
		}
	}

	// 读回目标结构
	dstSchema, err := dst.GetTableSchema(ctx, "t")
	if err != nil {
		t.Fatalf("读目标结构失败: %v", err)
	}

	// 验证默认值保留
	for _, c := range dstSchema.Columns {
		if c.DefaultValue == nil {
			if exp, ok := expectedDefaults[c.Name]; ok {
				t.Errorf("目标列 %s 默认值为 nil，期望 %s", c.Name, exp)
			}
			continue
		}
		exp, ok := expectedDefaults[c.Name]
		if !ok {
			continue
		}
		if *c.DefaultValue != exp {
			t.Errorf("目标列 %s 默认值 = %q，期望 %q", c.Name, *c.DefaultValue, exp)
		}
	}

	// 验证插入不指定默认值列时，目标库能正确填充
	if err := dst.WriteData(ctx, "t", []string{"id"}, []types.Row{{"id": int64(1)}}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	readBack, err := dst.ReadData(ctx, "t", 0, 1)
	if err != nil || len(readBack) != 1 {
		t.Fatalf("读回失败: err=%v len=%d", err, len(readBack))
	}

	if readBack[0]["str_def"] != "hello world" {
		t.Errorf("str_def 默认值未正确填充: %v", readBack[0]["str_def"])
	}
	if readBack[0]["int_def"] != int64(42) {
		t.Errorf("int_def 默认值未正确填充: %v", readBack[0]["int_def"])
	}
}
