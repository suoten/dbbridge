// Package polardb 注册 PolarDB 适配器（阿里云云原生数据库，MySQL 兼容协议）。
// PolarDB 提供 MySQL 兼容和 PostgreSQL 兼容两种模式，本适配器基于 MySQL 兼容模式。
package polardb

import (
	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.PolarDB, func() types.DatabaseAdapter {
		return mysqlcompat.New("PolarDB")
	})
}
