package lsp

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"mar/internal/diag"
	"mar/internal/project"
	"mar/internal/scaffold"
)

// `mar/check` answers "does this project compile", for a caller that cannot
// read squiggles: a language model writing Mar in the editor.
//
// It is not the same question hover and diagnostics answer. Those are per
// DOCUMENT, and the errors that matter most in a Mar project are the ones no
// single document holds:
//
//	a name a sibling module does not export   needs every module loaded
//	two pages declared on one path            needs `main` EVALUATED, because
//	                                          the page list is a value the
//	                                          program builds
//
// So this runs what `mar check .` runs, in the same order and with the same
// sentences: load and type-check every module, validate mar.json, then the
// boot checks.
//
// Three things about that are not free, and are why this is a request rather
// than a line in analyze():
//
//   - It EVALUATES the program's top level. On the biggest example here that
//     is about a second and a half, because that project's top-level values
//     are a game's map data.
//   - Evaluating runs code, and the loader resets global runtime state first
//     (the entity registry, the Path enum table). So it cannot run beside the
//     per-document analysis, which loads the project too: evalMu serializes
//     them.
//   - The message loop is single-threaded, so a check that took the loop with
//     it would freeze hover and completion for its whole duration. This
//     answers from a goroutine; responses carry their own id, so arriving out
//     of order is what the protocol expects.
type checkParams struct {
	// Root is the project directory. A file path is accepted too and resolved
	// to the project it belongs to, because a caller that has an open file
	// does not necessarily know where the project starts.
	Root string `json:"root"`
}

type checkError struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
	// Stage says which of the three the error came from, because the answer to
	// "what do I do now" differs: a type error is in one file, and a boot
	// error is about how the program is assembled.
	Stage string `json:"stage"`
}

type checkResult struct {
	OK      bool         `json:"ok"`
	Root    string       `json:"root,omitempty"`
	Modules int          `json:"modules,omitempty"`
	Errors  []checkError `json:"errors,omitempty"`
}

func (s *Server) handleCheck(msg *message) {
	var p checkParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.respond(msg.ID, checkResult{Errors: []checkError{{
			Message: "mar/check: could not read the request",
			Stage:   "request",
		}}})
		return
	}
	id := msg.ID
	go func() {
		defer func() {
			// A panic here would take the editor's language server with it,
			// and the editor is the one process the user does not restart.
			if rec := recover(); rec != nil {
				s.respond(id, checkResult{Errors: []checkError{{
					Message: "mar/check: the checker panicked; this is a bug in Mar rather than in your code",
					Stage:   "internal",
				}}})
			}
		}()
		s.respond(id, s.checkProject(p.Root))
	}()
}

func (s *Server) checkProject(root string) checkResult {
	if root == "" {
		return checkResult{Errors: []checkError{{
			Message: "mar/check: no project given",
			Stage:   "request",
		}}}
	}
	resolved, ok := project.RootFor(root)
	if !ok {
		return checkResult{Errors: []checkError{{
			Message: "mar/check: " + root + " is not inside a Mar project (no mar.json or Main.mar above it)",
			Stage:   "request",
		}}}
	}

	// One whole-program evaluation at a time, and never beside the
	// per-document analysis: both load the project, and loading resets global
	// runtime state.
	s.evalMu.Lock()
	defer s.evalMu.Unlock()

	res := checkResult{Root: resolved}

	proj, err := project.Load(resolved)
	if err != nil {
		res.Errors = append(res.Errors, describeCheckError(err, resolved, "typecheck"))
		return res
	}
	res.Modules = len(proj.Modules)

	if _, err := project.LoadManifestStructure(resolved); err != nil {
		res.Errors = append(res.Errors, describeCheckError(err, resolved, "manifest"))
		return res
	}

	if _, err := scaffold.CheckBoot(resolved); err != nil {
		res.Errors = append(res.Errors, describeCheckError(err, resolved, "boot"))
		return res
	}

	res.OK = true
	return res
}

// describeCheckError keeps the position and the FILE when the error carries
// them, and the sentence either way. The sentences are the CLI's own: a caller
// comparing this against `mar check` should not find two vocabularies.
//
// The file comes from diag.SourceError, which the project loader wraps every
// positioned error in, because a position alone does not say WHICH file. An
// earlier draft of this filled in Main.mar whenever there was a line number,
// which is right for a one-module project and a lie for every other: a type
// error in Shared.mar would have been reported against a file that does not
// contain it. A wrong filename is worse than none.
func describeCheckError(err error, root, stage string) checkError {
	out := checkError{Message: err.Error(), Stage: stage}
	var src *diag.SourceError
	if errors.As(err, &src) {
		out.File = src.Filename
		if !filepath.IsAbs(out.File) {
			out.File = filepath.Join(root, out.File)
		}
	}
	for wrapped := error(err); wrapped != nil; wrapped = errors.Unwrap(wrapped) {
		if line, col, msg, ok := extractPositioned(wrapped); ok {
			out.Line, out.Column, out.Message = line, col, msg
			break
		}
	}
	return out
}
