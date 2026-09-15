// 兼容性检查报告：迁移完成后扫描目标库实际结构，与源库逐项对比。
//
// 检查维度：列完整性、类型语义（中立 Kind 对比）、可空性、默认值、
// 自增属性、索引（按列集合匹配，忽略命名差异）、外键、字符集。
// 全程只读不动任何数据。
package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"dbbridge/internal/typeconv"
	types "dbbridge/pkg"
)

// CompatItem 兼容性检查条目
type CompatItem struct {
	Category   string `json:"category"` // column/type/nullable/default/increment/index/fk/charset
	Severity   string `json:"severity"` // error/warning/info
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// CompatTableReport 单表兼容性报告
type CompatTableReport struct {
	Table  string       `json:"table"`
	Status string       `json:"status"` // ok/warning/error
	Items  []CompatItem `json:"items,omitempty"`
}

// CompatReport 兼容性检查总报告
type CompatReport struct {
	Success bool                `json:"success"`
	Source  string              `json:"source"`
	Target  string              `json:"target"`
	Tables  []CompatTableReport `json:"tables"`
	Summary string              `json:"summary,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// CheckCompatParams 兼容性检查请求
type CheckCompatParams struct {
	Source types.ConnectionConfig `json:"source"`
	Target types.ConnectionConfig `json:"target"`
	Tables []string               `json:"tables,omitempty"`
}

// CheckCompatibility 对比源库与目标库实际结构，输出兼容性差异清单
func (s *SQLService) CheckCompatibility(ctx context.Context, req CheckCompatParams) *CompatReport {
	report := &CompatReport{Source: string(req.Source.Type), Target: string(req.Target.Type)}

	if req.Source.Type == req.Target.Type && req.Source.Database == req.Target.Database {
		report.Error = "源库与目标库是同一个库，无法对比"
		return report
	}

	srcAdapter := types.NewAdapter(req.Source.Type)
	tgtAdapter := types.NewAdapter(req.Target.Type)
	if srcAdapter == nil || tgtAdapter == nil {
		report.Error = "不支持的数据库类型"
		return report
	}
	defer srcAdapter.Close()
	defer tgtAdapter.Close()

	connectCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := srcAdapter.Connect(connectCtx, req.Source); err != nil {
		report.Error = "连接源库失败: " + err.Error()
		return report
	}
	if err := tgtAdapter.Connect(connectCtx, req.Target); err != nil {
		report.Error = "连接目标库失败: " + err.Error()
		return report
	}

	srcTables, err := srcAdapter.GetTables(ctx)
	if err != nil {
		report.Error = "读取源库表列表失败: " + err.Error()
		return report
	}
	want := map[string]bool{}
	for _, t := range req.Tables {
		want[strings.TrimSpace(t)] = true
	}

	okCount, warnCount, errCount := 0, 0, 0
	for _, tm := range srcTables {
		if len(want) > 0 && !want[tm.Name] {
			continue
		}
		tr := CompatTableReport{Table: tm.Name}

		exists, err := tgtAdapter.TableExists(ctx, tm.Name)
		if err != nil {
			tr.Items = append(tr.Items, CompatItem{
				Category: "schema", Severity: "error",
				Message: "检查目标表是否存在失败: " + err.Error(),
			})
		} else if !exists {
			tr.Items = append(tr.Items, CompatItem{
				Category: "schema", Severity: "error",
				Message:    "目标库缺少该表",
				Suggestion: "确认该表是否被排除在迁移范围外；若是意外缺失请重新迁移",
			})
		}

		if len(tr.Items) == 0 {
			tr.Items = append(tr.Items, compareTableSchemas(ctx, srcAdapter, tgtAdapter, tm.Name)...)
		}

		// 表级状态：有 error 记 error，否则有 warning 记 warning，否则 ok
		tr.Status = "ok"
		for _, it := range tr.Items {
			switch it.Severity {
			case "error":
				tr.Status = "error"
			case "warning":
				if tr.Status != "error" {
					tr.Status = "warning"
				}
			}
		}
		switch tr.Status {
		case "error":
			errCount++
		case "warning":
			warnCount++
		default:
			okCount++
		}
		report.Tables = append(report.Tables, tr)
	}

	report.Summary = fmt.Sprintf("共检查 %d 张表：结构一致 %d / 有差异需关注 %d / 存在缺失或错误 %d",
		len(report.Tables), okCount, warnCount, errCount)
	report.Success = true
	return report
}

// compareTableSchemas 对比单表结构（两侧表都已确认存在）
func compareTableSchemas(ctx context.Context, srcAdapter, tgtAdapter types.DatabaseAdapter, table string) []CompatItem {
	var items []CompatItem
	add := func(category, severity, message, suggestion string) {
		items = append(items, CompatItem{Category: category, Severity: severity, Message: message, Suggestion: suggestion})
	}

	srcSchema, err := srcAdapter.GetTableSchema(ctx, table)
	if err != nil {
		add("schema", "error", "读取源表结构失败: "+err.Error(), "")
		return items
	}
	tgtSchema, err := tgtAdapter.GetTableSchema(ctx, table)
	if err != nil {
		add("schema", "error", "读取目标表结构失败: "+err.Error(), "")
		return items
	}

	tgtCols := map[string]types.ColumnMeta{}
	for _, c := range tgtSchema.Columns {
		tgtCols[strings.ToLower(c.Name)] = c
	}

	// ---- 列 ----
	for _, sc := range srcSchema.Columns {
		tc, ok := tgtCols[strings.ToLower(sc.Name)]
		if !ok {
			add("column", "error",
				fmt.Sprintf("目标表缺少列 %s（%s）", sc.Name, sc.DataType),
				"该列的数据不会存在于目标库，业务代码读写该列会报错；请确认迁移列映射")
			continue
		}
		// 类型语义对比（中立 Kind；双方都识别才比）
		ks, kt := typeconv.Normalize(sc.BaseType), typeconv.Normalize(tc.BaseType)
		if ks != typeconv.KindUnknown && kt != typeconv.KindUnknown && ks != kt {
			add("type", "warning",
				fmt.Sprintf("列 %s 类型语义有差异：源 %s → 目标 %s", sc.Name, sc.DataType, tc.DataType),
				"确认目标类型能承接源数据（如时间按文本存储、布尔按整型存储属预期行为）")
		} else if kt == typeconv.KindUnknown && ks != typeconv.KindUnknown {
			add("type", "warning",
				fmt.Sprintf("列 %s 目标类型 %s 无法归类识别", sc.Name, tc.DataType),
				"人工确认该类型是否正确承接了源数据")
		}
		// 可空性（目标比源更严 → 潜在写入失败）
		if sc.Nullable && !tc.Nullable {
			add("nullable", "warning",
				fmt.Sprintf("列 %s 源库允许 NULL，目标库为 NOT NULL", sc.Name),
				"若源数据存在 NULL 值，目标库写入会失败；请补默认值或调整约束")
		} else if !sc.Nullable && tc.Nullable {
			add("nullable", "info",
				fmt.Sprintf("列 %s 源库为 NOT NULL，目标库允许 NULL", sc.Name),
				"约束放松属预期（如迁移工具跳过了部分约束），需人工确认是否可接受")
		}
		// 自增
		if sc.AutoIncrement && !tc.AutoIncrement {
			add("increment", "warning",
				fmt.Sprintf("列 %s 源库为自增列，目标库未设置自增", sc.Name),
				"继续写入该表会主键冲突；PG 系需补建序列，工具迁移时会自动 setval，手工建表则不会")
		}
		// 默认值（双方都有且归一化后不同）
		if sc.DefaultValue != nil && tc.DefaultValue != nil {
			if normalizeDefault(*sc.DefaultValue) != normalizeDefault(*tc.DefaultValue) {
				add("default", "info",
					fmt.Sprintf("列 %s 默认值有差异：源 %s → 目标 %s", sc.Name, *sc.DefaultValue, *tc.DefaultValue),
					"默认值跨方言转换可能有表达形式差异（如 CURRENT_TIMESTAMP 与 now()），语义通常等价")
			}
		} else if sc.DefaultValue != nil && tc.DefaultValue == nil {
			dv := strings.TrimSpace(*sc.DefaultValue)
			if dv != "" && !strings.EqualFold(dv, "NULL") {
				add("default", "warning",
					fmt.Sprintf("列 %s 源库默认值 %s 在目标库丢失", sc.Name, dv),
					"目标库新插入行不带该列时会写入 NULL（或报错），业务行为可能变化")
			}
		}
	}
	// 目标多出的列
	srcCols := map[string]bool{}
	for _, c := range srcSchema.Columns {
		srcCols[strings.ToLower(c.Name)] = true
	}
	for _, c := range tgtSchema.Columns {
		if !srcCols[strings.ToLower(c.Name)] {
			add("column", "info",
				fmt.Sprintf("目标库多出列 %s（%s），源库无此列", c.Name, c.DataType),
				"通常是迁移工具/目标库自动生成的列，若非预期请人工核对")
		}
	}

	// ---- 索引（按列集合匹配，忽略命名差异）----
	tgtIdx := map[string]bool{}
	for _, idx := range tgtSchema.Indexes {
		if idx.IsPrimary {
			continue
		}
		tgtIdx[indexKey(idx)] = true
	}
	for _, idx := range srcSchema.Indexes {
		if idx.IsPrimary {
			continue
		}
		if !tgtIdx[indexKey(idx)] {
			unique := ""
			if idx.IsUnique {
				unique = "唯一"
			}
			add("index", "warning",
				fmt.Sprintf("缺少%s索引：(%s)（源库索引名 %s）", unique, indexColumnsLabel(idx), idx.Name),
				"缺失索引会导致目标库查询变慢或失去唯一性保护，建议在目标库补建")
		}
	}

	// ---- 外键 ----
	tgtFK := map[string]bool{}
	for _, fk := range tgtSchema.ForeignKeys {
		tgtFK[foreignKeyKey(fk)] = true
	}
	for _, fk := range srcSchema.ForeignKeys {
		if !tgtFK[foreignKeyKey(fk)] {
			add("fk", "warning",
				fmt.Sprintf("缺少外键约束：%s → %s(%s)", strings.Join(fk.Columns, ","), fk.RefTable, strings.Join(fk.RefColumns, ",")),
				"目标库失去引用完整性保护（孤儿数据不会被拦截）；迁移报告 FKErrors 有补建失败记录，需人工补建")
		}
	}

	// ---- 字符集（仅 MySQL 系之间有表级字符集可比）----
	if srcSchema.Charset != "" && tgtSchema.Charset != "" &&
		!strings.EqualFold(srcSchema.Charset, tgtSchema.Charset) {
		add("charset", "info",
			fmt.Sprintf("字符集有差异：源 %s / 目标 %s", srcSchema.Charset, tgtSchema.Charset),
			"若两者均为 utf8 系则无需处理；否则抽查中文/emoji 数据是否正常")
	}

	return items
}

// indexKey 索引指纹：唯一性 + 排序后的列集合（忽略命名差异）
func indexKey(idx types.IndexMeta) string {
	cols := append([]string(nil), idx.Columns...)
	sort.Strings(cols)
	return fmt.Sprintf("%v|%s", idx.IsUnique, strings.Join(cols, ","))
}

func indexColumnsLabel(idx types.IndexMeta) string {
	return strings.Join(idx.Columns, ", ")
}

// foreignKeyKey 外键指纹：列 + 引用表 + 引用列
func foreignKeyKey(fk types.ForeignKeyMeta) string {
	cols := append([]string(nil), fk.Columns...)
	sort.Strings(cols)
	refCols := append([]string(nil), fk.RefColumns...)
	sort.Strings(refCols)
	return strings.ToLower(fmt.Sprintf("%s|%s|%s", strings.Join(cols, ","), fk.RefTable, strings.Join(refCols, ",")))
}

// normalizeDefault 默认值归一化（忽略大小写、括号、引号形式差异）
func normalizeDefault(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	s = strings.Trim(s, "'\"")
	return s
}
