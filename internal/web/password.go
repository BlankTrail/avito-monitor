// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The panel password is stored as a hash and never as itself.
//
// The donor compares a plaintext column with subtle.ConstantTimeCompare, which
// is the right comparison over the wrong thing: settings.panel_password_hash
// lives in a file people copy to another machine, back up to a cloud drive and
// paste into bug reports, and a column holding the password makes every one of
// those a disclosure.
//
// crypto/pbkdf2 is standard library as of Go 1.24, so this costs no dependency.
// PBKDF2-HMAC-SHA256 rather than argon2 or bcrypt for exactly that reason — the
// budget is eight direct dependencies, and a ninth for a login form on the
// loopback interface would not be a good trade.

// pbkdf2Iterations is the work factor.
//
// 600 000 is what OWASP states for PBKDF2-HMAC-SHA256. It costs a few hundred
// milliseconds once per login, on a panel with one user, which is nothing; and
// it is what turns a leaked database column from a lookup into a campaign.
const pbkdf2Iterations = 600_000

const (
	saltBytes = 16
	keyBytes  = 32
)

// ErrEmptyPassword is returned when there is nothing to hash.
var ErrEmptyPassword = errors.New("web: пароль не может быть пустым")

// HashPassword returns the string that goes in the column.
//
// The format is pbkdf2$<iterations>$<salt hex>$<key hex>. The iteration count
// travels with the hash so it can be raised later without locking anybody out:
// an old hash verifies against its own number, and is rewritten at the next
// change of password rather than at the next login, because rewriting on login
// would need this function to reach the store.
func HashPassword(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", ErrEmptyPassword
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("web: генерация соли: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, plain, salt, pbkdf2Iterations, keyBytes)
	if err != nil {
		return "", fmt.Errorf("web: хеширование пароля: %w", err)
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s",
		pbkdf2Iterations, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// VerifyPassword reports whether plain is the password behind hash.
//
// False for anything it cannot read, and that is the whole of its error
// handling. A verifier that returned true — or that distinguished "malformed"
// from "wrong" to its caller — would turn a hand-edited column, a truncated
// restore or a hash written by a newer release into an open door.
func VerifyPassword(hash, plain string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, plain, salt, iter, len(want))
	if err != nil {
		return false
	}
	// Constant time: a comparison that returned early would leak the key one
	// byte at a time to anything that can measure a response, which on a
	// machine somebody shares is not hypothetical.
	return subtle.ConstantTimeCompare(got, want) == 1
}
