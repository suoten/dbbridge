package service

import (
	"strings"
	"testing"

	types "dbbridge/pkg"
)

// TestGenerateConnStringsAllTypes 验证全部 23 种数据库类型都能生成连接字符串模板。
// 不连真实数据库，仅验证模板生成不报错且包含关键信息。
func TestGenerateConnStringsAllTypes(t *testing.T) {
	dbTypes := []types.DatabaseType{
		types.MySQL,
		types.PostgreSQL,
		types.SQLite,
		types.MariaDB,
		types.OceanBase,
		types.TiDB,
		types.PolarDB,
		types.OpenGauss,
		types.Dameng,
		types.KingbaseES,
		types.Aurora,
		types.CockroachDB,
		types.Oracle,
		types.MSSQL,
		types.Db2,
		types.MongoDB,
		types.Redis,
		types.Cassandra,
		types.ScyllaDB,
		types.InfluxDB,
		types.TimescaleDB,
		types.TDengine,
		types.Access,
	}

	svc := NewSQLService()
	for _, dbType := range dbTypes {
		t.Run(string(dbType), func(t *testing.T) {
			cfg := types.ConnectionConfig{
				Type:     dbType,
				Host:     "testhost",
				Port:     12345,
				Username: "testuser",
				Password: "testpass",
				Database: "testdb",
			}
			// SQLite 和 Access 不需要 Host/Port
			if dbType == types.SQLite {
				cfg.Database = "/tmp/test.db"
			}
			if dbType == types.Access {
				cfg.Database = "C:\\data\\test.mdb"
			}

			result := svc.GenerateConnStrings(cfg)
			if !result.Success {
				t.Fatalf("生成失败: %s", result.Error)
			}
			if len(result.Templates) == 0 {
				t.Error("模板为空")
			}

			// 验证模板中包含主机名（SQLite/Access 除外）
			if dbType != types.SQLite && dbType != types.Access {
				found := false
				for _, tpl := range result.Templates {
					if strings.Contains(tpl, "testhost") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("模板中未包含主机名 testhost: %+v", result.Templates)
				}
			}

			// 验证每种数据库至少有 3 种语言模板
			if len(result.Templates) < 3 {
				t.Errorf("模板数量 %d < 3: %+v", len(result.Templates), result.Templates)
			}
		})
	}
}

// TestGenerateConnStringsEmptyDB 验证空数据库名报错
func TestGenerateConnStringsEmptyDB(t *testing.T) {
	svc := NewSQLService()
	result := svc.GenerateConnStrings(types.ConnectionConfig{
		Type: types.MySQL, Host: "h", Username: "u", Password: "p",
	})
	if result.Success {
		t.Error("空数据库名应报错")
	}
}

// TestGenerateConnStringsPasswordEscape 验证密码特殊字符转义
func TestGenerateConnStringsPasswordEscape(t *testing.T) {
	svc := NewSQLService()
	result := svc.GenerateConnStrings(types.ConnectionConfig{
		Type: types.MySQL, Host: "h", Port: 3306,
		Username: "root", Password: "p@ss w0rd", Database: "db",
	})
	if !result.Success {
		t.Fatalf("生成失败: %s", result.Error)
	}
	// SQLAlchemy 模板应包含 URL 编码后的密码
	py := result.Templates["Python (SQLAlchemy)"]
	if !strings.Contains(py, "p%40ss") {
		t.Errorf("密码未转义: %s", py)
	}
}
