// SPDX-License-Identifier: AGPL-3.0-or-later

// Package export writes what was collected, in the formats a person can open.
//
// Lifted in shape from WB_GO's internal/export: the Writer seam, the Value
// type, the catalogue-order rule and the option discipline are that package's
// and are unchanged. The catalogue itself is not — that one is about
// Wildberries — and three formats are carried rather than six, because the spec
// asks for CSV, XLSX and JSON and a SQL dump nobody asked for is a format to
// keep working for ever.
//
// One selection gives identical columns in every format. That is the claim this
// package exists to keep, and everything else here follows from it.
package export

// FieldType is what a value is, for whoever renders it.
//
// It is the type and not the column name that decides whether a number is
// divided by a hundred, which is why money has its own: a price rendered as a
// plain integer is a hundredfold error that still sorts correctly and still
// looks like a price.
type FieldType string

const (
	TypeText  FieldType = "text"
	TypeInt   FieldType = "int"
	TypeMoney FieldType = "money"
	TypeTime  FieldType = "time"
	TypeBool  FieldType = "bool"
)

// Field is one exportable column.
type Field struct {
	// Key identifies the field in a saved selection and names its export
	// column. Lowercase and free of spaces because it travels in URLs and in
	// file headers, and it must not change once released: a saved selection
	// naming a key nobody recognises silently loses a column.
	Key   string
	Name  string
	Group string
	Type  FieldType
}

// catalogue is the declaration itself.
//
// Order matters and nothing sorts it: it is the order of the checkboxes on the
// screen and of the columns in every file, so two people who ticked the same
// boxes in different orders get the same spreadsheet.
var catalogue = []Field{
	// What this row is about. First, because a file whose identity columns are
	// at the far right is a file somebody has to scroll to read.
	{Key: "item_id", Name: "Номер объявления", Group: "Объявление", Type: TypeInt},
	{Key: "thread_id", Name: "Сквозной номер", Group: "Объявление", Type: TypeInt},
	{Key: "title", Name: "Заголовок", Group: "Объявление", Type: TypeText},
	{Key: "url", Name: "Ссылка", Group: "Объявление", Type: TypeText},

	{Key: "price", Name: "Цена", Group: "Цена", Type: TypeMoney},
	{Key: "price_kind", Name: "Что с ценой", Group: "Цена", Type: TypeText},
	{Key: "discount_pct", Name: "Скидка, %", Group: "Цена", Type: TypeInt},

	// The name first, because it is the one a person reads. «Продавец: 1» was
	// what this group used to export on its own — a row number that means
	// nothing to anybody opening the file, and something different on every
	// install.
	{Key: "seller_name", Name: "Продавец", Group: "Продавец", Type: TypeText},
	{Key: "seller_key", Name: "Продавец, ключ", Group: "Продавец", Type: TypeText},
	{Key: "seller_id", Name: "Продавец, номер у нас", Group: "Продавец", Type: TypeInt},

	{Key: "watch", Name: "Наблюдение", Group: "Наблюдение", Type: TypeText},
	{Key: "position", Name: "Позиция в выдаче", Group: "Наблюдение", Type: TypeInt},
	{Key: "present", Name: "Сейчас в выдаче", Group: "Наблюдение", Type: TypeBool},

	{Key: "first_seen", Name: "Впервые увидели", Group: "Время", Type: TypeTime},
	{Key: "last_seen", Name: "Последний раз видели", Group: "Время", Type: TypeTime},
	{Key: "snapshot_at", Name: "Снимок сделан", Group: "Время", Type: TypeTime},

	{Key: "is_xl", Name: "Продвигается", Group: "Признаки", Type: TypeBool},
	{Key: "is_reserved", Name: "Забронировано", Group: "Признаки", Type: TypeBool},
}

// Fields is every exportable column, in catalogue order.
func Fields() []Field {
	out := make([]Field, len(catalogue))
	copy(out, catalogue)
	return out
}

// FieldByKey finds one field. The bool is false for a key the catalogue does
// not have, which is a saved selection from a newer release.
func FieldByKey(key string) (Field, bool) {
	for _, f := range catalogue {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// Groups is every heading, in the order the catalogue introduces them.
func Groups() []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range catalogue {
		if !seen[f.Group] {
			seen[f.Group] = true
			out = append(out, f.Group)
		}
	}
	return out
}

// Columns turns a selection into the column set, in catalogue order.
//
// The walk is over the catalogue rather than over the selection, which is what
// makes the order the catalogue's and not the order somebody clicked in. It also
// makes a key named twice one column rather than two.
//
// An empty selection is every column. «Выгрузить» with nothing ticked means
// everything, not nothing: a file with no columns is the one output nobody can
// have meant, and it is exactly what a link with no parameters produces.
//
// Unknown keys are reported and never silently dropped: a saved selection from
// another release must not lose a column without saying so, or the person finds
// out from a spreadsheet missing a column, months later, if at all.
func Columns(selection []string) (cols []Field, unknown []string) {
	if len(selection) == 0 {
		return Fields(), nil
	}
	want := make(map[string]bool, len(selection))
	for _, key := range selection {
		want[key] = true
	}
	for _, f := range catalogue {
		if want[f.Key] {
			cols = append(cols, f)
			delete(want, f.Key)
		}
	}
	for _, key := range selection {
		if want[key] {
			unknown = append(unknown, key)
			delete(want, key)
		}
	}
	return cols, unknown
}
