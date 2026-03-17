package cli

import (
	"fmt"
	"os"
	"strings"
)

func runCompletion(binaryName string, args []string) error {
	if len(args) != 1 {
		return completionUsageError(binaryName)
	}

	script, err := renderCompletionScript(binaryName, args[0])
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(script)
	return err
}

func completionUsageError(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Completion usage"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s completion <zsh|bash|fish>", binaryName))
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Generate zsh completion with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s completion zsh", binaryName)))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func renderCompletionScript(binaryName, shell string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "zsh":
		return renderZshCompletion(binaryName), nil
	case "bash":
		return renderBashCompletion(binaryName), nil
	case "fish":
		return renderFishCompletion(binaryName), nil
	default:
		useColor := cliSupportsANSIStream(os.Stderr)
		var b strings.Builder
		fmt.Fprintf(&b, "%s %q\n", colorizeCLI(useColor, "\033[1;31m", "Unsupported shell"), shell)
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Use one of: %s\n", colorizeCLI(useColor, "\033[1;32m", "zsh, bash, fish"))
		return "", styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}
}

func renderZshCompletion(binaryName string) string {
	return fmt.Sprintf(`_%s() {
  local -a commands
  local -a fly_commands
  local -a shells
  local -a format_flags
  local -a format_check_flags
  commands=(
    'init:Create a new Mar project with a starter app'
    'edit:Edit a Mar file directly in the terminal'
    'dev:Run development mode with hot reload'
    'compile:Compile a .mar app into executables for all supported platforms'
    'fly:Prepare and deploy a Fly.io app'
    'completion:Generate shell completion scripts'
    'format:Format Mar source files'
    'lsp:Start the Mar Language Server'
    'version:Show version and build information'
  )
  fly_commands=(
    'init:Prepare Fly.io deployment files for your app'
    'deploy:Rebuild the Linux executable for Fly.io and run fly deploy'
  )
  shells=(
    'zsh:zsh shell'
    'bash:bash shell'
    'fish:fish shell'
  )
  format_flags=(
    '--check:Check formatting without writing files'
    '--stdin:Read Mar source from stdin'
  )
  format_check_flags=(
    '--check:Check formatting without writing files'
  )

  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi

  case "${words[2]}" in
    edit)
      if (( CURRENT == 3 )); then
        _files -g '*.mar'
      fi
      ;;
    dev|compile)
      if (( CURRENT == 3 )); then
        _files -g '*.mar'
      elif (( CURRENT == 4 )); then
        _message 'output name'
      fi
      ;;
    fly)
      if (( CURRENT == 3 )); then
        _describe 'fly command' fly_commands
        return
      fi
      case "${words[3]}" in
        init|deploy)
          if (( CURRENT == 4 )); then
            _files -g '*.mar'
          fi
          ;;
      esac
      ;;
    completion)
      if (( CURRENT == 3 )); then
        _describe 'shell' shells
      fi
      ;;
    format)
      local has_stdin=0
      if (( ${words[(I)--stdin]} )); then
        has_stdin=1
      fi
      if [[ "${PREFIX}" == -* ]]; then
        if (( has_stdin )); then
          _describe 'format flag' format_check_flags
        else
          _describe 'format flag' format_flags
        fi
        return
      fi
      if (( has_stdin )); then
        _describe 'format flag' format_check_flags
        return
      fi
      _files -g '*.mar'
      ;;
  esac
}

compdef _%s %s
`, binaryName, binaryName, binaryName)
}

func renderBashCompletion(binaryName string) string {
	return fmt.Sprintf(`_%s_completion() {
  local cur prev prev2
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  prev2="${COMP_WORDS[COMP_CWORD-2]}"

  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "init edit dev compile fly completion format lsp version" -- "${cur}") )
    return 0
  fi

  case "${COMP_WORDS[1]}" in
    edit)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") )
      fi
      ;;
    dev|compile)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") )
      fi
      ;;
    fly)
      if [[ ${COMP_CWORD} -eq 2 ]]; then
        COMPREPLY=( $(compgen -W "init deploy" -- "${cur}") )
        return 0
      fi
      case "${prev}" in
        init|deploy)
          COMPREPLY=( $(compgen -f -X '!*.mar' -- "${cur}") )
          ;;
      esac
      ;;
    completion)
      COMPREPLY=( $(compgen -W "zsh bash fish" -- "${cur}") )
      ;;
    format)
      local has_stdin=0
      local word
      for word in "${COMP_WORDS[@]}"; do
        if [[ "${word}" == "--stdin" ]]; then
          has_stdin=1
          break
        fi
      done

      if [[ "${cur}" == -* ]]; then
        if [[ ${has_stdin} -eq 1 ]]; then
          COMPREPLY=( $(compgen -W "--check" -- "${cur}") )
        else
          COMPREPLY=( $(compgen -W "--check --stdin" -- "${cur}") )
        fi
        return 0
      fi

      if [[ ${has_stdin} -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "--check" -- "${cur}") )
      else
        COMPREPLY=(
          $(compgen -W "--check --stdin" -- "${cur}")
          $(compgen -f -X '!*.mar' -- "${cur}")
        )
      fi
      ;;
  esac
}

complete -F _%s_completion %s
`, binaryName, binaryName, binaryName)
}

func renderFishCompletion(binaryName string) string {
	return fmt.Sprintf(`complete -c %s -f
complete -c %s -n '__fish_use_subcommand' -a init -d 'Create a new Mar project with a starter app'
complete -c %s -n '__fish_use_subcommand' -a edit -d 'Edit a Mar file directly in the terminal'
complete -c %s -n '__fish_use_subcommand' -a dev -d 'Run development mode with hot reload'
complete -c %s -n '__fish_use_subcommand' -a compile -d 'Compile a .mar app into executables for all supported platforms'
complete -c %s -n '__fish_use_subcommand' -a fly -d 'Prepare and deploy a Fly.io app'
complete -c %s -n '__fish_use_subcommand' -a completion -d 'Generate shell completion scripts'
complete -c %s -n '__fish_use_subcommand' -a format -d 'Format Mar source files'
complete -c %s -n '__fish_use_subcommand' -a lsp -d 'Start the Mar Language Server'
complete -c %s -n '__fish_use_subcommand' -a version -d 'Show version and build information'

complete -c %s -n '__fish_seen_subcommand_from fly; and not __fish_seen_subcommand_from init deploy' -a init -d 'Prepare Fly.io deployment files for your app'
complete -c %s -n '__fish_seen_subcommand_from fly; and not __fish_seen_subcommand_from init deploy' -a deploy -d 'Rebuild the Linux executable for Fly.io and run fly deploy'

complete -c %s -n '__fish_seen_subcommand_from edit; and test (count (commandline -opc)) -eq 2' -a '(__fish_complete_suffix .mar)'
complete -c %s -n '__fish_seen_subcommand_from dev compile; and test (count (commandline -opc)) -eq 2' -a '(__fish_complete_suffix .mar)'
complete -c %s -n '__fish_seen_subcommand_from fly; and __fish_seen_subcommand_from init deploy' -a '(__fish_complete_suffix .mar)'
complete -c %s -n '__fish_seen_subcommand_from format; and not __fish_seen_subcommand_from --stdin' -l check -d 'Check formatting without writing files'
complete -c %s -n '__fish_seen_subcommand_from format; and not __fish_seen_subcommand_from --stdin' -l stdin -d 'Read Mar source from stdin'
complete -c %s -n '__fish_seen_subcommand_from format; and not __fish_seen_subcommand_from --stdin' -a '(__fish_complete_suffix .mar)'
complete -c %s -n '__fish_seen_subcommand_from format; and __fish_seen_subcommand_from --stdin' -l check -d 'Check formatting without writing files'
complete -c %s -n '__fish_seen_subcommand_from completion' -a 'zsh bash fish'
`, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName, binaryName)
}
