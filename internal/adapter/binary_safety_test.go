package adapter_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	types "dbbridge/pkg"

	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	"dbbridge/internal/adapter/sqlite"
)

// TestBinarySafetyBlobRoundTrip 验证 BLOB 数据在 SQLite→SQLite 往返中保持二进制安全
// 包含 NULL 字节、非 UTF-8 字节、空切片等各种边界情况
func TestBinarySafetyBlobRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE blob_test (
  id INTEGER PRIMARY KEY,
  data BLOB,
  text_val TEXT
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 包含各种边界情况的二进制数据
	testData := [][]byte{
		{0x00, 0x01, 0x02, 0x03},               // NULL 字节开头
		{0xFF, 0xFE, 0xFD},                      // 高位字节（非 UTF-8）
		{0x00},                                  // 单个 NULL 字节
		{},                                      // 空切片
		{0x48, 0x65, 0x6C, 0x6C, 0x6F},          // "Hello" 的 ASCII
		{0xE4, 0xBD, 0xA0, 0xE5, 0xA5, 0xBD},   // "你好" 的 UTF-8
		bytes.Repeat([]byte{0xAB, 0xCD}, 1000),  // 大二进制数据
	}

	rows := make([]types.Row, len(testData))
	for i, d := range testData {
		rows[i] = types.Row{
			"id":        int64(i + 1),
			"data":      d,
			"text_val":  "text" + string(rune('A'+i)),
		}
	}

	if err := src.WriteData(ctx, "blob_test", []string{"id", "data", "text_val"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 读取并验证
	data, err := src.ReadData(ctx, "blob_test", 0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}

	if len(data) != len(testData) {
		t.Fatalf("行数 = %d, want %d", len(data), len(testData))
	}

	for i, row := range data {
		expected := testData[i]
		got, ok := row["data"].([]byte)
		if !ok {
			gotStr, isStr := row["data"].(string)
			if isStr {
				got = []byte(gotStr)
			} else {
				t.Errorf("行 %d: data 类型 %T 不是 []byte 或 string", i, row["data"])
				continue
			}
		}

		if len(expected) == 0 && len(got) == 0 {
			// 空切片和 nil 都算匹配
			continue
		}

		if !bytes.Equal(expected, got) {
			t.Errorf("行 %d: BLOB 数据不匹配\n  expected (%d bytes): %x\n  got      (%d bytes): %x",
				i, len(expected), expected, len(got), got)
		}
	}
}

// TestTimeTimeHandling 验证 time.Time 值在 SQLite→SQLite 往返中正确处理
func TestTimeTimeHandling(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE time_test (
  id INTEGER PRIMARY KEY,
  created_at TEXT
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 写入 time.Time 值
	now := time.Date(2026, 1, 15, 10, 30, 45, 123000000, time.UTC)
	rows := []types.Row{
		{"id": int64(1), "created_at": now},
		{"id": int64(2), "created_at": now.Add(500 * time.Millisecond)},
		{"id": int64(3), "created_at": nil},
	}

	if err := src.WriteData(ctx, "time_test", []string{"id", "created_at"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 读取并验证
	data, err := src.ReadData(ctx, "time_test", 0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}

	if len(data) != 3 {
		t.Fatalf("行数 = %d, want 3", len(data))
	}

	// 第一行应包含格式化后的时间
	row1 := data[0]
	val, ok := row1["created_at"].(string)
	if !ok {
		t.Errorf("行1 created_at 类型 %T 不是 string", row1["created_at"])
	} else {
		if val == "" {
			t.Error("行1 created_at 为空字符串")
		}
		t.Logf("行1 created_at = %s", val)
	}

	// 第三行应为 nil
	if data[2]["created_at"] != nil {
		t.Errorf("行3 created_at 应为 nil, got %T(%v)", data[2]["created_at"], data[2]["created_at"])
	}
}

// TestNullValuesRoundTrip 验证 NULL 值在各类型列中的正确往返
func TestNullValuesRoundTrip(t *testing.T) {
	ctx := context.Background()

	src := &sqlite.Adapter{}
	if err := src.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接源 SQLite 失败: %v", err)
	}
	defer src.Close()

	if err := src.ExecContext(ctx, `CREATE TABLE null_test (
  id INTEGER PRIMARY KEY,
  int_val INTEGER,
  real_val REAL,
  text_val TEXT,
  blob_val BLOB
)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 全 NULL 行 + 有值行
	rows := []types.Row{
		{"id": int64(1), "int_val": nil, "real_val": nil, "text_val": nil, "blob_val": nil},
		{"id": int64(2), "int_val": int64(42), "real_val": 3.14, "text_val": "hello", "blob_val": []byte("binary")},
	}

	if err := src.WriteData(ctx, "null_test", []string{"id", "int_val", "real_val", "text_val", "blob_val"}, rows); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	data, err := src.ReadData(ctx, "null_test", 0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}

	if len(data) != 2 {
		t.Fatalf("行数 = %d, want 2", len(data))
	}

	// 第一行全 NULL（除 id）
	row1 := data[0]
	for k, v := range row1 {
		if k != "id" && v != nil {
			t.Errorf("行1 列 %s 应为 nil, got %T(%v)", k, v, v)
		}
	}

	// 第二行有值
	row2 := data[1]
	if row2["int_val"] == nil {
		t.Error("行2 int_val 不应为 nil")
	}
	if row2["text_val"] == nil {
		t.Error("行2 text_val 不应为 nil")
	}
}
