// Package opengauss 注册 openGauss 适配器（PG 兼容，基于 pgcompat 共享基座）。
package opengauss

import (
	"dbbridge/internal/adapter/pgcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.OpenGauss, func() types.DatabaseAdapter {
		return pgcompat.New("openGauss")
	})
}
