//go:build windows

package agent

// detect is a no-op on Windows: the process-tree walk relies on a POSIX `ps`,
// and the .cmd shim does not do agent detection either. Attribution on Windows
// comes only from an explicit CTX_WIRE_AGENT in the environment.
func detect() string { return "" }
