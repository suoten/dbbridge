// Package oceanbase 注册 OceanBase 适配器（MySQL 兼容模式，基于 mysqlcompat 共享基座）。
package oceanbase

import (
	types "dbbridge/pkg"
	"dbbridge/internal/adapter/mysqlcompat"
)

func init() {
	types.RegisterAdapter(types.OceanBase, func() types.DatabaseAdapter {
		return mysqlcompat.New("OceanBase")
	})
}
