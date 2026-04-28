// Package scaffold creates new mar project layouts. `mar init <name>`
// asks for a directory name, drops a minimal Main.mar / mar.json so the
// user can `mar dev <name>` immediately and see something running.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// Init creates a new project at <name>/ with a minimal counter
// scaffold and a mar.json. Errors if the directory already exists.
func Init(path string) error {
	if path == "" {
		return fmt.Errorf("project name is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	// Use just the directory name (not the full path) for the manifest's
	// "name" field — that's what humans want to see when they edit it.
	name := filepath.Base(path)

	files := map[string]string{
		"mar.json": fmt.Sprintf(`{
  "name": "%s",
  "entry": "Main.mar",
  "server": {
    "port": 3000
  }
}
`, name),
		"Main.mar": `module Main exposing (main)


-- A starter mar app: a counter you can run with ` + "`mar dev`" + `.
-- Add backend routes (App.fullstack) or more pages (App.frontend
-- with multiple Page.create entries) as the project grows.


import View exposing (column, title, text, button, center, paddingTop, spacing)


type alias Model = Int


type Msg
    = Increment
    | Decrement


init : () -> (Model, Effect String Msg)
init _ =
    (0, Effect.none)


update : Msg -> Model -> (Model, Effect String Msg)
update msg model =
    case msg of
        Increment ->
            (model + 1, Effect.none)

        Decrement ->
            (model - 1, Effect.none)


view : Model -> View Msg
view model =
    column [ spacing 30, center, paddingTop 70 ]
        [ title [] "Counter"
        , button [ center ] Increment "+"
        , text [ center ] (String.fromInt model)
        , button [ center ] Decrement "-"
        ]


-- Top-level binding so App.frontend can ship just the relevant module.
page : Page
page = Page.root init update view


main : Effect String ()
main =
    App.frontend [page]
`,
		".gitignore": `*.db
*.db-shm
*.db-wal
dist/
`,
		"README.md": fmt.Sprintf("# %s\n\nA mar project. Get started:\n\n```bash\nmar dev\n```\n\nOpens http://localhost:3000.\n", name),
	}

	for relPath, content := range files {
		full := filepath.Join(path, relPath)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
