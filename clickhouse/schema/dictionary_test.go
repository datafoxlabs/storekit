package schema_test

import (
	"strings"
	"testing"

	"github.com/datafoxlabs/storekit/clickhouse/schema"
)

func retentionRulesDictionary() *schema.Dictionary {
	return &schema.Dictionary{
		Database:    "apm",
		Name:        "retention_rules_dict",
		IfNotExists: true,
		Attributes: []*schema.DictionaryAttr{
			{Name: "env", Type: schema.String},
			{Name: "service_name", Type: schema.String},
			{Name: "keep_errors", Type: schema.UInt8},
			{Name: "slow_threshold_ns", Type: schema.UInt64},
			{Name: "sample_percent", Type: schema.UInt8},
		},
		PrimaryKeys: []string{"env", "service_name"},
		Source: schema.DictionarySource{
			Database: "apm",
			Table:    "retention_rules",
			Query:    "SELECT env, service_name FROM apm.`retention_rules` FINAL WHERE enabled = 1 AND is_del = 0",
		},
		Layout:   schema.LayoutComplexKeyHashed,
		Lifetime: schema.DictionaryLifetime{Min: 60, Max: 120},
	}
}

func TestRenderCreateDictionary(t *testing.T) {
	sql := retentionRulesDictionary().RenderCreateSQL()

	const want = "CREATE DICTIONARY IF NOT EXISTS apm.`retention_rules_dict`\n" +
		"(\n" +
		"    env String,\n" +
		"    service_name String,\n" +
		"    keep_errors UInt8,\n" +
		"    slow_threshold_ns UInt64,\n" +
		"    sample_percent UInt8\n" +
		")\n" +
		"PRIMARY KEY env, service_name\n" +
		"SOURCE(CLICKHOUSE(DB 'apm' TABLE 'retention_rules' " +
		"QUERY $q$SELECT env, service_name FROM apm.`retention_rules` FINAL WHERE enabled = 1 AND is_del = 0$q$))\n" +
		"LAYOUT(COMPLEX_KEY_HASHED())\n" +
		"LIFETIME(MIN 60 MAX 120)"

	if sql != want {
		t.Errorf("RenderCreateSQL()\n got: %s\nwant: %s", sql, want)
	}
}

// TestDictionarySourceQueryIsDollarQuoted covers the reason the source query
// cannot be a plain string literal: it is SQL, so it carries the single quotes
// that would otherwise close the literal early and leave ClickHouse parsing the
// rest of the query as syntax.
func TestDictionarySourceQueryIsDollarQuoted(t *testing.T) {
	dict := retentionRulesDictionary()
	dict.Source.Query = "SELECT env FROM apm.rules WHERE env = 'prod'"

	sql := dict.RenderCreateSQL()
	if !strings.Contains(sql, "QUERY $q$SELECT env FROM apm.rules WHERE env = 'prod'$q$") {
		t.Errorf("source query is not dollar-quoted: %s", sql)
	}
}

// TestDictionaryReferenceIsNotQuotedAsAnIdentifier guards a failure that only
// appears at query time. dictGet takes the dictionary name as a string literal,
// so backticks would become part of the name and every lookup would fail --
// while the CREATE DICTIONARY that does want them still succeeds.
func TestDictionaryReferenceIsNotQuotedAsAnIdentifier(t *testing.T) {
	dict := retentionRulesDictionary()

	if got, want := dict.Reference(), "apm.retention_rules_dict"; got != want {
		t.Errorf("Reference() = %q, want %q", got, want)
	}
	if strings.Contains(dict.Reference(), "`") {
		t.Errorf("Reference() must not be identifier-quoted: %q", dict.Reference())
	}
	// The identifier position does want them, so the two differ on purpose.
	if !strings.Contains(dict.RenderCreateSQL(), "apm.`retention_rules_dict`") {
		t.Errorf("CREATE should quote the name as an identifier: %s", dict.RenderCreateSQL())
	}
}

// TestDictionaryValidateRejectsACompositeKeyWithoutAComplexLayout covers the
// one misconfiguration ClickHouse accepts and then gets wrong: a HASHED layout
// under a multi-column key reads only the first column, so lookups return the
// wrong row rather than failing.
func TestDictionaryValidateRejectsACompositeKeyWithoutAComplexLayout(t *testing.T) {
	dict := retentionRulesDictionary()
	dict.Layout = schema.LayoutHashed

	err := dict.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error: HASHED cannot hold a two-column key")
	}
	if !strings.Contains(err.Error(), "COMPLEX_KEY") {
		t.Errorf("error should name the fix: %v", err)
	}

	// A single-column key under HASHED is the correct pairing.
	dict.PrimaryKeys = []string{"env"}
	if err := dict.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for a single-column HASHED key", err)
	}
}

func TestDictionaryValidate(t *testing.T) {
	if err := retentionRulesDictionary().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	tests := map[string]func(*schema.Dictionary){
		"primary key is not an attribute": func(d *schema.Dictionary) {
			d.PrimaryKeys = []string{"env", "missing_column"}
		},
		"no attributes":  func(d *schema.Dictionary) { d.Attributes = nil },
		"no primary key": func(d *schema.Dictionary) { d.PrimaryKeys = nil },
		"no source": func(d *schema.Dictionary) {
			d.Source = schema.DictionarySource{Database: "apm"}
		},
		"lifetime max below min": func(d *schema.Dictionary) {
			d.Lifetime = schema.DictionaryLifetime{Min: 120, Max: 60}
		},
		"no name": func(d *schema.Dictionary) { d.Name = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			dict := retentionRulesDictionary()
			mutate(dict)
			if err := dict.Validate(); err == nil {
				t.Errorf("Validate() = nil, want an error")
			}
		})
	}
}

func TestRenderDropDictionary(t *testing.T) {
	sql := retentionRulesDictionary().RenderDropSQL()
	assertContains(t, sql, "DROP DICTIONARY IF EXISTS apm.`retention_rules_dict`")
}

func TestDictionaryAttrDefaultAndTypeArgs(t *testing.T) {
	attr := &schema.DictionaryAttr{Name: "sample_percent", Type: schema.UInt8, Default: "5"}
	if got, want := attr.String(), "sample_percent UInt8 DEFAULT 5"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	sized := &schema.DictionaryAttr{Name: "trace_id", Type: schema.FixedString, TypeArgs: "32"}
	if got, want := sized.String(), "trace_id FixedString(32)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestDictionarySourceNamedCollection covers the shape that keeps credentials
// out of the dictionary definition. The source authenticates in its own context
// rather than inheriting the creating session -- a local source skips the
// network but still authenticates -- so it needs credentials from somewhere,
// and a named collection is where they can be held and encrypted at rest.
func TestDictionarySourceNamedCollection(t *testing.T) {
	dict := retentionRulesDictionary()
	dict.Source = schema.DictionarySource{
		NamedCollection: "rules_source",
		Query:           "SELECT env FROM apm.rules",
	}

	sql := dict.RenderCreateSQL()
	if !strings.Contains(sql, "SOURCE(CLICKHOUSE(NAME rules_source QUERY $q$SELECT env FROM apm.rules$q$))") {
		t.Errorf("named collection not rendered: %s", sql)
	}
	if err := dict.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
