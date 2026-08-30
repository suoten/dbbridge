// Package opengauss 注册 openGauss 适配器（PG 兼容，基于 pgcompat 共享基座）。
package opengauss

import (
	types "dbbridge/pkg"
	"dbbridge/internal/adapter/pgcompat"
)

func init() {
	types.RegisterAdapter(types.OpenGauss, func() types.DatabaseAdapter {
		return pgcompat.New("openGauss")
	})
}
