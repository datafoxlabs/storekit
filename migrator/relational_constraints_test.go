package migrator

import (
	"strings"
	"testing"
)

func TestRelationalConstraints(t *testing.T) {
	table := &Table{
		Name: "children",
		Columns: []*Column{
			{Name: "workspace", Type: TypeVarchar, Size: 32, PrimaryKey: true, NotNull: true},
			{Name: "child_id", Type: TypeVarchar, Size: 32, PrimaryKey: true, NotNull: true},
			{Name: "parent_id", Type: TypeVarchar, Size: 32, NotNull: true},
			{Name: "size_bytes", Type: TypeBigInt, NotNull: true},
		},
		ForeignKeys: []*ForeignKey{{
			Name: "children_parent_fk",
			Columns: []string{"workspace", "parent_id"},
			ReferencedTable: "parents",
			ReferencedColumns: []string{"workspace", "parent_id"},
		}},
		Checks: []*Check{{Name: "children_positive_size", Expression: "size_bytes > 0"}},
	}
	for _, tc := range []struct {
		name string
		dialect interface{ CreateTableSQL(*Table) ([]string, error) }
		fk string
		check string
	}{
		{"postgres", &PostgresDialect{}, "CONSTRAINT \"children_parent_fk\" FOREIGN KEY (\"workspace\", \"parent_id\") REFERENCES \"parents\" (\"workspace\", \"parent_id\")", "CONSTRAINT \"children_positive_size\" CHECK (size_bytes > 0)"},
		{"mysql", &MySQLDialect{}, "CONSTRAINT `children_parent_fk` FOREIGN KEY (`workspace`, `parent_id`) REFERENCES `parents` (`workspace`, `parent_id`)", "CONSTRAINT `children_positive_size` CHECK (size_bytes > 0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := tc.dialect.CreateTableSQL(table)
			if err != nil { t.Fatal(err) }
			if !strings.Contains(statements[0], tc.fk) || !strings.Contains(statements[0], tc.check) {
				t.Fatalf("missing constraints in %s", statements[0])
			}
		})
	}
}

func TestInvalidForeignKeyFailsBeforeDDL(t *testing.T) {
	table := &Table{
		Name: "child",
		Columns: []*Column{{Name: "id", Type: TypeInt, PrimaryKey: true}},
		ForeignKeys: []*ForeignKey{{Name: "bad_fk", Columns: []string{"missing"}, ReferencedTable: "parent", ReferencedColumns: []string{"id"}}},
	}
	if _, err := (&MySQLDialect{}).CreateTableSQL(table); err == nil || !strings.Contains(err.Error(), "missing local column") {
		t.Fatalf("invalid FK was accepted: %v", err)
	}
}
