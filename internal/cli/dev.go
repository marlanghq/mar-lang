package cli

import (
	"fmt"
	"net/http"
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

const devDatabasePathOverrideEnv = "MAR_DATABASE_PATH"

func runDev(binaryName, inputPath, outputPath string) error {
	launchCWD, err := os.Getwd()
	if err != nil {
		return err
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

		app, parseErr := parseMarFile(absInput)
		if parseErr != nil {
			fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "Build failed:"), parseErr)
			return
		}
		if app.Auth != nil && strings.EqualFold(strings.TrimSpace(app.Auth.EmailTransport), "smtp") {
			fmt.Printf(
				"  %s %s %s %s %s\n",
				colorizeCLI(useColor, "\033[1;36m", "Login emails"),
				"use",
				colorizeCLI(useColor, "\033[1;33m", "console"),
				"in dev. Production still uses the configured",
				colorizeCLI(useColor, "\033[1;33m", "smtp."),
			)
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

		nextProcess, runErr := startDevProcess(absOutput, launchCWD, absInput, app.Database)
		if runErr != nil {
			fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "Run failed:"), runErr)
			return
		}
		process = nextProcess
		adminURL := fmt.Sprintf("http://127.0.0.1:%d/_mar/admin", app.Port)
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", app.Port)
		ready, exited, processErr := waitForDevServer(healthURL, 8*time.Second, process.done)
		if exited {
			process = nil
			if processErr != nil {
				fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;31m", "App exited:"), processErr)
			}
			return
		}
		if !ready {
			fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;33m", "Warning:"), "server is still starting; open "+adminURL+" manually if needed.")
		} else if !adminOpened {
			if err := openBrowser(adminURL); err != nil {
				fmt.Printf("%s %s\n", colorizeCLI(useColor, "\033[1;33m", "Warning:"), "could not open browser: "+err.Error())
			}
		}
		adminOpened = true

		fmt.Printf(
			"\n%s %s (%d ms)\n",
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

func startDevProcess(outputPath string, launchCWD string, sourcePath string, databasePath string) (*devProcess, error) {
	runDir := filepath.Dir(outputPath)
	cmd := exec.Command(outputPath, "serve")
	cmd.Dir = runDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "MAR_DEV_MODE=1")
	if strings.TrimSpace(launchCWD) != "" {
		cmd.Env = append(cmd.Env, "MAR_DEV_LAUNCH_CWD="+launchCWD)
	}
	if override := resolveDevDatabaseOverride(sourcePath, databasePath); override != "" {
		cmd.Env = append(cmd.Env, devDatabasePathOverrideEnv+"="+override)
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

func resolveDevDatabaseOverride(sourcePath string, databasePath string) string {
	trimmedDatabase := strings.TrimSpace(databasePath)
	if trimmedDatabase == "" {
		return ""
	}

	cleanedDatabase := filepath.Clean(trimmedDatabase)
	if filepath.IsAbs(cleanedDatabase) {
		return ""
	}

	trimmedSource := strings.TrimSpace(sourcePath)
	if trimmedSource == "" {
		return ""
	}

	absoluteSource := trimmedSource
	if !filepath.IsAbs(absoluteSource) {
		var err error
		absoluteSource, err = filepath.Abs(trimmedSource)
		if err != nil {
			return ""
		}
	}

	return filepath.Join(filepath.Dir(absoluteSource), cleanedDatabase)
}

func waitForDevServer(url string, timeout time.Duration, processDone <-chan error) (bool, bool, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case err := <-processDone:
			return false, true, err
		default:
		}

		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return true, false, nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}

	select {
	case err := <-processDone:
		return false, true, err
	default:
	}

	return false, false, nil
}
