package main

import (
	"os"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/ui"
)

// loadRegistry builds the layered filter registry rooted at the current
// directory (trusted project filters > user filters > built-in).
func loadRegistry() (*filter.Registry, error) {
	wd, err := os.Getwd()
	if err != nil {
		return filter.LoadBuiltin()
	}
	return filter.Load(wd)
}

func themeForStdout() ui.Theme {
	return themeForFile(os.Stdout)
}

func themeForFile(f *os.File) ui.Theme {
	return ui.ForFile(f)
}
