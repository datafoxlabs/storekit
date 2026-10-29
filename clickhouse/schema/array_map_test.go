package schema

import "testing"

func TestColumnIsArrayMap(t *testing.T) {
	cases := []struct {
		name string
		col  *Column
		want bool
	}{
		{
			name: "keys/values nested is an array map",
			col: NewColumn("labels", Nested).SetNested([]*Column{
				NewColumn("keys", LowCardinalityString),
				NewColumn("values", String),
			}),
			want: true,
		},
		{
			name: "nested with other subcolumns is not an array map",
			col: NewColumn("events", Nested).SetNested([]*Column{
				NewColumn("timestamp", DateTime64ns),
				NewColumn("name", LowCardinalityString),
				NewColumn("attributes", Map),
			}),
			want: false,
		},
		{
			name: "nested missing values subcolumn is not an array map",
			col: NewColumn("partial", Nested).SetNested([]*Column{
				NewColumn("keys", String),
			}),
			want: false,
		},
		{
			name: "Map type is not a parallel-array map",
			col:  NewColumn("attrs", Map).SetTypeArgs("String, String"),
			want: false,
		},
		{
			name: "scalar column is not an array map",
			col:  NewColumn("severity", LowCardinalityString),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.col.IsArrayMap(); got != tc.want {
				t.Errorf("IsArrayMap() = %v, want %v", got, tc.want)
			}
		})
	}
}
