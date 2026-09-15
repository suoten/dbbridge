package service

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

func TestGenerateConnStringsMySQL(t *testing.T) {
	r := (&SQLService{}).GenerateConnStrings(types.ConnectionConfig{
		Type: types.MySQL, Host: "10.0.0.5", Port: 3307, Username: "root",
		Password: "p@ss w0rd", Database: "shop", Charset: "utf8mb4",
	})
	if !r.Success {
		t.Fatalf("生成失败: %s", r.Error)
	}
	go_ := r.Templates["Go (database/sql)"]
	if !strings.Contains(go_, "10.0.0.5:3307") || !strings.Contains(go_, "p%40ss+w0rd") {
		t.Errorf("Go DSN 密码未转义或主机端口错误: %s", go_)
	}
	jdbc := r.Templates["Java (JDBC)"]
	if !strings.Contains(jdbc, "jdbc:mysql://10.0.0.5:3307/shop") {
		t.Errorf("JDBC 模板错误: %s", jdbc)
	}
	py := r.Templates["Python (SQLAlchemy)"]
	if !strings.Contains(py, "mysql+pymysql://root:p%40ss+w0rd@10.0.0.5:3307/shop") {
		t.Errorf("SQLAlchemy 模板错误: %s", py)
	}
}

func TestGenerateConnStringsPGDefaultPort(t *testing.T) {
	r := (&SQLService{}).GenerateConnStrings(types.ConnectionConfig{
		Type: types.PostgreSQL, Host: "pg.local", Username: "u", Password: "p", Database: "d", SSLMode: "require",
	})
	if !r.Success {
		t.Fatalf("生成失败: %s", r.Error)
	}
	if !strings.Contains(r.Templates["Go (database/sql)"], "port=5432") ||
		!strings.Contains(r.Templates["Go (database/sql)"], "sslmode=require") {
		t.Errorf("PG 默认端口/sslmode 错误: %s", r.Templates["Go (database/sql)"])
	}
}

func TestGenerateConnStringsMSSQLInstance(t *testing.T) {
	r := (&SQLService{}).GenerateConnStrings(types.ConnectionConfig{
		Type: types.MSSQL, Host: "sql.local", Instance: "SQLEXPRESS", Username: "sa", Password: "p", Database: "master",
	})
	if !r.Success {
		t.Fatalf("生成失败: %s", r.Error)
	}
	if !strings.Contains(r.Templates["Java (JDBC)"], "instanceName=SQLEXPRESS") {
		t.Errorf("JDBC 实例名缺失: %s", r.Templates["Java (JDBC)"])
	}
	found := false
	for _, n := range r.Notes {
		if strings.Contains(n, "实例名") {
			found = true
		}
	}
	if !found {
		t.Errorf("实例名提示缺失: %v", r.Notes)
	}
}

func TestGenerateConnStringsSQLite(t *testing.T) {
	r := (&SQLService{}).GenerateConnStrings(types.ConnectionConfig{Type: types.SQLite, Database: "E:\\data\\app.db"})
	if !r.Success {
		t.Fatalf("生成失败: %s", r.Error)
	}
	if !strings.Contains(r.Templates["Java (JDBC)"], "jdbc:sqlite:E:\\data\\app.db") {
		t.Errorf("SQLite JDBC 模板错误: %s", r.Templates["Java (JDBC)"])
	}
}

func TestGenerateConnStringsDamengHonest(t *testing.T) {
	r := (&SQLService{}).GenerateConnStrings(types.ConnectionConfig{
		Type: types.Dameng, Host: "dm.local", Username: "SYSDBA", Password: "p", Database: "SYSDBA",
	})
	if !r.Success {
		t.Fatalf("生成失败: %s", r.Error)
	}
	// 达梦没有 PHP/Node 官方驱动，不应编造模板，且应给出说明
	if _, ok := r.Templates["PHP (PDO)"]; ok {
		t.Errorf("达梦不应生成 PHP PDO 模板: %v", r.Templates)
	}
	if _, ok := r.Templates["Node.js (mssql)"]; ok {
		t.Errorf("达梦不应生成 Node 模板: %v", r.Templates)
	}
	found := false
	for _, n := range r.Notes {
		if strings.Contains(n, "ODBC") {
			found = true
		}
	}
	if !found {
		t.Errorf("达梦驱动说明缺失: %v", r.Notes)
	}
}

func TestGenerateConnStringsEmptyDatabase(t *testing.T) {
	r := (&SQLService{}).GenerateConnStrings(types.ConnectionConfig{Type: types.MySQL, Host: "h"})
	if r.Success {
		t.Errorf("数据库名为空应报错: %+v", r)
	}
}
