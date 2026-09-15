// 连接字符串生成器：按目标数据库类型生成常见语言/框架的连接串模板。
//
// 纯函数无副作用：只基于连接配置生成模板文本，不触库不校验连通性。
// 小众组合（如达梦的 PHP PDO）没有官方驱动时如实说明，不编造模板。
package service

import (
	"fmt"
	"net/url"
	"strings"

	types "dbbridge/pkg"
)

// ConnStrResult 连接字符串生成结果
type ConnStrResult struct {
	Success   bool              `json:"success"`
	DBType    string            `json:"dbType"`
	Templates map[string]string `json:"templates,omitempty"` // key: Java (JDBC) / Python (SQLAlchemy) / Go (database/sql) / PHP (PDO) / Node.js
	Notes     []string          `json:"notes,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// GenerateConnStrings 生成各语言连接字符串模板
func (s *SQLService) GenerateConnStrings(cfg types.ConnectionConfig) *ConnStrResult {
	result := &ConnStrResult{DBType: string(cfg.Type)}
	if strings.TrimSpace(cfg.Database) == "" && cfg.Type != types.SQLite {
		result.Error = "数据库名为空，无法生成连接串"
		return result
	}

	esc := url.QueryEscape // 嵌入 URL 的密码做转义
	host := cfg.Host
	if host == "" {
		host = "localhost"
	}
	port := cfg.Port
	db := cfg.Database
	user := cfg.Username
	pass := cfg.Password

	tpl := map[string]string{}
	var notes []string

	defaultPort := func(p int, def int) int {
		if p > 0 {
			return p
		}
		return def
	}

	switch cfg.Type {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase:
		p := defaultPort(port, 3306)
		scheme := "mysql"
		if cfg.Type == types.MariaDB {
			scheme = "mariadb"
		}
		charset := cfg.Charset
		if charset == "" {
			charset = "utf8mb4"
		}
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:%s://%s:%d/%s?useUnicode=true&characterEncoding=%s&useSSL=false&serverTimezone=Asia/Shanghai\nuser=%s\npassword=%s",
			scheme, host, p, db, charset, user, pass)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("mysql+pymysql://%s:%s@%s:%d/%s?charset=%s", user, esc(pass), host, p, db, charset)
		tpl["Go (database/sql)"] = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=Local", user, esc(pass), host, p, db, charset)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('mysql:host=%s;port=%d;dbname=%s;charset=%s', '%s', '%s');", host, p, db, charset, user, pass)
		tpl["Node.js (mysql2)"] = fmt.Sprintf("{ host: '%s', port: %d, user: '%s', password: '%s', database: '%s', charset: '%s' }", host, p, user, pass, db, charset)

	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		p := defaultPort(port, 5432)
		ssl := cfg.SSLMode
		if ssl == "" {
			ssl = "disable"
		}
		jdbcScheme := map[types.DatabaseType]string{
			types.PostgreSQL:  "postgresql",
			types.OpenGauss:   "opengauss",
			types.KingbaseES:  "kingbase8",
			types.CockroachDB: "postgresql",
		}[cfg.Type]
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:%s://%s:%d/%s\nuser=%s\npassword=%s", jdbcScheme, host, p, db, user, pass)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("postgresql+psycopg2://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		tpl["Go (database/sql)"] = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s", host, p, user, pass, db, ssl)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('pgsql:host=%s;port=%d;dbname=%s', '%s', '%s');", host, p, db, user, pass)
		tpl["Node.js (pg)"] = fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		if cfg.Type == types.KingbaseES {
			notes = append(notes, "金仓兼容 PG 协议：Go/Python/PHP/Node 可直接用 PG 驱动连接；Java 官方驱动为 kingbase8.jar")
		}
		if cfg.Type == types.OpenGauss {
			notes = append(notes, "openGauss 兼容 PG 协议：Go/Python/PHP/Node 可直接用 PG 驱动连接；Java 官方驱动为 opengauss-jdbc")
		}

	case types.MSSQL:
		p := defaultPort(port, 1433)
		if cfg.Instance != "" {
			tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:sqlserver://%s;instanceName=%s;databaseName=%s;encrypt=true;trustServerCertificate=true\nuser=%s\npassword=%s",
				host, cfg.Instance, db, user, pass)
		} else {
			tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:sqlserver://%s:%d;databaseName=%s;encrypt=true;trustServerCertificate=true\nuser=%s\npassword=%s",
				host, p, db, user, pass)
		}
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("mssql+pyodbc://%s:%s@%s:%d/%s?driver=ODBC+Driver+17+for+SQL+Server", user, esc(pass), host, p, db)
		tpl["Go (database/sql)"] = fmt.Sprintf("sqlserver://%s:%s@%s:%d?database=%s", user, esc(pass), host, p, url.QueryEscape(db))
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('sqlsrv:Server=%s,%d;Database=%s', '%s', '%s');", host, p, db, user, pass)
		tpl["Node.js (mssql)"] = fmt.Sprintf("{ server: '%s', port: %d, user: '%s', password: '%s', database: '%s', options: { encrypt: true, trustServerCertificate: true } }", host, p, user, pass, db)
		if cfg.Instance != "" {
			notes = append(notes, "使用实例名连接时 JDBC 走 SQL Browser 服务（UDP 1434），Go/Node 驱动对实例名支持有限，建议改用端口直连")
		}

	case types.SQLite:
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:sqlite:%s", db)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("sqlite:///%s", db)
		tpl["Go (database/sql)"] = fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", db)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('sqlite:%s');", db)
		tpl["Node.js (better-sqlite3)"] = fmt.Sprintf("new Database('%s');", db)
		notes = append(notes, "SQLite 无主机/端口/账号概念，\"数据库名\"即数据库文件路径（本工具也按此约定读取）")

	case types.Dameng:
		p := defaultPort(port, 5236)
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:dm://%s:%d\nuser=%s\npassword=%s\nschema=%s", host, p, user, pass, db)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("dm+dmPython://%s:%s@%s:%d", user, esc(pass), host, p)
		tpl["Go (database/sql)"] = fmt.Sprintf("dm://%s:%s@%s:%d?schema=%s", user, esc(pass), host, p, url.QueryEscape(db))
		notes = append(notes, "达梦驱动需从达梦官网获取：Java 为 DmJdbcDriver18.jar，Go 为 dm.jdbc 包（github 亦有社区移植），Python 为 dmPython")
		notes = append(notes, "达梦无官方 PHP PDO / Node.js 驱动，如需接入建议经 ODBC（Windows）或 unixODBC（Linux）桥接")
		notes = append(notes, "达梦以\"模式（schema）\"组织对象，连接串中的库名按 schema 处理")

	default:
		result.Error = fmt.Sprintf("不支持的数据库类型: %s", cfg.Type)
		return result
	}

	result.Success = true
	result.Templates = tpl
	result.Notes = notes
	return result
}
