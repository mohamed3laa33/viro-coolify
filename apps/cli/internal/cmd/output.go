package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// printJSON writes v as indented JSON to w.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table renders rows as an aligned, tab-separated table with a header.
func table(w io.Writer, header []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

// emit prints v either as JSON (when --json) or via the supplied table renderer.
// table is only invoked in non-JSON mode, so callers can build rows lazily.
func (a *App) emit(w io.Writer, v any, renderTable func()) error {
	if a.jsonOut {
		return printJSON(w, v)
	}
	renderTable()
	return nil
}

// dash returns "-" for empty strings so tables stay aligned.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
