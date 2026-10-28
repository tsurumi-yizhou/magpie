package agent

// The agents magpie knows that aren't on this machine, with the commands
// their vendors give to install them (#727): on a new computer the Agents
// page said only "Install Claude Code, Codex, Gemini CLI, OpenCode…", and
// which command installs each was left to be looked up. magpie shows the
// commands to copy and run in a terminal; it doesn't run them itself, as an
// install needs what a new machine may not have yet (Node.js for npm,
// Homebrew), and a global npm prefix that may want sudo.
//
// The package's command is the Node.js package manager's global install
// alone, not Node.js's install and then npm's in one line (Sun1090:
// "curl xxx && npm xxxx" put Node.js's install in every agent's line): the
// package manager this machine has, the first of bun, yarn, pnpm and npm
// (packageManager), installs the package, and the page says to get Node.js
// from nodejs.org when there is none at all.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// InstallCmd is one way to install an agent's CLI.
type InstallCmd struct {
	// Via is what runs it: script (the vendor's installer, in a shell),
	// powershell (the vendor's installer, on Windows), brew, or the
	// Node.js package manager that installs the package: npm, pnpm, yarn
	// or bun
	Via     string `json:"via"`
	Command string `json:"command"`
	// Admin is the package manager's global folder, for an npm command run
	// where only an administrator can change it (Windows' Program Files);
	// "" otherwise
	Admin string `json:"admin,omitempty"`
}

// Install is an agent not on this machine, and how to install it.
type Install struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Icon     string       `json:"icon,omitempty"`
	Commands []InstallCmd `json:"commands"`
	// Missing: the agent's settings are here but its CLI isn't (CLIMissing),
	// so it is listed on the Agents page and offered here again too
	Missing bool `json:"missing,omitempty"`
}

// InstallSet is what the Agents page gets: the agents not on this machine
// with their install commands, and the Node.js package manager their
// commands run with here.
type InstallSet struct {
	Agents []Install `json:"agents"`
	// PackageManager is the Node.js package manager the agents' commands
	// run with here, the first of packageManagers that is (packageManager):
	// bun, yarn, pnpm or npm; "" when none is, and the commands are npm's
	// then, with the page saying to get Node.js first
	PackageManager string `json:"packageManager"`
}

// vendorInstall is the installer each vendor's own docs give first, where
// it isn't a package manager's — Claude Code's (code.claude.com/docs/en/setup),
// Codex's (github.com/openai/codex's README) and OpenCode's
// (opencode.ai/docs), the ones whose own updaters cliSpecs knows by where
// they put the binary — and the Homebrew cask and formula the READMEs name
// (Codex's, Gemini CLI's).
var vendorInstall = map[string]func(goos string) []InstallCmd{
	"claude": func(goos string) []InstallCmd {
		if goos == "windows" {
			return []InstallCmd{{Via: "powershell", Command: "irm https://claude.ai/install.ps1 | iex"}}
		}
		return []InstallCmd{{Via: "script", Command: "curl -fsSL https://claude.ai/install.sh | bash"}}
	},
	"opencode": func(goos string) []InstallCmd {
		if goos == "windows" {
			return nil
		}
		return []InstallCmd{{Via: "script", Command: "curl -fsSL https://opencode.ai/install | bash"}}
	},
	"codex": func(goos string) []InstallCmd {
		switch goos {
		case "windows":
			return []InstallCmd{{Via: "powershell", Command: `powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"`}}
		case "darwin":
			return []InstallCmd{{Via: "script", Command: "curl -fsSL https://chatgpt.com/codex/install.sh | sh"}, {Via: "brew", Command: "brew install --cask codex"}}
		}
		return []InstallCmd{{Via: "script", Command: "curl -fsSL https://chatgpt.com/codex/install.sh | sh"}}
	},
	// PenguinHarness's (penguin.ooo; its README): a tarball in ~/.penguin
	// that brings its own Node.js, which it needs at 24 or later
	"penguin": func(goos string) []InstallCmd {
		if goos == "windows" {
			return []InstallCmd{{Via: "powershell", Command: "irm https://penguin.ooo/install.ps1 | iex"}}
		}
		return []InstallCmd{{Via: "script", Command: "curl -fsSL https://penguin.ooo/install.sh | sh"}}
	},
	"gemini": func(goos string) []InstallCmd {
		if goos == "windows" {
			return nil
		}
		return []InstallCmd{{Via: "brew", Command: "brew install gemini-cli"}}
	},
}

// installCommands is how to install the agent with the given id on goos:
// the vendor's installer first, then the package, the one cliSpecs updates,
// installed with pm, the Node.js package manager here; nil for one magpie
// knows no command for.
func installCommands(id, goos, pm string) []InstallCmd {
	var out []InstallCmd
	if only := cliSpecs[id].only; len(only) > 0 && !slices.Contains(only, goos) {
		return nil
	}
	if f := vendorInstall[id]; f != nil {
		out = append(out, f(goos)...)
	}
	if spec, ok := cliSpecs[id]; ok && len(spec.npm) > 0 {
		if pm == "" {
			pm = "npm"
		}
		out = append(out, InstallCmd{Via: pm, Command: packageManagers[pm] + spec.npm[0]})
	}
	return out
}

// packageManagers are how each Node.js package manager installs a package
// globally, by the name the command runs under.
var packageManagers = map[string]string{
	"npm":  "npm install -g ",
	"pnpm": "pnpm add -g ",
	"yarn": "yarn global add ",
	"bun":  "bun add -g ",
}

// packageManagerOrder is the order magpie looks for a Node.js package
// manager in: bun, yarn, pnpm, then npm, which Node.js comes with.
var packageManagerOrder = []string{"bun", "yarn", "pnpm"}

// packageManager is the Node.js package manager the agents' commands run
// with here: the first of packageManagerOrder this machine has, else npm
// where nodeHere; "" when none is, and the page says to get Node.js first.
func packageManager() string {
	for _, name := range packageManagerOrder {
		p, ok := toolPath(name)
		if !ok {
			continue
		}
		// Yarn 2 and later have no global command (yarnClassic)
		if name == "yarn" && !yarnClassic(p) {
			continue
		}
		return name
	}
	if nodeHere() {
		return "npm"
	}
	return ""
}

var yarnVersions memo

// yarnClassic says whether yarn is Yarn 1, whose "yarn global add"
// installs a package globally: Yarn 2 and later have no global command, so
// the package managers after it in packageManagerOrder are looked for.
func yarnClassic(yarn string) bool {
	v := yarnVersions.get(yarn, "", time.Hour, 5*time.Minute, func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := proc.CommandContext(ctx, yarn, "--version").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	})
	return strings.HasPrefix(v, "1.")
}

// nodeHere says whether this machine has Node.js's npm: on PATH, in one
// of the folders a user's tools go in (nvm's, fnm's, mise's, volta's,
// Homebrew's, …) or, on Windows, in the PATH a terminal opened now has
// and in Node.js's installer's own folder.
func nodeHere() bool {
	var dirs []string
	if runtime.GOOS == "windows" {
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			dirs = proc.SystemDirs(filepath.Join(pf, "nodejs"))
		}
	}
	_, ok := toolPath("npm", dirs...)
	return ok
}

// toolPath is where this machine has the program name: on PATH, in one of
// the folders a user's tools go in (nvm's, fnm's, mise's, volta's,
// Homebrew's, bun's, …) or, on Windows, in the PATH a terminal opened now
// has; dirs are looked in after those, in order. Windows' names carry
// .cmd or .exe, as Node.js's npm.cmd and mise's shim npm.exe do.
func toolPath(name string, dirs ...string) (string, bool) {
	if p, err := proc.Look(name); err == nil {
		return p, true
	}
	all := proc.UserBinDirs()
	if runtime.GOOS == "windows" {
		all = append(all, proc.SystemDirs(proc.LoginPath()...)...)
	}
	return toolAt(append(all, dirs...), toolNames(name, runtime.GOOS))
}

// toolAt is where one of dirs has a file named one of names, false when
// none does.
func toolAt(dirs, names []string) (string, bool) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		for _, n := range names {
			if p := filepath.Join(d, n); isFile(p) {
				return p, true
			}
		}
	}
	return "", false
}

// toolNames are the names a program has on goos: its own, and on Windows
// .cmd or .exe, as Node.js's npm.cmd and mise's shim npm.exe do.
func toolNames(name, goos string) []string {
	if goos == "windows" {
		return []string{name + ".cmd", name + ".exe"}
	}
	return []string{name}
}

// Installs is every agent magpie knows a command for that isn't on this
// machine, in the Agents page's order, with the Node.js package manager
// their commands run with here.
func Installs() InstallSet {
	pm := packageManager()
	set := InstallSet{Agents: installsOf(All(), runtime.GOOS, pm), PackageManager: pm}
	if pm == "npm" && adminNPM {
		npmNeedsAdmin(set.Agents, npmGlobalPrefix())
	}
	return set
}

// npmNeedsAdmin marks the npm commands of out with prefix, npm's global
// folder, when it is one this user can't write in: Node.js's own under
// Program Files, where npm's prefix was left there (新v学员 on Discord),
// whose npm install -g fails with EPERM outside a terminal opened as
// administrator.
func npmNeedsAdmin(out []Install, prefix string) {
	if prefix == "" || canWrite(prefix) {
		return
	}
	for i := range out {
		for j := range out[i].Commands {
			if out[i].Commands[j].Via == "npm" {
				out[i].Commands[j].Admin = prefix
			}
		}
	}
}

var npmPrefixes memo

// npmGlobalPrefix is where npm install -g puts packages (npm prefix -g),
// "" when that isn't known.
func npmGlobalPrefix() string {
	npm, err := proc.Look("npm")
	if err != nil {
		pf := proc.SystemDirs(os.Getenv("ProgramFiles"))
		if len(pf) == 0 {
			return ""
		}
		npm = filepath.Join(pf[0], "nodejs", "npm.cmd")
		if !isFile(npm) {
			return ""
		}
	}
	return npmPrefixes.get(npm, "", time.Hour, 5*time.Minute, func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := proc.CommandContext(ctx, npm, "prefix", "-g")
		cmd.Stdin = nil
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		p := strings.TrimSpace(string(out))
		if !filepath.IsAbs(p) {
			return "", errors.New("npm prefix -g: " + p)
		}
		return p, nil
	})
}

func installsOf(all []*Agent, goos, pm string) []Install {
	out := []Install{}
	for _, a := range all {
		missing := a.CLIMissing()
		if a.WSL != "" || a.Detected() && !missing {
			continue
		}
		cmds := installCommands(a.ID, goos, pm)
		if len(cmds) == 0 {
			continue
		}
		out = append(out, Install{ID: a.ID, Name: a.Name, Icon: a.Icon, Commands: cmds, Missing: missing})
	}
	return out
}

// cliShared are the agents whose folder an app or an editor's extension
// of theirs keeps too, with a CLI of its own inside or none: ~/.codex (the
// Codex app), ~/.claude (Claude Desktop's Code, the editors' extensions),
// ~/.gemini (Antigravity, Gemini Code Assist), OpenCode's (its desktop
// app), ~/.cline (Cline's extensions), Goose's (Goose Desktop). Their
// folder without the CLI is no sign it was uninstalled while one of those
// is here (cliHeld).
var cliShared = map[string]bool{"codex": true, "claude": true, "gemini": true, "opencode": true, "cline": true, "goose": true}

// CLIMissing reports an agent that is here by its settings alone: its
// folder or config is, its command-line program isn't — not on PATH nor
// where users' tools go (proc.FindTool) nor where its own installer puts
// it — and nothing else that reads the folder is either (cliHeld): no
// desktop app of its (DeepSeek Harness Desktop, star on Discord), no
// editor extension, no IDE. That is an uninstall that left ~/.dsh (#843),
// ~/.claude (#2177) or ~/.hermes/config.yaml (#2173) behind. The Agents
// page shows it as gone, with none of its settings, and Install another
// agent offers it again where magpie knows how. Only for an agent that is
// its CLI: one magpie knows an install command for, or cliOnly; never one
// detected its own way (detect), but for Goose, whose is its folder or its
// program.
func (a *Agent) CLIMissing() bool {
	if a.WSL != "" || a.Plugin != "" || a.Bin == "" || a.detect != nil && a.ID != "goose" {
		return false
	}
	if len(installCommands(a.ID, runtime.GOOS, "")) == 0 && !cliOnly[a.ID] {
		return false
	}
	if !a.Detected() {
		return false
	}
	held, known := a.cliHeld()
	return known && !held
}
