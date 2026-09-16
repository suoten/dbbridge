//go:build !windows

package access

import (
	"context"
	"fmt"

	types "dbbridge/pkg"
)

// Connect 在非 Windows 平台上返回友好错误
// Microsoft Access 仅在 Windows 上有 ODBC 驱动支持。
// Linux/macOS 用户可通过 mdbtools 或 Libre Base 导出为 SQLite/MySQL 后再迁移。
func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	return fmt.Errorf("access: Microsoft Access 适配器仅在 Windows 上可用（需要 Microsoft Access Database Engine ODBC 驱动）。" +
		"Linux/macOS 用户请先将 .mdb/.accdb 文件导出为 SQLite 或 MySQL，再使用 DBBridge 迁移")
}
