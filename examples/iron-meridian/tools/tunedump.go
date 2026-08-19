//go:build ignore

// Temporary: serialize .mar modules (in dependency order) so the offline
// renderer can evaluate them. Mirrors project.checkOrdered's env threading,
// because checking is what ELABORATES the tree the runtime loads.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

func main() {
	tEnv := typecheck.BaseEnv()
	aliasesBy := map[string]map[string]typecheck.TypeAlias{}
	customsBy := map[string]map[string]typecheck.CustomType{}
	var mods []any

	for _, f := range os.Args[1:] {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		mod, err := parser.Parse(string(src))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: parse: %v\n", f, err)
			os.Exit(1)
		}
		name := strings.Join([]string(mod.Name), ".")

		impAliases := map[string]typecheck.TypeAlias{}
		impCustoms := map[string]typecheck.CustomType{}
		for _, imp := range mod.Imports {
			in := strings.Join([]string(imp.Module), ".")
			for k, v := range aliasesBy[in] {
				impAliases[in+"."+k] = v
			}
			for k, v := range customsBy[in] {
				impCustoms[in+"."+k] = v
			}
		}
		res, err := typecheck.CheckModuleWith(mod, tEnv, impAliases, impCustoms)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: check: %v\n", f, err)
			os.Exit(1)
		}
		for vn, t := range res.ValueTypes {
			tEnv.Define(name+"."+vn, t)
		}
		aliasesBy[name] = res.TypeAliases
		customsBy[name] = res.CustomTypes
		mods = append(mods, jsserve.SerializeModule(mod))
		_ = ast.Module{}
	}
	out, _ := json.Marshal(map[string]any{"modules": mods})
	os.Stdout.Write(out)
}
