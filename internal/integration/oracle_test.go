//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	types "dbbridge/pkg"

	_ "github.com/sijms/go-ora/v2"
)

// oracleRaw Oracle 校验连接
func oracleRaw(t *testing.T) *rawDB {
	return openRaw(t, "oracle", "oracle://system:Oracle123@127.0.0.1:15321/FREEPDB1")
}

func oracleTargetCfg() types.ConnectionConfig {
	return types.ConnectionConfig{Type: types.Oracle, Host: "127.0.0.1", Port: 15321, Username: "system", Password: "Oracle123", Database: "FREEPDB1"}
}

// getColDefault 读取列默认值（DATA_DEFAULT 为 LONG，取回 Go 侧判断）
func getColDefault(t *testing.T, raw *rawDB, table, column string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var def sql.NullString
	err := raw.db.QueryRowContext(ctx,
		`SELECT data_default FROM user_tab_columns WHERE table_name=:1 AND column_name=:2`, table, column).Scan(&def)
	if err != nil {
		t.Fatalf("读取列默认值失败 %s.%s: %v", table, column, err)
	}
	return strings.TrimSpace(def.String)
}

// TestOracle_AutoIncrementAndDateDefaults 生产级验证（MySQL → Oracle）：
//
//  1. AUTO_INCREMENT → IDENTITY 列（12c+）：迁移期显式插入 id 后，
//     START WITH LIMIT VALUE 重播种，应用不传 id 插入仍自动递增
//  2. 日期类字符串默认值（MySQL 风格 '2024-01-01 12:30:45'）→ 显式
//     TO_DATE/TO_TIMESTAMP：直接嵌入字面量会报 ORA-01858（修复点回归）
//  3. DataOnly 轮结构读取（含 IDENTITY 列识别）+ 重播种后再次探测插入
func TestOracle_AutoIncrementAndDateDefaults(t *testing.T) {
	const table = "ORA_EVENTS"
	const nRows = 60

	// —— 源：MySQL 表（AUTO_INCREMENT + 日期字符串默认值）——
	src := mysqlTargetCfg("dbbridge")
	msrc := mysqlRaw(t, "dbbridge")
	msrc.exec("DROP TABLE IF EXISTS " + table)
	msrc.exec(fmt.Sprintf(`CREATE TABLE %s (
		id INT AUTO_INCREMENT PRIMARY KEY,
		title VARCHAR(100) NOT NULL,
		event_date DATE DEFAULT '2024-01-01',
		updated_at DATETIME DEFAULT '2024-06-01 12:30:45',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		amount DECIMAL(10,2) DEFAULT 0
	)`, table))
	for i := 1; i <= nRows; i++ {
		msrc.exec(fmt.Sprintf(
			`INSERT INTO %s (id, title, event_date, updated_at, amount) VALUES (%d, 'event_%d', '2024-03-%02d', '2024-07-01 08:%02d:00', %d.25)`,
			table, i, i, (i-1)%28+1, (i-1)%60, i))
	}
	t.Cleanup(func() { msrc.exec("DROP TABLE IF EXISTS " + table) })

	// —— 目标：Oracle，前置清理 ——
	dst := oracleTargetCfg()
	ora := oracleRaw(t)
	ora.exec(fmt.Sprintf("BEGIN EXECUTE IMMEDIATE 'DROP TABLE %s PURGE'; EXCEPTION WHEN OTHERS THEN IF SQLCODE != -942 THEN RAISE; END IF; END;", table))

	// —— 首轮迁移 ——
	rep := runMigration(t, src, dst, []string{table}, nil)
	assertReportClean(t, rep)
	ora.mustEqual("Oracle 行数", nRows, ora.countRows(table))
	ora.mustEqual("MAX(id) 应等于行数", nRows,
		ora.count(fmt.Sprintf(`SELECT COALESCE(MAX(id),0) FROM %s`, table)))

	// IDENTITY 列落位（12c+）
	ora.mustEqual("id 列应为 IDENTITY", 1, ora.count(fmt.Sprintf(
		`SELECT COUNT(*) FROM user_tab_columns WHERE table_name='%s' AND column_name='ID' AND identity_column='YES'`, table)))

	// 日期默认值：显式 TO_DATE/TO_TIMESTAMP（ORA-01858 修复点回归锁）
	// DATA_DEFAULT 为 LONG 类型，不能用 LIKE，取回 Go 侧断言
	ora.mustContain("event_date 默认值", getColDefault(t, ora, table, "EVENT_DATE"), "TO_DATE")
	ora.mustContain("updated_at 默认值", getColDefault(t, ora, table, "UPDATED_AT"), "TO_TIMESTAMP")
	ora.mustContain("created_at 默认值", strings.ToUpper(getColDefault(t, ora, table, "CREATED_AT")), "CURRENT_TIMESTAMP")

	// —— 重播种探测：不传 id 插入必须成功且自增值 > 迁移数据最大 id ——
	ora.exec(fmt.Sprintf(`INSERT INTO %s (title) VALUES ('probe_after_migration')`, table))
	ora.mustEqual("探测插入后行数", nRows+1, ora.countRows(table))
	ora.mustEqual("自增种子应大于迁移数据最大 id", int64(0),
		ora.count(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE title='probe_after_migration' AND id <= %d`, table, nRows)))
	ora.exec(fmt.Sprintf(`DELETE FROM %s WHERE title='probe_after_migration'`, table))

	// —— DataOnly 轮：目标结构读取（IDENTITY 列识别）+ 再次探测 ——
	ora.exec(fmt.Sprintf(`DELETE FROM %s`, table))
	rep = runMigration(t, src, dst, []string{table}, func(c *types.MigrationConfig) {
		c.DataOnly = true
	})
	assertReportClean(t, rep)
	ora.mustEqual("DataOnly 后行数", nRows, ora.countRows(table))
	ora.exec(fmt.Sprintf(`INSERT INTO %s (title) VALUES ('probe_dataonly')`, table))
	ora.mustEqual("DataOnly 后探测插入行数", nRows+1, ora.countRows(table))
	t.Log("Oracle 全部通过：IDENTITY 自增 + 日期默认值转换 + 重播种")
}
