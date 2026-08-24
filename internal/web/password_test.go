// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"
)

// TestTheStoredFormIsNotThePassword.
//
// panel_password_hash lives in a file people copy to another machine, back up
// to a cloud drive and paste into bug reports. A column holding the password
// itself makes every one of those a disclosure.
func TestTheStoredFormIsNotThePassword(t *testing.T) {
	const plain = "правильный конь батарейка скрепка"
	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, plain) || strings.Contains(hash, "батарейка") {
		t.Fatalf("the password is in its own hash: %q", hash)
	}
	if !VerifyPassword(hash, plain) {
		t.Error("the right password was refused")
	}
}

// TestTheWrongPasswordIsRefused, including the near misses a comparison bug
// lets through.
func TestTheWrongPasswordIsRefused(t *testing.T) {
	hash, err := HashPassword("секрет")
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{
		"", "секре", "секрет ", " секрет", "секретъ", "СЕКРЕТ", "другое",
	} {
		if VerifyPassword(hash, wrong) {
			t.Errorf("%q was accepted", wrong)
		}
	}
}

// TestTwoHashesOfOnePasswordDiffer.
//
// A salt per password. Without one, two installations with the same password
// have the same column, and one leaked database tells an attacker which other
// ones to try it on.
func TestTwoHashesOfOnePasswordDiffer(t *testing.T) {
	a, err := HashPassword("секрет")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("секрет")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of one password are identical; there is no salt")
	}
	if !VerifyPassword(a, "секрет") || !VerifyPassword(b, "секрет") {
		t.Error("a salted hash does not verify")
	}
}

// TestAnUnreadableHashRefusesRatherThanAccepts.
//
// A hand-edited database, a truncated restore, a column written by a newer
// release. Every one of these must refuse: a verifier that returned true on a
// hash it could not parse would turn a corrupted column into an open door.
func TestAnUnreadableHashRefusesRatherThanAccepts(t *testing.T) {
	for _, junk := range []string{
		"", "секрет", "pbkdf2", "pbkdf2$", "pbkdf2$1$", "pbkdf2$notanumber$aa$bb",
		"pbkdf2$100000$нехекс$bb", "scrypt$1$aa$bb",
	} {
		if VerifyPassword(junk, "секрет") {
			t.Errorf("the hash %q accepted a password", junk)
		}
		if VerifyPassword(junk, "") {
			t.Errorf("the hash %q accepted an empty password", junk)
		}
	}
}

// TestNoPasswordIsNotAPassword. An empty hash is what a fresh install has, and
// it must not be satisfiable by anything — the caller decides whether to ask at
// all, and a verifier that said yes to "" would make that decision for it.
func TestNoPasswordIsNotAPassword(t *testing.T) {
	if VerifyPassword("", "") {
		t.Error("an empty hash accepted an empty password")
	}
}

// TestHashingRefusesAnEmptyPassword. «Требовать пароль» with nothing typed is a
// form mistake, and storing the hash of "" would make it look configured.
func TestHashingRefusesAnEmptyPassword(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("an empty password was hashed")
	}
	if _, err := HashPassword("   "); err == nil {
		t.Error("a password of spaces was hashed")
	}
}
