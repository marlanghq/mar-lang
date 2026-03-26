# Mar Developer Tools

This extension adds syntax highlighting, snippets/autocomplete, and LSP features for `.mar` files.

Mar has a built-in `User` entity in every app, and entity operations are protected by default. The extension reflects that model in snippets and editor support.

## Features

- Syntax highlighting for:
- Mar declarations (`app`, `port`, `database`, `entity`, `auth`, `type alias`)
- Public assets config (`public`, `dir`, `mount`, `spa_fallback`)
- System config (`system`, `request_logs_buffer`, `http_max_request_body_mb`, auth rate limits, security headers like `security_frame_policy`/`security_referrer_policy`/`security_content_type_nosniff`, and `sqlite_*` options like `sqlite_mmap_size_mb` and `sqlite_cache_size_kb`)
- Rule/authz keywords (`rule`, `expect`, `when`, `authorize`)
- Relationship syntax (`belongs_to User`, `belongs_to current_user`, `belongs_to reviewer: current_user`, `belongs_to customer: User optional`)
- `belongs_to current_user` ownership flows, where Mar fills the built-in `user` relationship automatically
- Named ownership relationships such as `belongs_to reviewer: current_user`, where Mar fills the chosen logical field automatically
- Action syntax (`action <name> { input: Alias ... todo = load Todo { ... } updatedTodo = update Todo { ... } }`)
- Auth config keys (`code_ttl_minutes`, `session_ttl_hours`, `email_transport`, etc.)
- Built-in `User` entity support and auth-aware snippets
- Field modifiers (`primary`, `auto`, `optional`, `default`)
- Built-in types (`Int`, `String`, `Bool`, `Float`, `Posix`)
- Built-in functions (`contains`, `starts_with`, `ends_with`, `length`, `matches`)
- Context variables (`input`, `input.field`, action aliases like `todo.field`, `user_authenticated`, `user_email`, `user_id`, `user_role`)
- Comments (`--`), strings, numbers, booleans, null, operators, and punctuation
- `Posix` follows Elm's `Time.Posix` convention and represents Unix milliseconds
- `default` lets you assign literal field defaults such as `done: Bool default false`
- Snippets/autocomplete (examples):
- `app`
- `entity`
- `field`
- `belongs_to`
- `belongs_to_current_user`
- `belongs_to_named_current_user`
- `belongs_to_named`
- `rule`
- `authorize`
- `auth`
- `User`
- `public`
- `system`
- `authzcrud`
- `typealias`
- `action`
- `load`
- `create`
- `update`
- `delete`
- `actioncreate`
- Database path tip:
- Use `database "app.db"` for a simple relative path.
- Relative paths are resolved from the process working directory.
- LSP (via `mar lsp`):
- Parse diagnostics while editing
- Keyword completions
- Go to definition
- Find references
- Rename symbol
- Hover documentation
- Document symbols (Outline)
- Quick fixes (code actions)
- Format document support

## Install in VSCode

1. Open Extensions in VSCode.
2. Search for `Mar Developer Tools`.
3. Click `Install`.

If needed, set `mar.languageServer.path` in VSCode settings (examples: `mar`, `/abs/path/to/mar`).

## Format on Save

1. Open VS Code settings (`settings.json`) and configure:

```json
{
  "[mar]": {
    "editor.defaultFormatter": "mar-lang.mar-language-support",
    "editor.formatOnSave": true
  }
}
```

2. Save a `.mar` file to apply Mar formatting automatically.

## Notes

- Keep `mar` available in your `PATH` so the extension can start LSP and formatting.
