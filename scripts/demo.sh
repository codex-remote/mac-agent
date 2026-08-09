#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
demo_root="$(mktemp -d "${TMPDIR:-/tmp}/ai-coding-remote-demo.XXXXXX")"
project_dir="$demo_root/greeting-service"
agent_binary="$demo_root/mac-agent"

mkdir -p "$project_dir"

cat > "$project_dir/go.mod" <<'EOF'
module example.com/greeting-service

go 1.23.0
EOF

cat > "$project_dir/greeting.go" <<'EOF'
package greeting

func Greet(name string) string {
	return "Hello, " + name + "!"
}
EOF

cat > "$project_dir/greeting_test.go" <<'EOF'
package greeting

import "testing"

func TestGreet(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "Codex", want: "Hello, Codex!"},
		{name: "   ", want: "Hello, stranger!"},
	}
	for _, test := range tests {
		if got := Greet(test.name); got != test.want {
			t.Errorf("Greet(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}
EOF

git -C "$project_dir" init -q -b main
git -C "$project_dir" config user.name "AI Coding Remote Demo"
git -C "$project_dir" config user.email "demo@example.invalid"
git -C "$project_dir" add go.mod greeting.go greeting_test.go
git -C "$project_dir" commit -qm "test: create failing greeting requirement"

echo "Demo project: $project_dir"
echo "Confirming the initial test fails..."
if (cd "$project_dir" && go test ./...) >/dev/null 2>&1; then
	echo "Expected the initial test to fail, but it passed." >&2
	exit 1
fi

echo "Building mac-agent..."
go build -C "$repo_root" -o "$agent_binary" ./cmd/agent

prompt='Implement the greeting requirement in this repository. Greet must trim surrounding whitespace. For a blank or whitespace-only name it must return "Hello, stranger!". For a non-empty name it must greet the trimmed name. Keep the existing public API, make the smallest focused change, run go test ./..., and do not commit the result.'

echo "Running real Codex through mac-agent..."
"$agent_binary" run \
	--working-dir "$project_dir" \
	--timeout 10m \
	--prompt "$prompt"

echo
echo "Verifying the completed requirement..."
(cd "$project_dir" && go test ./...)
echo
git -C "$project_dir" diff --stat
git -C "$project_dir" diff -- greeting.go greeting_test.go
echo
echo "Demo succeeded. The project remains available at: $project_dir"
