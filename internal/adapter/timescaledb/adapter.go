// Package timescaledb 注册 TimescaleDB 适配器（基于 PostgreSQL 的时序数据库扩展）。
// 使用 PG 线协议，基于 pgcompat 共享基座，差异仅在于品牌名和扩展特性提示。
package timescaledb

import (
	"dbbridge/internal/adapter/pgcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.TimescaleDB, func() types.DatabaseAdapter {
		return pgcompat.New("TimescaleDB")
	})
}
