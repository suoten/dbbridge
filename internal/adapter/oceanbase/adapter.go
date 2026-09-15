// Package oceanbase 注册 OceanBase 适配器（MySQL 兼容模式，基于 mysqlcompat 共享基座）。
package oceanbase

import (
	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.OceanBase, func() types.DatabaseAdapter {
		return mysqlcompat.New("OceanBase")
	})
}
