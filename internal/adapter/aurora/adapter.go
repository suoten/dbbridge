// Package aurora 注册 Amazon Aurora 适配器（AWS 云原生数据库，MySQL 兼容协议）。
// Aurora 提供 MySQL 兼容和 PostgreSQL 兼容两种版本，本适配器基于 MySQL 兼容版。
package aurora

import (
	"dbbridge/internal/adapter/mysqlcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.Aurora, func() types.DatabaseAdapter {
		return mysqlcompat.New("Amazon Aurora")
	})
}
