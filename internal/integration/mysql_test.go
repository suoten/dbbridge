//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	types "dbbridge/pkg"
)

// mysqlRaw MySQL 校验连接
func mysqlRaw(t *testing.T, database string) *rawDB {
	return openRaw(t, "mysql", fmt.Sprintf("root:root123@tcp(127.0.0.1:3307)/%s?multiStatements=true", database))
}

func mysqlTargetCfg(database string) types.ConnectionConfig {
	return types.ConnectionConfig{Type: types.MySQL, Host: "127.0.0.1", Port: 3307, Username: "root", Password: "root123", Database: database, Charset: "utf8mb4"}
}

// TestMySQL_MappedThreeRounds 生产级核心矩阵：
// 映射到 ods 库 × 三轮迁移（首轮/备份轮/先删后建轮）+ DataOnly 追加轮。
func TestMySQL_MappedThreeRounds(t *testing.T) {
	src := buildSQLiteSource(t)
	dst := mysqlTargetCfg("dbbridge")

	// 前置：重建 ods 库彻底隔离历史残留
	raw := mysqlRaw(t, "")
	raw.exec("DROP DATABASE IF EXISTS ods; CREATE DATABASE ods")

	tables := []string{"users", "orders"}

	// —— 回归轮：未映射，行为与既往版本一致（落 dbbridge）——
	rep := runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.DropIfExists = true
	})
	assertReportClean(t, rep)
	raw2 := mysqlRaw(t, "dbbridge")
	raw2.mustEqual("dbbridge.users 行数", srcUsers, raw2.countRows("`users`"))
	raw2.mustEqual("dbbridge.orders 行数", srcOrders, raw2.countRows("`orders`"))
	raw2.mustZero("dbbridge 中不应出现 _bak 表",
		raw2.count(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='dbbridge' AND TABLE_NAME LIKE '\_bak\_%'`))
	t.Log("回归轮通过：未映射行为不变")

	// —— 首轮：映射到 ods ——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
	})
	assertReportClean(t, rep)
	ods := mysqlRaw(t, "ods")
	ods.mustEqual("ods.users 首轮行数", srcUsers, ods.countRows("`users`"))
	ods.mustEqual("ods.orders 首轮行数", srcOrders, ods.countRows("`orders`"))
	// 跨库延迟外键已补建（orders.user_id → ods.users.id）
	ods.mustEqual("ods.orders 外键数", 1,
		ods.count(`SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA='ods' AND TABLE_NAME='orders'`))
	// 自增修复：不带 id 插入不冲突
	ods.exec("INSERT INTO `users`(name) VALUES ('autoinc_probe')")
	ods.mustEqual("自增修复后 users 行数", srcUsers+1, ods.countRows("`users`"))
	t.Log("首轮通过：映射落位 + 跨库外键 + 自增修复")

	time.Sleep(1100 * time.Millisecond) // 备份名精确到秒，避免同名冲突

	// —— 备份轮：目标存在同名表 → 备份落在 ods，新表数据精确 ——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.BackupBefore = true
	})
	assertReportClean(t, rep)
	ods.mustEqual("ods 中备份表数量", 2,
		ods.count(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='ods' AND TABLE_NAME LIKE '\_bak\_%'`))
	ods.mustEqual("ods.users 备份轮行数", srcUsers, ods.countRows("`users`"))
	ods.mustEqual("ods.orders 备份轮行数", srcOrders, ods.countRows("`orders`"))
	ods.mustZero("备份轮不应产生重复数据",
		ods.count(`SELECT COUNT(*) FROM (SELECT id, COUNT(*) c FROM users GROUP BY id HAVING c > 1) d`))
	t.Log("备份轮通过：备份表落位、无重复")

	time.Sleep(1100 * time.Millisecond)

	// —— 先删后建轮：不留新备份，行数精确 ——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.DropIfExists = true
	})
	assertReportClean(t, rep)
	ods.mustEqual("先删后建后备份表数量（应保持 2 不新增）", 2,
		ods.count(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='ods' AND TABLE_NAME LIKE '\_bak\_%'`))
	ods.mustEqual("ods.users 先删后建行数", srcUsers, ods.countRows("`users`"))
	ods.mustEqual("ods.orders 先删后建行数", srcOrders, ods.countRows("`orders`"))
	t.Log("先删后建轮通过")

	time.Sleep(1100 * time.Millisecond)

	// —— DataOnly + 映射轮：先清空目标再追加（DataOnly 为追加语义）——
	// 该轮校验点：限定名下目标结构读取（GetTableSchema schema 感知修复点）
	ods.exec("DELETE FROM ods.orders; DELETE FROM ods.users")
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
		c.DataOnly = true
	})
	assertReportClean(t, rep)
	ods.mustEqual("DataOnly 后 ods.users 行数", srcUsers, ods.countRows("`users`"))
	ods.mustEqual("DataOnly 后 ods.orders 行数", srcOrders, ods.countRows("`orders`"))
	t.Log("DataOnly 轮通过：限定名下目标结构读取正常")
}

// TestMySQL_BackupRestoreRoundTrip 适配器级备份→恢复往返（限定名路径）。
func TestMySQL_BackupRestoreRoundTrip(t *testing.T) {
	src := buildSQLiteSource(t)
	dst := mysqlTargetCfg("dbbridge")

	// 备份 ods.users → 恢复 → 数据必须原样
	// 前置：清掉矩阵测试残留（隔离），再重新迁移到 ods
	mysqlRaw(t, "").exec("DROP TABLE IF EXISTS ods.orders; DROP TABLE IF EXISTS ods.users")

	rep := runMigration(t, src, dst, []string{"users", "orders"}, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
	})
	assertReportClean(t, rep)

	target := connect(t, dst)
	ctx := context.Background()
	backupName, err := target.BackupTable(ctx, "ods.users")
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	ods := mysqlRaw(t, "ods")
	ods.mustEqual("备份后 ods.users 应不存在（被重命名）", 0,
		ods.count(`SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='ods' AND TABLE_NAME='users'`))
	ods.mustEqual("备份表行数", srcUsers, ods.count("SELECT COUNT(*) FROM "+qualifiedFromBacktick(backupName)))

	if err := target.RestoreFromBackup(ctx, backupName, "ods.users"); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	ods.mustEqual("恢复后 ods.users 行数", srcUsers, ods.countRows("`users`"))
	t.Log("MySQL 备份→恢复往返通过")
}
