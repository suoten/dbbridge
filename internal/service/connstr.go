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

	case types.Oracle:
		p := defaultPort(port, 1521)
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:oracle:thin:@%s:%d/%s\nuser=%s\npassword=%s", host, p, db, user, pass)
		tpl["Python (cx_Oracle)"] = fmt.Sprintf("oracle+cx_oracle://%s:%s@%s:%d/?service_name=%s", user, esc(pass), host, p, db)
		tpl["Go (godror)"] = fmt.Sprintf(`user="%s" password="%s" connectString="%s:%d/%s"`, user, pass, host, p, db)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('oci:dbname=%s:%d/%s', '%s', '%s');", host, p, db, user, pass)
		tpl["Node.js (oracledb)"] = fmt.Sprintf("{ user: '%s', password: '%s', connectString: '%s:%d/%s' }", user, pass, host, p, db)

	case types.Db2:
		p := defaultPort(port, 50000)
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:db2://%s:%d/%s\nuser=%s\npassword=%s", host, p, db, user, pass)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("db2+ibm_db://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		tpl["Go (go_ibm_db)"] = fmt.Sprintf("DATABASE=%s;HOSTNAME=%s;PORT=%d;PROTOCOL=TCPIP;UID=%s;PWD=%s;", db, host, p, user, pass)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('ibm:DRIVER={IBM DB2 ODBC DRIVER};DATABASE=%s;HOSTNAME=%s;PORT=%d;PROTOCOL=TCPIP;', '%s', '%s');", db, host, p, user, pass)
		tpl["Node.js (ibm_db)"] = fmt.Sprintf(`{ database: '%s', hostname: '%s', port: %d, user: '%s', password: '%s', protocol: 'TCPIP' }`, db, host, p, user, pass)

	case types.Cassandra, types.ScyllaDB:
		p := defaultPort(port, 9042)
		tpl["Java (DataStax)"] = fmt.Sprintf("Datastax Java Driver: contactPoints=%s:%d, keyspace=%s, username=%s, password=%s", host, p, db, user, pass)
		tpl["Python (cassandra-driver)"] = fmt.Sprintf("Cluster(['%s'], port=%d, auth_provider=PlainTextAuthProvider(username='%s', password='%s')).connect('%s')", host, p, user, pass, db)
		tpl["Go (gocql)"] = fmt.Sprintf("cluster := gocql.NewCluster(\"%s\"); cluster.Port = %d; cluster.Keyspace = \"%s\"; cluster.Authenticator = gocql.PasswordAuthenticator{Username: \"%s\", Password: \"%s\"}", host, p, db, user, pass)
		tpl["Node.js (cassandra-driver)"] = fmt.Sprintf("{ contactPoints: ['%s:%d'], localDataCenter: 'datacenter1', keyspace: '%s', credentials: { username: '%s', password: '%s' } }", host, p, db, user, pass)
		notes = append(notes, "Cassandra/ScyllaDB 使用 CQL 协议（端口 9042），非 SQL 驱动")

	case types.MongoDB:
		p := defaultPort(port, 27017)
		authDb := "admin"
		tpl["Java (JDBC)"] = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=%s", user, esc(pass), host, p, db, authDb)
		tpl["Python (pymongo)"] = fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s", user, esc(pass), host, p, authDb)
		tpl["Go (mongo-driver)"] = fmt.Sprintf("mongodb://%s:%s@%s:%d/?authSource=%s", user, esc(pass), host, p, authDb)
		tpl["Node.js (mongoose)"] = fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=%s", user, esc(pass), host, p, db, authDb)
		notes = append(notes, "MongoDB 使用自有协议（端口 27017），非 SQL 驱动")

	case types.Redis:
		p := defaultPort(port, 6379)
		tpl["Java (Jedis)"] = fmt.Sprintf("redis://%s:%s@%s:%d/%d", user, esc(pass), host, p, 0)
		tpl["Python (redis-py)"] = fmt.Sprintf("redis://%s:%s@%s:%d/0", user, esc(pass), host, p)
		tpl["Go (go-redis)"] = fmt.Sprintf("redis://%s:%s@%s:%d/0", user, esc(pass), host, p)
		tpl["Node.js (ioredis)"] = fmt.Sprintf("redis://:%s@%s:%d/0", esc(pass), host, p)
		notes = append(notes, "Redis 使用 RESP 协议（端口 6379），非 SQL 驱动")

	case types.InfluxDB:
		p := defaultPort(port, 8086)
		tpl["Java (influxdb-client)"] = fmt.Sprintf("http://%s:%d?token=%s&org=%s&bucket=%s", host, p, esc(pass), user, db)
		tpl["Python (influxdb-client)"] = fmt.Sprintf("InfluxDBClient(url='http://%s:%d', token='%s', org='%s')", host, p, pass, user)
		tpl["Go (influxdb-client-go)"] = fmt.Sprintf("http://%s:%d?token=%s&org=%s", host, p, esc(pass), user)
		tpl["Node.js (@influxdata/influxdb-client)"] = fmt.Sprintf("new InfluxDB({ url: 'http://%s:%d', token: '%s' })", host, p, pass)
		notes = append(notes, "InfluxDB 2.x 使用 HTTP API（端口 8086），非 SQL 驱动")

	case types.TDengine:
		p := defaultPort(port, 6041)
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:TAOS://%s:%d/%s?user=%s&password=%s", host, p, db, user, pass)
		tpl["Python (taospy)"] = fmt.Sprintf("taos://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		tpl["Go (driver-go)"] = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", user, esc(pass), host, p, db)
		tpl["Node.js (@tdengine/client)"] = fmt.Sprintf("{ host: '%s', port: %d, user: '%s', password: '%s', database: '%s' }", host, p, user, pass, db)
		notes = append(notes, "TDengine 使用自有协议（端口 6041），支持 SQL 语法但非标准 SQL")

	case types.TimescaleDB:
		p := defaultPort(port, 5432)
		ssl := cfg.SSLMode
		if ssl == "" {
			ssl = "disable"
		}
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:postgresql://%s:%d/%s\nuser=%s\npassword=%s", host, p, db, user, pass)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("postgresql+psycopg2://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		tpl["Go (database/sql)"] = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s", host, p, user, pass, db, ssl)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('pgsql:host=%s;port=%d;dbname=%s', '%s', '%s');", host, p, db, user, pass)
		tpl["Node.js (pg)"] = fmt.Sprintf("postgresql://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		notes = append(notes, "TimescaleDB 基于 PostgreSQL，可直接使用 PG 驱动连接")

	case types.Aurora:
		p := defaultPort(port, 3306)
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:mysql://%s:%d/%s?useSSL=true&requireSSL=true\nuser=%s\npassword=%s", host, p, db, user, pass)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("mysql+pymysql://%s:%s@%s:%d/%s?ssl_enabled=true", user, esc(pass), host, p, db)
		tpl["Go (database/sql)"] = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?tls=true&parseTime=True&loc=Local", user, esc(pass), host, p, db)
		tpl["Node.js (mysql2)"] = fmt.Sprintf("{ host: '%s', port: %d, user: '%s', password: '%s', database: '%s', ssl: {} }", host, p, user, pass, db)
		notes = append(notes, "Aurora (MySQL 兼容版) 使用 MySQL 协议，建议启用 SSL/TLS")

	case types.PolarDB:
		p := defaultPort(port, 3306)
		tpl["Java (JDBC)"] = fmt.Sprintf("jdbc:mysql://%s:%d/%s?useSSL=true\nuser=%s\npassword=%s", host, p, db, user, pass)
		tpl["Python (SQLAlchemy)"] = fmt.Sprintf("mysql+pymysql://%s:%s@%s:%d/%s", user, esc(pass), host, p, db)
		tpl["Go (database/sql)"] = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=True&loc=Local", user, esc(pass), host, p, db)
		tpl["Node.js (mysql2)"] = fmt.Sprintf("{ host: '%s', port: %d, user: '%s', password: '%s', database: '%s' }", host, p, user, pass, db)
		notes = append(notes, "PolarDB (MySQL 兼容版) 使用 MySQL 协议")

	case types.Access:
		tpl["Java (UCanAccess)"] = fmt.Sprintf("jdbc:ucanaccess://%s", db)
		tpl["Python (pyodbc)"] = fmt.Sprintf("DRIVER={Microsoft Access Driver (*.mdb, *.accdb)};DBQ=%s;", db)
		tpl["Go (adodb)"] = fmt.Sprintf(`Provider=Microsoft.Jet.OLEDB.4.0;Data Source=%s;`, db)
		tpl["PHP (PDO)"] = fmt.Sprintf("new PDO('odbc:DRIVER={Microsoft Access Driver (*.mdb)};DBQ=%s');", db)
		notes = append(notes, "Access 是桌面文件型数据库，\"数据库名\"即 .mdb/.accdb 文件路径")
		notes = append(notes, "需安装 Microsoft Access Database Engine 或 ODBC 驱动")

	default:
		result.Error = fmt.Sprintf("不支持的数据库类型: %s", cfg.Type)
		return result
	}

	result.Success = true
	result.Templates = tpl
	result.Notes = notes
	return result
}
