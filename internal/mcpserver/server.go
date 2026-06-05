// Package mcpserver exposes ctx-wire's filtering over the Model Context Protocol
// for agents that cannot use hooks (Visual Studio Copilot, VS Code Copilot).
//
// MCP cannot transparently intercept the host's terminal: it can only offer a
// callable tool. So ctx-wire publishes a run_command tool and steers the agent,
// via the tool description, to prefer it over the native shell. The tool takes
// a structured program plus args array, which avoids shell parsing entirely and
// works identically on Windows.
package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/runner"
	"ctx-wire/internal/scrub"
)

// runInput is the structured argument for the run_command tool. Using an args
// array (not a shell string) means there is no quoting or operator parsing, so
// behavior is identical across platforms.
type runInput struct {
	Command string   `json:"command" jsonschema:"the executable to run, e.g. \"dotnet\" or \"git\""`
	Args    []string `json:"args,omitempty" jsonschema:"arguments passed to the command, one element per token"`
}

// runOutput is the structured result of a run_command call.
type runOutput struct {
	Output   string `json:"output" jsonschema:"the filtered, secret-scrubbed command output"`
	ExitCode int    `json:"exit_code" jsonschema:"the command's exit code"`
}

const toolDescription = "Run a shell command and return its output with noise filtered out and " +
	"secrets redacted, using far fewer tokens than raw output. Prefer this over running commands " +
	"in the native terminal: pass the executable in 'command' and each argument as a separate " +
	"element of 'args' (do not include shell operators like |, &&, or redirects). On failure the " +
	"full output is saved to disk and referenced in the result."

// readInput is the structured argument for the read_file tool.
type readInput struct {
	Path     string `json:"path" jsonschema:"path to the file to read"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"cap the number of lines returned (0 = filter default)"`
}

// readOutput is the structured result of a read_file call.
type readOutput struct {
	Content   string `json:"content" jsonschema:"the filtered, secret-scrubbed file content"`
	Truncated bool   `json:"truncated" jsonschema:"true if the content was capped"`
}

const readDescription = "Read a file and return its contents with secrets redacted and noise filtered, " +
	"using fewer tokens than the raw file. Prefer this over the native file-read tool for large or " +
	"noisy files. Pass the file path in 'path'; optionally cap output with 'max_lines'. Works on " +
	"every platform (no shell or 'cat' needed)."

// New builds an MCP server with the run_command and read_file tools registered.
// The filter registry is loaded once and shared across calls for the life of
// the server.
func New(reg *filter.Registry, version string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "ctx-wire",
		Title:   "ctx-wire",
		Version: version,
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_command",
		Description: toolDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, runOutput, error) {
		output, code, err := runner.Capture(ctx, reg, in.Command, in.Args)
		if err != nil {
			// Launch failure: report as a tool error so the agent sees it,
			// without failing the protocol-level call.
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, runOutput{ExitCode: code}, nil
		}
		return nil, runOutput{Output: output, ExitCode: code}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_file",
		Description: readDescription,
	}, func(_ context.Context, _ *mcp.CallToolRequest, in readInput) (*mcp.CallToolResult, readOutput, error) {
		out, truncated, err := readFile(reg, in.Path, in.MaxLines)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, readOutput{}, nil
		}
		return nil, readOutput{Content: out, Truncated: truncated}, nil
	})

	return srv
}

// readFile reads path, scrubs secrets, applies the same filter `cat <path>`
// would (blank-line collapse, caps), and optionally caps to maxLines. It is pure
// Go (no shell, no `cat`), so it behaves identically on Windows.
func readFile(reg *filter.Registry, path string, maxLines int) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	content := scrub.Scrub(string(data))

	truncated := false
	if f := reg.Find("cat " + path); f != nil {
		res := filter.ApplyWithMeta(f, content)
		content = res.Output
		truncated = res.Truncated
	}

	if maxLines > 0 {
		lines := strings.Split(content, "\n")
		if len(lines) > maxLines {
			content = strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
			truncated = true
		}
	}
	return content, truncated, nil
}

// Serve runs the MCP server over stdio until the client disconnects or ctx is
// cancelled.
func Serve(ctx context.Context, reg *filter.Registry, version string) error {
	return New(reg, version).Run(ctx, &mcp.StdioTransport{})
}
