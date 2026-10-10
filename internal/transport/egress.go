// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import "net/url"

// PublicEgress is an exit as it may be written down: the scheme, the login and
// the address, never the password.
//
// Found on 11 October 2026 in the film installation's passes table: every pass
// through a proxy list recorded its exit as
// «socks5://login:password@gw-ru.zengw.io:8080» — the password in the database,
// in every backup of it, and one query away from the health screen. The login
// stays: on a provider that sells sessions it is what tells sid-1 from sid-7,
// and that difference is the reason the column exists.
//
// The pacer keys on the raw string and is not affected: this is only what
// leaves the transport in a Result.
func PublicEgress(egress string) string {
	u, err := url.Parse(egress)
	if err != nil || u.User == nil {
		return egress
	}
	if _, has := u.User.Password(); !has {
		return egress
	}
	u.User = url.User(u.User.Username())
	return u.String()
}
