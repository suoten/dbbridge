package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	types "dbbridge/pkg"
)

func TestCheckCompatibilityAllOK(t *testing.T) {
	source, target, srcDB := setupValidateEnv(t)
	defer srcDB.Close()
	svc := &SQLService{}
	r := svc.CheckCompatibility(context.Background(), struct {
		Source types.ConnectionConfig `json:"source"`
		Target types.ConnectionConfig `json:"target"`
		Tables []string               `json:"tables,omitempty"`
	}{Source: source, Target: target})
	if !r.Success {
		t.Fatalf("检查失败: %s", r.Error)
	}
	for _, tr := range r.Tables {
		if tr.Status != "ok" {
			t.Errorf("表 %s 应为 ok，实际 %s: %+v", tr.Table, tr.Status, tr.Items)
		}
	}
	if !strings.Contains(r.Summary, "存在缺失或错误 0") {
		t.Errorf("摘要异常: %s", r.Summary)
	}
}

func TestCheckCompatibilityDetectsMissingColumn(t *testing.T) {
	source, target, srcDB := setupValidateEnv(t)
	defer srcDB.Close()
	svc := &SQLService{}

	// 目标库删除一列（模拟结构丢失/手工误删）
	tgtDB, err := sql.Open("sqlite", target.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer tgtDB.Close()
	if _, err := tgtDB.Exec(`ALTER TABLE departments DROP COLUMN name`); err != nil {
		t.Skipf("SQLite 版本不支持 DROP COLUMN，跳过: %v", err)
	}

	r := svc.CheckCompatibility(context.Background(), struct {
		Source types.ConnectionConfig `json:"source"`
		Target types.ConnectionConfig `json:"target"`
		Tables []string               `json:"tables,omitempty"`
	}{Source: source, Target: target})
	if !r.Success {
		t.Fatalf("检查失败: %s", r.Error)
	}
	found := false
	for _, tr := range r.Tables {
		if tr.Table != "departments" {
			continue
		}
		if tr.Status != "error" {
			t.Errorf("departments 应为 error，实际 %s", tr.Status)
		}
		for _, it := range tr.Items {
			if it.Category == "column" && it.Severity == "error" && strings.Contains(it.Message, "name") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("未检出缺失列 name: %+v", r.Tables)
	}
	_ = srcDB
}

func TestCheckCompatibilityMissingTable(t *testing.T) {
	source, target, srcDB := setupValidateEnv(t)
	defer srcDB.Close()
	tgtDB, err := sql.Open("sqlite", target.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tgtDB.Exec(`DROP TABLE departments`); err != nil {
		t.Fatal(err)
	}
	tgtDB.Close()

	r := (&SQLService{}).CheckCompatibility(context.Background(), struct {
		Source types.ConnectionConfig `json:"source"`
		Target types.ConnectionConfig `json:"target"`
		Tables []string               `json:"tables,omitempty"`
	}{Source: source, Target: target})
	if !r.Success {
		t.Fatalf("检查失败: %s", r.Error)
	}
	found := false
	for _, tr := range r.Tables {
		if tr.Table == "departments" && tr.Status == "error" {
			found = true
		}
	}
	if !found {
		t.Errorf("未检出缺失表 departments: %+v", r.Tables)
	}
}

func TestCheckCompatibilitySameDB(t *testing.T) {
	cfg := types.ConnectionConfig{Type: types.SQLite, Database: "E:\\x\\a.db"}
	r := (&SQLService{}).CheckCompatibility(context.Background(), struct {
		Source types.ConnectionConfig `json:"source"`
		Target types.ConnectionConfig `json:"target"`
		Tables []string               `json:"tables,omitempty"`
	}{Source: cfg, Target: cfg})
	if r.Success {
		t.Errorf("同库对比应报错: %+v", r)
	}
}
