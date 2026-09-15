// Package dameng 注册达梦 DM 适配器。
//
// DM8 支持 MySQL 兼容模式，通过 MySQL 驱动连接（基于 mysqlcompat 共享基座）。
// 仅覆写版本查询（兼容 Oracle 模式的 v$version 回退）。
package dameng

import (
	"context"
	"fmt"

	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

// Adapter 达梦 DM 适配器
type Adapter struct {
	*mysqlcompat.Base
}

func init() {
	types.RegisterAdapter(types.Dameng, func() types.DatabaseAdapter {
		return &Adapter{Base: mysqlcompat.New("Dameng DM")}
	})
}

// GetVersion 获取达梦版本（VERSION() 失败时回退 v$version）
func (a *Adapter) GetVersion(ctx context.Context) (string, error) {
	var version string
	if err := a.DB().QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err == nil {
		return "Dameng DM " + version, nil
	}
	// 显式列出 BANNER 单列：旧实现 SELECT * 会随列数变化 Scan 失败，
	// 导致兼容模式下永远探测不到版本
	if err := a.DB().QueryRowContext(ctx,
		"SELECT BANNER FROM v$version WHERE ROWNUM = 1").Scan(&version); err == nil {
		return "Dameng DM " + version, nil
	}
	// 两种探测均失败：连接层有问题（如无权限/非 DM 实例），返回错误而非
	// "未知版本" 假成功，否则 TestConnection 会把坏连接当成可用连接
	return "", fmt.Errorf("Dameng DM: 无法获取版本（VERSION() 与 v$version 探测均失败），请检查连接配置")
}
