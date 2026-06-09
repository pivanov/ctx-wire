package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ctx-wire/internal/install"
	"ctx-wire/internal/shim"
	"ctx-wire/internal/ui"
)

// cmdShims manages the optional default PATH shim set. Hook/plugin-capable agents
// no longer get shims from `init` (their hook/plugin already covers model-visible
// commands, and shims early on PATH slow shell startup), so this is how a user
// adds them deliberately or removes them. It only ever touches ctx-wire's own
// managed shim files; it never alters the binary, hooks, config, or data.
func cmdShims(args []string) int {
	if isHelpArg(args) || len(args) == 0 {
		printHelp(os.Stdout, helpDoc{
			usage: []string{
				"ctx-wire shims status",
				"ctx-wire shims install",
				"ctx-wire shims uninstall",
			},
			summary: "Manage the optional default PATH shims (inspect, install, or remove them).",
			notes: []string{
				"`init` installs shims only for steering-only agents. Use `shims install` to opt in on a hook/plugin-capable agent, or `shims uninstall` to remove them if they slow shell startup. `uninstall` removes only ctx-wire-managed shim files; it preserves the binary, hooks, config, filters, and gain/tee data.",
			},
			examples: []string{
				"ctx-wire shims status",
				"ctx-wire shims uninstall   # stop shims slowing your shell prompt",
			},
		})
		if len(args) == 0 {
			return 2
		}
		return 0
	}

	dest, err := install.SelfInstallPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire shims: %v\n", err)
		return 1
	}
	installDir := filepath.Dir(dest)
	theme := themeForStdout()

	switch args[0] {
	case "install":
		// Reuse init's installer so opt-in shims are identical to the default set.
		return installShims(dest, theme)
	case "uninstall":
		return shimsUninstall(installDir, theme)
	case "status":
		return shimsStatus(installDir, theme)
	default:
		fmt.Fprintf(os.Stderr, "ctx-wire shims: unknown subcommand %q (use status, install, or uninstall)\n", args[0])
		return 2
	}
}

// shimsUninstall removes only ctx-wire-managed shim files, across EVERY managed
// shim dir on PATH (a stale earlier dir can be the one first on PATH), leaving the
// binary, hooks, config, and data intact. This is the targeted removal `uninstall`
// is too broad for.
func shimsUninstall(installDir string, theme ui.Theme) int {
	dirs := shim.ManagedDirsWith(installDir)
	removed := 0
	for _, d := range dirs {
		report, err := shim.UninstallDefault(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire shims: %v\n", err)
			return 1
		}
		shim.ClearKeep(d) // removing shims clears the explicit-intent marker
		if len(report.Removed) > 0 {
			fmt.Printf("%s %d managed shim(s) from %s\n", theme.OK.Render("Removed"), len(report.Removed), theme.Path.Render(report.Dir))
			removed += len(report.Removed)
		}
		if len(report.Skipped) > 0 {
			fmt.Printf("%s left non-ctx-wire files untouched in %s: %s\n", theme.Dim.Render("note:"), theme.Path.Render(d), strings.Join(report.Skipped, ", "))
		}
	}
	clearNudgeMarker(installDir) // shim set changed: a later reinstall may advise again
	if removed == 0 {
		fmt.Printf("%s no managed shims to remove\n", theme.OK.Render("OK"))
	}
	return 0
}

// shimsStatus reports which managed shims are installed and which actually resolve
// first on PATH (the ground truth for whether they are on the hot path), across
// every managed shim dir.
func shimsStatus(installDir string, theme ui.Theme) int {
	dirs := shim.ManagedDirsWith(installDir)
	installed, active, uses, total := shim.AggregateStatus(dirs)
	fmt.Printf("%s %d/%d managed shims installed across %d dir(s)\n",
		theme.OK.Render("Shims"), installed, total, len(dirs))
	for _, d := range dirs {
		st := shim.Inspect(d, shim.DefaultCommands)
		line := fmt.Sprintf("  %s: %d installed, %d first on PATH", theme.Path.Render(d), len(st.Installed), len(st.Active))
		// Surface stale deprecated-only shims (e.g. a leftover `cat`) that the
		// default-command count would otherwise hide as "0 installed".
		if dep := shim.Inspect(d, shim.DeprecatedShims); len(dep.Installed) > 0 {
			line += fmt.Sprintf("  (+%d stale deprecated: %s)", len(dep.Installed), strings.Join(dep.Installed, ", "))
		}
		fmt.Println(line)
	}
	if active > 0 {
		fmt.Printf("  %s %d managed command(s) resolve to a shim (on the shell's hot path)\n", theme.Warn.Render("active:"), active)
	} else if installed > 0 {
		fmt.Printf("  %s\n", theme.Dim.Render("none resolve first on PATH; the real commands shadow them, so they cost nothing at the prompt"))
	}
	if uses > 0 {
		fmt.Printf("  %d shim capture(s) recorded\n", uses)
	}
	return 0
}
