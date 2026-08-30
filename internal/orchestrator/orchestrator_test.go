package orchestrator

import (
	"context"
	"reflect"
	"testing"

	types "dbbridge/pkg"
)

func TestRunRejectsSQLFileMode(t *testing.T) {
	o := NewOrchestrator(types.MigrationConfig{
		SQLFilePath: "dump.sql",
	}, nil, nil)
	report, err := o.Run(context.Background())
	if err == nil {
		t.Fatal("SQL 文件模式应在启动阶段快速失败，实际未报错")
	}
	if report == nil {
		t.Fatal("失败时应返回已初始化的报告而非 nil")
	}
	if got := err.Error(); got != "SQL 文件迁移模式暂未支持，请使用直连模式" {
		t.Errorf("报错信息 = %q, 期望包含明确的暂不支持提示", got)
	}
}

func TestSplitSQL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "单条语句无分号结尾",
			input: "CREATE TABLE t (id INT)",
			want:  []string{"CREATE TABLE t (id INT)"},
		},
		{
			name:  "多条语句",
			input: "CREATE TABLE a (id INT); CREATE TABLE b (id INT);",
			want:  []string{"CREATE TABLE a (id INT)", " CREATE TABLE b (id INT)"},
		},
		{
			name:  "引号内的分号不分割",
			input: "CREATE TABLE t (c VARCHAR(10) DEFAULT 'a;b'); COMMENT ON TABLE t IS 'x;y'",
			want:  []string{"CREATE TABLE t (c VARCHAR(10) DEFAULT 'a;b')", " COMMENT ON TABLE t IS 'x;y'"},
		},
		{
			name:  "双引号内的分号不分割",
			input: `SELECT "col;name" FROM t;`,
			want:  []string{`SELECT "col;name" FROM t`},
		},
		{
			name:  "空语句被忽略",
			input: ";;;CREATE TABLE t (id INT);;;",
			want:  []string{"CREATE TABLE t (id INT)"},
		},
	}
	for _, c := range cases {
		got := splitSQL(c.input)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: splitSQL = %#v, want %#v", c.name, got, c.want)
		}
	}
}
