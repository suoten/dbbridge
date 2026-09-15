// Package mysql 注册 MySQL 适配器（基于 mysqlcompat 共享基座）。
package mysql

import (
	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.MySQL, func() types.DatabaseAdapter {
		return mysqlcompat.New("MySQL")
	})
}
