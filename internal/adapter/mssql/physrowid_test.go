package mssql

import (
	"strings"
	"testing"
)

// 防回归一：%%physloc%% 是 MSSQL 物理行定位符的字面写法（双百分号），
// 曾经的 Bug 是把它直接写在 fmt.Sprintf 的格式串里，%% 转义后输出为
// 单百分号 %physloc%，导致无主键表迁移第一批查询报 syntax error 102。
// 修复后通过 %s 参数传入常量，本测试锁定"输出的 SQL 必须保留双百分号"。
func TestBuildPhysicalRowIDQueryKeepsDoublePercent(t *testing.T) {
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

			// 必须出现双百分号的物理行定位符，共 2 处（SELECT 列 + ORDER BY）或 3 处（含 WHERE）
			wantCount := 2
			if tc.hasLastRowID {
				wantCount = 3
			}
			if got := strings.Count(query, mssqlPhysLoc); got != wantCount {
				t.Fatalf("%%physloc%% 出现次数 = %d, 期望 %d\nSQL: %s", got, wantCount, query)
			}

			// 绝不允许出现被 Sprintf 吃掉一半的单百分号残留
			if strings.Contains(query, " %physloc%") {
				t.Fatalf("出现单百分号残留（Sprintf 转义 Bug 复发）:\n%s", query)
			}

			// 结构完整性（两个分支共有）；ORDER BY 必须与 WHERE 同用
			// CONVERT(bigint, ...) 表达式（有符号 bigint 排序），禁止裸 %%physloc%%
			for _, want := range []string{
				"CONVERT(bigint, %%physloc%%) AS _physrowid",
				"FROM [orders]",
				"ORDER BY CONVERT(bigint, %%physloc%%)",
			} {
				if !strings.Contains(query, want) {
					t.Errorf("SQL 缺少片段 %q\nSQL: %s", want, query)
				}
			}
			if tc.hasLastRowID {
				if !strings.Contains(query, "WHERE CONVERT(bigint, %%physloc%%) > @p1") {
					t.Errorf("续批 SQL 缺少游标比较条件\nSQL: %s", query)
				}
				if !strings.Contains(query, "FETCH NEXT @p2 ROWS ONLY") {
					t.Errorf("续批 SQL limit 应占位 @p2\nSQL: %s", query)
				}
			} else {
				if !strings.Contains(query, "OFFSET 0 ROWS FETCH NEXT @p1 ROWS ONLY") {
					t.Errorf("首批 SQL limit 应占位 @p1\nSQL: %s", query)
				}
			}
		})
	}
}

// 防回归二：游标比较与排序必须使用同一表达式。
// 曾经的 Bug：WHERE 用 CONVERT(bigint, %%physloc%%) 比较，
// ORDER BY 却用原始 %%physloc%%（varbinary 按无符号二进制排序）。
// CONVERT(bigint,...) 是有符号解释，高位为 1 的 physloc 转换后是负数，
// 在二进制排序中却排在最后：续批游标推进后这些行被跳过，
// 造成静默丢数据且程序误报迁移完成。
func TestBuildPhysicalRowIDQueryOrdersByConvertedExpression(t *testing.T) {
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

			// ORDER BY 必须包裹 CONVERT(bigint, ...)，禁止裸 %%physloc%%
			const orderByConverted = "ORDER BY CONVERT(bigint, %%physloc%%)"
			if !strings.Contains(query, orderByConverted) {
				t.Errorf("ORDER BY 必须使用与 WHERE 相同的 CONVERT(bigint,...) 表达式\nSQL: %s", query)
			}

			// WHERE 游标比较与 ORDER BY 的表达式必须逐字一致（续批）
			const compareExpr = "CONVERT(bigint, %%physloc%%)"
			if tc.hasLastRowID && !strings.Contains(query, "WHERE "+compareExpr+" > @p1 "+orderByConverted) {
				t.Errorf("WHERE 比较表达式与 ORDER BY 表达式不一致\nSQL: %s", query)
			}
		})
	}
}
