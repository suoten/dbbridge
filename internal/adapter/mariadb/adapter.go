// Package mariadb 注册 MariaDB 适配器（基于 mysqlcompat 共享基座）。
package mariadb

import (
	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.MariaDB, func() types.DatabaseAdapter {
		return mysqlcompat.New("MariaDB")
	})
}
