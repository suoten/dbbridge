// Package tidb 注册 TiDB 适配器（MySQL 兼容，基于 mysqlcompat 共享基座）。
package tidb

import (
	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.TiDB, func() types.DatabaseAdapter {
		return mysqlcompat.New("TiDB")
	})
}
