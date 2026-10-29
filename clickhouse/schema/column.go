package schema

import (
	"fmt"
	"strings"
)

// ColumnType ClickHouse 列类型枚举
type ColumnType uint8

const (
	UInt64 ColumnType = iota
	UInt64Nullable
	UInt32
	UInt32Nullable
	UInt16
	UInt16Nullable
	UInt8
	UInt8Nullable
	Int64
	Int64Nullable
	Int32
	Int32Nullable
	Int16
	Int16Nullable
	Int8
	Int8Nullable
	Float64
	Float64Nullable
	Float32
	String
	IPv6
	IPv4
	ArrayString
	ArrayUInt8
	ArrayUInt16
	ArrayUInt32
	ArrayInt64
	ArrayFloat32
	ArrayFloat64
	ArrayDateTime64ns
	Date
	DateTime
	DateTime64
	DateTime64ms
	DateTime64us
	DateTime64ns
	FixString8
	LowCardinalityString
	ArrayLowCardinalityString
	ENUM8
	Nested
	JSON
	ArrayBFloat16
	FixedString
	SimpleAggregateFunction
	AggregateFunction
	Map // Map(KeyType, ValueType)，通过 TypeArgs 指定参数
)

// columnTypeFormatString 列类型格式化字符串模板
//
// 注意：带 timezone 占位符的类型在 String() 和 StringWithTimezone() 方法中处理，
// 此处使用 %s 作为 timezone 占位符。
var columnTypeFormatString = []string{
	UInt64:                    "UInt64",
	UInt64Nullable:            "Nullable(UInt64)",
	UInt32:                    "UInt32",
	UInt32Nullable:            "Nullable(UInt32)",
	UInt16:                    "UInt16",
	UInt16Nullable:            "Nullable(UInt16)",
	UInt8:                     "UInt8",
	UInt8Nullable:             "Nullable(UInt8)",
	Int64:                     "Int64",
	Int64Nullable:             "Nullable(Int64)",
	Int32:                     "Int32",
	Int32Nullable:             "Nullable(Int32)",
	Int16:                     "Int16",
	Int16Nullable:             "Nullable(Int16)",
	Int8:                      "Int8",
	Int8Nullable:              "Nullable(Int8)",
	Float64:                   "Float64",
	Float64Nullable:           "Nullable(Float64)",
	Float32:                   "Float32",
	String:                    "String",
	IPv6:                      "IPv6",
	IPv4:                      "IPv4",
	ArrayString:               "Array(String)",
	ArrayUInt8:                "Array(UInt8)",
	ArrayUInt16:               "Array(UInt16)",
	ArrayUInt32:               "Array(UInt32)",
	ArrayInt64:                "Array(Int64)",
	ArrayFloat32:              "Array(Float32)",
	ArrayFloat64:              "Array(Float64)",
	ArrayDateTime64ns:         "Array(DateTime64(9, '%s'))",
	Date:                      "Date",
	DateTime:                  "DateTime('%s')",
	DateTime64:                "DateTime64(0, '%s')",
	DateTime64ms:              "DateTime64(3, '%s')",
	DateTime64us:              "DateTime64(6, '%s')",
	DateTime64ns:              "DateTime64(9, '%s')",
	FixString8:                "FixedString(8)",
	LowCardinalityString:      "LowCardinality(String)",
	ArrayLowCardinalityString: "Array(LowCardinality(String))",
	ENUM8:                     "Enum8(%s)",
	Nested:                    "Nested(%s)",
	JSON:                      "JSON",
	ArrayBFloat16:             "Array(BFloat16)",
	FixedString:               "FixedString(%s)",
	SimpleAggregateFunction:   "SimpleAggregateFunction(%s, %s)",
	AggregateFunction:         "AggregateFunction(%s, %s)",
	Map:                       "Map(%s)",
}

// needsTimezone 返回该类型是否包含时区占位符
func (t ColumnType) needsTimezone() bool {
	switch t {
	case DateTime, DateTime64, DateTime64ms, DateTime64us, DateTime64ns, ArrayDateTime64ns:
		return true
	default:
		return false
	}
}

// String renders the type without a timezone override; ClickHouse owns the default.
func (t ColumnType) String() string {
	return t.StringWithTimezone("")
}

// StringWithTimezone 返回类型字符串（使用指定时区）
func (t ColumnType) StringWithTimezone(tz string) string {
	format := columnTypeFormatString[t]
	if t.needsTimezone() {
		if tz == "" {
			if t == DateTime {
				return "DateTime"
			}
			return strings.ReplaceAll(format, ", '%s'", "")
		}
		return fmt.Sprintf(format, tz)
	}
	return format
}

// NestedColumns Nested 列集合
type NestedColumns []Column

// StringWithTimezone 渲染 Nested 列定义
func (ns NestedColumns) StringWithTimezone(tz string) string {
	columns := make([]string, 0, len(ns))
	for _, n := range ns {
		codec := ""
		if n.Codec != CodecDefault {
			codec = fmt.Sprintf("CODEC(%s)", n.Codec.String())
		}

		columnType := n.Type.StringWithTimezone(tz)
		if n.TypeArgs != "" {
			columnType = fmt.Sprintf(n.Type.StringWithTimezone(tz), n.TypeArgs)
		}
		columns = append(columns, fmt.Sprintf("`%s` %s %s", n.Name, columnType, codec))
	}
	return strings.Join(columns, ",\n")
}

// CodecType 压缩编码类型
type CodecType uint8

const (
	CodecDefault CodecType = iota // lz4
	CodecLZ4
	CodecLZ4HC
	CodecZSTD
	CodecT64
	CodecDelta
	CodecDoubleDelta
	CodecGorilla
	CodecNone
)

var codecTypeString = []string{
	CodecDefault:     "",
	CodecLZ4:         "LZ4",
	CodecLZ4HC:       "LZ4HC",
	CodecZSTD:        "ZSTD(1)",
	CodecT64:         "T64",
	CodecDelta:       "Delta",
	CodecDoubleDelta: "DoubleDelta",
	CodecGorilla:     "Gorilla",
	CodecNone:        "None",
}

func (t CodecType) String() string {
	return codecTypeString[t]
}

// IndexType 二级索引类型
type IndexType uint8

const (
	IndexNone IndexType = iota
	IndexMinmax
	IndexSet
	IndexBloomfilter
	IndexTokenbf
	IndexVector
	// IndexTextArray is ClickHouse's full-text index with the array tokenizer:
	// each array element is one token, so it fits mapKeys()/mapValues() where
	// whole attribute keys and values are the unit of match. It needs an
	// expression, so it is only usable through Table.Indexes, not SetIndex.
	IndexTextArray
)

var indexTypeString = []string{
	IndexNone:        "",
	IndexMinmax:      "minmax",
	IndexSet:         "set(300)",
	IndexBloomfilter: "bloom_filter(0.01)",
	IndexTokenbf:     "tokenbf_v1(32768, 3, 0)",
	IndexVector:      "vector_similarity('hnsw', 'cosineDistance', 4096)",
	IndexTextArray:   "text(tokenizer = 'array')",
}

func (t IndexType) String() string {
	return indexTypeString[t]
}

// TimeFuncType 时间函数类型，用于 PARTITION BY / TTL
type TimeFuncType uint8

const (
	TimeFuncNone TimeFuncType = iota
	TimeFuncThirtySecond
	TimeFuncMinute
	TimeFuncTwoMinute
	TimeFuncFiveMinute
	TimeFuncTenMinute
	TimeFuncTwentyMinute
	TimeFuncHour
	TimeFuncTwoHour
	TimeFuncFourHour
	TimeFuncTwelveHour
	TimeFuncDay
	TimeFuncWeek
	TimeFuncMonth
	TimeFuncYYYYMM
	TimeFuncYYYYMMDD
	TimeFuncDate
	TimeFuncDateTime
)

var timeFuncTypeString = []string{
	TimeFuncNone:         "%s",
	TimeFuncThirtySecond: "toStartOfInterval(%s, INTERVAL 30 SECOND)",
	TimeFuncMinute:       "toStartOfMinute(%s)",
	TimeFuncTwoMinute:    "toStartOfInterval(%s, INTERVAL 2 MINUTE)",
	TimeFuncFiveMinute:   "toStartOfInterval(%s, INTERVAL 5 MINUTE)",
	TimeFuncTenMinute:    "toStartOfTenMinutes(%s)",
	TimeFuncTwentyMinute: "toStartOfInterval(%s, INTERVAL 20 MINUTE)",
	TimeFuncHour:         "toStartOfHour(%s)",
	TimeFuncTwoHour:      "toStartOfInterval(%s, INTERVAL 2 hour)",
	TimeFuncFourHour:     "toStartOfInterval(%s, INTERVAL 4 hour)",
	TimeFuncTwelveHour:   "toStartOfInterval(%s, INTERVAL 12 hour)",
	TimeFuncDay:          "toStartOfDay(%s)",
	TimeFuncWeek:         "toStartOfWeek(%s)",
	TimeFuncMonth:        "toStartOfMonth(%s)",
	TimeFuncYYYYMM:       "toYYYYMM(%s)",
	TimeFuncYYYYMMDD:     "toYYYYMMDD(%s)",
	TimeFuncDate:         "toDate(%s)",
	TimeFuncDateTime:     "toDateTime(%s)",
}

// String 应用时间函数到指定时间字段
func (t TimeFuncType) String(timeKey string) string {
	return fmt.Sprintf(timeFuncTypeString[t], timeKey)
}

// EngineType 表引擎类型
type EngineType string

const (
	EngineTypeDistributed                    EngineType = "Distributed"
	EngineTypeMergeTree                      EngineType = "MergeTree"
	EngineTypeReplacingMergeTree             EngineType = "ReplacingMergeTree"
	EngineTypeAggregatingMergeTree           EngineType = "AggregatingMergeTree"
	EngineTypeReplicatedReplacingMergeTree   EngineType = "ReplicatedReplacingMergeTree"
	EngineTypeReplicatedAggregatingMergeTree EngineType = "ReplicatedAggregatingMergeTree"
	EngineTypeSummingMergeTree               EngineType = "SummingMergeTree"
)

// TableEngine 表引擎定义
type TableEngine struct {
	Name   EngineType
	Params string
}

// IsValid 验证引擎类型是否合法
func (t TableEngine) IsValid() bool {
	return t.Name == "" || t.Name == EngineTypeMergeTree || t.Name == EngineTypeAggregatingMergeTree ||
		t.Name == EngineTypeReplacingMergeTree || t.Name == EngineTypeSummingMergeTree ||
		t.Name == EngineTypeReplicatedReplacingMergeTree
}

// String 生成 ENGINE 字符串
func (t TableEngine) String() string {
	engine := t.Name
	params := t.Params

	if t.Name == "" {
		engine = EngineTypeMergeTree
		params = ""
	}

	return fmt.Sprintf("%s(%s)", engine, params)
}

// DiskType 存储磁盘类型
type DiskType uint8

const (
	Volume DiskType = iota
	Disk
)

func (t DiskType) String() string {
	switch t {
	case Volume:
		return "VOLUME"
	case Disk:
		return "DISK"
	default:
		return "Unknown"
	}
}

const (
	CKDBTypeClickhouse = "clickhouse"
	CKDBTypeByconity   = "byconity"
)

// Column ClickHouse 列定义
type Column struct {
	Name             string      // 列名
	Type             ColumnType  // 数据类型
	TypeArgs         string      // 类型参数（用于 FixedString、Nested、Map 等参数化类型）
	AggregateArg     string      // 聚合函数参数（如 "min", "max", "sum"）
	DataType         ColumnType  // 聚合函数的数据类型
	Codec            CodecType   // 压缩算法
	Codecs           []CodecType // 多个压缩算法（如 Delta, ZSTD）
	Index            IndexType   // 二级索引
	MapIndex         IndexType   // Map 列的键/值索引，渲染为 mapKeys()/mapValues() 一对
	GroupBy          bool        // 在 AggregatingMergeTree 表中用于 group by 的字段
	Comment          string      // 列注释
	Nested           []*Column   // Nested 子列
	TimeFiled        bool        // 时间字段，用来设置 partition 和 ttl
	Projection       bool        // 是否为投影列
	DefaultExpr      string      // 默认值表达式
	MaterializedExpr string      // 物化列表达式
}

// MakeModifyTimeZoneSQL 生成修改列时区的 ALTER SQL
func (c *Column) MakeModifyTimeZoneSQL(database, table, timeZone string) string {
	if timeZone == "" || !c.Type.needsTimezone() {
		return ""
	}
	newTimeZoneType := c.Type.StringWithTimezone(timeZone)
	return fmt.Sprintf("ALTER TABLE %s.`%s` MODIFY COLUMN %s %s", database, table, c.Name, newTimeZoneType)
}

func (c *Column) SetGroupBy() *Column {
	c.GroupBy = true
	return c
}

func (c *Column) SetCodec(ct CodecType) *Column {
	c.Codec = ct
	return c
}

func (c *Column) SetCodecs(cts ...CodecType) *Column {
	c.Codecs = cts
	return c
}

func (c *Column) SetIndex(i IndexType) *Column {
	c.Index = i
	return c
}

// SetMapIndex indexes a Map column's keys and values as a pair, rendering
// `mapKeys(col)` and `mapValues(col)` as two separate skip indexes.
//
// The pair is the unit because that is how Map attributes get queried:
// `mapContains(col,k) AND col[k]=v` reads the key index first and the value
// index second. Indexing the Map column itself instead would merge keys and
// values into one token set, which no query shape can use.
//
// SetIndex cannot express this — it always indexes the bare column name.
func (c *Column) SetMapIndex(i IndexType) *Column {
	c.MapIndex = i
	return c
}

func (c *Column) SetComment(comment string) *Column {
	c.Comment = comment
	return c
}

func (c *Column) SetTypeArgs(args string) *Column {
	c.TypeArgs = args
	return c
}

func (c *Column) SetNested(nested []*Column) *Column {
	c.Nested = nested
	return c
}

const (
	// NestedKeysSubcolumn / NestedValuesSubcolumn name the subcolumns of a
	// parallel-array map Nested column. ClickHouse stores Nested(keys ..., values ...)
	// as the array pair <col>.keys / <col>.values.
	NestedKeysSubcolumn   = "keys"
	NestedValuesSubcolumn = "values"
)

// IsArrayMap reports whether the column is a parallel-array map: a Nested column
// whose subcolumns are exactly keys and values. Only this shape may be addressed
// via indexOf(<col>.keys, k) → <col>.values[...]. Other Nested columns (e.g. the
// spans events/links columns, whose subcolumns are timestamp/name/attributes)
// are NOT array maps and must not be treated as one.
func (c *Column) IsArrayMap() bool {
	if c.Type != Nested {
		return false
	}
	var hasKeys, hasValues bool
	for _, sub := range c.Nested {
		switch sub.Name {
		case NestedKeysSubcolumn:
			hasKeys = true
		case NestedValuesSubcolumn:
			hasValues = true
		}
	}
	return hasKeys && hasValues
}

func (c *Column) SetTimeFiled() *Column {
	c.TimeFiled = true
	return c
}

func (c *Column) SetProjection() *Column {
	c.Projection = true
	return c
}

func (c *Column) SetAggregateType(aggregateArg string, dataType ColumnType) *Column {
	c.AggregateArg = aggregateArg
	c.DataType = dataType
	return c
}

func (c *Column) SetDefault(expr string) *Column {
	c.DefaultExpr = expr
	return c
}

func (c *Column) SetMaterialized(expr string) *Column {
	c.MaterializedExpr = expr
	return c
}

func (c *Column) IsTimeFiled() bool {
	return c.TimeFiled
}

// StringWithTimezone 渲染列定义（使用指定时区）
func (c *Column) StringWithTimezone(tz string) string {
	var builder strings.Builder

	columnType := c.Type.StringWithTimezone(tz)
	if c.Type == SimpleAggregateFunction || c.Type == AggregateFunction {
		columnType = fmt.Sprintf(c.Type.StringWithTimezone(tz), c.AggregateArg, c.DataType.StringWithTimezone(tz))
	}
	if c.TypeArgs != "" {
		columnType = fmt.Sprintf(c.Type.StringWithTimezone(tz), c.TypeArgs)
	}
	builder.WriteString(fmt.Sprintf("`%s` %s", c.Name, columnType))

	if c.DefaultExpr != "" {
		builder.WriteString(fmt.Sprintf(" DEFAULT %s", c.DefaultExpr))
	}

	if c.MaterializedExpr != "" {
		builder.WriteString(fmt.Sprintf(" MATERIALIZED %s", c.MaterializedExpr))
	}

	if c.Comment != "" {
		builder.WriteString(fmt.Sprintf(" COMMENT '%s'", c.Comment))
	}

	if len(c.Codecs) > 0 {
		codecStrings := make([]string, len(c.Codecs))
		for i, codec := range c.Codecs {
			codecStrings[i] = codec.String()
		}
		builder.WriteString(fmt.Sprintf(" CODEC(%s)", strings.Join(codecStrings, ", ")))
	} else if c.Codec != CodecDefault {
		builder.WriteString(fmt.Sprintf(" CODEC(%s)", c.Codec))
	}
	return builder.String()
}

// NewColumn 创建列定义（带智能默认值）
func NewColumn(name string, t ColumnType) *Column {
	index := IndexNone
	codec := CodecZSTD
	switch t {
	case UInt8:
		index = IndexSet
	case UInt16, UInt32, Int32, IPv4, IPv6, ArrayUInt16:
		index = IndexMinmax
		codec = CodecZSTD
	case UInt64, Int64:
		codec = CodecT64
		index = IndexMinmax
	case DateTime, DateTime64ms, DateTime64us, DateTime64ns:
		codec = CodecDoubleDelta
	case Float64:
		codec = CodecGorilla
		index = IndexMinmax
	case LowCardinalityString, String:
		codec = CodecZSTD
	default:
	}
	return &Column{
		Name:  name,
		Type:  t,
		Codec: codec,
		Index: index,
	}
}
