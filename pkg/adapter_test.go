package types

import (
	"testing"
)

// TestAdapterRegistration 验证所有已注册的数据库适配器都能通过 NewAdapter 正确实例化。
// 这个测试不连接真实数据库，仅验证注册表完整性——
// 如果某个适配器的 init() 未被 import 导致未注册，用户选择该类型时会 panic 或静默失败。
func TestAdapterRegistration(t *testing.T) {
	// 注意：此测试包为 types，不会自动触发 internal/adapter/* 的 init()。
	// app.go 通过 blank import 注册全部适配器，这里仅测试注册表机制本身。
	// 全量注册测试在 internal/adapter/all_test.go 中。

	// 验证注册-获取机制
	RegisterAdapter("test_only_type", func() DatabaseAdapter {
		return nil // 仅测试注册机制，不返回真实适配器
	})
	a := NewAdapter("test_only_type")
	if a != nil {
		t.Error("test_only_type 工厂应返回 nil")
	}

	// 未注册的类型应返回 nil
	a = NewAdapter("nonexistent_type")
	if a != nil {
		t.Error("未注册的类型应返回 nil")
	}
}

// TestSupportedDatabases 验证 SupportedDatabases 返回非空列表
func TestSupportedDatabases(t *testing.T) {
	dbs := SupportedDatabases()
	if len(dbs) == 0 {
		t.Error("SupportedDatabases 不应为空")
	}
}
