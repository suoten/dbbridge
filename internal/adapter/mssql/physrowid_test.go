package mssql

import (
	"strings"
	"testing"
)

// 防回归：%%physloc%% 是 MSSQL 物理行定位符的字面写法（双百分号），
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
			query := buildPhysicalRowIDQuery(colList, "orders", tc.hasLastRowID)

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

			// 结构完整性（两个分支共有）
			for _, want := range []string{
				"CONVERT(bigint, %%physloc%%) AS _physrowid",
				"FROM [orders]",
				"ORDER BY %%physloc%%",
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
