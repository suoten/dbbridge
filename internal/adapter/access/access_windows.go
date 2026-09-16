//go:build windows

package access

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	types "dbbridge/pkg"

	_ "github.com/alexbrainman/odbc" // 注册 ODBC 驱动（Windows）
)

// Connect 通过 ODBC 连接 Access 数据库
// Windows 上需安装 Microsoft Access Database Engine（32 位或 64 位需匹配应用位数）
func (a *Adapter) Connect(ctx context.Context, config types.ConnectionConfig) error {
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("access: 请指定 Access 数据库文件路径（.mdb 或 .accdb）")
	}

	dbPath := config.Database
	// 根据 Access 驱动类型选择连接串
	// 优先使用 ACE 驱动（.accdb），兼容老版 Jet 驱动（.mdb）
	var dsn string
	if strings.HasSuffix(strings.ToLower(dbPath), ".accdb") {
		dsn = fmt.Sprintf("Driver={Microsoft Access Driver (*.mdb, *.accdb)};DBQ=%s;", dbPath)
	} else {
		dsn = fmt.Sprintf("Driver={Microsoft Access Driver (*.mdb)};DBQ=%s;", dbPath)
	}

	// 如果有用户名密码，追加到连接串
	if config.Username != "" {
		dsn += fmt.Sprintf("UID=%s;PWD=%s;", config.Username, config.Password)
	}

	db, err := sql.Open("odbc", dsn)
	if err != nil {
		return fmt.Errorf("access: 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1) // Access 是桌面文件型数据库，并发写入受限

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("access: 连接失败: %w（请确认文件路径正确且已安装 Microsoft Access Database Engine）", err)
	}

	a.db = db
	return nil
}
