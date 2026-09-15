// 迁移适配指南生成器：迁移前评估"业务代码跟着改"的工作量。
//
// 只读不动任何数据：连接源库读元数据，结合类型映射与触发器/存储过程转换
// 预演（只转换不执行），生成一份人工核对清单：
//   - 列类型映射差异（如 DATETIME2 → TIMESTAMP、BOOL → TINYINT(1)）
//   - 触发器/存储过程哪些能自动转换、哪些必须手写
//   - CHECK 约束、字符集/排序规则、自增列等需要关注的点
//   - 目标方言的通用语义差异提示（标识符大小写、分页、序列等）
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

// GuideItem 指南条目
type GuideItem struct {
	Category   string `json:"category"`         // trigger/routine/check/charset/increment/dialect
	Severity   string `json:"severity"`         // error/warning/info
	Table      string `json:"table,omitempty"`  // 关联表（可空）
	Object     string `json:"object,omitempty"` // 关联对象名（触发器/过程名等）
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// GuideTypeChange 列类型映射差异
type GuideTypeChange struct {
	Table      string `json:"table"`
	Column     string `json:"column"`
	SourceType string `json:"sourceType"`
	TargetType string `json:"targetType"`
	Note       string `json:"note,omitempty"` // 语义差异说明
}

// GuideReport 迁移适配指南报告
type GuideReport struct {
	Success bool              `json:"success"`
	Source  string            `json:"source"`
	Target  string            `json:"target"`
	Tables  int               `json:"tables"`
	Changes []GuideTypeChange `json:"changes,omitempty"`
	Items   []GuideItem       `json:"items,omitempty"`
	// 触发器/存储过程预演统计
	TriggersTotal  int    `json:"triggersTotal"`
	TriggersOk     int    `json:"triggersOk"`
	TriggersFailed int    `json:"triggersFailed"`
	RoutinesTotal  int    `json:"routinesTotal"`
	RoutinesOk     int    `json:"routinesOk"`
	RoutinesFailed int    `json:"routinesFailed"`
	Summary        string `json:"summary,omitempty"`
	Error          string `json:"error,omitempty"`
}

// GenerateMigrationGuide 生成迁移适配指南（只读源库元数据 + 纯函数转换预演）
func (s *SQLService) GenerateMigrationGuide(ctx context.Context, source types.ConnectionConfig, targetDialect string, tables []string) *GuideReport {
	report := &GuideReport{Source: string(source.Type)}

	target, err := ParseDialect(targetDialect)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	if target == source.Type {
		report.Error = "源与目标方言相同，无需适配指南"
		return report
	}
	report.Target = string(target)

	srcAdapter := types.NewAdapter(source.Type)
	if srcAdapter == nil {
		report.Error = fmt.Sprintf("不支持的数据库类型: %s", source.Type)
		return report
	}
	defer srcAdapter.Close()
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := srcAdapter.Connect(connectCtx, source); err != nil {
		report.Error = "连接源库失败: " + err.Error()
		return report
	}

	tgtAdapter := types.NewAdapter(target)
	if tgtAdapter == nil {
		report.Error = fmt.Sprintf("不支持的目标方言: %s", target)
		return report
	}

	// 表清单
	tableMetas, err := srcAdapter.GetTables(ctx)
	if err != nil {
		report.Error = "读取表列表失败: " + err.Error()
		return report
	}
	want := map[string]bool{}
	for _, t := range tables {
		want[strings.TrimSpace(t)] = true
	}

	isPGTgt := isPGFamilyDialect(target)
	isMySQLTgt := isMySQLFamilyDialect(target)

	for _, tm := range tableMetas {
		if len(want) > 0 && !want[tm.Name] {
			continue
		}
		report.Tables++
		schema, err := srcAdapter.GetTableSchema(ctx, tm.Name)
		if err != nil {
			report.Items = append(report.Items, GuideItem{
				Category: "schema", Severity: "error", Table: tm.Name,
				Message: "读取表结构失败: " + err.Error(),
			})
			continue
		}

		for _, col := range schema.Columns {
			tgtType := tgtAdapter.MapType(col)
			if normalizeTypeName(tgtType) == normalizeTypeName(col.DataType) {
				continue
			}
			report.Changes = append(report.Changes, GuideTypeChange{
				Table:      tm.Name,
				Column:     col.Name,
				SourceType: col.DataType,
				TargetType: tgtType,
				Note:       typeChangeNote(col, target, isPGTgt),
			})
		}

		if len(schema.Checks) > 0 {
			report.Items = append(report.Items, GuideItem{
				Category: "check", Severity: "info", Table: tm.Name,
				Message:    fmt.Sprintf("表带 %d 个 CHECK 约束，约束表达式跨方言可能有语法/语义差异", len(schema.Checks)),
				Suggestion: "迁移后逐条核对 CHECK 表达式在目标库的语义（如字符串比较、日期函数）",
			})
		}

		if schema.Charset != "" && !isMySQLTgt {
			report.Items = append(report.Items, GuideItem{
				Category: "charset", Severity: "info", Table: tm.Name,
				Message:    fmt.Sprintf("源表使用字符集 %s（排序规则 %s）", schema.Charset, schema.Collation),
				Suggestion: charsetAdvice(schema.Charset, target),
			})
		}

		// 自增列（PG 系目标会在数据迁移后由工具自动 setval 修复序列，这里提示语义）
		for _, col := range schema.Columns {
			if col.AutoIncrement && col.IsPrimaryKey && isPGTgt {
				report.Items = append(report.Items, GuideItem{
					Category: "increment", Severity: "info", Table: tm.Name, Object: col.Name,
					Message:    "自增主键列映射为 SERIAL/BIGSERIAL，显式插入的值不会推进序列",
					Suggestion: "工具在数据迁移完成后会自动 setval 修复；若用手工 SQL 插入过数据需自行修复",
				})
			}
		}
	}

	// 触发器/存储过程转换预演（只转换不执行）
	triggers, err := srcAdapter.GetTriggers(ctx)
	if err != nil {
		report.Items = append(report.Items, GuideItem{
			Category: "trigger", Severity: "warning",
			Message: "读取触发器列表失败（不影响表和数据迁移）: " + err.Error(),
		})
	} else {
		report.TriggersTotal = len(triggers)
		for _, tr := range triggers {
			_, err := srcAdapter.GenerateTriggerDDL(tr, target)
			if err != nil {
				report.TriggersFailed++
				report.Items = append(report.Items, GuideItem{
					Category: "trigger", Severity: "error", Table: tr.Table, Object: tr.Name,
					Message:    fmt.Sprintf("触发器无法自动转换（%s %s %s）: %s", tr.Timing, tr.Event, tr.Table, err.Error()),
					Suggestion: "需人工改写为目标方言的 CREATE TRIGGER 语句",
				})
			} else {
				report.TriggersOk++
			}
		}
	}

	routines, err := srcAdapter.GetRoutines(ctx)
	if err != nil {
		report.Items = append(report.Items, GuideItem{
			Category: "routine", Severity: "warning",
			Message: "读取存储过程/函数列表失败（不影响表和数据迁移）: " + err.Error(),
		})
	} else {
		report.RoutinesTotal = len(routines)
		for _, r := range routines {
			_, err := srcAdapter.GenerateRoutineDDL(r, target)
			if err != nil {
				report.RoutinesFailed++
				report.Items = append(report.Items, GuideItem{
					Category: "routine", Severity: "error", Object: r.Name,
					Message:    fmt.Sprintf("存储%s无法自动转换: %s", routineKindName(r.Type), err.Error()),
					Suggestion: "需人工改写为目标方言的 CREATE PROCEDURE/FUNCTION；注意过程体内部的方言函数与流程控制语法",
				})
			} else {
				report.RoutinesOk++
			}
		}
	}

	// 目标方言通用语义提示
	report.Items = append(report.Items, dialectHints(target)...)

	sort.SliceStable(report.Items, func(i, j int) bool {
		return severityRank(report.Items[i].Severity) < severityRank(report.Items[j].Severity)
	})

	report.Summary = buildGuideSummary(report)
	report.Success = true
	return report
}

func routineKindName(t string) string {
	if strings.EqualFold(t, "function") {
		return "函数"
	}
	return "过程"
}

func severityRank(sev string) int {
	switch sev {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func isPGFamilyDialect(t types.DatabaseType) bool {
	switch t {
	case types.PostgreSQL, types.OpenGauss, types.KingbaseES, types.CockroachDB:
		return true
	}
	return false
}

func isMySQLFamilyDialect(t types.DatabaseType) bool {
	switch t {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase:
		return true
	}
	return false
}

// normalizeTypeName 类型名归一化比较（去空白、小写）
func normalizeTypeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// typeChangeNote 按类型 Kind 与目标方言给出语义差异说明
func typeChangeNote(col types.ColumnMeta, target types.DatabaseType, isPGTgt bool) string {
	k := typeconv.Normalize(col.BaseType)
	switch k {
	case typeconv.KindBool:
		return "布尔类型按目标方言存储，业务代码判断真值的方式可能需要调整"
	case typeconv.KindEnum, typeconv.KindSet:
		return "枚举/集合类型转为 VARCHAR，数据库不再强制取值范围，需应用层校验"
	case typeconv.KindJSON:
		if isPGTgt {
			return "JSON → JSONB：JSONB 二进制存储去重排序键，查询用 @> 等操作符而非 JSON_EXTRACT"
		}
		return "JSON 类型存储方式有差异，查询语法需按目标方言调整"
	case typeconv.KindDateTime, typeconv.KindTimestamp:
		if isPGTgt {
			return "时间类型 → TIMESTAMP：PG 不做时区换算，写入值按原样存储；MySQL 的 0000-00-00 零值 PG 不接受"
		}
		return "时间类型精度/范围有差异，极端值（如 0001-01-01）可能写入失败"
	case typeconv.KindYear:
		return "YEAR 类型映射为整型/小整型，业务代码按 YEAR 语义使用需确认"
	case typeconv.KindUUID:
		return "UUID 类型存储形式有差异（原生 UUID vs VARCHAR），索引与比较行为可能不同"
	case typeconv.KindBit:
		return "BIT 类型语义各库差异较大，业务代码按位读写需人工确认"
	}
	if col.Unsigned && !isMySQLFamilyDialect(target) {
		return "UNSIGNED 属性在目标库不存在，若实际数据超出有符号范围会溢出报错"
	}
	if col.Unsigned && isMySQLFamilyDialect(target) {
		return "UNSIGNED 属性保留"
	}
	return ""
}

// charsetAdvice 字符集建议
func charsetAdvice(charset string, target types.DatabaseType) string {
	cs := strings.ToLower(charset)
	if strings.Contains(cs, "utf8") || strings.Contains(cs, "unicode") {
		return "源为 UTF-8 系字符集，目标库（按数据库级编码）通常可无缝承接，无需逐表处理"
	}
	if strings.Contains(cs, "gbk") || strings.Contains(cs, "gb2312") {
		return fmt.Sprintf("源为 %s 中文字符集，目标库需确认数据库级编码支持，建议先在测试库验证生僻字与表情符号", charset)
	}
	if strings.Contains(cs, "latin") {
		return "源为 latin1 字符集，若存量数据实为 UTF-8 字节（历史误配），直接迁移会乱码，建议迁移前做编码转储验证"
	}
	return "目标库无表级字符集概念（按数据库级编码存储），建议迁移后抽查特殊字符数据"
}

// dialectHints 目标方言通用语义提示
func dialectHints(target types.DatabaseType) []GuideItem {
	switch {
	case isPGFamilyDialect(target):
		return []GuideItem{
			{Category: "dialect", Severity: "info",
				Message:    "PG 系未加引号的标识符统一折叠为小写：CREATE TABLE UserList 实际存为 userlist",
				Suggestion: "业务 SQL 与 ORM 中不要混用引号大小写；工具迁移时统一为小写表名/列名"},
			{Category: "dialect", Severity: "info",
				Message:    "PG 无 LIMIT offset,count 双参数语法，分页用 LIMIT n OFFSET m",
				Suggestion: "业务代码中的分页语句需按此调整（工具体检会逐条标出）"},
			{Category: "dialect", Severity: "info",
				Message:    "PG 自增取值不能再用 LAST_INSERT_ID()，改为 INSERT ... RETURNING id 或 currval",
				Suggestion: "涉及取自增 ID 的业务代码必须改写，并发下取错 ID 是高频事故点"},
		}
	case isMySQLFamilyDialect(target):
		return []GuideItem{
			{Category: "dialect", Severity: "info",
				Message:    "MySQL 8.0 起 GROUP BY 不再隐式排序，依赖排序结果的业务代码需显式 ORDER BY",
				Suggestion: "迁移后回归测试依赖 GROUP BY 顺序的报表/列表逻辑"},
			{Category: "dialect", Severity: "info",
				Message:    "MySQL 默认 ONLY_FULL_GROUP_BY：SELECT 非聚合列必须出现在 GROUP BY 中",
				Suggestion: "来自 PG/MSSQL 的宽松 GROUP BY 语句可能报错 1055，体检会标出"},
		}
	case target == types.MSSQL:
		return []GuideItem{
			{Category: "dialect", Severity: "info",
				Message:    "MSSQL 分页用 SELECT TOP n 或 ORDER BY ... OFFSET n FETCH NEXT m ROWS ONLY，不支持 LIMIT",
				Suggestion: "业务代码分页语句需改写，体检会逐条标出"},
			{Category: "dialect", Severity: "info",
				Message:    "MSSQL 的 NVARCHAR 才存储 Unicode，工具已统一映射为 N 系类型",
				Suggestion: "手工建表时注意用 NVARCHAR/NCHAR，否则中文会变问号"},
		}
	case target == types.Dameng:
		return []GuideItem{
			{Category: "dialect", Severity: "info",
				Message:    "达梦默认大小写敏感且未加引号的标识符按大写存储（CASE_SENSITIVE=Y）",
				Suggestion: "与工具迁移的表名大小写保持一致，混用易找不到表"},
			{Category: "dialect", Severity: "info",
				Message:    "达梦对 LIMIT 的支持依兼容模式而定，Oracle 模式下用 ROWNUM 或 OFFSET ... FETCH",
				Suggestion: "确认初始化实例时的兼容模式后统一分页写法"},
		}
	case target == types.SQLite:
		return []GuideItem{
			{Category: "dialect", Severity: "info",
				Message:    "SQLite 使用类型亲和性：DATETIME/JSON 等落为 TEXT，无真正的日期类型",
				Suggestion: "日期比较按字符串规则执行，业务代码需统一写入格式（工具已统一为标准 datetime 文本）"},
		}
	}
	return nil
}

// buildGuideSummary 生成摘要
func buildGuideSummary(r *GuideReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "共评估 %d 张表，列类型映射差异 %d 处", r.Tables, len(r.Changes))
	if r.TriggersTotal > 0 {
		fmt.Fprintf(&sb, "；触发器 %d 个（可自动转换 %d，需人工改写 %d）", r.TriggersTotal, r.TriggersOk, r.TriggersFailed)
	}
	if r.RoutinesTotal > 0 {
		fmt.Fprintf(&sb, "；存储过程/函数 %d 个（可自动转换 %d，需人工改写 %d）", r.RoutinesTotal, r.RoutinesOk, r.RoutinesFailed)
	}
	errCount, warnCount := 0, 0
	for _, it := range r.Items {
		switch it.Severity {
		case "error":
			errCount++
		case "warning":
			warnCount++
		}
	}
	fmt.Fprintf(&sb, "；待办事项 %d 项（其中必须人工处理 %d 项）", len(r.Items), errCount)
	_ = warnCount
	return sb.String()
}
