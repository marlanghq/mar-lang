package main

import (
	"fmt"
	"os"
	"strings"
)

// runCompletion handles `mar completion <shell>` — emits a shell
// completion script to stdout. Three shells are supported (zsh, bash,
// fish) which together cover macOS / Linux defaults out of the box.
//
// Install (zsh):
//
//	mar completion zsh > "${fpath[1]}/_mar"
//	# or, into a personal fpath dir
//	mar completion zsh > ~/.zsh/completions/_mar
//	# then in ~/.zshrc:
//	#   fpath=(~/.zsh/completions $fpath)
//	#   autoload -U compinit && compinit
//
// Install (bash):
//
//	echo 'source <(mar completion bash)' >> ~/.bashrc
//
// Install (fish):
//
//	mar completion fish > ~/.config/fish/completions/mar.fish
func runCompletion(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: mar completion <zsh|bash|fish>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Generates a shell completion script for tab-completing")
		fmt.Fprintln(os.Stderr, "`mar` subcommands and flags. Pipe the output to your shell's")
		fmt.Fprintln(os.Stderr, "completion directory; see the comment block in")
		fmt.Fprintln(os.Stderr, "cmd/mar/completion.go for per-shell install paths.")
		return 2
	}
	shell := strings.ToLower(strings.TrimSpace(args[0]))
	switch shell {
	case "zsh":
		fmt.Print(zshCompletion())
	case "bash":
		fmt.Print(bashCompletion())
	case "fish":
		fmt.Print(fishCompletion())
	default:
		fmt.Fprintf(os.Stderr, "mar completion: unsupported shell %q\n", shell)
		fmt.Fprintln(os.Stderr, "Supported shells: zsh, bash, fish")
		return 2
	}
	return 0
}

// buildTargets — kept in lockstep with the runBuild flag handler in
// main.go. If you add a new target there (e.g. freebsd-amd64), append
// it here so the shell offers it for `mar build --target <tab>`.
const buildTargets = "darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 ios"

// zshCompletion: zsh's _describe gives us "command: short description"
// hints in the menu — the most informative experience of the three
// shells. Conditional logic uses zsh's `(( CURRENT == n ))` to figure
// out "what argument am I completing right now" — n=2 means the
// subcommand slot, n=3 the first arg to the subcommand, etc.
func zshCompletion() string {
	return `#compdef mar

_mar() {
  local -a commands
  commands=(
    'dev:Run with hot reload'
    'build:Compile to dist/ or a native/iOS bundle'
    'init:Scaffold a new project'
    'check:Parse + type-check (no run)'
    'format:Reformat .mar files in place'
    'config:Print mar.json'
    'migrate:Show pending or applied schema migrations'
    'fly:Scaffold or deploy a Fly.io backend'
    'repl:Interactive REPL'
    'lsp:Language server over stdio'
    'completion:Generate shell completion scripts'
    'version:Print version and build info'
    'help:Show top-level help'
  )

  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi

  case "${words[2]}" in
    dev|check|config)
      # First positional is a path or .mar file.
      if (( CURRENT == 3 )); then
        _files -g '*.mar' || _path_files -/
      fi
      ;;
    build)
      # build accepts both flags and a positional path. Show flag
      # menu when the user is typing a '-', otherwise paths.
      if [[ "${PREFIX}" == -* ]]; then
        _describe 'build flag' \
          '--target:Build target' \
          '-t:Build target (short)' \
          '--out:Output directory' \
          '-o:Output directory (short)' \
          '--base-url:iOS only — backend URL baked into the bundle'
        return
      fi
      # After --target / -t, suggest the known target list.
      if [[ "${words[CURRENT-1]}" == "--target" || "${words[CURRENT-1]}" == "-t" ]]; then
        local -a targets
        targets=(${(s: :)$(echo '` + buildTargets + `')})
        _describe 'target' targets
        return
      fi
      # After --out / -o, suggest directories.
      if [[ "${words[CURRENT-1]}" == "--out" || "${words[CURRENT-1]}" == "-o" ]]; then
        _path_files -/
        return
      fi
      # Fallback: paths (the project to build).
      _files -g '*.mar' || _path_files -/
      ;;
    format)
      if [[ "${PREFIX}" == -* ]]; then
        _describe 'format flag' '--check:Exit 1 if any file needs reformatting'
        return
      fi
      _files -g '*.mar'
      ;;
    init)
      # init takes a name, not a path — leave completion empty so
      # the shell doesn't offer misleading file matches.
      ;;
    migrate)
      # _describe expects an array variable, not loose args. Without
      # this indirection, zsh leaks internal completion helpers
      # (_a_11, _tmpd, etc.) into the candidate list.
      if (( CURRENT == 3 )); then
        local -a migrate_subs
        migrate_subs=(
          'plan:Show what next boot will apply or block'
          'status:Show applied migrations history'
        )
        _describe 'subcommand' migrate_subs
        return
      fi
      if (( CURRENT == 4 )); then
        _files -g '*.mar' || _path_files -/
      fi
      ;;
    fly)
      if (( CURRENT == 3 )); then
        local -a fly_subs
        fly_subs=(
          'init:Scaffold deploy/fly/{Dockerfile,fly.toml}'
          'provision:Create app + volume + secrets on Fly.io'
          'deploy:Build linux-amd64 binary and run fly deploy'
          'logs:Tail logs from the running machine(s)'
          'status:Show app + machine status'
          'destroy:Destroy the fly app and its volume'
        )
        _describe 'subcommand' fly_subs
        return
      fi
      if (( CURRENT == 4 )); then
        _path_files -/
      fi
      ;;
    completion)
      if (( CURRENT == 3 )); then
        _describe 'shell' \
          'zsh:zsh shell' \
          'bash:bash shell' \
          'fish:fish shell'
      fi
      ;;
  esac
}

compdef _mar mar
`
}

// bashCompletion: less rich than zsh (no descriptions in the menu)
// but available everywhere. Same shape: dispatch on $COMP_WORDS[1]
// to pick the per-subcommand completion logic.
func bashCompletion() string {
	return `_mar_completion() {
  local cur prev
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"

  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "dev build init check format config migrate fly repl lsp completion version help" -- "${cur}") )
    return 0
  fi

  case "${COMP_WORDS[1]}" in
    dev|check|config)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        # Match .mar files OR directories.
        COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") $(compgen -d -- "${cur}") )
      fi
      ;;
    build)
      # --target / -t: known build targets.
      if [[ "${prev}" == "--target" || "${prev}" == "-t" ]]; then
        COMPREPLY=( $(compgen -W "` + buildTargets + `" -- "${cur}") )
        return 0
      fi
      # --out / -o: directories.
      if [[ "${prev}" == "--out" || "${prev}" == "-o" ]]; then
        COMPREPLY=( $(compgen -d -- "${cur}") )
        return 0
      fi
      # If user is typing a flag, list available flags.
      if [[ "${cur}" == -* ]]; then
        COMPREPLY=( $(compgen -W "--target -t --out -o --base-url" -- "${cur}") )
        return 0
      fi
      # Otherwise a path / .mar project.
      COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") $(compgen -d -- "${cur}") )
      ;;
    format)
      if [[ "${cur}" == -* ]]; then
        COMPREPLY=( $(compgen -W "--check" -- "${cur}") )
        return 0
      fi
      COMPREPLY=( $(compgen -W "--check" -- "${cur}") $(compgen -f -X '!*.mar' -- "${cur}") )
      ;;
    completion)
      COMPREPLY=( $(compgen -W "zsh bash fish" -- "${cur}") )
      ;;
    migrate)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "plan status" -- "${cur}") )
      fi
      ;;
    fly)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "init provision deploy logs status destroy" -- "${cur}") )
      fi
      ;;
  esac
}

complete -F _mar_completion mar
`
}

// fishCompletion: fish's completion DSL is declarative — one line
// per "when this condition holds, suggest these options". More verbose
// than zsh/bash but easy to read top-to-bottom.
func fishCompletion() string {
	return `complete -c mar -f

# Top-level subcommands
complete -c mar -n '__fish_use_subcommand' -a dev        -d 'Run with hot reload'
complete -c mar -n '__fish_use_subcommand' -a build      -d 'Compile to dist/ or a native/iOS bundle'
complete -c mar -n '__fish_use_subcommand' -a init       -d 'Scaffold a new project'
complete -c mar -n '__fish_use_subcommand' -a check      -d 'Parse + type-check (no run)'
complete -c mar -n '__fish_use_subcommand' -a format     -d 'Reformat .mar files in place'
complete -c mar -n '__fish_use_subcommand' -a config     -d 'Print mar.json'
complete -c mar -n '__fish_use_subcommand' -a migrate    -d 'Show pending or applied schema migrations'
complete -c mar -n '__fish_use_subcommand' -a fly        -d 'Scaffold or deploy a Fly.io backend'
complete -c mar -n '__fish_use_subcommand' -a repl       -d 'Interactive REPL'
complete -c mar -n '__fish_use_subcommand' -a lsp        -d 'Language server over stdio'
complete -c mar -n '__fish_use_subcommand' -a completion -d 'Generate shell completion scripts'
complete -c mar -n '__fish_use_subcommand' -a version    -d 'Print version and build info'
complete -c mar -n '__fish_use_subcommand' -a help       -d 'Show top-level help'

# Path-taking subcommands → .mar files + directories
complete -c mar -n '__fish_seen_subcommand_from dev check config' -a '(__fish_complete_suffix .mar)'

# build: flags first, then targets, then directories
complete -c mar -n '__fish_seen_subcommand_from build' -l target   -d 'Build target' -x -a '` + buildTargets + `'
complete -c mar -n '__fish_seen_subcommand_from build' -s t                            -x -a '` + buildTargets + `'
complete -c mar -n '__fish_seen_subcommand_from build' -l out      -d 'Output directory' -x -a '(__fish_complete_directories (commandline -ct))'
complete -c mar -n '__fish_seen_subcommand_from build' -s o                               -x -a '(__fish_complete_directories (commandline -ct))'
complete -c mar -n '__fish_seen_subcommand_from build' -l base-url -d 'iOS-only backend URL'
complete -c mar -n '__fish_seen_subcommand_from build' -a '(__fish_complete_suffix .mar)'

# format: --check flag and .mar files
complete -c mar -n '__fish_seen_subcommand_from format' -l check -d 'Exit 1 if any file needs reformatting'
complete -c mar -n '__fish_seen_subcommand_from format' -a '(__fish_complete_suffix .mar)'

# completion: shell name
complete -c mar -n '__fish_seen_subcommand_from completion' -a 'zsh bash fish'

# migrate / fly: subcommands
complete -c mar -n '__fish_seen_subcommand_from migrate' -a 'plan status'
complete -c mar -n '__fish_seen_subcommand_from fly' -a 'init provision deploy logs status destroy'
`
}
