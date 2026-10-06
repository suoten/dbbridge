package adapter

import (
	"context"
	"fmt"
	"testing"

	"dbbridge/internal/adapter/sqlite"
	types "dbbridge/pkg"
)

// TestBatchInsertLargeBatch 验证多值 INSERT 在大批量写入时的正确性
// 写入 1200 行（超过 500 行/批的阈值，触发多批多值 INSERT）
func TestBatchInsertLargeBatch(t *testing.T) {
	ctx := context.Background()

	a := &sqlite.Adapter{}
	if err := a.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer a.Close()

	if err := a.ExecContext(ctx, `CREATE TABLE bulk_test (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value REAL DEFAULT 0,
		data BLOB
	)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 构造 1200 行数据（超过 multiRowBatchSize=500，触发多批）
	rowCount := 1200
	rows := make([]types.Row, 0, rowCount)
	for i := 1; i <= rowCount; i++ {
		var blobData []byte
		if i%3 == 0 {
			blobData = []byte(fmt.Sprintf("blob-%d", i))
		}
		rows = append(rows, types.Row{
			"id":    int64(i),
			"name":  fmt.Sprintf("item-%04d", i),
			"value": float64(i) * 1.5,
			"data":  blobData,
		})
	}

	cols := []string{"id", "name", "value", "data"}
	if err := a.WriteData(ctx, "bulk_test", cols, rows); err != nil {
		t.Fatalf("写入 %d 行失败: %v", rowCount, err)
	}

	// 验证行数
	count, err := a.GetRowCount(ctx, "bulk_test")
	if err != nil {
		t.Fatalf("获取行数失败: %v", err)
	}
	if count != int64(rowCount) {
		t.Errorf("行数 = %d, want %d", count, rowCount)
	}

	// 验证首行和末行
	firstRow, err := a.ReadDataKeyset(ctx, "bulk_test", "id", nil, 1)
	if err != nil || len(firstRow) != 1 {
		t.Fatalf("读首行失败: err=%v, len=%d", err, len(firstRow))
	}
	if firstRow[0]["name"] != "item-0001" {
		t.Errorf("首行 name = %v, want item-0001", firstRow[0]["name"])
	}

	lastRows, err := a.ReadDataKeyset(ctx, "bulk_test", "id", int64(rowCount-1), 1)
	if err != nil || len(lastRows) != 1 {
		t.Fatalf("读末行失败: err=%v, len=%d", err, len(lastRows))
	}
	if lastRows[0]["name"] != fmt.Sprintf("item-%04d", rowCount) {
		t.Errorf("末行 name = %v, want item-%04d", lastRows[0]["name"], rowCount)
	}

	// 验证 BLOB 数据（第 3 行应有 BLOB）
	blobRow, err := a.ReadDataKeyset(ctx, "bulk_test", "id", int64(2), 1)
	if err != nil || len(blobRow) != 1 {
		t.Fatalf("读第 3 行失败: err=%v, len=%d", err, len(blobRow))
	}
	blob, ok := blobRow[0]["data"].([]byte)
	if !ok {
		t.Errorf("第 3 行 data 应为 []byte, got %T", blobRow[0]["data"])
	} else if string(blob) != "blob-3" {
		t.Errorf("第 3 行 data = %q, want blob-3", string(blob))
	}

	// 验证 NULL BLOB（第 2 行应为 nil）
	nullRow, err := a.ReadDataKeyset(ctx, "bulk_test", "id", int64(1), 1)
	if err != nil || len(nullRow) != 1 {
		t.Fatalf("读第 2 行失败: err=%v, len=%d", err, len(nullRow))
	}
	if nullRow[0]["data"] != nil {
		t.Errorf("第 2 行 data 应为 nil, got %T(%v)", nullRow[0]["data"], nullRow[0]["data"])
	}
}

// TestBatchInsertEmptyAndSingle 验证空数据和单行数据的边界情况
func TestBatchInsertEmptyAndSingle(t *testing.T) {
	ctx := context.Background()

	a := &sqlite.Adapter{}
	if err := a.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer a.Close()

	if err := a.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// 空数据不应报错
	if err := a.WriteData(ctx, "t", []string{"id", "val"}, []types.Row{}); err != nil {
		t.Errorf("写入空数据报错: %v", err)
	}

	// 单行数据
	if err := a.WriteData(ctx, "t", []string{"id", "val"}, []types.Row{
		{"id": int64(1), "val": "single"},
	}); err != nil {
		t.Fatalf("写入单行失败: %v", err)
	}

	count, err := a.GetRowCount(ctx, "t")
	if err != nil {
		t.Fatalf("获取行数失败: %v", err)
	}
	if count != 1 {
		t.Errorf("行数 = %d, want 1", count)
	}
}

// TestBatchInsertExactly500 验证恰好 500 行（批次边界）
func TestBatchInsertExactly500(t *testing.T) {
	ctx := context.Background()

	a := &sqlite.Adapter{}
	if err := a.Connect(ctx, types.ConnectionConfig{Database: ":memory:"}); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer a.Close()

	if err := a.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, n INTEGER)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	rows := make([]types.Row, 0, 500)
	for i := 1; i <= 500; i++ {
		rows = append(rows, types.Row{"id": int64(i), "n": int64(i * 10)})
	}

	if err := a.WriteData(ctx, "t", []string{"id", "n"}, rows); err != nil {
		t.Fatalf("写入 500 行失败: %v", err)
	}

	count, err := a.GetRowCount(ctx, "t")
	if err != nil {
		t.Fatalf("获取行数失败: %v", err)
	}
	if count != 500 {
		t.Errorf("行数 = %d, want 500", count)
	}
}
