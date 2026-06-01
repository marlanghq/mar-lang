# vscode-mar

Language support for the [mar](../) programming language.

## Features

- Syntax highlighting for `.mar` files.
- Diagnostics (parse + type errors as squiggles).
- Hover (type signatures).
- Go-to-definition.
- Find references.
- Rename.
- Workspace + document symbols (outline, breadcrumbs, file picker).
- Completion (autocomplete top-level names with type info).
- Inlay hints (inferred types shown inline next to definitions
  without explicit signatures).
- Code actions:
  - Quick fix "Did you mean `X`?" for typos that are close to a known
    symbol.
  - Quick fix "Replace with `field`" for missing-field errors.
  - Refactor "Add type annotation" inserts the inferred signature
    above a value declaration.
- Format on save (calls `mar format` over the open document).

## Requirements

The `mar` binary must be on your `$PATH`, or you can override the path
in your settings:

```json
{
  "mar.serverPath": "/path/to/mar"
}
```

## Development

```bash
cd vscode-mar
npm install
npm run compile
```

Then in VSCode, **Run → Start Debugging** (F5) to launch an Extension
Development Host with the extension loaded. Open any `.mar` file to
activate it.

## Packaging

```bash
npm install
npm run compile
npm run package    # produces mar-language-support-X.Y.Z.vsix
code --install-extension mar-language-support-0.0.6.vsix
```
