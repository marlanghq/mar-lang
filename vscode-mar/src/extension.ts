// VSCode extension entry point. Launches `mar lsp` over stdio and forwards
// LSP messages to the editor's language client.

import {
  ExtensionContext,
  workspace,
  window,
} from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function activate(context: ExtensionContext) {
  const config = workspace.getConfiguration("mar");
  const command = config.get<string>("serverPath", "mar");

  const serverOptions: ServerOptions = {
    run: { command, args: ["lsp"], transport: TransportKind.stdio },
    debug: { command, args: ["lsp"], transport: TransportKind.stdio },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "mar" }],
    synchronize: {
      // Re-evaluate when the user edits mar.json — affects port,
      // not type-checking, but reasonable to refresh.
      fileEvents: workspace.createFileSystemWatcher("**/mar.json"),
    },
    outputChannelName: "mar",
  };

  client = new LanguageClient("mar", "mar", serverOptions, clientOptions);

  client.start().catch((err: Error) => {
    window.showErrorMessage(
      `Failed to start mar language server (${command} lsp): ${err.message}. ` +
        `Set "mar.serverPath" if mar isn't on $PATH.`,
    );
  });
}

export function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}
