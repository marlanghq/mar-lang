package ast

// EachChild calls visit once for every expression directly inside e, in
// source order. It does not recurse: a walker that wants the whole tree calls
// itself from visit, which is what lets it decide what to do at each node.
//
// A walker written against it cannot forget a kind: before it existed each
// walker carried its own copy of this switch, and a kind missing from a copy
// was a subtree that walker silently never saw. (Not every walker uses it yet:
// typecheck's walkAll and walkChildren still enumerate children themselves.)
//
// Literals, names, constructors and field accessors hold no expressions. A nil
// child (there is none in a parsed tree) is handed to visit as it is.
func EachChild(e Expr, visit func(Expr)) {
	switch n := e.(type) {
	case *EApp:
		visit(n.Fn)
		visit(n.Arg)
	case *EBinop:
		visit(n.Left)
		visit(n.Right)
	case *ELambda:
		visit(n.Body)
	case *EIf:
		visit(n.Cond)
		visit(n.Then)
		visit(n.Else)
	case *ECase:
		visit(n.Subject)
		for _, b := range n.Branches {
			visit(b.Body)
		}
	case *ELet:
		for _, b := range n.Bindings {
			visit(b.Body)
		}
		visit(n.Body)
	case *ETuple:
		for _, m := range n.Members {
			visit(m)
		}
	case *EList:
		for _, el := range n.Elements {
			visit(el)
		}
	case *ERecord:
		for _, f := range n.Fields {
			visit(f.Value)
		}
	case *ERecordUpdate:
		visit(n.Record)
		for _, f := range n.Fields {
			visit(f.Value)
		}
	case *EFieldAccess:
		visit(n.Record)
	case *ENegate:
		visit(n.Inner)
	}
}
