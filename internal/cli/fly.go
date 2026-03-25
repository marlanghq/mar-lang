package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"mar/internal/model"

	"golang.org/x/term"
)

const flyDatabasePathEnv = "MAR_DATABASE_PATH"

var flyAppNameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var errFlySecretPromptInterrupted = errors.New("interrupted")

type flyRegion struct {
	Continent string
	Name      string
	Code      string
}

var flyRegions = []flyRegion{
	{Continent: "Africa", Name: "Johannesburg, South Africa", Code: "jnb"},
	{Continent: "Asia Pacific", Name: "Mumbai, India", Code: "bom"},
	{Continent: "Asia Pacific", Name: "Singapore, Singapore", Code: "sin"},
	{Continent: "Asia Pacific", Name: "Sydney, Australia", Code: "syd"},
	{Continent: "Asia Pacific", Name: "Tokyo, Japan", Code: "nrt"},
	{Continent: "Europe", Name: "Amsterdam, Netherlands", Code: "ams"},
	{Continent: "Europe", Name: "Frankfurt, Germany", Code: "fra"},
	{Continent: "Europe", Name: "London, United Kingdom", Code: "lhr"},
	{Continent: "Europe", Name: "Paris, France", Code: "cdg"},
	{Continent: "Europe", Name: "Stockholm, Sweden", Code: "arn"},
	{Continent: "North America", Name: "Ashburn, Virginia (US)", Code: "iad"},
	{Continent: "North America", Name: "Chicago, Illinois (US)", Code: "ord"},
	{Continent: "North America", Name: "Dallas, Texas (US)", Code: "dfw"},
	{Continent: "North America", Name: "Los Angeles, California (US)", Code: "lax"},
	{Continent: "North America", Name: "San Jose, California (US)", Code: "sjc"},
	{Continent: "North America", Name: "Secaucus, NJ (US)", Code: "ewr"},
	{Continent: "North America", Name: "Toronto, Canada", Code: "yyz"},
	{Continent: "South America", Name: "Sao Paulo, Brazil", Code: "gru"},
}

type flyInitResult struct {
	FlyAppName      string
	VolumeName      string
	RegionCode      string
	RegionName      string
	Port            int
	BuildRoot       string
	BinaryName      string
	BinaryPath      string
	DatabaseLocal   string
	DatabaseFly     string
	DockerfilePath  string
	FlyTomlPath     string
	SMTPPasswordEnv string
	EmailFrom       string
	Warnings        []string
	Notes           []string
}

func runFly(binaryName string, args []string) error {
	if len(args) == 0 {
		return flyUsageError(binaryName)
	}
	switch args[0] {
	case "init":
		if len(args) != 2 {
			return flyUsageError(binaryName)
		}
		return runFlyInit(binaryName, args[1])
	case "provision":
		if len(args) != 2 {
			return flyUsageError(binaryName)
		}
		return runFlyProvision(binaryName, args[1])
	case "deploy":
		inputPath, assumeYes, ok := parseFlyDeployArgs(args[1:])
		if !ok {
			return flyUsageError(binaryName)
		}
		return runFlyDeploy(inputPath, assumeYes)
	case "destroy":
		if len(args) != 2 {
			return flyUsageError(binaryName)
		}
		return runFlyDestroy(binaryName, args[1])
	default:
		return flyUsageError(binaryName)
	}
}

func flyUsageError(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly usage"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly init <app.mar>", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly provision <app.mar>", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly deploy <app.mar> [--yes]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly destroy <app.mar>", binaryName))
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Prepare Fly.io deployment files with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s fly init <app.mar>", binaryName)))
	fmt.Fprintf(&b, "  Create the Fly app, volume, and secrets with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s fly provision <app.mar>", binaryName)))
	fmt.Fprintf(&b, "  Deploy the current app with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s fly deploy <app.mar> [--yes]", binaryName)))
	fmt.Fprintf(&b, "  Permanently destroy the Fly.io app with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s fly destroy <app.mar>", binaryName)))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func parseFlyDeployArgs(args []string) (inputPath string, assumeYes bool, ok bool) {
	if len(args) == 1 && args[0] != "--yes" {
		return args[0], false, true
	}
	if len(args) == 2 {
		if args[0] == "--yes" && args[1] != "--yes" {
			return args[1], true, true
		}
		if args[1] == "--yes" && args[0] != "--yes" {
			return args[0], true, true
		}
	}
	return "", false, false
}

func runFlyInit(binaryName, inputPath string) error {
	app, err := parseMarFile(inputPath)
	if err != nil {
		return err
	}
	printAppWarnings(app)
	if err := validateFlyInitPrereqs(app); err != nil {
		return err
	}
	confirmed, err := confirmFlyAction(
		"Fly init",
		[]string{
			"It will generate the deployment configuration files in the " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1;35m", "deploy/fly") + " folder.",
			"This is the first of 3 steps to put your Mar app into production on Fly.io.",
			"Learn more about Fly.io at: " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[38;5;141m", "https://fly.io"),
		},
		"Fly init canceled",
	)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}

	flyDir := filepath.Join("deploy", "fly")
	shouldContinue, err := confirmFlyInitRecreate(flyDir)
	if err != nil {
		return err
	}
	if !shouldContinue {
		return nil
	}
	if flyDirHasContents(flyDir) {
		if err := os.RemoveAll(flyDir); err != nil {
			return err
		}
		useColor := cliSupportsANSIStream(os.Stdout)
		fmt.Println()
		fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;31m", "Deleted deploy/fly and all its contents."))
	}

	flyAppName, err := resolveFlyAppName(app)
	if err != nil {
		return err
	}
	region, err := resolveFlyRegion()
	if err != nil {
		return err
	}

	buildRoot, outputName := defaultBuildLayout(inputPath, "")
	target := runtimeTarget{OS: "linux", Arch: "amd64"}
	outputPath := targetOutputPath(buildRoot, outputName, target)

	if err := os.MkdirAll(flyDir, 0o755); err != nil {
		return err
	}

	dbLocal, dbFly := resolveFlyDatabasePaths(app.Database, outputName)
	volumeName := outputName + "_data"
	result := flyInitResult{
		FlyAppName:     flyAppName,
		BuildRoot:      buildRoot,
		BinaryName:     outputName,
		BinaryPath:     outputPath,
		RegionCode:     region.Code,
		RegionName:     region.Name,
		Port:           app.Port,
		DatabaseLocal:  dbLocal,
		DatabaseFly:    dbFly,
		DockerfilePath: filepath.Join(flyDir, "Dockerfile"),
		FlyTomlPath:    filepath.Join(flyDir, "fly.toml"),
		VolumeName:     volumeName,
	}
	if app.Auth != nil {
		result.SMTPPasswordEnv = strings.TrimSpace(app.Auth.SMTPPasswordEnv)
		result.EmailFrom = strings.TrimSpace(app.Auth.EmailFrom)
		if result.SMTPPasswordEnv != "" {
			result.Notes = append(result.Notes, fmt.Sprintf("SMTP password will be read from the %s environment variable at runtime.", result.SMTPPasswordEnv))
		}
	}
	if dbLocal != dbFly {
		result.Notes = append(result.Notes, fmt.Sprintf("SQLite path will be rewritten for Fly.io: %s -> %s", dbLocal, dbFly))
	}
	if !flyCLIAvailable() {
		result.Warnings = append(result.Warnings, "Fly.io CLI was not found locally. You will need it before deploy.")
	}

	if err := os.WriteFile(result.DockerfilePath, []byte(renderFlyDockerfile(result)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(result.FlyTomlPath, []byte(renderFlyToml(result)), 0o644); err != nil {
		return err
	}
	printFlyInitSummary(inputPath, result)
	return nil
}

func validateFlyInitPrereqs(app *model.App) error {
	if app == nil || app.Auth == nil {
		return nil
	}

	emailFrom := strings.TrimSpace(app.Auth.EmailFrom)
	if looksLikePlaceholderEmail(emailFrom) {
		return placeholderEmailFlyInitError(emailFrom)
	}

	return nil
}

func confirmFlyAction(title string, details []string, canceledMessage string) (bool, error) {
	if !stdinIsTerminal() {
		return true, nil
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", title))
	for _, detail := range details {
		fmt.Printf("  %s\n", detail)
	}
	reader := bufio.NewReader(os.Stdin)
	confirmed, err := promptYesNo(reader, os.Stdout, useColor, "Continue? [y/N]", false)
	if err != nil {
		return false, err
	}
	if !confirmed {
		fmt.Println()
		fmt.Printf("%s\n\n", colorizeCLI(useColor, "\033[1;31m", canceledMessage))
		return false, nil
	}
	return true, nil
}

func promptYesNo(reader *bufio.Reader, out io.Writer, useColor bool, label string, defaultYes bool) (bool, error) {
	for {
		fmt.Fprintf(out, "  %s ", colorizeCLI(useColor, "\033[1;36m", label))
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		case "":
			return defaultYes, nil
		default:
			fmt.Fprintf(out, "  %s\n", colorizeCLI(useColor, "\033[1;33m", "Please answer yes or no."))
		}
	}
}

func confirmFlyDestroy(binaryName, appName string) (bool, error) {
	if !stdinIsTerminal() {
		useColor := cliSupportsANSIStream(os.Stderr)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly destroy requires confirmation"))
		fmt.Fprintf(&b, "  Re-run %s in an interactive terminal to confirm destroying %s.\n", colorizeCLI(useColor, "\033[1;32m", binaryName+" fly destroy <app.mar>"), colorizeCLI(useColor, "\033[1;36m", appName))
		return false, styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly destroy"))
	fmt.Printf("  %s\n", "This step permanently destroys the Fly.io app configured in deploy/fly/fly.toml.")
	fmt.Printf("  %s\n", "It is intended for test apps, temporary environments, or apps you no longer need.")
	fmt.Printf("  %s %s\n", "Fly.io app:", colorizeCLI(useColor, "\033[1;36m", appName))
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;31m", "This action is destructive and cannot be undone."))

	reader := bufio.NewReader(os.Stdin)
	confirmed, err := promptYesNo(reader, os.Stdout, useColor, "Continue? [y/N]", false)
	if err != nil {
		return false, err
	}
	if !confirmed {
		fmt.Println()
		fmt.Printf("%s\n\n", colorizeCLI(useColor, "\033[1;31m", "Fly destroy canceled"))
		return false, nil
	}
	return true, nil
}

func confirmFlyDestroyAppName(appName string) (bool, error) {
	if !stdinIsTerminal() {
		return true, nil
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("  Type %s to confirm: ", colorizeCLI(useColor, "\033[1;36m", appName))
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return false, err
		}
		answer := strings.TrimSpace(line)
		if answer == appName {
			return true, nil
		}
		fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;33m", "The app name did not match. Please try again."))
	}
}

func ensureFlyAppExists(flyCmd, appName string) error {
	var captured bytes.Buffer
	cmd := exec.Command(flyCmd, "status", "-a", appName)
	cmd.Stdout = &captured
	cmd.Stderr = &captured
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err == nil {
		return nil
	}

	output := strings.TrimSpace(captured.String())
	if strings.Contains(output, "Could not find App") {
		useColor := cliSupportsANSIStream(os.Stderr)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly.io app was not found"))
		fmt.Fprintf(&b, "  I could not find the Fly.io app named %s.\n", colorizeCLI(useColor, "\033[1;36m", appName))
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Check whether the app has already been removed or whether %s still points to the right app.\n", colorizeCLI(useColor, "\033[1;35m", "deploy/fly/fly.toml"))
		return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}
	if output != "" {
		return errors.New(output)
	}
	return errors.New("failed to verify whether the Fly.io app exists")
}

func confirmFlyInitRecreate(flyDir string) (bool, error) {
	if !flyDirHasContents(flyDir) {
		return true, nil
	}
	if !stdinIsTerminal() {
		useColor := cliSupportsANSIStream(os.Stderr)
		coloredFlyDir := colorizeCLI(useColor, "\033[1;35m", flyDir)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly init would recreate ")+coloredFlyDir)
		fmt.Fprintf(&b, "  %s already contains files.\n", coloredFlyDir)
		fmt.Fprintf(&b, "  %s\n", colorizeCLI(useColor, "\033[1;31m", "If you continue, that folder will be deleted and all Fly.io deployment files will be recreated."))
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Re-run this command in an interactive terminal to confirm the overwrite.\n")
		return false, styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	coloredFlyDir := colorizeCLI(useColor, "\033[1;35m", flyDir)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Fly deploy files already exist"))
	fmt.Printf("  %s already contains files.\n", coloredFlyDir)
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;31m", "If you continue, that folder will be deleted and all Fly.io deployment files will be recreated."))
	reader := bufio.NewReader(os.Stdin)
	confirmed, err := promptYesNo(reader, os.Stdout, useColor, "Continue? [y/N]", false)
	if err != nil {
		return false, err
	}
	if !confirmed {
		fmt.Println()
		fmt.Printf("%s\n\n", colorizeCLI(useColor, "\033[1;31m", "Fly init canceled"))
		return false, nil
	}
	return true, nil
}

func flyDirHasContents(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func buildFlyLinuxExecutable(app *model.App, outputPath string, options buildOptions) error {
	payload, err := buildAppPayload(app, options)
	if err != nil {
		return err
	}
	return packageExecutable(runtimeTarget{OS: "linux", Arch: "amd64"}, outputPath, payload)
}

func runFlyProvision(binaryName, inputPath string) error {
	useColor := cliSupportsANSIStream(os.Stdout)

	app, err := parseMarFile(inputPath)
	if err != nil {
		return err
	}
	printAppWarnings(app)
	flyDir := filepath.Join("deploy", "fly")
	dockerfilePath := filepath.Join(flyDir, "Dockerfile")
	flyTomlPath := filepath.Join(flyDir, "fly.toml")

	if err := requireFlyDeployFiles(dockerfilePath, flyTomlPath); err != nil {
		return err
	}

	if _, err := readFlyAppName(flyTomlPath); err != nil {
		return err
	}
	if _, err := readFlyPrimaryRegion(flyTomlPath); err != nil {
		return err
	}
	if _, err := readFlyVolumeName(flyTomlPath); err != nil {
		return err
	}
	if _, err := findFlyCommand(); err != nil {
		return err
	}

	confirmed, err := confirmFlyAction(
		"Fly provision",
		[]string{
			"This step creates the Fly.io resources your app needs.",
			"It will log in to Fly.io if needed, create the " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1m", "app") + ", and create the " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1m", "persistent volume") + " for SQLite.",
			"Before continuing, make sure you already have an " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1m", "SMTP") + " provider configured, such as Resend (" + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[38;5;141m", "https://resend.com") + ").",
			"You will be asked for the " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1m", "SMTP secret") + " value, and it will be stored on Fly.io.",
		},
		"Fly provision canceled",
	)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly provision"))
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "App source:"), inputPath)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Config:"), flyTomlPath)

	flyAppName, err := readFlyAppName(flyTomlPath)
	if err != nil {
		return err
	}
	regionCode, err := readFlyPrimaryRegion(flyTomlPath)
	if err != nil {
		return err
	}
	volumeName, err := readFlyVolumeName(flyTomlPath)
	if err != nil {
		return err
	}
	flyCmd, err := findFlyCommand()
	if err != nil {
		return err
	}

	totalSteps := 4
	if app.Auth != nil && strings.TrimSpace(app.Auth.SMTPPasswordEnv) != "" {
		totalSteps = 5
	}
	step := 1
	printStepTitle := func(label string) {
		fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", fmt.Sprintf("Step %d/%d", step, totalSteps)))
		fmt.Printf("  %s\n", label)
		step++
	}

	printStepTitle("Checking Fly authentication")
	if err := ensureFlyAuth(flyCmd); err != nil {
		return err
	}

	printStepTitle("Creating the Fly app")
	if err := runFlyCLICommand(useColor, inputPath, "Create app", flyCmd, "apps", "create", flyAppName); err != nil {
		return err
	}

	printStepTitle("Creating the Fly volume")
	if err := runFlyCLICommand(
		useColor,
		inputPath,
		"Create volume",
		flyCmd,
		"volumes",
		"create",
		volumeName,
		"--region",
		regionCode,
		"--size",
		"1",
		"--yes",
		"-a",
		flyAppName,
	); err != nil {
		return err
	}

	if app.Auth != nil && strings.TrimSpace(app.Auth.SMTPPasswordEnv) != "" {
		printStepTitle("Configuring the SMTP secret")
		secretValue, err := resolveFlySecretValue(strings.TrimSpace(app.Auth.SMTPPasswordEnv))
		if err != nil {
			return err
		}
		fmt.Println()
		if err := runFlyCLICommand(
			useColor,
			inputPath,
			"Set SMTP secret",
			flyCmd,
			"secrets",
			"set",
			strings.TrimSpace(app.Auth.SMTPPasswordEnv)+"="+secretValue,
			"-a",
			flyAppName,
		); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;32m", "Fly provision finished."))
	fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Printf("  Run %s\n", colorizeCLI(useColor, "\033[1;32m", binaryName+" fly deploy "+inputPath))
	fmt.Printf("  %s\n\n", "That is the final step. It will build the Linux executable for this app and publish it to Fly.io.")
	return nil
}

func runFlyDeploy(inputPath string, assumeYes bool) error {
	useColor := cliSupportsANSIStream(os.Stdout)

	app, err := parseMarFile(inputPath)
	if err != nil {
		return err
	}
	printAppWarnings(app)
	buildRoot, outputName := defaultBuildLayout(inputPath, "")
	outputPath := targetOutputPath(buildRoot, outputName, runtimeTarget{OS: "linux", Arch: "amd64"})
	flyDir := filepath.Join("deploy", "fly")
	dockerfilePath := filepath.Join(flyDir, "Dockerfile")
	flyTomlPath := filepath.Join(flyDir, "fly.toml")

	if err := requireFlyDeployFiles(dockerfilePath, flyTomlPath); err != nil {
		return err
	}

	flyAppName, err := readFlyAppName(flyTomlPath)
	if err != nil {
		return err
	}

	flyCmd, err := findFlyCommand()
	if err != nil {
		return err
	}

	if !assumeYes {
		confirmed, err := confirmFlyAction(
			"Fly deploy",
			[]string{
				"This step publishes the current version of your app to Fly.io.",
				"It will rebuild the Linux executable, use the generated " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1;35m", "deploy/fly/fly.toml") + " config, and run " + colorizeCLI(cliSupportsANSIStream(os.Stdout), "\033[1;32m", "fly deploy") + ".",
				"If the deploy succeeds, the Fly.io app URL will be opened automatically.",
			},
			"Fly deploy canceled",
		)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly deploy"))
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "App source:"), inputPath)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Config:"), flyTomlPath)

	fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Step 1/3"))
	fmt.Printf("  %s\n", "Validating Fly deployment files")
	fmt.Printf("  %s\n", dockerfilePath)
	fmt.Printf("  %s\n", flyTomlPath)

	fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Step 2/3"))
	fmt.Printf("  %s\n", "Preparing the Linux executable used by Fly.io")
	if err := buildFlyLinuxExecutable(app, outputPath, buildOptions{PrintSummary: false, SourcePath: inputPath}); err != nil {
		return err
	}
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;32m", "Ready:"), outputPath)

	if app.Auth != nil && looksLikePlaceholderEmail(strings.TrimSpace(app.Auth.EmailFrom)) {
		fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;31m", "Warnings"))
		fmt.Printf("  %s\n", "auth.email_from still looks like a placeholder: "+strings.TrimSpace(app.Auth.EmailFrom))
	}

	fmt.Printf("\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Step 3/3"))
	fmt.Printf("  %s\n", "Running Fly deploy")
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;32m", flyCmd+" deploy -c "+flyTomlPath))
	fmt.Println()

	cmd := exec.Command(flyCmd, "deploy", "-c", flyTomlPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;32m", "Fly deploy finished"))
	if flyAppName != "" {
		appURL := "https://" + flyAppName + ".fly.dev"
		fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;37m", "Opening:"), colorizeCLI(useColor, "\033[34m", appURL))
		if err := openBrowser(appURL); err != nil {
			fmt.Printf("  %s\n", "Open that URL manually if needed.")
		}
	}
	fmt.Println()
	return nil
}

func runFlyDestroy(binaryName, inputPath string) error {
	useColor := cliSupportsANSIStream(os.Stdout)
	flyDir := filepath.Join("deploy", "fly")
	flyTomlPath := filepath.Join(flyDir, "fly.toml")

	if err := requireFlyConfigFile(flyTomlPath); err != nil {
		return err
	}

	flyAppName, err := readFlyAppName(flyTomlPath)
	if err != nil {
		return err
	}
	flyCmd, err := findFlyCommand()
	if err != nil {
		return err
	}

	confirmed, err := confirmFlyDestroy(binaryName, flyAppName)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}

	fmt.Println()
	fmt.Printf("  %s\n", "Checking Fly.io authentication.")
	if err := ensureFlyAuth(flyCmd); err != nil {
		return err
	}
	fmt.Printf("  %s\n", "Checking whether the Fly.io app exists.")
	if err := ensureFlyAppExists(flyCmd, flyAppName); err != nil {
		return err
	}
	confirmed, err = confirmFlyDestroyAppName(flyAppName)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}

	fmt.Println()
	fmt.Printf("  %s\n", "Destroying the Fly.io app")
	if err := runFlyCLICommand(useColor, inputPath, "Destroy app", flyCmd, "apps", "destroy", flyAppName, "--yes"); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("%s\n\n", colorizeCLI(useColor, "\033[1;32m", "Fly destroy finished."))
	return nil
}

func requireFlyDeployFiles(dockerfilePath, flyTomlPath string) error {
	missing := make([]string, 0, 2)
	for _, path := range []string{dockerfilePath, flyTomlPath} {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			missing = append(missing, path)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly deploy files are missing"))
	for _, path := range missing {
		fmt.Fprintf(&b, "  %s\n", path)
	}
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Before you can deploy to Fly.io, you need to generate the deployment configuration files.\n")
	fmt.Fprintf(&b, "  Run: %s\n", colorizeCLI(useColor, "\033[1;32m", "mar fly init <app.mar>"))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func requireFlyConfigFile(flyTomlPath string) error {
	info, err := os.Stat(flyTomlPath)
	if err == nil && !info.IsDir() {
		return nil
	}

	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly deploy config is missing"))
	fmt.Fprintf(&b, "  %s\n", flyTomlPath)
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Run: %s\n", colorizeCLI(useColor, "\033[1;32m", "mar fly init <app.mar>"))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func resolveFlyAppName(app *model.App) (string, error) {
	defaultName := slugifyFlyAppName(app.AppName)
	if defaultName == "" {
		defaultName = "mar-app"
	}
	if !stdinIsTerminal() {
		return defaultName, nil
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly app name"))
	fmt.Printf("  This is the app name you will use on Fly.io.\n")
	fmt.Printf("  Press Enter to use %s\n", colorizeCLI(useColor, "\033[1;36m", defaultName))
	fmt.Printf("  %s ", colorizeCLI(useColor, "\033[1;36m", "Fly app name?"))

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", err
	}
	name := strings.TrimSpace(line)
	if name == "" {
		name = defaultName
	}
	if !isValidFlyAppName(name) {
		return "", fmt.Errorf("invalid Fly app name %q: use lowercase letters, numbers, and hyphens", name)
	}
	return name, nil
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func slugifyFlyAppName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	var out []rune
	var prevHyphen bool
	runes := []rune(trimmed)
	for i, r := range runes {
		switch {
		case r >= 'A' && r <= 'Z':
			if i > 0 && !prevHyphen {
				prev := runes[i-1]
				if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
					out = append(out, '-')
				}
			}
			out = append(out, r+'a'-'A')
			prevHyphen = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, r)
			prevHyphen = false
		default:
			if !prevHyphen && len(out) > 0 {
				out = append(out, '-')
				prevHyphen = true
			}
		}
	}
	slug := strings.Trim(string(out), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return slug
}

func isValidFlyAppName(value string) bool {
	return flyAppNameRe.MatchString(strings.TrimSpace(value))
}

func resolveFlyRegion() (flyRegion, error) {
	if code := strings.TrimSpace(os.Getenv("FLY_REGION")); code != "" {
		region, ok := findFlyRegion(code)
		if !ok {
			return flyRegion{}, fmt.Errorf("invalid FLY_REGION %q: use one of the supported Fly region codes", code)
		}
		return region, nil
	}
	if !stdinIsTerminal() {
		useColor := cliSupportsANSIStream(os.Stderr)
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly region is required"))
		fmt.Fprintf(&b, "  I need a Fly region to generate the deployment files.\n")
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Re-run this command in an interactive terminal, or export FLY_REGION with a valid Fly region code.\n")
		return flyRegion{}, styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly region"))
	fmt.Printf("  Choose the region where your Fly volume will be created.\n")
	fmt.Printf("  Pick the location closest to your users, then enter its code.\n\n")
	fmt.Printf(
		"  %s %s\n",
		colorizeCLI(useColor, "\033[1;36m", fmt.Sprintf("%-32s", "NAME")),
		colorizeCLI(useColor, "\033[1;36m", "CODE"),
	)

	lastContinent := ""
	for _, region := range flyRegions {
		if region.Continent != lastContinent {
			fmt.Println()
			fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1m", region.Continent))
			lastContinent = region.Continent
		}
		fmt.Printf("  %-32s %s\n", region.Name, colorizeCLI(useColor, "\033[1;36m", region.Code))
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("\n  %s ", colorizeCLI(useColor, "\033[1;36m", "Region code?"))
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return flyRegion{}, err
		}
		code := strings.ToLower(strings.TrimSpace(line))
		region, ok := findFlyRegion(code)
		if ok {
			return region, nil
		}
		fmt.Println()
		fmt.Printf("%s %q\n", colorizeCLI(useColor, "\033[1;31m", "Invalid Fly region"), code)
		fmt.Println()
		fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Printf("  Enter one of the region codes listed above, such as %s, %s, or %s.\n",
			colorizeCLI(useColor, "\033[1;32m", "gru"),
			colorizeCLI(useColor, "\033[1;32m", "iad"),
			colorizeCLI(useColor, "\033[1;32m", "fra"),
		)
	}
}

func findFlyRegion(code string) (flyRegion, bool) {
	normalized := strings.ToLower(strings.TrimSpace(code))
	for _, region := range flyRegions {
		if region.Code == normalized {
			return region, true
		}
	}
	return flyRegion{}, false
}

func resolveFlyDatabasePaths(databasePath, binaryName string) (string, string) {
	local := strings.TrimSpace(databasePath)
	base := filepath.Base(local)
	base = strings.TrimSpace(base)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = binaryName + ".db"
	}
	return local, filepath.ToSlash(filepath.Join("/data", base))
}

func renderFlyDockerfile(result flyInitResult) string {
	return strings.TrimSpace(fmt.Sprintf(`
# Generated by `+"`mar fly init`"+`.
# This image runs the Linux executable generated by Mar for Fly.io deployment.

FROM debian:bookworm-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY %s /app/%s

EXPOSE %d
CMD ["/app/%s", "serve"]
`, filepath.ToSlash(result.BinaryPath), result.BinaryName, result.Port, result.BinaryName)) + "\n"
}

func renderFlyToml(result flyInitResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by `mar fly init`.\n")
	fmt.Fprintf(&b, "# This file configures how this Mar app is deployed on Fly.io.\n\n")
	fmt.Fprintf(&b, "app = %q\n", result.FlyAppName)
	fmt.Fprintf(&b, "primary_region = %q\n\n", result.RegionCode)
	fmt.Fprintf(&b, "[build]\n")
	fmt.Fprintf(&b, "  dockerfile = %q\n\n", "Dockerfile")
	fmt.Fprintf(&b, "[env]\n")
	fmt.Fprintf(&b, "  %s = %q\n\n", flyDatabasePathEnv, result.DatabaseFly)
	fmt.Fprintf(&b, "[mounts]\n")
	fmt.Fprintf(&b, "  source = %q\n", result.VolumeName)
	fmt.Fprintf(&b, "  destination = %q\n\n", "/data")
	fmt.Fprintf(&b, "[http_service]\n")
	fmt.Fprintf(&b, "  internal_port = %d\n", result.Port)
	fmt.Fprintf(&b, "  force_https = true\n")
	fmt.Fprintf(&b, "  auto_stop_machines = %q\n", "stop")
	fmt.Fprintf(&b, "  auto_start_machines = true\n")
	fmt.Fprintf(&b, "  min_machines_running = 0\n\n")
	fmt.Fprintf(&b, "  [[http_service.checks]]\n")
	fmt.Fprintf(&b, "    method = %q\n", "GET")
	fmt.Fprintf(&b, "    path = %q\n", "/health")
	fmt.Fprintf(&b, "    interval = %q\n", "10s")
	fmt.Fprintf(&b, "    timeout = %q\n", "2s")
	fmt.Fprintf(&b, "    grace_period = %q\n\n", "10s")
	fmt.Fprintf(&b, "[[vm]]\n")
	fmt.Fprintf(&b, "  memory = %q\n", "1gb")
	fmt.Fprintf(&b, "  cpus = 1\n")
	fmt.Fprintf(&b, "  memory_mb = 1024\n")
	return b.String()
}

func looksLikePlaceholderEmail(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return false
	}
	placeholders := []string{
		"example",
		"yourdomain",
		"localhost",
		".local",
	}
	for _, token := range placeholders {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func placeholderEmailFlyInitError(email string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	fieldName := colorizeCLI(useColor, "\033[1m", "auth.email_from")
	emailValue := colorizeCLI(useColor, "\033[36m", email)
	url := colorizeCLI(useColor, "\033[34m", "https://mar-lang.dev/#advanced/deploy")
	authKeyword := colorizeCLI(useColor, "\033[1m", "auth")
	exampleValues := func(value string) string {
		return colorizeCLI(useColor, "\033[36m", value)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly init blocked"))
	fmt.Fprintf(&b, "  %s is still using a placeholder value: %s\n", fieldName, emailValue)
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  This app needs a real sender address before deploy.\n")
	fmt.Fprintf(&b, "  Otherwise users will not be able to receive login codes.\n")
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Configure SMTP with a real email provider and a real sender address.\n")
	fmt.Fprintf(&b, "  We currently recommend Resend for the simplest setup.\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  Example:\n")
	fmt.Fprintf(&b, "    %s {\n", authKeyword)
	fmt.Fprintf(&b, "      code_ttl_minutes %s\n", exampleValues("10"))
	fmt.Fprintf(&b, "      session_ttl_hours %s\n", exampleValues("24"))
	fmt.Fprintf(&b, "      email_transport %s\n", exampleValues("smtp"))
	fmt.Fprintf(&b, "      email_from %s\n", exampleValues("\"no-reply@yourdomain.com\""))
	fmt.Fprintf(&b, "      email_subject %s\n", exampleValues("\"Your login code\""))
	fmt.Fprintf(&b, "      smtp_host %s\n", exampleValues("\"smtp.resend.com\""))
	fmt.Fprintf(&b, "      smtp_port %s\n", exampleValues("587"))
	fmt.Fprintf(&b, "      smtp_username %s\n", exampleValues("\"resend\""))
	fmt.Fprintf(&b, "      smtp_password_env %s\n", exampleValues("\"RESEND_API_KEY\""))
	fmt.Fprintf(&b, "      smtp_starttls %s\n", exampleValues("true"))
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "\n")
	fmt.Fprintf(&b, "  Learn more:\n")
	fmt.Fprintf(&b, "    %s\n", url)

	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func flyCLIAvailable() bool {
	if _, err := exec.LookPath("fly"); err == nil {
		return true
	}
	if _, err := exec.LookPath("flyctl"); err == nil {
		return true
	}
	return false
}

func findFlyCommand() (string, error) {
	if _, err := exec.LookPath("fly"); err == nil {
		return "fly", nil
	}
	if _, err := exec.LookPath("flyctl"); err == nil {
		return "flyctl", nil
	}

	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly.io CLI is required"))
	fmt.Fprintf(&b, "  I could not find %q or %q in your PATH.\n", "fly", "flyctl")
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Install: %s\n", "https://fly.io/docs/flyctl/install/")
	return "", styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func printFlyInitSummary(inputPath string, result flyInitResult) {
	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly.io deployment"))
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Source:"), inputPath)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Fly app:"), result.FlyAppName)
	fmt.Printf("  %s %s (%s)\n", colorizeCLI(useColor, "\033[1;36m", "Fly region:"), result.RegionName, result.RegionCode)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Linux binary path:"), result.BinaryPath)
	if result.DatabaseLocal != "" && result.DatabaseFly != "" {
		fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "SQLite path:"), result.DatabaseLocal)
		fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Fly SQLite path:"), result.DatabaseFly)
	}
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;32m", "Created"))
	fmt.Printf("  %s\n", result.DockerfilePath)
	fmt.Printf("  %s\n", result.FlyTomlPath)

	if len(result.Notes) > 0 {
		fmt.Println()
		fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;36m", "Notes"))
		for _, note := range result.Notes {
			fmt.Printf("  %s\n", formatFlyInitNote(useColor, result, note))
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Println()
		fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;31m", "Warnings"))
		for _, warning := range result.Warnings {
			fmt.Printf("  %s\n", warning)
		}
	}

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Printf("  Run %s\n", colorizeCLI(useColor, "\033[1;32m", "mar fly provision "+inputPath))
	fmt.Printf("  %s\n", "That will log in to Fly.io if needed, create the app, create the volume, and set secrets.")
	fmt.Println()
}

func ensureFlyAuth(flyCmd string) error {
	check := exec.Command(flyCmd, "auth", "whoami")
	check.Stdout = nil
	check.Stderr = nil
	check.Stdin = nil
	if err := check.Run(); err == nil {
		return nil
	}
	return runFlyCLICommand(cliSupportsANSIStream(os.Stdout), "", "Fly login", flyCmd, "auth", "login")
}

func runFlyCLICommand(useColor bool, inputPath, title string, flyCmd string, args ...string) error {
	display := flyCmd
	if len(args) > 0 {
		display += " " + strings.Join(maskFlyCLIArgs(args), " ")
	}
	fmt.Printf("  %s\n", colorizeCLI(useColor, "\033[1;36m", title))
	fmt.Printf("    %s\n", colorizeCLI(useColor, "\033[1;32m", display))

	var captured bytes.Buffer
	cmd := exec.Command(flyCmd, args...)
	if shouldBufferFlyCLIOutput(args) {
		cmd.Stdout = &captured
		cmd.Stderr = &captured
	} else {
		cmd.Stdout = io.MultiWriter(os.Stdout, &captured)
		cmd.Stderr = io.MultiWriter(os.Stderr, &captured)
	}
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return formatFlyCLICommandError(useColor, inputPath, title, args, captured.String(), err)
	}
	if shouldPrintBufferedFlyCLIOutput(args) {
		if output := strings.TrimSpace(captured.String()); output != "" {
			fmt.Println(output)
		}
	}
	return nil
}

func formatFlyCLICommandError(useColor bool, inputPath, title string, args []string, output string, err error) error {
	trimmedOutput := strings.TrimSpace(output)

	if len(args) >= 3 && args[0] == "apps" && args[1] == "create" && strings.Contains(trimmedOutput, "Name has already been taken") {
		appName := args[2]
		initCommand := "mar fly init <app.mar>"
		provisionCommand := "mar fly provision <app.mar>"
		if strings.TrimSpace(inputPath) != "" {
			initCommand = "mar fly init " + inputPath
			provisionCommand = "mar fly provision " + inputPath
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly app name is already in use"))
		fmt.Fprintf(&b, "  Fly.io already has an app named %s.\n", colorizeCLI(useColor, "\033[1;36m", appName))
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Run %s again and pick a different Fly app name.\n", colorizeCLI(useColor, "\033[1;32m", initCommand))
		fmt.Fprintf(&b, "  Or edit %s and then rerun %s.\n", colorizeCLI(useColor, "\033[1;35m", "deploy/fly/fly.toml"), colorizeCLI(useColor, "\033[1;32m", provisionCommand))
		return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	if shouldBufferFlyCLIOutput(args) && trimmedOutput != "" {
		fmt.Println(trimmedOutput)
	}
	return err
}

func shouldBufferFlyCLIOutput(args []string) bool {
	return len(args) >= 2 && args[0] == "apps" && (args[1] == "create" || args[1] == "destroy")
}

func shouldPrintBufferedFlyCLIOutput(args []string) bool {
	return len(args) >= 2 && args[0] == "apps" && args[1] == "create"
}

func maskFlyCLIArgs(args []string) []string {
	masked := append([]string(nil), args...)
	if len(masked) >= 3 && masked[0] == "secrets" && masked[1] == "set" {
		for i := 2; i < len(masked); i++ {
			if strings.HasPrefix(masked[i], "-") {
				continue
			}
			if idx := strings.Index(masked[i], "="); idx > 0 {
				masked[i] = masked[i][:idx+1] + "********"
			}
		}
	}
	return masked
}

func resolveFlySecretValue(envName string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value, nil
	}
	if !stdinIsTerminal() {
		return "", fmt.Errorf("%s is required to set Fly secrets", envName)
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	fmt.Printf("\n  %s\n", colorizeCLI(useColor, "\033[1;36m", "SMTP secret"))
	fmt.Printf("    %s %s\n", "Enter the value for", colorizeCLI(useColor, "\033[1;36m", envName))
	fmt.Printf("    %s ", colorizeCLI(useColor, "\033[1;36m", "Value?"))
	value, err := readMaskedSecret(os.Stdin, os.Stdout)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%s cannot be empty", envName)
	}
	return value, nil
}

func readMaskedSecret(file *os.File, out io.Writer) (string, error) {
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		reader := bufio.NewReader(file)
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)

	return readMaskedSecretInput(file, out)
}

func readMaskedSecretInput(in io.Reader, out io.Writer) (string, error) {
	reader := bufio.NewReader(in)
	var chars []rune

	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				fmt.Fprintln(out)
				return string(chars), nil
			}
			return "", err
		}

		switch r {
		case '\r', '\n':
			fmt.Fprintln(out)
			return string(chars), nil
		case 3:
			fmt.Fprintln(out)
			return "", errFlySecretPromptInterrupted
		case 127, '\b':
			if len(chars) > 0 {
				chars = chars[:len(chars)-1]
				fmt.Fprint(out, "\b \b")
			}
		default:
			if r < 32 {
				continue
			}
			chars = append(chars, r)
			fmt.Fprint(out, "*")
		}
	}
}

func formatFlyInitNote(useColor bool, result flyInitResult, note string) string {
	formatted := note
	if result.SMTPPasswordEnv != "" {
		formatted = strings.ReplaceAll(formatted, result.SMTPPasswordEnv, colorizeCLI(useColor, "\033[1;36m", result.SMTPPasswordEnv))
	}
	return formatted
}

func readFlyAppName(flyTomlPath string) (string, error) {
	return readFlyTomlStringValue(flyTomlPath, "", "app")
}

func readFlyPrimaryRegion(flyTomlPath string) (string, error) {
	return readFlyTomlStringValue(flyTomlPath, "", "primary_region")
}

func readFlyVolumeName(flyTomlPath string) (string, error) {
	return readFlyTomlStringValue(flyTomlPath, "mounts", "source")
}

func readFlyTomlStringValue(flyTomlPath, section, key string) (string, error) {
	data, err := os.ReadFile(flyTomlPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", flyTomlPath, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	currentSection := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if currentSection != section {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)
		if value != "" {
			return value, nil
		}
		break
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to parse %s: %w", flyTomlPath, err)
	}
	if section == "" {
		return "", fmt.Errorf("missing %s entry in %s", key, flyTomlPath)
	}
	return "", fmt.Errorf("missing %s.%s entry in %s", section, key, flyTomlPath)
}
