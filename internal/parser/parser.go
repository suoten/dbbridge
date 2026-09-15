package parser

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"dbbridge/pkg"
)

// SQLFileParser SQL 文件流式解析器
// 支持 GB 级 SQL 文件的流式读取，逐条解析 SQL 语句
type SQLFileParser struct {
	filePath string
	dialect  types.DatabaseType // 源方言，如 MySQL
	reader   *bufio.Reader
	file     *os.File
	lineNum  int
}

// NewSQLFileParser 创建 SQL 文件解析器
func NewSQLFileParser(filePath string, dialect types.DatabaseType) *SQLFileParser {
	return &SQLFileParser{
		filePath: filePath,
		dialect:  dialect,
	}
}

// Open 打开文件
func (p *SQLFileParser) Open() error {
	f, err := os.Open(p.filePath)
	if err != nil {
		return fmt.Errorf("parser: 打开文件失败: %w", err)
	}
	p.file = f
	p.reader = bufio.NewReaderSize(f, 1024*1024) // 1MB 缓冲区
	return nil
}

// Close 关闭文件
func (p *SQLFileParser) Close() error {
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}

// StatementType SQL 语句类型
type StatementType int

const (
	StmtUnknown StatementType = iota
	StmtCreateTable
	StmtInsert
	StmtCreateIndex
	StmtDropTable
	StmtAlterTable
	StmtComment
	StmtUse
	StmtSet
	StmtOther
)

// Statement 解析出的单条 SQL 语句
type Statement struct {
	Type    StatementType
	SQL     string
	LineNum int
}

// NextStatement 读取下一条 SQL 语句
// 流式读取，每次只解析一条语句，不会全部加载到内存
func (p *SQLFileParser) NextStatement() (*Statement, error) {
	if p.reader == nil {
		return nil, fmt.Errorf("parser: 文件未打开")
	}

	var sb strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	hasContent := false
	startLine := p.lineNum + 1

	// processLine 处理一行：更新引号状态、累加语句缓冲，分号命中时返回完整语句。
	// 提取为闭包以便 EOF 时对"无换行结尾的最后一行"复用同一逻辑
	// （旧实现 EOF 分支直接丢弃 ReadString 返回的数据，文件末尾无换行时最后一行会静默丢失）。
	processLine := func(line string) *Statement {
		p.lineNum++
		trimmed := strings.TrimSpace(line)

		// 跳过空行和注释
		if !hasContent {
			if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") {
				return nil
			}
		}

		// 处理行内注释
		line = stripLineComment(line, inSingleQuote, inDoubleQuote, inBacktick)

		if strings.TrimSpace(line) == "" && !hasContent {
			return nil
		}

		sb.WriteString(line)
		hasContent = true

		// 扫描行内字符，判断引号状态和分号
		for i := 0; i < len(line); i++ {
			ch := line[i]

			switch ch {
			case '\'':
				if !inDoubleQuote && !inBacktick {
					// 处理转义的单引号（MySQL 用 \ 或 ''）
					if i > 0 && line[i-1] == '\\' {
						continue
					}
					// 字符串内的 '' 是转义引号，跳过后保持字符串状态
					if inSingleQuote && i+1 < len(line) && line[i+1] == '\'' {
						i++
						continue
					}
					inSingleQuote = !inSingleQuote
				}
			case '"':
				if !inSingleQuote && !inBacktick {
					inDoubleQuote = !inDoubleQuote
				}
			case '`':
				if !inSingleQuote && !inDoubleQuote {
					inBacktick = !inBacktick
				}
			case ';':
				if !inSingleQuote && !inDoubleQuote && !inBacktick {
					// 语句结束
					stmt := strings.TrimSpace(sb.String())
					// 去掉末尾分号
					stmt = strings.TrimSuffix(stmt, ";")
					stmt = strings.TrimSpace(stmt)
					if stmt != "" {
						return &Statement{
							Type:    classifyStatement(stmt, p.dialect),
							SQL:     stmt,
							LineNum: startLine,
						}
					}
					sb.Reset()
					hasContent = false
					startLine = p.lineNum + 1
				}
			}
		}
		return nil
	}

	finishStatement := func() (*Statement, error) {
		// 文件在引号未闭合时结束，说明语句不完整，明确报错而非静默当作完整语句
		if inSingleQuote || inDoubleQuote || inBacktick {
			return nil, fmt.Errorf("parser: 第 %d 行开始的语句在文件末尾引号未闭合", startLine)
		}
		if hasContent {
			stmt := strings.TrimSpace(sb.String())
			if stmt != "" {
				return &Statement{
					Type:    classifyStatement(stmt, p.dialect),
					SQL:     stmt,
					LineNum: startLine,
				}, nil
			}
		}
		return nil, io.EOF
	}

	for {
		line, err := p.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("parser: 读取失败: %w", err)
		}

		if err == io.EOF {
			// 最后一行可能无换行结尾：处理后再做结尾判断
			if line != "" {
				if stmt := processLine(line); stmt != nil {
					return stmt, nil
				}
			}
			return finishStatement()
		}

		if stmt := processLine(line); stmt != nil {
			return stmt, nil
		}
	}
}

// ParseAll 解析整个文件（用于小文件，不推荐用于大文件）
func (p *SQLFileParser) ParseAll() ([]Statement, error) {
	var statements []Statement
	for {
		stmt, err := p.NextStatement()
		if err == io.EOF {
			break
		}
		if err != nil {
			return statements, err
		}
		statements = append(statements, *stmt)
	}
	return statements, nil
}

// classifyStatement 分类 SQL 语句类型
func classifyStatement(sql string, dialect types.DatabaseType) StatementType {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	// 去掉前导换行和空格
	upper = strings.TrimLeft(upper, "\n\r ")

	switch {
	case strings.HasPrefix(upper, "CREATE TABLE"),
		strings.HasPrefix(upper, "CREATE  TABLE"): // 有时有两个空格
		return StmtCreateTable
	case strings.HasPrefix(upper, "INSERT INTO"),
		strings.HasPrefix(upper, "INSERT "),
		strings.HasPrefix(upper, "INSERT\t"):
		return StmtInsert
	case strings.HasPrefix(upper, "CREATE INDEX"),
		strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
		return StmtCreateIndex
	case strings.HasPrefix(upper, "DROP TABLE"):
		return StmtDropTable
	case strings.HasPrefix(upper, "ALTER TABLE"):
		return StmtAlterTable
	case strings.HasPrefix(upper, "COMMENT ON"),
		strings.HasPrefix(upper, "COMMENT "):
		return StmtComment
	case strings.HasPrefix(upper, "USE "):
		return StmtUse
	case strings.HasPrefix(upper, "SET "):
		return StmtSet
	default:
		return StmtOther
	}
}

// stripLineComment 去除行内注释（-- 和 #）
func stripLineComment(line string, inSingleQuote, inDoubleQuote, inBacktick bool) string {
	if inSingleQuote || inDoubleQuote || inBacktick {
		return line
	}

	// 检查 -- 注释（MySQL 风格，需 -- 后跟空格）
	for i := 0; i < len(line)-2; i++ {
		if line[i] == '-' && line[i+1] == '-' && (line[i+2] == ' ' || line[i+2] == '\t') {
			// 检查 -- 是否在引号内
			if !isInQuote(line[:i]) {
				return line[:i]
			}
		}
	}

	// 检查 # 注释
	for i := 0; i < len(line); i++ {
		if line[i] == '#' {
			if !isInQuote(line[:i]) {
				return line[:i]
			}
		}
	}

	return line
}

// isInQuote 检查给定字符串中引号是否已闭合
func isInQuote(s string) bool {
	inSingle := false
	inDouble := false
	inBacktick := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '\'':
			if i > 0 && s[i-1] == '\\' {
				continue
			}
			if !inDouble && !inBacktick {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle && !inBacktick {
				inDouble = !inDouble
			}
		case '`':
			if !inSingle && !inDouble {
				inBacktick = !inBacktick
			}
		}
	}

	return inSingle || inDouble || inBacktick
}
