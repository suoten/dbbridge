// Package mariadb 注册 MariaDB 适配器（基于 mysqlcompat 共享基座）。
package mariadb

import (
	types "dbbridge/pkg"
	"dbbridge/internal/adapter/mysqlcompat"
)

func init() {
	types.RegisterAdapter(types.MariaDB, func() types.DatabaseAdapter {
		return mysqlcompat.New("MariaDB")
	})
}
