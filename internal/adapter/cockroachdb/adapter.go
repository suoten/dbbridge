// Package cockroachdb 注册 CockroachDB 适配器（PG 线协议，基于 pgcompat 共享基座）。
// 差异点：不支持注释查询、二进制类型为 BYTES。
package cockroachdb

import (
	"dbbridge/internal/adapter/pgcompat"
	types "dbbridge/pkg"
)

func init() {
	types.RegisterAdapter(types.CockroachDB, func() types.DatabaseAdapter {
		return pgcompat.New("CockroachDB",
			pgcompat.WithNoComments(),
			pgcompat.WithBinaryType("BYTES"),
		)
	})
}
