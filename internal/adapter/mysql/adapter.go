// Package mysql 注册 MySQL 适配器（基于 mysqlcompat 共享基座）。
package mysql

import (
	types "dbbridge/pkg"
	"dbbridge/internal/adapter/mysqlcompat"
)

func init() {
	types.RegisterAdapter(types.MySQL, func() types.DatabaseAdapter {
		return mysqlcompat.New("MySQL")
	})
}
