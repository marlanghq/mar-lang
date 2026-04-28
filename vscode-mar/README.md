# vscode-mar

Language support for the [mar](../) programming language.

## Features

- Syntax highlighting for `.mar` files (keywords, types, strings, comments).
- Diagnostics: parse + type errors appear as squiggles in real time
  (powered by `mar lsp` over stdio).

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
npm run package    # produces vscode-mar-X.Y.Z.vsix
code --install-extension vscode-mar-0.1.0.vsix
```
