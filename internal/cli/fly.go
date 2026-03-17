package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"mar/internal/model"
)

const flyDatabasePathEnv = "MAR_DATABASE_PATH"

var flyAppNameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

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
	FlyAppName       string
	VolumeName       string
	RegionCode       string
	RegionName       string
	Port             int
	BuildRoot        string
	BinaryName       string
	BinaryPath       string
	DatabaseLocal    string
	DatabaseFly      string
	DockerfilePath   string
	FlyTomlPath      string
	SMTPPasswordEnv  string
	EmailFrom        string
	Warnings         []string
	Notes            []string
}

func runFly(binaryName string, args []string) error {
	if len(args) == 0 {
		return flyUsageError(binaryName)
	}
	switch args[0] {
	case "init":
		if len(args) != 2 && len(args) != 3 {
			return flyUsageError(binaryName)
		}
		return runFlyInit(binaryName, args[1], strings.TrimSpace(optionalArg(args, 2)))
	case "deploy":
		if len(args) != 2 {
			return flyUsageError(binaryName)
		}
		return runFlyDeploy(args[1])
	default:
		return flyUsageError(binaryName)
	}
}

func flyUsageError(binaryName string) error {
	useColor := cliSupportsANSIStream(os.Stderr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", colorizeCLI(useColor, "\033[1;31m", "Fly usage"))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly init <app.mar> [fly-app-name]", binaryName))
	fmt.Fprintf(&b, "  %s\n", fmt.Sprintf("%s fly deploy <app.mar>", binaryName))
	fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
	fmt.Fprintf(&b, "  Prepare Fly.io deployment files with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s fly init <app.mar>", binaryName)))
	fmt.Fprintf(&b, "  Deploy the current app with: %s\n", colorizeCLI(useColor, "\033[1;32m", fmt.Sprintf("%s fly deploy <app.mar>", binaryName)))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func optionalArg(args []string, index int) string {
	if index >= 0 && index < len(args) {
		return args[index]
	}
	return ""
}

func runFlyInit(binaryName, inputPath, requestedAppName string) error {
	app, err := parseMarFile(inputPath)
	if err != nil {
		return err
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
	}

	flyAppName, err := resolveFlyAppName(app, requestedAppName)
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
	if err := buildFlyLinuxExecutable(app, outputPath, buildOptions{PrintSummary: false, SourcePath: inputPath}); err != nil {
		return err
	}

	if err := os.MkdirAll(flyDir, 0o755); err != nil {
		return err
	}

	dbLocal, dbFly := resolveFlyDatabasePaths(app.Database, outputName)
	volumeName := outputName + "_data"
	result := flyInitResult{
		FlyAppName:       flyAppName,
		BuildRoot:        buildRoot,
		BinaryName:       outputName,
		BinaryPath:       outputPath,
		RegionCode:       region.Code,
		RegionName:       region.Name,
		Port:             app.Port,
		DatabaseLocal:    dbLocal,
		DatabaseFly:      dbFly,
		DockerfilePath:   filepath.Join(flyDir, "Dockerfile"),
		FlyTomlPath:      filepath.Join(flyDir, "fly.toml"),
		VolumeName:       volumeName,
	}
	if app.Auth != nil {
		result.SMTPPasswordEnv = strings.TrimSpace(app.Auth.SMTPPasswordEnv)
		result.EmailFrom = strings.TrimSpace(app.Auth.EmailFrom)
		if looksLikePlaceholderEmail(result.EmailFrom) {
			return placeholderEmailFlyInitError(result.EmailFrom)
		}
		if result.SMTPPasswordEnv != "" {
			result.Notes = append(result.Notes, fmt.Sprintf("SMTP password will be read from the %s environment variable at runtime.", result.SMTPPasswordEnv))
		}
	}
	if dbLocal != dbFly {
		result.Notes = append(result.Notes, fmt.Sprintf("SQLite path will be rewritten for Fly: %s -> %s", dbLocal, dbFly))
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
		fmt.Fprintf(&b, "  Running fly init again will delete that folder and recreate all Fly.io deployment files.\n")
		fmt.Fprintf(&b, "\n%s\n", colorizeCLI(useColor, "\033[1;33m", "Hint:"))
		fmt.Fprintf(&b, "  Re-run this command in an interactive terminal to confirm the overwrite.\n")
		return false, styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
	}

	useColor := cliSupportsANSIStream(os.Stdout)
	coloredFlyDir := colorizeCLI(useColor, "\033[1;35m", flyDir)
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Fly deploy files already exist"))
	fmt.Printf("  %s already contains files.\n", coloredFlyDir)
	fmt.Printf("  Running fly init again will delete that folder and recreate all Fly.io deployment files.\n")
	fmt.Printf("  %s ", colorizeCLI(useColor, "\033[1;36m", "Continue? [y/N]"))

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
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

func runFlyDeploy(inputPath string) error {
	useColor := cliSupportsANSIStream(os.Stdout)

	app, err := parseMarFile(inputPath)
	if err != nil {
		return err
	}

	buildRoot, outputName := defaultBuildLayout(inputPath, "")
	outputPath := targetOutputPath(buildRoot, outputName, runtimeTarget{OS: "linux", Arch: "amd64"})
	flyDir := filepath.Join("deploy", "fly")
	dockerfilePath := filepath.Join(flyDir, "Dockerfile")
	flyTomlPath := filepath.Join(flyDir, "fly.toml")

	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly deploy"))
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "App source:"), inputPath)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Config:"), flyTomlPath)

	if err := requireFlyDeployFiles(dockerfilePath, flyTomlPath); err != nil {
		return err
	}

	flyAppName, err := readFlyAppName(flyTomlPath)
	if err != nil {
		return err
	}

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

	flyCmd, err := findFlyCommand()
	if err != nil {
		return err
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
		appURL := "https://" + flyAppName + ".fly.dev/"
		fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Opening:"), colorizeCLI(useColor, "\033[34m", appURL))
		if err := openBrowser(appURL); err != nil {
			fmt.Printf("  %s\n", "Open that URL manually if needed.")
		}
	}
	fmt.Println()
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
	fmt.Fprintf(&b, "  Run: %s\n", colorizeCLI(useColor, "\033[1;32m", "mar fly init <app.mar>"))
	return styledCLIError(strings.TrimRight(b.String(), "\n") + "\n")
}

func resolveFlyAppName(app *model.App, requested string) (string, error) {
	if name := strings.TrimSpace(requested); name != "" {
		if !isValidFlyAppName(name) {
			return "", fmt.Errorf("invalid Fly app name %q: use lowercase letters, numbers, and hyphens", name)
		}
		return name, nil
	}

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
	fmt.Printf("  It should match the name you will pass later to %s\n", colorizeCLI(useColor, "\033[1;32m", "fly apps create"))
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
	printStep := func(number int, command string) {
		fmt.Printf("  %d. %s\n", number, colorizeCLI(useColor, "\033[1;32m", command))
	}
	fmt.Println()
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1m", "Fly.io deployment"))
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Source:"), inputPath)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Fly app:"), result.FlyAppName)
	fmt.Printf("  %s %s (%s)\n", colorizeCLI(useColor, "\033[1;36m", "Fly region:"), result.RegionName, result.RegionCode)
	fmt.Printf("  %s %s\n", colorizeCLI(useColor, "\033[1;36m", "Linux binary:"), result.BinaryPath)
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
	fmt.Printf("%s\n", colorizeCLI(useColor, "\033[1;33m", "Next steps"))
	cliValueAccent := "\033[1;36m"
	printStep(1, "fly auth login")
	printStep(2, "fly apps create "+colorizeCLI(useColor, cliValueAccent, result.FlyAppName))
	printStep(3, "fly volumes create "+colorizeCLI(useColor, cliValueAccent, result.VolumeName)+" --region "+colorizeCLI(useColor, cliValueAccent, result.RegionCode)+" --size "+colorizeCLI(useColor, cliValueAccent, "1")+" -a "+colorizeCLI(useColor, cliValueAccent, result.FlyAppName))
	if result.SMTPPasswordEnv != "" {
		printStep(4, "fly secrets set "+result.SMTPPasswordEnv+"="+colorizeCLI(useColor, cliValueAccent, "<your-api-key>")+" -a "+colorizeCLI(useColor, cliValueAccent, result.FlyAppName))
		printStep(5, "mar fly deploy "+colorizeCLI(useColor, cliValueAccent, inputPath))
	} else {
		printStep(4, "mar fly deploy "+colorizeCLI(useColor, cliValueAccent, inputPath))
	}
	fmt.Println()
}

func formatFlyInitNote(useColor bool, result flyInitResult, note string) string {
	formatted := note
	if result.SMTPPasswordEnv != "" {
		formatted = strings.ReplaceAll(formatted, result.SMTPPasswordEnv, colorizeCLI(useColor, "\033[1;36m", result.SMTPPasswordEnv))
	}
	return formatted
}

func readFlyAppName(flyTomlPath string) (string, error) {
	data, err := os.ReadFile(flyTomlPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", flyTomlPath, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "app") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != "app" {
			continue
		}
		return strings.Trim(strings.TrimSpace(parts[1]), `"`), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to parse %s: %w", flyTomlPath, err)
	}
	return "", fmt.Errorf("missing app entry in %s", flyTomlPath)
}
