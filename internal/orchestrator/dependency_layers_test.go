package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	_ "dbbridge/internal/adapter/sqlite"
	types "dbbridge/pkg"
)

// TestDependencyLayers 锁定 DataOnly 外键依赖分层与先删后建逆序预删的拓扑逻辑：
// orders.user_id → users.id 时，写入分层必须 users 先于 orders。
// 回归背景：真实库集成测试实测（MySQL 1452 / PG 23503）——
// 目标表已存在且携带外键时并行写数据无顺序保证，子表先写触发外键违反；
// 先删后建并行乱序删除被子表外键阻塞（PG 2BP01）。
func TestDependencyLayers(t *testing.T) {
	// 构建带外键链的 SQLite 源库：orders → users
	dir := t.TempDir()
	src := types.NewAdapter(types.SQLite)
	ctx := context.Background()
	if err := src.Connect(ctx, types.ConnectionConfig{Type: types.SQLite, Database: filepath.Join(dir, "t.db")}); err != nil {
		t.Fatalf("连接 SQLite 失败: %v", err)
	}
	defer src.Close()
	for _, ddl := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`CREATE TABLE roles (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL REFERENCES users(id), role_id INTEGER NOT NULL REFERENCES roles(id))`,
	} {
		if err := src.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}

	o := NewOrchestrator(types.MigrationConfig{
		Target:        types.ConnectionConfig{Type: types.PostgreSQL},
		SchemaDefault: "ods",
	}, nil, nil)
	o.sourceAdapter = src

	tables := []string{"orders", "roles", "users"}
	layers := o.dependencyLayers(ctx, tables)
	if len(layers) == 0 {
		t.Fatal("存在外键依赖，必须返回分层结果")
	}
	// 期望分层：{roles, users} → {orders}（roles 与 users 无依赖可并行）
	if len(layers) != 2 {
		t.Fatalf("期望 2 层，实际 %d 层: %+v", len(layers), layers)
	}
	pos := map[string]int{}
	for li, layer := range layers {
		for _, it := range layer {
			pos[it.name] = li
		}
	}
	if pos["orders"] != 1 {
		t.Errorf("orders（子表）必须在第 2 层，实际第 %d 层", pos["orders"]+1)
	}
	if pos["users"] != 0 || pos["roles"] != 0 {
		t.Errorf("users/roles（父表）必须与 orders 不同层且在第 1 层: users=%d roles=%d", pos["users"], pos["roles"])
	}

	// 无依赖表集：返回 nil（单层全并行，行为与旧版一致）
	if got := o.dependencyLayers(ctx, []string{"users", "roles"}); got != nil {
		t.Errorf("无依赖表集应返回 nil，实际 %+v", got)
	}

	// 单表：无需分层
	if got := o.dependencyLayers(ctx, []string{"orders"}); got != nil {
		t.Errorf("单表应返回 nil，实际 %+v", got)
	}
}
