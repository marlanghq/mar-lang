package runtime

import (
	"strings"
	"testing"
)

// `title` is OPTIONAL, so it cannot be a field of the page record's TYPE — a
// field in a record type is required, and a page may have none. The type is
// an open record, so its tail accepts anything at all, and two mistakes went
// through `mar check` AND a clean boot:
//
//	title = 42          the title silently became the module name
//	titel = "Notes"     the typo was accepted, ignored, same result
//
// Both are facts about the record the program wrote, and the boot knows every
// field a page may carry.

func pageRec(extra map[string]Value) VRecord {
	rec := VRecord{Fields: map[string]Value{
		"path": VString{V: "/"}, "init": VUnit{}, "update": VUnit{}, "view": VUnit{},
	}, Order: []string{"path", "init", "update", "view"}}
	for k, v := range extra {
		rec.Fields[k] = v
		rec.Order = append(rec.Order, k)
	}
	return rec
}

// everyPageConstructor is the point: `readPageRecord` was shared by four of
// them and `Page.create` / `Page.protected` each had their own copy, so a
// check added to the shared one landed on four constructors and missed the
// two most used. That is the same drift this session kept finding between
// build paths, inside one file.
var everyPageConstructor = []string{
	"pageCreate", "pageProtected", "pageAdminProtected",
	"pageDynamic", "pageDynamicProtected", "pageDynamicAdminProtected",
}

func callPageCtor(t *testing.T, name string, rec VRecord) error {
	t.Helper()
	fn, ok := appBuiltins()[name].(VFn)
	if !ok || fn.Native == nil {
		t.Fatalf("%s is not a native builtin", name)
	}
	_, err := fn.Native([]Value{rec})
	return err
}

func TestEveryPageConstructorRefusesAFieldThatIsNotOne(t *testing.T) {
	for _, ctor := range everyPageConstructor {
		err := callPageCtor(t, ctor, pageRec(map[string]Value{"titel": VString{V: "Notes"}}))
		if err == nil {
			t.Errorf("%s: `titel` is accepted and ignored, and the page keeps the module's name", ctor)
			continue
		}
		if !strings.Contains(err.Error(), "titel") {
			t.Errorf("%s: the message must name the field, got: %v", ctor, err)
		}
	}
}

func TestEveryPageConstructorRefusesANonTextTitle(t *testing.T) {
	for _, ctor := range everyPageConstructor {
		err := callPageCtor(t, ctor, pageRec(map[string]Value{"title": VInt{V: 42}}))
		if err == nil {
			t.Errorf("%s: a title of 42 silently becomes the module name", ctor)
			continue
		}
		if !strings.Contains(err.Error(), "whole number 42") {
			t.Errorf("%s: the message must name the value the way the language does, got: %v", ctor, err)
		}
	}
}

func TestAnOrdinaryPageRecordIsAccepted(t *testing.T) {
	for _, ctor := range everyPageConstructor {
		rec := pageRec(map[string]Value{
			"title":         VString{V: "Notes"},
			"subscriptions": VUnit{},
		})
		if err := callPageCtor(t, ctor, rec); err != nil {
			t.Errorf("%s refused the ordinary shape: %v", ctor, err)
		}
	}
}
