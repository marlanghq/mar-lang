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

// flySubcommandList — kept in lockstep with runFly's switch in fly.go.
// `db` is the alias for `database`; we list both so tab completes
// either spelling. Order matches the typical lifecycle (preview →
// deploy → ops); the -V flag in zsh / fish's natural declaration
// order preserve it in the menu.
const flySubcommandList = "preview deploy destroy logs status admin database db secrets"

// flySecretsSubs / flyDatabaseSubs / flyAdminSubs — third-level
// subcommands under fly. Same lockstep contract: edit the
// corresponding case-statement in fly_secrets.go / fly_database.go /
// runFlyAdmin and remember to add the new sub here so the shell
// finishes the trail.
const flySecretsSubs = "set list ls unset rm"
const flyDatabaseSubs = "backup backups"
const flyAdminSubs = "list ls"

// adminSubs — `mar admin <add|remove|list>`. The aliases `rm` and
// `ls` are accepted by the CLI but skipped here to keep the menu
// short; users who know the alias don't need the prompt.
const adminSubs = "add remove list"

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
    'admin:Manage local admin emails in mar.json'
    'fly:Deploy + operate a Fly.io app'
    'cloudflare-pages:Deploy a frontend bundle to Cloudflare Pages'
    'repl:Interactive REPL'
    'lsp:Language server over stdio'
    'completion:Generate shell completion scripts'
    'version:Print version and build info'
    'help:Show top-level help'
  )

  if (( CURRENT == 2 )); then
    # -V preserves the order above (dev/build/init/…) instead of
    # sorting alphabetically. See the fly_subs block below for
    # the full rationale.
    _describe -V 'mar-command' 'command' commands
    return
  fi

  case "${words[2]}" in
    dev)
      # dev accepts --no-open and a positional path.
      if [[ "${PREFIX}" == -* ]]; then
        _describe 'dev flag' '--no-open:Skip auto-opening the browser'
        return
      fi
      if (( CURRENT == 3 )); then
        _files -g '*.mar' || _path_files -/
      fi
      ;;
    check|config)
      # First positional is a path or .mar file.
      if (( CURRENT == 3 )); then
        _files -g '*.mar' || _path_files -/
      fi
      ;;
    build)
      # build accepts both flags and a positional path. Show flag
      # menu when the user is typing a '-', otherwise paths.
      if [[ "${PREFIX}" == -* ]]; then
        local -a build_flags
        # Long-form-then-short-form pairs (--target/-t, --out/-o).
        # -V keeps that pairing instead of zsh sorting alphabetically.
        build_flags=(
          '--target:Build target'
          '-t:Build target (short)'
          '--out:Output directory'
          '-o:Output directory (short)'
        )
        _describe -V 'mar-build-flag' 'build flag' build_flags
        return
      fi
      # After --target / -t, suggest the known target list.
      if [[ "${words[CURRENT-1]}" == "--target" || "${words[CURRENT-1]}" == "-t" ]]; then
        local -a targets
        targets=(${(s: :)$(echo '` + buildTargets + `')})
        # Targets are grouped by OS in buildTargets (darwin, linux,
        # windows, ios). Keep that grouping in the menu.
        _describe -V 'mar-build-target' 'target' targets
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
        # plan (future) before status (past) matches how operators
        # think about migrations day-to-day: "what's about to happen"
        # is the first question, "what already happened" the
        # follow-up.
        migrate_subs=(
          'plan:Show what next boot will apply or block'
          'status:Show applied migrations history'
        )
        _describe -V 'mar-migrate-subcommand' 'subcommand' migrate_subs
        return
      fi
      if (( CURRENT == 4 )); then
        _files -g '*.mar' || _path_files -/
      fi
      ;;
    fly)
      if (( CURRENT == 3 )); then
        local -a fly_subs
        # Order matches the natural lifecycle — preview shows what
        # would happen, deploy ships, logs/status/admin/database/
        # secrets are ops-on-running, destroy is the cleanup. Without
        # the -V flag below, zsh's _describe would sort these
        # alphabetically (admin first) which obscures the sequence.
        fly_subs=(
          'preview:Show what would be deployed (no side effects)'
          'deploy:Build + ship to Fly (creates app/volume/secrets as needed)'
          'logs:Tail logs from the running machine(s)'
          'status:Show app + machine status'
          'admin:Read-only inspection of production _mar_admins'
          'database:Database ops (backups) on the production volume'
          'db:Alias for database'
          'secrets:Manage env: refs as Fly secrets'
          'destroy:Destroy the fly app and its volume'
        )
        # -V puts matches in a named group that zsh leaves UNsorted
        # (the group name is opaque — it just has to be unique enough
        # that other completions don't collide with it).
        _describe -V 'mar-fly-subcommand' 'subcommand' fly_subs
        return
      fi
      # Sub-subcommands for fly admin/database/secrets — driven by
      # the second word (the fly sub).
      case "${words[3]}" in
        admin)
          if (( CURRENT == 4 )); then
            _describe -V 'mar-fly-admin' 'subcommand' \
              'list:Show production _mar_admins rows' \
              'ls:Alias for list'
            return
          fi
          ;;
        database|db)
          if (( CURRENT == 4 )); then
            _describe -V 'mar-fly-database' 'subcommand' \
              'backup:Take a snapshot now' \
              'backups:List the backup catalog'
            return
          fi
          ;;
        secrets)
          if (( CURRENT == 4 )); then
            _describe -V 'mar-fly-secrets' 'subcommand' \
              'set:Set one secret interactively' \
              'list:Show which env: refs are set on Fly' \
              'ls:Alias for list' \
              'unset:Remove a secret from Fly' \
              'rm:Alias for unset'
            return
          fi
          ;;
        deploy)
          # deploy accepts --no-open in addition to a path.
          if [[ "${PREFIX}" == -* ]]; then
            _describe 'fly deploy flag' '--no-open:Skip auto-opening the browser'
            return
          fi
          ;;
      esac
      if (( CURRENT == 4 )); then
        _path_files -/
      fi
      ;;
    admin)
      # mar admin add|remove|list — manage admin emails in mar.json.
      # add/remove take an email arg; list takes none.
      if (( CURRENT == 3 )); then
        _describe -V 'mar-admin-subcommand' 'subcommand' \
          'add:Add an admin email' \
          'remove:Remove an admin email' \
          'rm:Alias for remove' \
          'list:Show current admins' \
          'ls:Alias for list'
      fi
      ;;
    cloudflare-pages)
      if (( CURRENT == 3 )); then
        _describe -V 'mar-cfpages-subcommand' 'subcommand' \
          'deploy:Build the static bundle and push it to Cloudflare Pages'
        return
      fi
      case "${words[3]}" in
        deploy)
          if [[ "${PREFIX}" == -* ]]; then
            _describe 'cloudflare-pages deploy flag' '--no-open:Skip auto-opening the browser'
            return
          fi
          if (( CURRENT == 4 )); then
            _files -g '*.mar' || _path_files -/
          fi
          ;;
      esac
      ;;
    completion)
      if (( CURRENT == 3 )); then
        local -a shells
        # zsh first — it gets the richest completion (descriptions
        # in the menu) so listing it first hints at "this is the
        # most polished path".
        shells=(
          'zsh:zsh shell'
          'bash:bash shell'
          'fish:fish shell'
        )
        _describe -V 'mar-completion-shell' 'shell' shells
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
    COMPREPLY=( $(compgen -W "dev build init check format config migrate admin fly cloudflare-pages repl lsp completion version help" -- "${cur}") )
    return 0
  fi

  case "${COMP_WORDS[1]}" in
    dev)
      if [[ "${cur}" == -* ]]; then
        COMPREPLY=( $(compgen -W "--no-open" -- "${cur}") )
        return 0
      fi
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") $(compgen -d -- "${cur}") )
      fi
      ;;
    check|config)
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
        COMPREPLY=( $(compgen -W "--target -t --out -o" -- "${cur}") )
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
    admin)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "` + adminSubs + ` rm ls" -- "${cur}") )
      fi
      ;;
    cloudflare-pages)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "deploy" -- "${cur}") )
        return 0
      fi
      case "${COMP_WORDS[2]}" in
        deploy)
          if [[ "${cur}" == -* ]]; then
            COMPREPLY=( $(compgen -W "--no-open" -- "${cur}") )
            return 0
          fi
          if [[ ${COMP_CWORD} -eq 3 ]]; then
            COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") $(compgen -d -- "${cur}") )
          fi
          ;;
      esac
      ;;
    fly)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "` + flySubcommandList + `" -- "${cur}") )
        return 0
      fi
      # Sub-subcommands for fly admin/database/secrets + --no-open
      # flag for fly deploy.
      case "${COMP_WORDS[2]}" in
        admin)
          if [[ ${COMP_CWORD} -eq 3 ]]; then
            COMPREPLY=( $(compgen -W "` + flyAdminSubs + `" -- "${cur}") )
          fi
          ;;
        database|db)
          if [[ ${COMP_CWORD} -eq 3 ]]; then
            COMPREPLY=( $(compgen -W "` + flyDatabaseSubs + `" -- "${cur}") )
          fi
          ;;
        secrets)
          if [[ ${COMP_CWORD} -eq 3 ]]; then
            COMPREPLY=( $(compgen -W "` + flySecretsSubs + `" -- "${cur}") )
          fi
          ;;
        deploy)
          if [[ "${cur}" == -* ]]; then
            COMPREPLY=( $(compgen -W "--no-open" -- "${cur}") )
          fi
          ;;
      esac
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
complete -c mar -n '__fish_use_subcommand' -a admin      -d 'Manage local admin emails in mar.json'
complete -c mar -n '__fish_use_subcommand' -a fly        -d 'Deploy + operate a Fly.io app'
complete -c mar -n '__fish_use_subcommand' -a cloudflare-pages -d 'Deploy a frontend bundle to Cloudflare Pages'
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
complete -c mar -n '__fish_seen_subcommand_from build' -a '(__fish_complete_suffix .mar)'

# format: --check flag and .mar files
complete -c mar -n '__fish_seen_subcommand_from format' -l check -d 'Exit 1 if any file needs reformatting'
complete -c mar -n '__fish_seen_subcommand_from format' -a '(__fish_complete_suffix .mar)'

# completion: shell name
complete -c mar -n '__fish_seen_subcommand_from completion' -a 'zsh bash fish'

# dev: --no-open flag
complete -c mar -n '__fish_seen_subcommand_from dev' -l no-open -d 'Skip auto-opening the browser'

# migrate
complete -c mar -n '__fish_seen_subcommand_from migrate' -a 'plan status'

# admin (top-level): add / remove (rm) / list (ls)
complete -c mar -n '__fish_seen_subcommand_from admin' -a 'add remove rm list ls'

# fly: top-level subcommands. The condition '__fish_seen_subcommand_from
# fly; and not __fish_seen_subcommand_from <sub>' would gate this to the
# fly-level slot; fish's contains-based completion makes that verbose.
# We rely on fish narrowing automatically once a sub is typed.
complete -c mar -n '__fish_seen_subcommand_from fly' \
  -a 'preview deploy logs status admin database db secrets destroy'

# fly deploy: --no-open flag
complete -c mar -n '__fish_seen_subcommand_from fly; and __fish_seen_subcommand_from deploy' \
  -l no-open -d 'Skip auto-opening the browser'

# fly admin sub
complete -c mar -n '__fish_seen_subcommand_from fly; and __fish_seen_subcommand_from admin' \
  -a 'list ls'

# fly database sub
complete -c mar -n '__fish_seen_subcommand_from fly; and __fish_seen_subcommand_from database db' \
  -a 'backup backups'

# fly secrets sub
complete -c mar -n '__fish_seen_subcommand_from fly; and __fish_seen_subcommand_from secrets' \
  -a 'set list ls unset rm'

# cloudflare-pages: only deploy for now. The condition ANDs on the
# top-level cloudflare-pages slot so we don't suggest "deploy" when
# the user already typed e.g. "mar fly deploy".
complete -c mar -n '__fish_seen_subcommand_from cloudflare-pages' \
  -a 'deploy' -d 'Build the static bundle and push it to Cloudflare Pages'

# cloudflare-pages deploy: --no-open flag
complete -c mar -n '__fish_seen_subcommand_from cloudflare-pages; and __fish_seen_subcommand_from deploy' \
  -l no-open -d 'Skip auto-opening the browser'
`
}
