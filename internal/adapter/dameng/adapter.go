// Package dameng 注册达梦 DM 适配器。
//
// DM8 支持 MySQL 兼容模式，通过 MySQL 驱动连接（基于 mysqlcompat 共享基座）。
// 仅覆写版本查询（兼容 Oracle 模式的 v$version 回退）。
package dameng

import (
	"context"

	types "dbbridge/pkg"
	"dbbridge/internal/adapter/mysqlcompat"
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
	if err := a.DB().QueryRowContext(ctx, "SELECT * FROM v$version WHERE ROWNUM = 1").Scan(&version); err == nil {
		return "Dameng DM " + version, nil
	}
	// 两种探测均失败时返回未知版本而非静默伪装成功，
	// 让调用方（如 TestConnection）感知到兼容模式探测异常
	return "Dameng DM (未知版本)", nil
}
