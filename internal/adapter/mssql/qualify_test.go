package mssql

import (
	"strings"
	"testing"
)

// TestBracketQualified 锁定 sp_rename 字符串字面量参数的逐段限定语义。
// 回归背景：曾用 '[%s]' 包整个限定名，'[ods.orders]' 在 T-SQL 中是
// "带点号的单个标识符"而非 schema 限定，sp_rename 会静默找不到对象。
func TestBracketQualified(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"orders", "[dbo].[orders]"},
		{"ods.orders", "[ods].[orders]"},
		{"weird]name.tbl", "[weird]]name].[tbl]"},
	}
	for _, tc := range cases {
		if got := bracketQualified(tc.in); got != tc.want {
			t.Errorf("bracketQualified(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestQualifyTableAndBare 限定名拆分与裸表名提取
func TestQualifyTableAndBare(t *testing.T) {
	if got := qualifyTable("orders"); got != "[orders]" {
		t.Errorf("qualifyTable(orders) = %q, want [orders]", got)
	}
	if got := qualifyTable("ods.orders"); got != "[ods].[orders]" {
		t.Errorf("qualifyTable(ods.orders) = %q, want [ods].[orders]", got)
	}
	if got := qualifyBare("ods.orders"); got != "orders" {
		t.Errorf("qualifyBare(ods.orders) = %q, want orders", got)
	}
	if got := qualifyBare("orders"); got != "orders" {
		t.Errorf("qualifyBare(orders) = %q, want orders", got)
	}
	// 含引号的标识符必须转义（防注入）
	if got := qualifyTable(`a]b.c`); !strings.Contains(got, "a]]b") {
		t.Errorf("qualifyTable 应双写右方括号, got %q", got)
	}
}
