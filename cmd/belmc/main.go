package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"belm/internal/model"
	"belm/internal/parser"
	"belm/internal/runtime"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "compile":
		if len(args) != 3 {
			return errors.New("usage: belmc compile <input.belm> <output.json>")
		}
		app, err := parseBelmFile(args[1])
		if err != nil {
			return err
		}
		return writeManifest(args[2], app)
	case "serve":
		if len(args) != 2 {
			return errors.New("usage: belmc serve <input.belm>")
		}
		app, err := parseBelmFile(args[1])
		if err != nil {
			return err
		}
		return serveApp(app)
	case "serve-manifest":
		if len(args) != 2 {
			return errors.New("usage: belmc serve-manifest <manifest.json>")
		}
		app, err := readManifest(args[1])
		if err != nil {
			return err
		}
		return serveApp(app)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseBelmFile(path string) (*model.App, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parser.Parse(string(content))
}

func writeManifest(path string, app *model.App) error {
	if app == nil {
		return errors.New("nil app")
	}
	data, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("Compiled %s\n", path)
	return nil
}

func readManifest(path string) (*model.App, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var app model.App
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, err
	}
	if app.AppName == "" {
		return nil, errors.New("manifest missing appName")
	}
	return &app, nil
}

func serveApp(app *model.App) error {
	r, err := runtime.New(app)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.Serve(context.Background())
}

func printUsage() {
	fmt.Println("belmc commands:")
	fmt.Println("  belmc compile <input.belm> <output.json>")
	fmt.Println("  belmc serve <input.belm>")
	fmt.Println("  belmc serve-manifest <manifest.json>")
}
