package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// The install commands are the vendors' own (#727): the installer their
// docs give first, then the package, the one the update uses, installed
// with npm where magpie knows no other Node.js package manager; per OS.
func TestInstallCommands(t *testing.T) {
	cmds := func(id, goos string) []string {
		var out []string
		for _, c := range installCommands(id, goos, "") {
			out = append(out, c.Via+": "+c.Command)
		}
		return out
	}
	for _, c := range []struct {
		id, goos string
		want     []string
	}{
		{"claude", "darwin", []string{"script: curl -fsSL https://claude.ai/install.sh | bash", "npm: npm install -g @anthropic-ai/claude-code"}},
		{"claude", "windows", []string{"powershell: irm https://claude.ai/install.ps1 | iex", "npm: npm install -g @anthropic-ai/claude-code"}},
		{"codex", "darwin", []string{"script: curl -fsSL https://chatgpt.com/codex/install.sh | sh", "brew: brew install --cask codex", "npm: npm install -g @openai/codex"}},
		{"codex", "linux", []string{"script: curl -fsSL https://chatgpt.com/codex/install.sh | sh", "npm: npm install -g @openai/codex"}},
		{"gemini", "linux", []string{"brew: brew install gemini-cli", "npm: npm install -g @google/gemini-cli"}},
		{"gemini", "windows", []string{"npm: npm install -g @google/gemini-cli"}},
		{"opencode", "darwin", []string{"script: curl -fsSL https://opencode.ai/install | bash", "npm: npm install -g opencode-ai"}},
		{"opencode", "windows", []string{"npm: npm install -g opencode-ai"}},
		{"deveco", "darwin", []string{"npm: npm install -g @deveco/deveco-code"}},
		{"deveco", "windows", []string{"npm: npm install -g @deveco/deveco-code"}},
		{"deveco", "linux", nil}, // no Linux build in its package
		{"pi", "linux", []string{"npm: npm install -g @earendil-works/pi-coding-agent"}},
		{"cursor", "darwin", nil}, // no command magpie knows
	} {
		if got := cmds(c.id, c.goos); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s on %s: %q, want %q", c.id, c.goos, got, c.want)
		}
	}
}

// The package is installed with the Node.js package manager here, each in
// its own global-install words; with none here it is npm's command, and the
// page says to get Node.js first.
func TestInstallCommandsWithPackageManager(t *testing.T) {
	for _, c := range []struct {
		pm   string
		via  string
		want string
	}{
		{"npm", "npm", "npm install -g @earendil-works/pi-coding-agent"},
		{"pnpm", "pnpm", "pnpm add -g @earendil-works/pi-coding-agent"},
		{"yarn", "yarn", "yarn global add @earendil-works/pi-coding-agent"},
		{"bun", "bun", "bun add -g @earendil-works/pi-coding-agent"},
		{"", "npm", "npm install -g @earendil-works/pi-coding-agent"},
	} {
		got := installCommands("pi", "linux", c.pm)
		if len(got) != 1 || got[0].Via != c.via || got[0].Command != c.want {
			t.Errorf("with %q: %+v, want %s: %s", c.pm, got, c.via, c.want)
		}
	}
}

// Only the agents not on this machine are offered, and not WSL's twins.
func TestInstallsOnlyMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	here := &Agent{ID: "codex", Name: "Codex", Path: filepath.Join(home, "codex.toml"), Dir: home}
	gone := &Agent{ID: "gemini", Name: "Gemini CLI", Icon: "gemini", Path: filepath.Join(home, "nope", "settings.json"), Bin: "gemini"}
	wsl := &Agent{ID: "claude@wsl:Ubuntu", Name: "Claude Code", WSL: "Ubuntu", Path: filepath.Join(home, "nope", "x")}
	none := &Agent{ID: "cursor", Name: "Cursor CLI", Path: filepath.Join(home, "nope", "y")}
	got := installsOf([]*Agent{here, gone, wsl, none}, "darwin", "")
	if len(got) != 1 || got[0].ID != "gemini" || got[0].Name != "Gemini CLI" || got[0].Icon != "gemini" || len(got[0].Commands) != 2 {
		t.Fatalf("installs: %+v", got)
	}
	if got := installsOf(nil, "linux", ""); got == nil {
		t.Error("none is [] for the page, not null")
	}
}

// The Node.js package manager is the first of bun, yarn, pnpm and npm that
// is here; Yarn 2 and later are passed over, having no global command, and
// with none here the page gets npm's commands and no package manager.
func TestPackageManager(t *testing.T) {
	for _, c := range []struct {
		progs map[string]string
		want  string
	}{
		{map[string]string{"bun": "#!/bin/sh\n", "npm": "#!/bin/sh\n"}, "bun"},
		{map[string]string{"npm": "#!/bin/sh\n", "pnpm": "#!/bin/sh\n"}, "pnpm"},
		{map[string]string{"npm": "#!/bin/sh\n", "yarn": "#!/bin/sh\necho 1.22.22\n"}, "yarn"},
		{map[string]string{"npm": "#!/bin/sh\n", "yarn": "#!/bin/sh\necho 3.6.0\n"}, "npm"},
		{map[string]string{"pnpm": "#!/bin/sh\n", "yarn": "#!/bin/sh\necho 4.1.0\n"}, "pnpm"},
		{map[string]string{"npm": "#!/bin/sh\n"}, "npm"},
		{nil, ""},
	} {
		dir := t.TempDir()
		t.Setenv("PATH", dir)
		for name, script := range c.progs {
			testenv.Program(t, filepath.Join(dir, name), script)
		}
		if got := packageManager(); got != c.want {
			t.Errorf("with %v: %q, want %q", c.progs, got, c.want)
		}
	}
}

// The page's npm commands are the package manager's here, and with none
// here they are npm's, which the page says to get Node.js for.
func TestInstallsPackageManager(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	testenv.Program(t, filepath.Join(bin, "bun"), "#!/bin/sh\n")
	set := Installs()
	if set.PackageManager != "bun" {
		t.Fatalf("package manager: %+v", set)
	}
	for _, x := range set.Agents {
		for _, c := range x.Commands {
			if c.Via == "bun" && !strings.HasPrefix(c.Command, "bun add -g ") {
				t.Errorf("%s: %q", x.ID, c.Command)
			}
		}
	}
}

// toolAt finds a program among dirs, and a folder named npm isn't npm.
func TestToolAt(t *testing.T) {
	empty, nvm, win, odd, miseExe := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	testenv.Program(t, filepath.Join(nvm, "npm"), "#!/bin/sh\n")
	if err := os.WriteFile(filepath.Join(miseExe, "npm.exe"), []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(win, "npm.cmd"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(odd, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		dirs []string
		goos string
		want bool
	}{
		{nil, "darwin", false},
		{[]string{empty, odd, ""}, "linux", false},
		{[]string{empty, nvm}, "darwin", true},
		{[]string{nvm}, "windows", false},
		{[]string{empty, win}, "windows", true},
		{[]string{win}, "linux", false},
		// mise's shims folder in its default (exe) mode
		{[]string{empty, miseExe}, "windows", true},
	} {
		if _, got := toolAt(c.dirs, toolNames("npm", c.goos)); got != c.want {
			t.Errorf("toolAt(%q, %s) = %v, want %v", c.dirs, c.goos, got, c.want)
		}
	}
}
