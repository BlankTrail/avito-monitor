// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"fmt"
	"io"
)

// This file is the name-to-writer map, and it is here rather than beside a
// caller because there is more than one: the panel writes an export into an
// HTTP response, and anything that later writes one into a file to send
// somewhere would need the same switch. Two copies would be two lists of the
// formats the product supports, and the one that fell behind would be the one
// refusing what the other offered.

// NewWriter is the writer for a format name.
//
// An empty name is CSV, deliberately: it is what a link with no format
// parameter means, and refusing it would make the first thing a person tries
// the one that fails.
//
// Three formats and not the donor's six. The spec asks for CSV, XLSX and JSON;
// a SQLite file and two SQL dialects are three more outputs to keep working for
// ever, for a use case nobody in this product has stated.
func NewWriter(format string, w io.Writer, o Options) (Writer, error) {
	switch format {
	case "csv", "":
		return NewCSV(w, o)
	case "json":
		return NewJSON(w, o)
	case "jsonl":
		return NewJSONL(w, o)
	case "xlsx":
		return NewXLSX(w, o)
	}
	return nil, fmt.Errorf("export: формат %q эта сборка не знает", format)
}

// Extension is the file suffix a format is opened by.
//
// Not cosmetic: it is what decides whether the thing arriving in a downloads
// folder opens in a spreadsheet or in a text editor, and a name without it
// opens in neither.
func Extension(format string) (string, error) {
	switch format {
	case "csv", "":
		return "csv", nil
	case "json":
		return "json", nil
	case "jsonl":
		return "jsonl", nil
	case "xlsx":
		return "xlsx", nil
	}
	return "", fmt.Errorf("export: формат %q эта сборка не знает", format)
}

// ContentType is what the browser is told the bytes are.
//
// The XLSX one is the long OOXML name and not something shorter: a spreadsheet
// served as application/octet-stream lands in a downloads folder with no
// application attached to it, which on Windows means a double click does
// nothing.
func ContentType(format string) string {
	switch format {
	case "json", "jsonl":
		return "application/json; charset=utf-8"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}
