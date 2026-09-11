// The tools a language model working in this editor can call.
//
// Mar is in no training data. A model writing it here is guessing at a stdlib
// it has never seen, and it guesses from the languages it HAS seen: List.foldr
// with Haskell's argument order, a Basics.not that does not compile, a Float
// that does not exist in Mar at all. Every one of those compiles in the
// model's head and fails in the editor.
//
// What it is missing is not prose. It is ground truth, and this repository has
// two pieces of it already: a reference whose every example is evaluated by a
// test that requires it to be True, and a checker. This is the bridge from one
// to the other, and it is thin on purpose -- the knowledge stays in the
// language server, where hover and completion already read it.

import {
  CancellationToken,
  ExtensionContext,
  LanguageModelTextPart,
  LanguageModelTool,
  LanguageModelToolInvocationOptions,
  LanguageModelToolResult,
  lm,
  workspace,
} from "vscode";
import { LanguageClient } from "vscode-languageclient/node";

interface DescribedSymbol {
  name: string;
  kind: string;
  type?: string;
  doc?: string;
  examples?: string[];
}

interface DescribeResult {
  exact?: DescribedSymbol;
  matches?: DescribedSymbol[];
  total: number;
}

function render(s: DescribedSymbol): string {
  const lines: string[] = [];
  lines.push(s.type ? `${s.name} : ${s.type}` : s.name);
  if (s.doc) {
    lines.push("", s.doc);
  }
  if (s.examples?.length) {
    lines.push("", ...s.examples.map((e) => e.replace(" == ", "    -- ")));
  }
  return lines.join("\n");
}

class MarReferenceTool implements LanguageModelTool<{ name: string }> {
  constructor(private readonly client: LanguageClient) {}

  async invoke(
    options: LanguageModelToolInvocationOptions<{ name: string }>,
    token: CancellationToken,
  ): Promise<LanguageModelToolResult> {
    const name = (options.input?.name ?? "").trim();
    if (!name) {
      return new LanguageModelToolResult([
        new LanguageModelTextPart("Ask for a name, e.g. `List.map` or `not`."),
      ]);
    }

    let res: DescribeResult;
    try {
      res = await this.client.sendRequest<DescribeResult>(
        "mar/describe",
        { name },
        token,
      );
    } catch (err) {
      // The server not being up is a fact worth reporting rather than an
      // empty answer that reads like "Mar has no such name".
      return new LanguageModelToolResult([
        new LanguageModelTextPart(
          `The Mar language server did not answer (${String(err)}). ` +
            `Its reference is the only source for this, so nothing is known here rather than guessed.`,
        ),
      ]);
    }

    const out: string[] = [];
    if (res.exact) {
      out.push(render(res.exact));
    }
    if (res.matches?.length) {
      if (out.length) out.push("", "Related:");
      out.push(...res.matches.map((m) => (m.type ? `${m.name} : ${m.type}` : m.name)));
      if (res.total > res.matches.length) {
        out.push(`(${res.total} matched; showing ${res.matches.length})`);
      }
    }
    if (!out.length) {
      // Saying nothing exists is the useful answer here: it is what stops the
      // caller inventing a plausible signature for a name Mar does not have.
      out.push(`Mar has no name matching \`${name}\`.`);
    }
    return new LanguageModelToolResult([new LanguageModelTextPart(out.join("\n"))]);
  }

  prepareInvocation(options: LanguageModelToolInvocationOptions<{ name: string }>) {
    return { invocationMessage: `Looking up ${options.input?.name ?? "the Mar reference"}` };
  }
}

interface CheckError {
  file?: string;
  line?: number;
  column?: number;
  message: string;
  stage: string;
}

interface CheckResult {
  ok: boolean;
  root?: string;
  modules?: number;
  errors?: CheckError[];
}

class MarCheckTool implements LanguageModelTool<{ path?: string }> {
  constructor(private readonly client: LanguageClient) {}

  async invoke(
    options: LanguageModelToolInvocationOptions<{ path?: string }>,
    token: CancellationToken,
  ): Promise<LanguageModelToolResult> {
    // Whatever the caller names, or the workspace. A model has a project in
    // mind and not necessarily a path to its root; the server resolves either
    // to the project it belongs to.
    const root =
      options.input?.path?.trim() ||
      workspace.workspaceFolders?.[0]?.uri.fsPath ||
      "";
    if (!root) {
      return new LanguageModelToolResult([
        new LanguageModelTextPart("No Mar project is open, and no path was given."),
      ]);
    }

    let res: CheckResult;
    try {
      res = await this.client.sendRequest<CheckResult>("mar/check", { root }, token);
    } catch (err) {
      return new LanguageModelToolResult([
        new LanguageModelTextPart(
          `The Mar language server did not answer (${String(err)}). ` +
            `Nothing is known about this project rather than assumed to compile.`,
        ),
      ]);
    }

    if (res.ok) {
      const n = res.modules ?? 0;
      return new LanguageModelToolResult([
        new LanguageModelTextPart(
          `The project checks clean: ${n} module${n === 1 ? "" : "s"}, no errors. ` +
            `This is the same check \`mar check\` runs, including the boot checks that ` +
            `need the program evaluated.`,
        ),
      ]);
    }

    const lines = (res.errors ?? []).map((e) => {
      const at = e.file ? `${e.file}${e.line ? `:${e.line}:${e.column ?? 0}` : ""}` : "";
      // The stage is on the line because what to do next depends on it: a
      // typecheck error is in one file, a boot error is about how the program
      // is assembled, and a manifest error is in mar.json.
      return `[${e.stage}] ${at ? at + " " : ""}${e.message}`;
    });
    if (!lines.length) {
      lines.push("The check failed without saying why, which is a bug in Mar rather than in your code.");
    }
    return new LanguageModelToolResult([new LanguageModelTextPart(lines.join("\n"))]);
  }

  prepareInvocation() {
    return { invocationMessage: "Checking the Mar project" };
  }
}

export function registerTools(context: ExtensionContext, client: LanguageClient) {
  context.subscriptions.push(
    lm.registerTool("mar_reference", new MarReferenceTool(client)),
    lm.registerTool("mar_check", new MarCheckTool(client)),
  );
}
