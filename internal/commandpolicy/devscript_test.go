package commandpolicy

import "testing"

func TestIsLongRunningDevScript(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		// Long-running: must bypass so output streams live.
		{"bun run dev:api", "bun", []string{"run", "dev:api"}, true},
		{"npm run dev", "npm", []string{"run", "dev"}, true},
		{"npm start shorthand", "npm", []string{"start"}, true},
		{"yarn dev shorthand", "yarn", []string{"dev"}, true},
		{"pnpm run watch", "pnpm", []string{"run", "watch"}, true},
		{"namespaced serve", "bun", []string{"run", "serve:ssr"}, true},
		{"suffix watch", "npm", []string{"run", "css:watch"}, true},
		{"storybook", "pnpm", []string{"run", "storybook"}, true},
		{"with workspace flag and value", "pnpm", []string{"--filter", "web", "run", "dev"}, true},
		{"absolute bun path", "/Users/x/.bun/bin/bun", []string{"run", "dev"}, true},

		// One-shot scripts: must stay filtered.
		{"bun run build", "bun", []string{"run", "build"}, false},
		{"npm run test", "npm", []string{"run", "test"}, false},
		{"npm run lint", "npm", []string{"run", "lint"}, false},
		{"yarn run typecheck", "yarn", []string{"run", "typecheck"}, false},
		{"not a package runner", "make", []string{"dev"}, false},
		{"bun eval is not a script", "bun", []string{"-e", "1"}, false},
		{"no script token", "npm", []string{"run"}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLongRunningDevScript(tt.cmd, tt.args); got != tt.want {
				t.Fatalf("IsLongRunningDevScript(%q, %v) = %v, want %v", tt.cmd, tt.args, got, tt.want)
			}
		})
	}
}

func TestClassifyBypassDevScript(t *testing.T) {
	bypass, reason := ClassifyBypass("bun", []string{"run", "dev:api"})
	if !bypass || reason != "long-running dev script" {
		t.Fatalf("dev script: got (%v, %q), want (true, %q)", bypass, reason, "long-running dev script")
	}
	if bypass, _ := ClassifyBypass("bun", []string{"run", "build"}); bypass {
		t.Errorf("bun run build must not bypass")
	}
}
