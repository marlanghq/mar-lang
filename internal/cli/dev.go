package cli

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type watchedFileState struct {
	Exists  bool
	Size    int64
	ModTime int64
}

type devProcess struct {
	cmd  *exec.Cmd
	done chan error
}

func runDev(binaryName, inputPath, outputPath string) error {
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("dev command must run from the belm module root (go.mod not found)")
	}

	absInput, err := filepath.Abs(inputPath)
	if err != nil {
		return err
	}
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	displayInput := displayDevPath(inputPath, absInput)

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1m", "Dev mode"), displayInput)
	fmt.Printf("  %s Save the file to rebuild and restart automatically.\n", colorizeCLI(useColor, "\033[1;36m", "Hot reload enabled."))

	state, err := readWatchedState(absInput)
	if err != nil {
		return err
	}

	var process *devProcess
	adminOpened := false

	rebuildAndRestart := func(reason string) {
		normalizedReason := strings.TrimSpace(reason)
		if normalizedReason == "initial build" {
			fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Initial build"))
		} else if normalizedReason != "" {
			fmt.Printf("\n%s %s\n", colorizeCLI(useColor, "\033[1;33m", "Change detected:"), normalizedReason)
		}
		started := time.Now()
		fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;36m", "Compiling"), displayInput)

		app, parseErr := parseBelmFile(absInput)
		if parseErr != nil {
			fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "Build failed:"), parseErr)
			return
		}
		if buildErr := buildExecutableWithOptions(app, absOutput, buildOptions{
			PrintSummary: false,
			SourcePath:   absInput,
		}); buildErr != nil {
			fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "Build failed:"), buildErr)
			return
		}

		if process != nil {
			stopDevProcess(process)
			process = nil
		}

		nextProcess, runErr := startDevProcess(absOutput, adminOpened)
		if runErr != nil {
			fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "Run failed:"), runErr)
			return
		}
		process = nextProcess
		if !adminOpened {
			adminOpened = true
		}

		fmt.Printf(
			"%s %s (%d ms)\n",
			colorizeCLI(useColor, "\033[1;32m", "Build succeeded"),
			filepath.Base(absOutput),
			time.Since(started).Milliseconds(),
		)
	}

	rebuildAndRestart("initial build")

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var pendingChange bool
	var pendingSince time.Time

	for {
		select {
		case <-sigCh:
			if process != nil {
				stopDevProcess(process)
			}
			fmt.Println()
			fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Dev mode stopped"))
			fmt.Println()
			return nil
		case <-ticker.C:
			if process != nil {
				select {
				case err := <-process.done:
					process = nil
					if err != nil {
						fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "App exited:"), err)
					}
				default:
				}
			}

			nextState, readErr := readWatchedState(absInput)
			if readErr != nil {
				fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "Watch error:"), readErr)
				continue
			}
			if nextState != state {
				state = nextState
				pendingChange = true
				pendingSince = time.Now()
			}
			if pendingChange && time.Since(pendingSince) >= 250*time.Millisecond {
				pendingChange = false
				rebuildAndRestart("file saved")
			}
		}
	}
}

func readWatchedState(path string) (watchedFileState, error) {
	info, err := os.Stat(path)
	if err != nil {
		return watchedFileState{}, err
	}
	return watchedFileState{
		Exists:  true,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}, nil
}

func startDevProcess(outputPath string, noOpen bool) (*devProcess, error) {
	runDir := filepath.Dir(outputPath)
	cmd := exec.Command(outputPath, "serve")
	cmd.Dir = runDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	if noOpen {
		cmd.Env = append(os.Environ(), "BELM_ADMIN_NO_OPEN=1")
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	return &devProcess{cmd: cmd, done: done}, nil
}

func stopDevProcess(process *devProcess) {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return
	}

	terminated := false
	if runtime.GOOS == "windows" {
		_ = process.cmd.Process.Kill()
		terminated = true
	} else {
		if err := process.cmd.Process.Signal(syscall.SIGTERM); err == nil {
			terminated = true
		}
	}

	if !terminated {
		_ = process.cmd.Process.Kill()
	}

	select {
	case <-process.done:
	case <-time.After(3 * time.Second):
		_ = process.cmd.Process.Kill()
		<-process.done
	}
}

func displayDevPath(originalPath, absolutePath string) string {
	if trimmed := strings.TrimSpace(originalPath); trimmed != "" && !filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}

	cwd, err := os.Getwd()
	if err == nil {
		if rel, relErr := filepath.Rel(cwd, absolutePath); relErr == nil {
			rel = filepath.Clean(rel)
			parentPrefix := ".." + string(filepath.Separator)
			if rel != ".." && !strings.HasPrefix(rel, parentPrefix) {
				return rel
			}
		}
	}

	return filepath.Base(absolutePath)
}
