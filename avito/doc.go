// SPDX-License-Identifier: AGPL-3.0-or-later

// Package avito turns a captured Avito page into typed listings.
//
// It imports no net and no net/http, and it takes its clock as a parameter.
// Everything it needs arrives as bytes from a caller who did the fetching, so
// every test in this package is offline by construction — a property a test in
// this package enforces rather than assumes.
//
// Two rules govern the whole package and both exist because breaking them
// fails silently. Nothing here matches a regular expression against markup:
// elements are resolved with golang.org/x/net/html and their subtree text is
// taken, and JSON is decoded with encoding/json. And nothing here calls
// html.UnescapeString: the tokenizer has already decided what is escaped and
// what is not, and a second pass corrupts any title containing a literal
// ampersand without reporting anything.
package avito
