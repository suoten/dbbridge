//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	types "dbbridge/pkg"
)

// mssqlRaw MSSQL 校验连接（校验查询全部带 schema 限定）
func mssqlRaw(t *testing.T, database string) *rawDB {
	dsn := fmt.Sprintf("sqlserver://sa:YourStrong!Passw0rd@127.0.0.1:14333?database=%s", database)
	return openRaw(t, "sqlserver", dsn)
}

func mssqlTargetCfg(database string) types.ConnectionConfig {
	return types.ConnectionConfig{Type: types.MSSQL, Host: "127.0.0.1", Port: 14333, Username: "sa", Password: "YourStrong!Passw0rd", Database: database}
}

// cleanupMSSQLBackups 清理历史运行残留的备份表（含旧版本方括号命名缺陷产生的
// [_bak...] 字面名）。备份表会携带旧约束名（PK/FK）存活，且可能通过外键互相
// 引用导致单趟 DROP 被阻塞，故多趟循环直至清空（每趟至少删掉未被引用的表）。
// 注意：必须在清理正式表之前调用，否则残留备份表的 FK 会阻塞正式表 DROP。
func cleanupMSSQLBackups(t *testing.T, raw *rawDB) {
	for pass := 0; pass < 6; pass++ {
		dropped := 0
		for _, sch := range []string{"ods", "dbo"} {
			var names []string
			// '[_]bak%'：正常备份表；'[[]%'：旧版 sp_rename 方括号缺陷产生的字面
			// 带方括号名的备份表（表名本身含 '[' 字符，需用 [[] 转义匹配）
			rows, err := raw.db.Query(fmt.Sprintf(
				`SELECT t.name FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='%s' AND (t.name LIKE '[_]bak%%' OR t.name LIKE '[[]%%')`, sch))
			if err != nil {
				t.Fatalf("查询备份表失败: %v", err)
			}
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil {
					names = append(names, n)
				}
			}
			rows.Close()
			for _, n := range names {
				esc := strings.ReplaceAll(n, "]", "]]")
				_, err := raw.db.Exec(fmt.Sprintf("IF OBJECT_ID('[%s].[%s]') IS NOT NULL DROP TABLE [%s].[%s]", sch, esc, sch, esc))
				if err == nil {
					dropped++
				}
			}
		}
		if dropped == 0 {
			// 全部删除失败：复查是否仍有残留（可能被外键阻塞），有则报告而非静默放过
			for _, sch := range []string{"ods", "dbo"} {
				if n := raw.count(fmt.Sprintf(`SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='%s' AND (t.name LIKE '[_]bak%%' OR t.name LIKE '[[]%%')`, sch)); n > 0 {
					t.Fatalf("schema %s 中仍有 %d 张备份表无法删除（外键阻塞？）", sch, n)
				}
			}
			return
		}
	}
	t.Fatal("备份表清理未收敛（存在循环外键引用）")
}

// TestMSSQL_MappedSchemaMatrix 生产级核心矩阵（MSSQL）：
// 映射到 ods schema × 三轮迁移 + DataOnly（GetTableSchema schema 感知修复点）
// + ON [PRIMARY] 表空间子句 + 自增重播种。
// 部分映射：orders→ods（按表覆盖），users 不配置（落默认 dbo）——
// 覆盖“无全局默认 + 按表覆盖”组合与跨 schema 外键。
func TestMSSQL_MappedSchemaMatrix(t *testing.T) {
	src := buildSQLiteSource(t)
	dst := mssqlTargetCfg("master")

	// 前置：先清残留备份表（其 FK/约束名会阻塞正式表清理），再清正式表
	raw := mssqlRaw(t, "master")
	cleanupMSSQLBackups(t, raw)
	raw.exec(`
IF NOT EXISTS (SELECT 1 FROM sys.schemas WHERE name='ods') EXEC('CREATE SCHEMA ods');
IF OBJECT_ID('[ods].[orders]') IS NOT NULL DROP TABLE [ods].[orders];
IF OBJECT_ID('[ods].[users]') IS NOT NULL DROP TABLE [ods].[users];
IF OBJECT_ID('[dbo].[orders]') IS NOT NULL DROP TABLE [dbo].[orders];
IF OBJECT_ID('[dbo].[users]') IS NOT NULL DROP TABLE [dbo].[users];
`)

	tables := []string{"users", "orders"}

	// —— 首轮：orders→ods，users→dbo（未配置映射的部分保持默认）——
	rep := runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaTables = map[string]string{"orders": "ods"}
	})
	assertReportClean(t, rep)
	raw.mustEqual("dbo.users 首轮行数", srcUsers, raw.countRows("[dbo].[users]"))
	raw.mustEqual("ods.orders 首轮行数", srcOrders, raw.countRows("[ods].[orders]"))
	// 跨 schema 外键：ods.orders → dbo.users（约束名带随机后缀，按表名定位）
	raw.mustEqual("ods.orders 外键数（引用 dbo.users）", 1,
		raw.count(`SELECT COUNT(*) FROM sys.foreign_keys fk JOIN sys.tables t ON t.object_id=fk.parent_object_id JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='ods' AND t.name='orders'`))
	t.Log("MSSQL 首轮通过：部分映射落位 + 跨 schema 外键")

	time.Sleep(1100 * time.Millisecond) // 备份名精确到秒

	// —— 备份轮：sp_rename 逐段限定（'[ods].[orders]' 而非 '[ods.orders]'）——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaTables = map[string]string{"orders": "ods"}
		c.BackupBefore = true
	})
	assertReportClean(t, rep)
	raw.mustEqual("ods 中备份表数量", 1,
		raw.count(`SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='ods' AND t.name LIKE '[_]bak%'`))
	raw.mustEqual("dbo 中备份表数量", 1,
		raw.count(`SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='dbo' AND t.name LIKE '[_]bak%'`))
	raw.mustEqual("ods.orders 备份轮行数", srcOrders, raw.countRows("[ods].[orders]"))
	raw.mustEqual("dbo.users 备份轮行数", srcUsers, raw.countRows("[dbo].[users]"))
	t.Log("MSSQL 备份轮通过：sp_rename 限定名正确")

	time.Sleep(1100 * time.Millisecond)

	// —— 先删后建轮 + 表空间 ON [PRIMARY] ——
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaTables = map[string]string{"orders": "ods"}
		c.DropIfExists = true
		c.Tablespace = "PRIMARY"
	})
	assertReportClean(t, rep)
	raw.mustEqual("先删后建后 dbo.users 行数", srcUsers, raw.countRows("[dbo].[users]"))
	raw.mustEqual("先删后建后 ods.orders 行数", srcOrders, raw.countRows("[ods].[orders]"))
	// 自增重播种（DBCC CHECKIDENT 走 bracketQualified）：不带 id 插入不冲突
	raw.exec("INSERT INTO [dbo].[users](name) VALUES ('autoinc_probe')")
	raw.mustEqual("自增重播种后 dbo.users 行数", srcUsers+1, raw.countRows("[dbo].[users]"))
	t.Log("MSSQL 先删后建 + 表空间 + 重播种轮通过")

	time.Sleep(1100 * time.Millisecond)

	// —— DataOnly + 映射轮：目标结构读取走限定名（GetTableSchema 修复点）——
	// 上一轮插入了自增探针行，先清数据再迁移（与 MySQL/PG 一致的追加语义）
	raw.exec("DELETE FROM [ods].[orders]; DELETE FROM [dbo].[users];")
	rep = runMigration(t, src, dst, tables, func(c *types.MigrationConfig) {
		c.SchemaTables = map[string]string{"orders": "ods"}
		c.DataOnly = true
	})
	assertReportClean(t, rep)
	raw.mustEqual("DataOnly 后 dbo.users 行数", srcUsers, raw.countRows("[dbo].[users]"))
	raw.mustEqual("DataOnly 后 ods.orders 行数", srcOrders, raw.countRows("[ods].[orders]"))
	t.Log("MSSQL DataOnly 轮通过：限定名结构读取正常")
}

// TestMSSQL_BackupRestoreRoundTrip 适配器级限定名备份→恢复往返。
// 回归点：sp_rename 的 '[ods.orders]' 是带点号的单标识符而非 schema 限定。
func TestMSSQL_BackupRestoreRoundTrip(t *testing.T) {
	src := buildSQLiteSource(t)
	dst := mssqlTargetCfg("master")

	raw := mssqlRaw(t, "master")
	// 残留备份表可能持有同名 PK（schema 级唯一）阻塞建表，先清理
	cleanupMSSQLBackups(t, raw)
	raw.exec(`
IF NOT EXISTS (SELECT 1 FROM sys.schemas WHERE name='ods') EXEC('CREATE SCHEMA ods');
IF OBJECT_ID('[ods].[users]') IS NOT NULL DROP TABLE [ods].[users];
`)

	rep := runMigration(t, src, dst, []string{"users"}, func(c *types.MigrationConfig) {
		c.SchemaDefault = "ods"
	})
	assertReportClean(t, rep)

	target := connect(t, dst)
	ctx := context.Background()
	backupName, err := target.BackupTable(ctx, "ods.users")
	if err != nil {
		t.Fatalf("备份失败（sp_rename 限定名路径）: %v", err)
	}
	ods := mssqlRaw(t, "master")
	ods.mustEqual("备份后 ods.users 应不存在", 0,
		ods.count(`SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='ods' AND t.name='users'`))
	ods.mustEqual("备份表行数", srcUsers, ods.countRows(fmt.Sprintf("[ods].[%s]", backupName[len("ods."):])))

	if err := target.RestoreFromBackup(ctx, backupName, "ods.users"); err != nil {
		t.Fatalf("恢复失败（RENAME 目标侧裸名路径）: %v", err)
	}
	ods.mustEqual("恢复后 ods.users 行数", srcUsers, ods.countRows("[ods].[users]"))

	// DropBackup 限定名路径
	backupName2, err := target.BackupTable(ctx, "ods.users")
	if err != nil {
		t.Fatalf("二次备份失败: %v", err)
	}
	if err := target.DropBackup(ctx, backupName2); err != nil {
		t.Fatalf("删除备份失败（qualifyTable 限定名路径）: %v", err)
	}
	ods.mustEqual("DropBackup 后备份表应不存在", 0,
		ods.count(fmt.Sprintf(`SELECT COUNT(*) FROM sys.tables t JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name='ods' AND t.name='%s'`, backupName2[len("ods."):])))
	t.Log("MSSQL 备份→恢复→删备份往返通过")
}
