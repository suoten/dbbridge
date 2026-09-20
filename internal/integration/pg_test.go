//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	types "dbbridge/pkg"
)

func pgRaw(t *testing.T) *rawDB {
	return openRaw(t, "postgres", "postgres://postgres:postgres123@127.0.0.1:5433/postgres?sslmode=disable")
}

func pgTargetCfg() types.ConnectionConfig {
	return types.ConnectionConfig{Type: types.PostgreSQL, Host: "127.0.0.1", Port: 5433, Username: "postgres", Password: "postgres123", Database: "postgres", SSLMode: "disable"}
}

// TestPostgreSQL_MappingAndTablespace PG 全矩阵：
// 按表覆盖优先级（users→special / orders→ods）、跨 schema 外键、
// 真实 TABLESPACE 落位、备份→恢复往返、先删后建、DataOnly、序列修复。
func TestPostgreSQL_MappingAndTablespace(t *testing.T) {
	src := buildSQLiteSource(t)
	dst := pgTargetCfg()

	// 前置：重建 schema 彻底隔离历史残留；表空间需单条执行（不能在事务块内）
	raw := pgRaw(t)
	raw.exec(`DROP SCHEMA IF EXISTS ods CASCADE`)
	raw.exec(`DROP SCHEMA IF EXISTS special CASCADE`)
	raw.exec(`CREATE SCHEMA ods`)
	raw.exec(`CREATE SCHEMA special`)
	raw.exec(`DROP TABLESPACE IF EXISTS ts1`)
	raw.exec(`CREATE TABLESPACE ts1 LOCATION '/pgts'`)

	tables := []string{"users", "orders"}
	ods := pgRaw(t)
	special := pgRaw(t)

	// —— 首轮：按表覆盖 users→special，其余走全局默认 ods ——
	rep := runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.SchemaTables = map[string]string{"users": "special"}
	})
	assertReportClean(t, rep)
	// 校验统一用全限定名，不依赖 search_path
	ods.mustEqual("ods.orders 首轮行数", srcOrders, ods.countRows(`ods."orders"`))
	special.mustEqual("special.users 首轮行数", srcUsers, special.countRows(`special."users"`))
	// 跨 schema 外键：ods.orders → special.users
	ods.mustEqual("ods.orders 跨 schema 外键数", 1,
		ods.count(`SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema='ods' AND table_name='orders' AND constraint_type='FOREIGN KEY'`))
	// 序列修复：不带 id 插入不冲突
	special.exec(`INSERT INTO special.users(name) VALUES ('autoinc_probe')`)
	special.mustEqual("序列修复后 users 行数", srcUsers+1, special.countRows(`special."users"`))
	t.Log("PG 首轮通过：优先级落位 + 跨 schema 外键 + 序列修复")

	// —— 表空间轮：先删后建 orders，使其落在 ts1 上 ——
	// FK 引用表映射必须与首轮一致（users 实际在 special），
	// 否则引用会落到 ods.users（不存在）——与真实使用场景配置一致
	rep = runMigration(t, src, dst, []string{"orders"}, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.SchemaTables = map[string]string{"users": "special"}
		c.Tablespace = "ts1"
		c.DropIfExists = true
	})
	assertReportClean(t, rep)
	raw.mustEqual("orders 应落在 ts1 表空间", 1,
		raw.count(`SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_tablespace ts ON c.reltablespace=ts.oid WHERE ts.spcname='ts1' AND c.relname='orders' AND n.nspname='ods' AND c.relkind='r'`))
	t.Log("PG 表空间轮通过")

	time.Sleep(1100 * time.Millisecond)

	// —— 备份轮 ——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.SchemaTables = map[string]string{"users": "special"}
		c.BackupBefore = true
	})
	assertReportClean(t, rep)
	ods.mustEqual("ods 中备份表数量", 1,
		ods.count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='ods' AND table_name LIKE '\_bak\_%'`))
	special.mustEqual("special 中备份表数量", 1,
		special.count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='special' AND table_name LIKE '\_bak\_%'`))
	ods.mustEqual("ods.orders 备份轮行数", srcOrders, ods.countRows(`ods."orders"`))
	special.mustEqual("special.users 备份轮行数", srcUsers, special.countRows(`special."users"`))
	// 备份表携带的旧约束名存活时，新表约束靠随机后缀避开重名
	ods.mustEqual("ods.orders 外键（备份轮重建后）", 1,
		ods.count(`SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema='ods' AND table_name='orders' AND constraint_type='FOREIGN KEY'`))
	t.Log("PG 备份轮通过：约束名后缀避开备份表存活约束")

	// —— 先删后建轮 ——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.SchemaTables = map[string]string{"users": "special"}
		c.DropIfExists = true
	})
	assertReportClean(t, rep)
	ods.mustEqual("先删后建后 ods.orders 行数", srcOrders, ods.countRows(`ods."orders"`))
	special.mustEqual("先删后建后 special.users 行数", srcUsers, special.countRows(`special."users"`))

	// —— DataOnly 追加轮：先清空目标（DataOnly 为追加语义）——
	ods.exec(`DELETE FROM ods.orders`)
	special.exec(`DELETE FROM special.users`)
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.SchemaTables = map[string]string{"users": "special"}
		c.DataOnly = true
	})
	assertReportClean(t, rep)
	ods.mustEqual("DataOnly 后 ods.orders 行数", srcOrders, ods.countRows(`ods."orders"`))
	special.mustEqual("DataOnly 后 special.users 行数", srcUsers, special.countRows(`special."users"`))
	t.Log("PG 先删后建/DataOnly 轮通过")
}

// TestPostgreSQL_BackupRestoreRoundTrip 适配器级限定名备份→恢复往返。
// 回归点：RENAME TO 目标侧不允许带 schema。
func TestPostgreSQL_BackupRestoreRoundTrip(t *testing.T) {
	src := buildSQLiteSource(t)
	dst := pgTargetCfg()

	raw := pgRaw(t)
	raw.exec(`DROP SCHEMA IF EXISTS ods CASCADE; CREATE SCHEMA ods;`)

	rep := runMigration(t, src, dst, []string{"users"}, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
	})
	assertReportClean(t, rep)

	target := connect(t, dst)
	ctx := context.Background()
	backupName, err := target.BackupTable(ctx, "ods.users")
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	ods := pgRaw(t)
	ods.mustEqual("备份后 ods.users 应不存在", 0,
		ods.count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='ods' AND table_name='users'`))
	ods.mustEqual("备份表行数", srcUsers, ods.count("SELECT COUNT(*) FROM "+qualifiedFrom(backupName)))

	if err := target.RestoreFromBackup(ctx, backupName, "ods.users"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	ods.mustEqual("恢复后 ods.users 行数", srcUsers, ods.countRows(`ods."users"`))

	// DropBackup（限定名路径）
	backupName2, err := target.BackupTable(ctx, "ods.users")
	if err != nil {
		t.Fatalf("二次备份失败: %v", err)
	}
	if err := target.DropBackup(ctx, backupName2); err != nil {
		t.Fatalf("删除备份失败: %v", err)
	}
	ods.mustEqual("DropBackup 后备份表应不存在", 0,
		ods.count(fmt.Sprintf(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='ods' AND table_name='%s'`, backupName2)))
	t.Log("PG 备份→恢复→删备份往返通过")
}
