package db2

import (
	"strings"
	"testing"
)

// 防回归：Db2 物理行游标曾用 ROW_NUMBER() OVER (ORDER BY (VALUES(1))) 造行号，
// 常量排序下行号分配跨批次不确定，第一批与第二批的行号可能对应不同物理行，
// 造成重复/静默丢数据且程序误报迁移完成。现改为 RID()（BIGINT 物理行标识），
// 本测试锁定：
//  1. WHERE 游标比较与 ORDER BY 必须使用同一表达式 RID("表名")
//  2. 禁止 ROW_NUMBER 常量排序方案回归
//  3. _physrowid 别名必须加引号（Db2 会把未加引号的别名转大写，
//     编排器按 "_physrowid" 取游标值会取到 nil 导致游标无法推进）
func TestBuildPhysicalRowIDQueryUsesRID(t *testing.T) {
	colList := quoteIdentifiers([]string{"id", "name"})

	for _, tc := range []struct {
		name         string
		hasLastRowID bool
	}{
		{name: "first batch", hasLastRowID: false},
		{name: "next batch", hasLastRowID: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := buildPhysicalRowIDQuery(colList, qualifyTable("orders"), tc.hasLastRowID)

			// 禁止 ROW_NUMBER 常量排序方案回归
			if strings.Contains(query, "ROW_NUMBER") || strings.Contains(query, "VALUES(1)") {
				t.Errorf("禁止使用 ROW_NUMBER 常量排序（跨批次行号不确定，静默丢数据 Bug 复发）\nSQL: %s", query)
			}

			// WHERE 比较与 ORDER BY 表达式必须一致（qualifyTable("orders") → "orders"）
			const rid = `RID("orders")`
			if !strings.Contains(query, `AS "_physrowid"`) {
				t.Errorf("_physrowid 别名必须加引号防 Db2 转大写\nSQL: %s", query)
			}
			if tc.hasLastRowID {
				if !strings.Contains(query, "WHERE "+rid+" > ? "+`ORDER BY `+rid) {
					t.Errorf("WHERE 游标比较与 ORDER BY 表达式不一致\nSQL: %s", query)
				}
				if !strings.Contains(query, "FETCH FIRST ? ROWS ONLY") {
					t.Errorf("续批 limit 应占位 ?\nSQL: %s", query)
				}
			} else {
				if !strings.Contains(query, `FROM "orders" ORDER BY `+rid) {
					t.Errorf("首批 SQL 结构错误\nSQL: %s", query)
				}
			}
		})
	}
}
