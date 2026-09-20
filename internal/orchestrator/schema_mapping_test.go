package orchestrator

import (
	"testing"

	types "dbbridge/pkg"
)

// resolveTargetName 的优先级与语义锁定：
// 按表覆盖 > 正则规则（顺序首个命中）> 全局默认 > 原样返回。
// 未配置任何映射时必须原样返回（保证与既往版本行为完全一致）。
func TestResolveTargetName(t *testing.T) {
	mk := func(cfg types.MigrationConfig) *Orchestrator {
		o := NewOrchestrator(cfg, nil, nil)
		o.initSchemaMapping()
		return o
	}

	t.Run("未配置映射时原样返回", func(t *testing.T) {
		o := mk(types.MigrationConfig{Target: types.ConnectionConfig{Type: types.PostgreSQL}})
		for _, name := range []string{"orders", "user_info", "weird.dotted"} {
			if got := o.resolveTargetName(name); got != name {
				t.Errorf("resolveTargetName(%q) = %q, 期望原样返回", name, got)
			}
		}
	})

	t.Run("全局默认 Schema", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target:        types.ConnectionConfig{Type: types.PostgreSQL},
			SchemaDefault: "ods",
		})
		if got := o.resolveTargetName("orders"); got != "ods.orders" {
			t.Errorf("resolveTargetName(orders) = %q, 期望 ods.orders", got)
		}
	})

	t.Run("按表覆盖优先于全局默认", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target:        types.ConnectionConfig{Type: types.PostgreSQL},
			SchemaDefault: "ods",
			SchemaTables:  map[string]string{"orders": "ods_staging"},
		})
		if got := o.resolveTargetName("orders"); got != "ods_staging.orders" {
			t.Errorf("resolveTargetName(orders) = %q, 期望 ods_staging.orders", got)
		}
		if got := o.resolveTargetName("users"); got != "ods.users" {
			t.Errorf("resolveTargetName(users) = %q, 期望 ods.users", got)
		}
	})

	t.Run("正则规则顺序首个命中", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target: types.ConnectionConfig{Type: types.MSSQL},
			SchemaRegex: []types.SchemaRule{
				{Pattern: `^tmp_`, Target: "temp"},
				{Pattern: `^user`, Target: "sec"},
			},
		})
		if got := o.resolveTargetName("tmp_log"); got != "temp.tmp_log" {
			t.Errorf("resolveTargetName(tmp_log) = %q, 期望 temp.tmp_log", got)
		}
		if got := o.resolveTargetName("user_info"); got != "sec.user_info" {
			t.Errorf("resolveTargetName(user_info) = %q, 期望 sec.user_info", got)
		}
		// 未命中任何规则且无默认：原样返回
		if got := o.resolveTargetName("orders"); got != "orders" {
			t.Errorf("resolveTargetName(orders) = %q, 期望原样返回", got)
		}
	})

	t.Run("正则 Target 含点号时视为完整目标表名", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target: types.ConnectionConfig{Type: types.PostgreSQL},
			SchemaRegex: []types.SchemaRule{
				{Pattern: `^ods_(\w+)$`, Target: "archive.$1"},
			},
		})
		if got := o.resolveTargetName("ods_orders"); got != "archive.orders" {
			t.Errorf("resolveTargetName(ods_orders) = %q, 期望 archive.orders", got)
		}
	})

	t.Run("按表覆盖优先于正则规则", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target:       types.ConnectionConfig{Type: types.PostgreSQL},
			SchemaTables: map[string]string{"tmp_special": "keep"},
			SchemaRegex: []types.SchemaRule{
				{Pattern: `^tmp_`, Target: "temp"},
			},
		})
		if got := o.resolveTargetName("tmp_special"); got != "keep.tmp_special" {
			t.Errorf("resolveTargetName(tmp_special) = %q, 期望 keep.tmp_special", got)
		}
	})

	t.Run("非法正则跳过不阻断", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target: types.ConnectionConfig{Type: types.PostgreSQL},
			SchemaRegex: []types.SchemaRule{
				{Pattern: `([`, Target: "x"}, // 非法正则
				{Pattern: `^ok_`, Target: "good"},
			},
		})
		if got := o.resolveTargetName("ok_table"); got != "good.ok_table" {
			t.Errorf("resolveTargetName(ok_table) = %q, 期望 good.ok_table", got)
		}
	})

	t.Run("SQLite 等无 schema 目标库忽略映射", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target:        types.ConnectionConfig{Type: types.SQLite},
			SchemaDefault: "ods",
		})
		if got := o.resolveTargetName("orders"); got != "orders" {
			t.Errorf("resolveTargetName(orders) = %q, 期望原样返回（SQLite 无 schema）", got)
		}
	})

	t.Run("空 Schema 值的按表覆盖不生效", func(t *testing.T) {
		o := mk(types.MigrationConfig{
			Target:        types.ConnectionConfig{Type: types.PostgreSQL},
			SchemaDefault: "ods",
			SchemaTables:  map[string]string{"orders": ""},
		})
		if got := o.resolveTargetName("orders"); got != "ods.orders" {
			t.Errorf("resolveTargetName(orders) = %q, 期望 ods.orders", got)
		}
	})
}
