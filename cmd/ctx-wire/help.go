package main

import (
	"fmt"
	"io"
	"os"

	"ctx-wire/internal/ui"
)

// helpDoc is the structured help for one command. printHelp renders it in a
// single coherent shape, so every `ctx-wire <cmd> --help` reads the same way:
// usage form(s), a one-line summary, then optional flags, examples, and notes.
type helpDoc struct {
	usage    []string    // one or more "ctx-wire <cmd> ..." forms
	summary  string      // one-line description of what the command does
	commands [][2]string // subcommands {name, description}, under "commands:"
	flags    [][2]string // {flag, description}, under "flags:"
	examples []string    // example invocations
	notes    []string    // extra prose lines (caveats, pointers)
}

// printHelp writes a full help screen for a command to w, themed for the file.
func printHelp(w io.Writer, doc helpDoc) {
	theme := themeFor(w)
	for i, form := range doc.usage {
		label := "usage:"
		if i > 0 {
			label = "      " // align continuation forms under the first
		}
		fmt.Fprintf(w, "%s %s\n", theme.Label.Render(label), theme.Command.Render(form))
	}
	if doc.summary != "" {
		fmt.Fprintf(w, "\n  %s\n", doc.summary)
	}
	printHelpRows(w, theme, "commands:", doc.commands)
	printHelpRows(w, theme, "flags:", doc.flags)
	if len(doc.examples) > 0 {
		fmt.Fprintf(w, "\n%s\n", theme.Section.Render("examples:"))
		for _, ex := range doc.examples {
			fmt.Fprintf(w, "  %s\n", theme.Command.Render(ex))
		}
	}
	for _, n := range doc.notes {
		fmt.Fprintf(w, "\n  %s\n", theme.Dim.Render(n))
	}
}

// printHelpRows renders a labeled, name-aligned section (commands or flags).
func printHelpRows(w io.Writer, theme ui.Theme, title string, rows [][2]string) {
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	fmt.Fprintf(w, "\n%s\n", theme.Section.Render(title))
	for _, r := range rows {
		fmt.Fprintf(w, "  %s  %s\n", theme.Command.Render(fmt.Sprintf("%-*s", width, r[0])), theme.Dim.Render(r[1]))
	}
}

// usageHint prints a concise usage line plus a pointer to the full help, for
// error paths (a bad flag/arg). The full help screen (printHelp) is reserved for
// --help, so a typo gets a short reminder rather than a wall of flags.
func usageHint(w io.Writer, primary, cmd string) {
	usageLine(w, primary)
	fmt.Fprintf(w, "%s\n", themeFor(w).Dim.Render("run `ctx-wire "+cmd+" --help` for all options"))
}

// themeFor returns the theme for w: the file's theme when w is a *os.File,
// otherwise a plain (unstyled) theme. Shared by printHelp and usageLine so help
// and error output style consistently.
func themeFor(w io.Writer) ui.Theme {
	if f, ok := w.(*os.File); ok {
		return themeForFile(f)
	}
	return ui.Plain()
}
