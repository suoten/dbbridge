// SQL 脚本方言转换服务：把存量 .sql 脚本/建表语句从源方言转换为目标方言。
//
// 能力边界（诚实标注）：
//   - CREATE TABLE/INDEX 的结构化转换基于 mysqldump 风格解析器，仅 MySQL 系源可靠；
//     其他源方言的 DDL 原样输出并给出警告；
//   - INSERT 的引号风格转换（反引号→双引号）对所有源方言有效；
//   - 转换完成后自动做一次目标方言体检（sqllint），错误级发现附在 warnings 里，
//     让用户知道转换结果还需要人工确认什么。
package service

import (
	"fmt"
	"io"
	"os"
	"strings"

	"dbbridge/internal/converter"
	"dbbridge/internal/parser"
	"dbbridge/internal/sqlexpr"
	"dbbridge/internal/sqllint"
	types "dbbridge/pkg"
)

// ConvertSQLRequest SQL 脚本转换请求
type ConvertSQLRequest struct {
	SourceDialect string `json:"sourceDialect"` // 如 mysql / mssql / postgres
	TargetDialect string `json:"targetDialect"`
	SQL           string `json:"sql"` // 脚本内容
}

// ConvertSQLResult SQL 脚本转换结果
type ConvertSQLResult struct {
	Success   bool     `json:"success"`
	Converted string   `json:"converted,omitempty"`
	Warnings  []string `json:"warnings,omitempty"` // 需人工确认的事项
	Changes   []string `json:"changes,omitempty"`  // 表达式级改写清单（改了什么、改了几处）
	Error     string   `json:"error,omitempty"`
	// 统计
	TotalStatements int `json:"totalStatements"` // 解析到的语句总数
	ConvertedCount  int `json:"convertedCount"`  // 完成方言转换的语句数
	PassedThrough   int `json:"passedThrough"`   // 原样透传（未转换）的语句数
	SkippedCount    int `json:"skippedCount"`    // 跳过（SET/USE 等环境语句）的数量
}

// IsMySQLDialect 判断是否 MySQL 系方言（DDL 结构化解析仅对 MySQL 系可靠）
func IsMySQLDialect(t types.DatabaseType) bool {
	switch t {
	case types.MySQL, types.MariaDB, types.TiDB, types.OceanBase:
		return true
	}
	return false
}

// ParseDialect 方言字符串 → DatabaseType（大小写不敏感）
func ParseDialect(s string) (types.DatabaseType, error) {
	for _, db := range types.SupportedDatabases() {
		if strings.EqualFold(string(db), strings.TrimSpace(s)) {
			return db, nil
		}
	}
	return "", fmt.Errorf("不支持的数据库方言: %s", s)
}

// ConvertSQL 把 SQL 脚本内容从源方言转换为目标方言
func (s *SQLService) ConvertSQL(req ConvertSQLRequest) *ConvertSQLResult {
	result := &ConvertSQLResult{}

	source, err := ParseDialect(req.SourceDialect)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	target, err := ParseDialect(req.TargetDialect)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if strings.TrimSpace(req.SQL) == "" {
		result.Error = "SQL 内容为空"
		return result
	}
	if source == target {
		result.Success = true
		result.Converted = req.SQL
		result.Warnings = append(result.Warnings, "源与目标方言相同，未做转换")
		return result
	}

	// parser 是文件级流式解析器，content 落临时文件处理（用完即删）
	tmp, err := os.CreateTemp("", "dbbridge-sqlconv-*.sql")
	if err != nil {
		result.Error = "创建临时文件失败: " + err.Error()
		return result
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(req.SQL); err != nil {
		tmp.Close()
		result.Error = "写入临时文件失败: " + err.Error()
		return result
	}
	tmp.Close()

	p := parser.NewSQLFileParser(tmpPath, source)
	if err := p.Open(); err != nil {
		result.Error = err.Error()
		return result
	}
	defer p.Close()

	// 目标适配器只需 DDL 生成能力，无需连接数据库
	targetAdapter := types.NewAdapter(target)
	if targetAdapter == nil {
		result.Error = fmt.Sprintf("不支持的目标方言: %s", target)
		return result
	}
	conv := converter.NewConverter(source, target, targetAdapter)

	var out strings.Builder
	for {
		stmt, err := p.NextStatement()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Error = "解析 SQL 失败: " + err.Error()
			return result
		}
		if stmt == nil {
			break
		}
		result.TotalStatements++
		sql := strings.TrimSpace(stmt.SQL)
		if sql == "" || sql == ";" {
			continue
		}

		switch stmt.Type {
		case parser.StmtCreateTable, parser.StmtCreateIndex, parser.StmtDropTable, parser.StmtInsert:
			converted, err := conv.ConvertDDL(sql)
			if err != nil {
				// 结构化转换失败：原样保留并警告，绝不静默丢语句
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("第 %d 行: 转换失败已原样保留（%v）: %s", stmt.LineNum, err, truncateSQL(sql)))
				out.WriteString(sql + ";\n\n")
				result.PassedThrough++
				continue
			}
			out.WriteString(converted)
			if !strings.HasSuffix(strings.TrimSpace(converted), ";") {
				out.WriteString(";")
			}
			out.WriteString("\n\n")
			result.ConvertedCount++

		case parser.StmtUse, parser.StmtSet, parser.StmtComment:
			// 环境语句（USE db / SET NAMES 等）对目标库无意义，跳过
			result.SkippedCount++

		default:
			// ALTER TABLE / 其他语句（SELECT 等业务 SQL）：原样透传前做表达式级安全改写
			rewritten, changes := sqlexpr.Rewrite(source, target, sql)
			for _, c := range changes {
				result.Changes = append(result.Changes, fmt.Sprintf("第 %d 行: %s", stmt.LineNum, c))
			}
			out.WriteString(rewritten + ";\n\n")
			result.PassedThrough++
			if stmt.Type == parser.StmtAlterTable {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("第 %d 行: ALTER TABLE 暂不支持自动转换，请人工确认: %s", stmt.LineNum, truncateSQL(sql)))
			}
		}
	}

	result.Converted = out.String()

	// 转换后体检：把 error 级发现提升为 warnings，提示人工确认
	lint := sqllint.Lint(source, target, result.Converted)
	for _, f := range lint.Findings {
		if f.Severity == "error" {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("转换结果第 %d 行仍有不兼容构造: %s（建议: %s）", f.Line, f.Message, f.Suggestion))
		}
	}

	result.Success = true
	return result
}

func truncateSQL(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}
