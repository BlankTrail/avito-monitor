// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ru is the Russian this program has to get right in more than one
// place.
//
// Small on purpose. It exists because the alternative was a second copy of the
// plural rule — one in the notifier and one in the panel — of which only one
// would have had a test, and «5 наблюдение» on a screen is the clearest
// possible sign that nobody read the output.
package ru

// Plural picks the form that agrees with n.
//
// The teens are all the many-form and the last digit decides otherwise, which
// is the standard rule and the one a naive switch on n%10 gets backwards for
// eleven through fourteen.
func Plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
