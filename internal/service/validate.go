// 切换前数据校验服务：程序切换数据库前的"数据对不对"工具背书。
//
// 校验内容：
//  1. 行数对比 —— 两侧行数必须一致；
//  2. 抽样逐字段比对 —— 从源库按主键顺序抽 N 行，按主键在目标库点查对应行，
//     数值（浮点容差）、时间、字符串、二进制逐字段规范化比对。
//
// 设计原则：只做校验绝不动数据；任何读取错误如实报告而不是当 0 处理，
// 避免"校验通过"的假象。
package service

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	types "dbbridge/pkg"
)

// SQLService SQL 工具类服务（脚本转换 / 方言体检 / 数据校验）
type SQLService struct{}

// NewSQLService 创建 SQL 服务实例
func NewSQLService() *SQLService {
	return &SQLService{}
}

// ValidateRequest 数据校验请求
type ValidateRequest struct {
	Source     types.ConnectionConfig `json:"source"`
	Target     types.ConnectionConfig `json:"target"`
	Tables     []string               `json:"tables,omitempty"`     // 空 = 源库全部表
	SampleSize int                    `json:"sampleSize,omitempty"` // 每表抽样行数，默认 100
}

// TableValidation 单表校验结果
type TableValidation struct {
	Table         string   `json:"table"`
	Status        string   `json:"status"` // match / row_count_mismatch / sample_mismatch / missing_target / error
	SourceRows    int64    `json:"sourceRows"`
	TargetRows    int64    `json:"targetRows"`
	Sampled       int      `json:"sampled"`                 // 实际抽样行数
	Compared      int      `json:"compared"`                // 成功在目标库找到并比对的行数
	MissingRows   []string `json:"missingRows,omitempty"`   // 目标库缺失的主键值
	FieldMismatch []string `json:"fieldMismatch,omitempty"` // 字段差异描述
	Error         string   `json:"error,omitempty"`
}

// ValidateReport 校验报告
type ValidateReport struct {
	Success       bool              `json:"success"` // 全部 match 才为 true
	MatchCount    int               `json:"matchCount"`
	MismatchCount int               `json:"mismatchCount"`
	ErrorCount    int               `json:"errorCount"`
	Tables        []TableValidation `json:"tables"`
	Summary       string            `json:"summary"`
}

// ValidateData 对源/目标库执行数据校验
func (s *SQLService) ValidateData(ctx context.Context, req ValidateRequest) *ValidateReport {
	report := &ValidateReport{}

	if req.SampleSize <= 0 {
		req.SampleSize = 100
	}
	if req.SampleSize > 10000 {
		req.SampleSize = 10000
	}

	// 连接源库
	srcAdapter := types.NewAdapter(req.Source.Type)
	if srcAdapter == nil {
		report.Summary = fmt.Sprintf("不支持的源数据库类型: %s", req.Source.Type)
		return report
	}
	if err := srcAdapter.Connect(ctx, req.Source); err != nil {
		report.Summary = "连接源库失败: " + err.Error()
		return report
	}
	defer srcAdapter.Close()

	// 连接目标库
	tgtAdapter := types.NewAdapter(req.Target.Type)
	if tgtAdapter == nil {
		report.Summary = fmt.Sprintf("不支持的目标数据库类型: %s", req.Target.Type)
		return report
	}
	if err := tgtAdapter.Connect(ctx, req.Target); err != nil {
		report.Summary = "连接目标库失败: " + err.Error()
		return report
	}
	defer tgtAdapter.Close()

	// 确定校验表清单
	tables := req.Tables
	if len(tables) == 0 {
		metas, err := srcAdapter.GetTables(ctx)
		if err != nil {
			report.Summary = "获取源库表列表失败: " + err.Error()
			return report
		}
		for _, m := range metas {
			if strings.HasPrefix(m.Name, "_bak_") {
				continue // 备份表不参与校验
			}
			tables = append(tables, m.Name)
		}
	}

	for _, table := range tables {
		if ctx.Err() != nil {
			report.ErrorCount++
			report.Tables = append(report.Tables, TableValidation{
				Table: table, Status: "error", Error: "校验已取消",
			})
			break
		}
		tv := s.validateTable(ctx, srcAdapter, tgtAdapter, table, req.SampleSize)
		report.Tables = append(report.Tables, tv)
		switch tv.Status {
		case "match":
			report.MatchCount++
		case "error":
			report.ErrorCount++
		default:
			report.MismatchCount++
		}
	}

	report.Success = report.MismatchCount == 0 && report.ErrorCount == 0
	report.Summary = fmt.Sprintf("共校验 %d 张表: 一致 %d / 不一致 %d / 错误 %d",
		len(report.Tables), report.MatchCount, report.MismatchCount, report.ErrorCount)
	return report
}

// validateTable 校验单张表
func (s *SQLService) validateTable(ctx context.Context, src, tgt types.DatabaseAdapter, table string, sampleSize int) TableValidation {
	tv := TableValidation{Table: table, Status: "match"}

	// 目标表是否存在
	exists, err := tgt.TableExists(ctx, table)
	if err != nil {
		tv.Status = "error"
		tv.Error = "检查目标表存在性失败: " + err.Error()
		return tv
	}
	if !exists {
		tv.Status = "missing_target"
		tv.Error = "目标库中不存在该表"
		return tv
	}

	// 行数对比
	srcRows, err := src.GetRowCount(ctx, table)
	if err != nil {
		tv.Status = "error"
		tv.Error = "读取源库行数失败: " + err.Error()
		return tv
	}
	tgtRows, err := tgt.GetRowCount(ctx, table)
	if err != nil {
		tv.Status = "error"
		tv.Error = "读取目标库行数失败: " + err.Error()
		return tv
	}
	tv.SourceRows = srcRows
	tv.TargetRows = tgtRows
	if srcRows != tgtRows {
		tv.Status = "row_count_mismatch"
		tv.Error = fmt.Sprintf("行数不一致: 源 %d, 目标 %d（可能迁移中断或迁移后源库有新写入）", srcRows, tgtRows)
		// 行数不一致仍然继续抽样，帮用户定位差异范围
	}

	// 抽样比对：需要单列主键/唯一列做行对齐
	schema, err := src.GetTableSchema(ctx, table)
	if err != nil {
		tv.Status = "error"
		tv.Error = "读取源库表结构失败: " + err.Error()
		return tv
	}
	keyCol := pickSingleKey(schema)
	if keyCol == "" {
		// 无主键：只做行数校验
		if tv.Status == "match" {
			tv.FieldMismatch = append(tv.FieldMismatch, "表无单列主键/唯一键，仅完成行数校验，未做逐行比对")
		}
		return tv
	}

	// 两侧各按主键顺序读前 N 行，按规范化主键对齐逐字段比对。
	// 注意不能用 keyset"点查"单行：ReadDataKeyset 语义是 key > lastKey，
	// 传入目标行主键永远查不到该行本身。
	srcSample, err := src.ReadDataKeyset(ctx, table, keyCol, nil, sampleSize)
	if err != nil {
		tv.Status = "error"
		tv.Error = "读取源库抽样数据失败: " + err.Error()
		return tv
	}
	tgtSample, err := tgt.ReadDataKeyset(ctx, table, keyCol, nil, sampleSize)
	if err != nil {
		tv.Status = "error"
		tv.Error = "读取目标库抽样数据失败: " + err.Error()
		return tv
	}
	tv.Sampled = len(srcSample)

	// 目标库列集合（目标列可能因类型映射增减，按列名交集比对）
	tgtSchema, err := tgt.GetTableSchema(ctx, table)
	if err != nil {
		tv.Status = "error"
		tv.Error = "读取目标库表结构失败: " + err.Error()
		return tv
	}
	tgtCols := map[string]bool{}
	for _, c := range tgtSchema.Columns {
		tgtCols[c.Name] = true
	}

	// 目标行索引：规范化主键 → 行
	tgtIndex := make(map[string]types.Row, len(tgtSample))
	for _, r := range tgtSample {
		if k, ok := r[keyCol]; ok && k != nil {
			tgtIndex[normalizeKey(k)] = r
		}
	}

	sampleFailed := false
	for _, srcRow := range srcSample {
		keyVal := srcRow[keyCol]
		if keyVal == nil {
			continue
		}
		tgtRow, found := tgtIndex[normalizeKey(keyVal)]
		if !found {
			tv.MissingRows = append(tv.MissingRows, fmt.Sprintf("%v", keyVal))
			sampleFailed = true
			continue
		}
		tv.Compared++

		for colName, srcVal := range srcRow {
			if !tgtCols[colName] {
				continue // 目标库没有该列（类型映射导致的列改造），跳过
			}
			tgtVal, ok := tgtRow[colName]
			if !ok {
				continue
			}
			if !valuesEqual(srcVal, tgtVal) {
				tv.FieldMismatch = append(tv.FieldMismatch,
					fmt.Sprintf("主键 %v 字段 %s: 源=%v 目标=%v", keyVal, colName,
						formatValue(srcVal), formatValue(tgtVal)))
				sampleFailed = true
			}
		}
	}
	// 目标库多出的行（前 N 主键范围内）：源缺失
	if len(tgtSample) > 0 {
		srcKeys := make(map[string]bool, len(srcSample))
		for _, r := range srcSample {
			if k, ok := r[keyCol]; ok && k != nil {
				srcKeys[normalizeKey(k)] = true
			}
		}
		for _, r := range tgtSample {
			if k, ok := r[keyCol]; ok && k != nil {
				nk := normalizeKey(k)
				if !srcKeys[nk] {
					tv.FieldMismatch = append(tv.FieldMismatch,
						fmt.Sprintf("目标库多出主键 %v 的行（源库前 %d 行中不存在）", k, sampleSize))
					sampleFailed = true
				}
			}
		}
	}

	if sampleFailed {
		if tv.Status == "match" {
			tv.Status = "sample_mismatch"
		}
	}
	return tv
}

// pickSingleKey 找单列主键；没有则找单列非空唯一索引
func pickSingleKey(schema types.TableSchema) string {
	for _, idx := range schema.Indexes {
		if idx.IsPrimary && len(idx.Columns) == 1 {
			return idx.Columns[0]
		}
	}
	for _, c := range schema.Columns {
		if c.IsPrimaryKey {
			return c.Name
		}
	}
	for _, idx := range schema.Indexes {
		if idx.IsUnique && len(idx.Columns) == 1 {
			for _, c := range schema.Columns {
				if c.Name == idx.Columns[0] && !c.Nullable {
					return c.Name
				}
			}
		}
	}
	return ""
}

// normalizeKey 把主键值规范化为字符串作 map key（数值统一 float64，
// 避免 int64(1) 与 float64(1) 因驱动差异判为不同行）
func normalizeKey(v any) string {
	if f, ok := toFloat(v); ok {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return normalizeString(v)
}

// valuesEqual 规范化比较两个值：
//   - 都 nil 相等；一侧 nil 不等
//   - 数值：统一 float64，容差 1e-9（迁移中 NUMERIC 精度尾差不算不一致）
//   - time.Time：格式化为秒级字符串比较（不同库驱动返回精度不同）
//   - []byte：按字符串/字节比较
//   - 字符串：去除尾部空白后精确比较（CHAR 尾部填充差异）
func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// time.Time 归一化
	if ta, ok := a.(time.Time); ok {
		if tb, ok2 := b.(time.Time); ok2 {
			return ta.Equal(tb)
		}
		// 源是 time.Time、目标是字符串（常见：目标 TEXT/驱动差异）
		return ta.Format("2006-01-02 15:04:05.999") == normalizeTimeString(fmt.Sprintf("%v", b))
	}
	if tb, ok := b.(time.Time); ok {
		return tb.Format("2006-01-02 15:04:05.999") == normalizeTimeString(fmt.Sprintf("%v", a))
	}

	// 数值比较（int/int64/float64/uint 等全部统一）
	af, aIsNum := toFloat(a)
	bf, bIsNum := toFloat(b)
	if aIsNum && bIsNum {
		return math.Abs(af-bf) < 1e-9
	}

	// []byte / 字符串
	return normalizeString(a) == normalizeString(b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func normalizeString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimRight(x, " ")
	case []byte:
		return strings.TrimRight(string(x), " ")
	default:
		return strings.TrimRight(fmt.Sprintf("%v", x), " ")
	}
}

func normalizeTimeString(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "T", " "))
	// 去掉时区后缀（+08:00 / UTC 等）
	for _, suffix := range []string{" +0000 UTC", " UTC", "Z", " +08:00"} {
		s = strings.TrimSuffix(s, suffix)
	}
	// 去掉日期之后的时区偏移（如 2026-01-02 15:04:05+08:00）
	if len(s) > 10 {
		if i := strings.IndexAny(s[10:], "+-"); i >= 0 {
			s = s[:10+i]
		}
	}
	return strings.TrimSpace(s)
}

func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}
	if reflect.TypeOf(v).Kind() == reflect.Slice {
		return fmt.Sprintf("<binary %d bytes>", reflect.ValueOf(v).Len())
	}
	s := fmt.Sprintf("%v", v)
	if len(s) > 50 {
		s = s[:47] + "..."
	}
	return s
}
