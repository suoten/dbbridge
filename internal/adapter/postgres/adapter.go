// Package postgres 注册 PostgreSQL 适配器（基于 pgcompat 共享基座）。
package postgres

import (
	"dbbridge/internal/adapter/pgcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.PostgreSQL, func() types.DatabaseAdapter {
		return pgcompat.New("postgres")
	})
}
