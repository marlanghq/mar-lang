# mar-lang

- Official website: [https://mar-lang.dev](https://mar-lang.dev)
- Website source: [website/index.html](website/index.html)
- VSCode Extension Guide: [vscode-mar/README.md](vscode-mar/README.md)
- Official Sublime Text package: [marlanghq/mar-sublime](https://github.com/marlanghq/mar-sublime)
- Official Sublime Text LSP package: [marlanghq/mar-sublime-lsp](https://github.com/marlanghq/mar-sublime-lsp)

Examples:

- [examples/shared-todo.mar](examples/shared-todo.mar)
- [examples/personal-todo.mar](examples/personal-todo.mar)
- [examples/store.mar](examples/store.mar)

## Temporal types

Mar has two built-in temporal field types:

- `Date` for calendar-day values such as birthdays
- `DateTime` for timestamp values that include date and time

These types are semantic conveniences in the language and Admin UI. Internally,
both are stored as POSIX Unix milliseconds, and JSON responses also return both
as POSIX Unix milliseconds.

`Date` values are normalized to `00:00 UTC` for the selected day.

Note: parts of this project were developed with the assistance of generative AI tools.
