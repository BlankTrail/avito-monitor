// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"fmt"
	"strings"
)

// Lifted from WB_GO's internal/rules/predicate.go. The tree, its
// short-circuiting, its empty-group semantics and its unknown-field-is-false
// default are unchanged; only the Field catalogue is this target's, because a
// field name is the one part of a predicate that is about the domain rather
// than about predicates.
//
// This file is the condition half of a rule: a tree of typed predicates with
// and/or groups, evaluated over one change plus the reading it ended at.
//
// Spec section 6.2 weighed two alternatives and rejected both. Fixed forms per
// event class cannot express a compound condition — "price fell more than five
// percent while stock is under ten" is two facts about two different things.
// An expression language (CEL, expr) makes the user learn a syntax and gives
// an open-source product a surface where a typo becomes a rule that silently
// never fires. What is left is a small tree the interface can draw as blocks
// and this file can evaluate without parsing anything.

// Op is what a node does.
type Op string

// The operations a condition tree is built from. Two groupings and one leaf
// is the whole vocabulary — see Eval for why nothing else may hold.
const (
	// OpAnd and OpOr group other nodes. An empty group is true for And and
	// false for Or, which is what those words mean about nothing — and it
	// matters, because a rule saved with no conditions must fire on every
	// change of its kind rather than on none.
	OpAnd Op = "and"
	OpOr  Op = "or"
	// OpCompare is a leaf: one field against one number.
	OpCompare Op = "compare"
)

// Cmp is a comparison.
type Cmp string

// The six comparisons a leaf can make. Nothing else may hold — see compare,
// which refuses an unknown one rather than letting it match everything.
const (
	CmpLess        Cmp = "<"
	CmpLessOrEq    Cmp = "<="
	CmpGreater     Cmp = ">"
	CmpGreaterOrEq Cmp = ">="
	CmpEqual       Cmp = "=="
	CmpNotEqual    Cmp = "!="
)

// Field is what a leaf reads.
//
// A declared catalogue rather than a free string, for the same reason
// wb/fields.go is one: the interface draws the list from here, and a field
// nobody declares cannot be saved into a rule that then never matches.
type Field string

// The fields a condition can read: four about the move, four about the listing
// it is about. A condition can therefore say "the price fell by more than five
// percent on a listing we are sure about".
const (
	// The change itself.
	FieldPricePct   Field = "change.percent" // how far it moved, in percent
	FieldPriceDelta Field = "change.delta"   // how far it moved, in minor units
	FieldWas        Field = "change.was"
	FieldNow        Field = "change.now"

	// The change's context. Confidence is here and nowhere else in this
	// catalogue for a reason particular to this target: a listing-vanished at
	// 0.3 is a conclusion drawn from a window rather than a fact a page stated,
	// and somebody who wants only certainties must be able to write that.
	FieldConfidence Field = "change.confidence"
	FieldPosition   Field = "listing.position"
	FieldWatchID    Field = "listing.watch_id"
	FieldSellerID   Field = "listing.seller_id"
)

// Fields lists every field a condition can read, in the order the interface
// offers them.
func Fields() []Field {
	return []Field{
		FieldPricePct, FieldPriceDelta, FieldWas, FieldNow,
		FieldConfidence, FieldPosition, FieldWatchID, FieldSellerID,
	}
}

// FieldLabel is what a field is called on screen.
var fieldLabels = map[Field]string{
	FieldPricePct:   "Изменение, %",
	FieldPriceDelta: "Изменение, в копейках",
	FieldWas:        "Было",
	FieldNow:        "Стало",
	FieldConfidence: "Уверенность",
	FieldPosition:   "Позиция в выдаче",
	FieldWatchID:    "Наблюдение",
	FieldSellerID:   "Продавец",
}

// FieldLabel returns the on-screen name, falling back to the key itself so a
// field added here and forgotten in the labels renders as something visible
// rather than as an empty option nobody can pick.
func FieldLabel(f Field) string {
	if s, ok := fieldLabels[f]; ok {
		return s
	}
	return string(f)
}

// Node is one node of a condition tree.
//
// The zero Node is an empty And, which is true — a rule with no condition
// fires on every change of its kind, which is what "no condition" means.
type Node struct {
	Op    Op     `json:"op"`
	Nodes []Node `json:"nodes,omitempty"`

	Field Field   `json:"field,omitempty"`
	Cmp   Cmp     `json:"cmp,omitempty"`
	Value float64 `json:"value,omitempty"`
}

// Eval reports whether the condition holds for this event.
//
// A leaf whose field has no value is false, always, whichever comparison it
// carries. That is a decision and not an oversight: a price that appeared has
// no percentage, and "price fell by more than 5%" must not fire on it — but
// neither must "price rose by less than 5%", which a false-by-default leaf
// gets right and a "treat missing as zero" leaf gets exactly backwards.
func (n Node) Eval(ev Event) bool {
	switch n.Op {
	case OpOr:
		for _, child := range n.Nodes {
			if child.Eval(ev) {
				return true
			}
		}
		// An empty Or is false: "any of nothing" holds of nothing.
		return false
	case OpCompare:
		got, ok := ev.value(n.Field)
		if !ok {
			return false
		}
		return compare(got, n.Cmp, n.Value)
	case OpAnd, "":
		// The empty string is the zero Node, which is an empty And and
		// therefore true: a rule saved with no condition fires on every change
		// of its kind, which is what "no condition" means.
		for _, child := range n.Nodes {
			if !child.Eval(ev) {
				return false
			}
		}
		return true
	}
	// An operation this build cannot evaluate holds of nothing, for the same
	// reason an unknown comparison does: Validate refuses it before storage,
	// and one that arrives anyway — a hand-edited database, a rule written by
	// a newer release — must fire on nothing rather than on everything.
	return false
}

// Validate reports every problem in the tree at once, in the same spirit as
// job.Validate: a screen that reveals one problem per attempt makes the user
// submit five times to learn five things.
func (n Node) Validate() error {
	var bad []string
	n.validate("условие", &bad)
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("rules: %s", strings.Join(bad, "; "))
}

func (n Node) validate(path string, bad *[]string) {
	switch n.Op {
	case OpAnd, OpOr, "":
		for i, child := range n.Nodes {
			child.validate(fmt.Sprintf("%s[%d]", path, i), bad)
		}
	case OpCompare:
		known := false
		for _, f := range Fields() {
			if n.Field == f {
				known = true
				break
			}
		}
		if !known {
			*bad = append(*bad, fmt.Sprintf("%s: поле %q эта сборка не знает", path, n.Field))
		}
		switch n.Cmp {
		case CmpLess, CmpLessOrEq, CmpGreater, CmpGreaterOrEq, CmpEqual, CmpNotEqual:
		default:
			*bad = append(*bad, fmt.Sprintf("%s: сравнение %q эта сборка не знает", path, n.Cmp))
		}
		if len(n.Nodes) > 0 {
			// Silently ignored, these would be conditions the user wrote,
			// saw on the screen, and never had evaluated.
			*bad = append(*bad, fmt.Sprintf("%s: у сравнения не может быть вложенных условий", path))
		}
	default:
		*bad = append(*bad, fmt.Sprintf("%s: операция %q эта сборка не знает", path, n.Op))
	}
}

func compare(got float64, cmp Cmp, want float64) bool {
	switch cmp {
	case CmpLess:
		return got < want
	case CmpLessOrEq:
		return got <= want
	case CmpGreater:
		return got > want
	case CmpGreaterOrEq:
		return got >= want
	case CmpEqual:
		return got == want
	case CmpNotEqual:
		return got != want
	}
	// An unknown comparison is false rather than true. Validate refuses it
	// before it can be stored; if one arrives anyway — a database edited by
	// hand, a rule from a newer release — a rule that fires on everything is
	// worse than one that fires on nothing.
	return false
}
