// Package service - 适配器注册引导。
//
// service 包通过注册表（types.NewAdapter）创建适配器，
// 但注册依赖各 adapter 包的 init()。app.go 已做 blank import，
// 这里再显式引入一份，保证 service 被独立使用时（单元测试、
// headless webserver 只 import service）注册表同样是完整的。
package service

import (
	_ "dbbridge/internal/adapter/cockroachdb"
	_ "dbbridge/internal/adapter/dameng"
	_ "dbbridge/internal/adapter/kingbase"
	_ "dbbridge/internal/adapter/mariadb"
	_ "dbbridge/internal/adapter/mssql"
	_ "dbbridge/internal/adapter/mysql"
	_ "dbbridge/internal/adapter/oceanbase"
	_ "dbbridge/internal/adapter/opengauss"
	_ "dbbridge/internal/adapter/postgres"
	_ "dbbridge/internal/adapter/sqlite"
	_ "dbbridge/internal/adapter/tidb"
)
