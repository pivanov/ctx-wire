package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// openBrowser opens url in the user's default browser. It is intentionally tiny
// and isolated so the only side effect (launching an external program) is easy
// to review. It makes no network calls itself: the OS handler fetches the URL.
// Only http/https URLs are accepted, so this cannot be coerced into launching an
// arbitrary local handler.
func openBrowser(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("refusing to open non-http(s) URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, *bsd
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
