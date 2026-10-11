// SPDX-License-Identifier: AGPL-3.0-or-later

package ru

import "testing"

// TestTheTeensAreAllTheManyForm, which is the rule a naive last-digit switch
// gets wrong: 11, 12 and 14 take the same form as 5, not the forms 1, 2 and 4
// take.
func TestTheTeensAreAllTheManyForm(t *testing.T) {
	for _, n := range []int{11, 12, 13, 14, 111, 112, 114, 1011} {
		if got := Plural(n, "объявление", "объявления", "объявлений"); got != "объявлений" {
			t.Errorf("Plural(%d) = %q, want the many form", n, got)
		}
	}
}

// TestOtherwiseTheLastDigitDecides.
func TestOtherwiseTheLastDigitDecides(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "объявлений"},
		{1, "объявление"}, {2, "объявления"}, {3, "объявления"}, {4, "объявления"},
		{5, "объявлений"}, {9, "объявлений"}, {10, "объявлений"},
		{21, "объявление"}, {22, "объявления"}, {25, "объявлений"},
		{101, "объявление"}, {104, "объявления"},
	}
	for _, c := range cases {
		if got := Plural(c.n, "объявление", "объявления", "объявлений"); got != c.want {
			t.Errorf("Plural(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestANegativeCountUsesItsMagnitude. «-1 объявление» is what a difference
// reads as, and «-1 объявлений» reads as a machine talking.
func TestANegativeCountUsesItsMagnitude(t *testing.T) {
	if got := Plural(-1, "объявление", "объявления", "объявлений"); got != "объявление" {
		t.Errorf("Plural(-1) = %q", got)
	}
}

func TestThousandsGroupsByThree(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0", 999: "999", 1000: "1 000", 15500: "15 500", 1234567: "1 234 567", -2500: "-2 500",
	} {
		if got := Thousands(in); got != want {
			t.Errorf("Thousands(%d) = %q, want %q", in, got, want)
		}
	}
}
