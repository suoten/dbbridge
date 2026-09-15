// Package kingbase 注册人大金仓 KingbaseES 适配器（PG 兼容，基于 pgcompat 共享基座）。
package kingbase

import (
	"dbbridge/internal/adapter/pgcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.KingbaseES, func() types.DatabaseAdapter {
		return pgcompat.New("KingbaseES")
	})
}
