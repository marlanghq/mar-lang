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

export function registerTools(context: ExtensionContext, client: LanguageClient) {
  context.subscriptions.push(
    lm.registerTool("mar_reference", new MarReferenceTool(client)),
  );
}
