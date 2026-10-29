package schema

import (
	"fmt"
	"strings"
)

// Dictionary ClickHouse 字典定义
//
// A dictionary is a keyed lookup held in memory and readable from any
// expression through dictGet, which is what makes it the way to give a query
// with fixed SQL access to data that changes -- a correlated subquery cannot
// reference the outer row, and a join forces every ambiguous column in the
// projection to be alias-qualified.
type Dictionary struct {
	Database    string             // 所属数据库
	Name        string             // 字典名
	IfNotExists bool               // 是否添加 IF NOT EXISTS
	Attributes  []*DictionaryAttr  // 字典属性（含主键属性）
	PrimaryKeys []string           // PRIMARY KEY 属性名，多列即复合键
	Source      DictionarySource   // 数据来源
	Layout      DictionaryLayout   // 内存布局
	Lifetime    DictionaryLifetime // 重新加载间隔
}

// DictionaryAttr 字典属性
type DictionaryAttr struct {
	Name string
	Type ColumnType
	// TypeArgs 供参数化类型使用，例如 FixedString 的长度。
	TypeArgs string
	// Default 为源中缺失该属性时的取值，留空则不渲染 DEFAULT。
	Default string
}

func (a *DictionaryAttr) String() string {
	// Parameterised types carry their own placeholder, the same convention
	// Column follows, so the argument is substituted rather than appended.
	attrType := a.Type.String()
	if a.TypeArgs != "" {
		attrType = fmt.Sprintf(attrType, a.TypeArgs)
	}

	builder := strings.Builder{}
	builder.WriteString(a.Name)
	builder.WriteString(" ")
	builder.WriteString(attrType)
	if a.Default != "" {
		builder.WriteString(" DEFAULT " + a.Default)
	}
	return builder.String()
}

// DictionaryLayout ClickHouse 字典内存布局
type DictionaryLayout string

const (
	// LayoutHashed 单列键的哈希表。
	LayoutHashed DictionaryLayout = "HASHED"
	// LayoutComplexKeyHashed 复合键的哈希表，键为 tuple。
	LayoutComplexKeyHashed DictionaryLayout = "COMPLEX_KEY_HASHED"
	// LayoutFlat 连续数组，仅适用于稠密的小整数键。
	LayoutFlat DictionaryLayout = "FLAT"
)

// DictionarySource 字典的数据来源。
//
// Table 与 Query 二选一：给了 Query 就以它为准，Table 仅作为 ClickHouse 记录来源
// 用。需要过滤掉停用行、或需要 FINAL 折叠 Replacing 引擎的重复时用 Query。
type DictionarySource struct {
	Database string
	Table    string
	Query    string
	// NamedCollection 引用一个 named collection 提供连接参数与凭据。
	//
	// Source 使用独立的认证上下文，不继承创建字典的会话身份——本地 source 不走网络，
	// 但仍要认证。用 named collection 提供凭据，可以把它们挡在字典定义之外，并按
	// 服务端配置加密落盘。
	NamedCollection string
}

// DictionaryLifetime 字典的重新加载间隔（秒）。
//
// 这是数据改动到字典可见的延迟。ClickHouse 在 [Min, Max] 之间随机取值，避免多个
// 字典同时重载打在同一个源上。Min 与 Max 同为 0 表示只加载一次、永不刷新。
type DictionaryLifetime struct {
	Min int
	Max int
}

// RenderCreateSQL 渲染 CREATE DICTIONARY SQL
func (d *Dictionary) RenderCreateSQL() string {
	ifNotExists := ""
	if d.IfNotExists {
		ifNotExists = " IF NOT EXISTS"
	}

	attributes := make([]string, 0, len(d.Attributes))
	for _, attr := range d.Attributes {
		attributes = append(attributes, "    "+attr.String())
	}

	source := d.Source.render()

	return fmt.Sprintf(
		"CREATE DICTIONARY%s %s\n(\n%s\n)\nPRIMARY KEY %s\nSOURCE(%s)\nLAYOUT(%s())\nLIFETIME(MIN %d MAX %d)",
		ifNotExists,
		qualifyObjectName(d.Database, d.Name),
		strings.Join(attributes, ",\n"),
		strings.Join(d.PrimaryKeys, ", "),
		source,
		d.Layout,
		d.Lifetime.Min,
		d.Lifetime.Max,
	)
}

func (s *DictionarySource) render() string {
	parts := make([]string, 0, 4)
	if s.NamedCollection != "" {
		parts = append(parts, "NAME "+s.NamedCollection)
	}
	if s.Database != "" {
		parts = append(parts, fmt.Sprintf("DB '%s'", s.Database))
	}
	if s.Table != "" {
		parts = append(parts, fmt.Sprintf("TABLE '%s'", s.Table))
	}
	if s.Query != "" {
		// Dollar quoting: the query is full SQL and will contain the single
		// quotes that would otherwise terminate the literal.
		parts = append(parts, fmt.Sprintf("QUERY $q$%s$q$", s.Query))
	}
	return "CLICKHOUSE(" + strings.Join(parts, " ") + ")"
}

// RenderDropSQL 渲染 DROP DICTIONARY SQL
func (d *Dictionary) RenderDropSQL() string {
	return fmt.Sprintf("DROP DICTIONARY IF EXISTS %s", qualifyObjectName(d.Database, d.Name))
}

// Reference 返回 dictGet 系列函数使用的字典名。
//
// dictGet 把字典名当字符串字面量收，因此这里不能带标识符位置的反引号 —— 带上去
// 反引号会成为名字的一部分，每次查找都在运行时失败。
func (d *Dictionary) Reference() string {
	return d.Database + "." + d.Name
}

// Validate 报告 ClickHouse 会拒绝、或会静默误读的配置。
func (d *Dictionary) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("dictionary needs a name")
	}
	if len(d.Attributes) == 0 {
		return fmt.Errorf("dictionary %q has no attributes", d.Name)
	}
	if len(d.PrimaryKeys) == 0 {
		return fmt.Errorf("dictionary %q has no PRIMARY KEY", d.Name)
	}

	declared := make(map[string]struct{}, len(d.Attributes))
	for _, attr := range d.Attributes {
		declared[attr.Name] = struct{}{}
	}
	for _, key := range d.PrimaryKeys {
		if _, ok := declared[key]; !ok {
			return fmt.Errorf("dictionary %q: PRIMARY KEY %q is not among its attributes", d.Name, key)
		}
	}

	// A composite key needs the complex-key layout; HASHED silently reads only
	// the first key column, which yields wrong lookups rather than an error.
	if len(d.PrimaryKeys) > 1 && !strings.HasPrefix(string(d.Layout), "COMPLEX_KEY") {
		return fmt.Errorf(
			"dictionary %q: layout %s cannot hold a %d-column key, use a COMPLEX_KEY layout",
			d.Name, d.Layout, len(d.PrimaryKeys))
	}

	if d.Source.Table == "" && d.Source.Query == "" {
		return fmt.Errorf("dictionary %q has no source table or query", d.Name)
	}
	if d.Lifetime.Max < d.Lifetime.Min {
		return fmt.Errorf("dictionary %q: lifetime max %d is below min %d",
			d.Name, d.Lifetime.Max, d.Lifetime.Min)
	}
	return nil
}
