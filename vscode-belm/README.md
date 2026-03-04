# Belm Language Support (VS Code)

This extension adds syntax highlighting, snippets/autocomplete, and LSP features for `.belm` files.

## Features

- Syntax highlighting for:
- Belm declarations (`app`, `port`, `database`, `entity`, `auth`, `type alias`)
- Public assets config (`public`, `dir`, `mount`, `spa_fallback`)
- System config (`system`, `request_logs_buffer`)
- Rule/authz keywords (`rule`, `when`, `authorize`)
- Action syntax (`action <name> { input: Alias ... create Entity { ... } }`)
- Auth config keys (`user_entity`, `email_field`, etc.)
- Field modifiers (`primary`, `auto`, `optional`)
- Built-in types (`Int`, `String`, `Bool`, `Float`)
- Built-in functions (`contains`, `startsWith`, `endsWith`, `len`, `matches`, `isRole`)
- Context variables (`input`, `input.field`, `auth_authenticated`, `auth_email`, `auth_user_id`, `auth_role`)
- Comments (`--` and `#`), strings, numbers, booleans, null, operators, and punctuation
- Snippets/autocomplete (examples):
- `app`
- `entity`
- `field`
- `rule`
- `authorize`
- `auth`
- `public`
- `system`
- `authzcrud`
- `typealias`
- `action`
- `create`
- `actioncreate`
- Database path tip:
- Use `database "app.db"` for a simple relative path.
- Relative paths are resolved from the process working directory.
- LSP (via `belm lsp`):
- Parse diagnostics while editing
- Keyword completions
- Go to definition
- Find references
- Rename symbol
- Hover documentation
- Document symbols (Outline)
- Quick fixes (code actions)
- Format document support

## Run Locally (Development Host)

1. Build Belm from the repo root:
   - `go build -o belm ./cmd/belm`
2. Open this folder in VS Code:
   - `/Users/marcio/dev/github/belm/vscode-belm`
3. Install extension dependencies:
   - `npm install`
4. Press `F5` to start an Extension Development Host window.
5. Open any `.belm` file in the new window.

If needed, set `belm.languageServer.path` in VS Code settings (examples: `belm`, `/abs/path/to/belm`).

## Format on Save

1. Open VS Code settings (`settings.json`) and configure:

```json
{
  "[belm]": {
    "editor.defaultFormatter": "belm-dev.belm-language-support",
    "editor.formatOnSave": true
  }
}
```

2. Save a `.belm` file to apply Belm formatting automatically.

## Package for Installation

1. From this folder, create a package with `npx` (no global install needed):
   - `npx @vscode/vsce package`
2. Install the generated `.vsix` in VS Code:
   - Command Palette -> `Extensions: Install from VSIX...`
