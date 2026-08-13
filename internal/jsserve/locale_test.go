package jsserve

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The app's language comes from mar.json and reaches the browser as
// <html lang>. That attribute is not decoration: it is what a screen
// reader picks its voice from, and what the browser hyphenates and
// offers to translate by. Both shells hardcoded "en" once, which made
// every Portuguese app get read aloud in English.

func TestRenderShellCarriesTheLocale(t *testing.T) {
	SetLocale("pt-BR")
	t.Cleanup(func() { SetLocale("en") })

	rec := httptest.NewRecorder()
	renderShell(rec, &LiveProgram{})

	body := rec.Body.String()
	if !strings.Contains(body, `<html lang="pt-BR">`) {
		t.Errorf("shell is missing the locale in <html lang>:\n%.200s", body)
	}
	if got := rec.Header().Get("Content-Language"); got != "pt-BR" {
		t.Errorf("Content-Language = %q, want %q", got, "pt-BR")
	}
}

// The locale and the title are two %s slots in the same template, in
// that order. Swapping them would still compile and still render, just
// with the language in the tab and the title in the lang attribute.
func TestRenderShellDoesNotSwapLocaleAndTitle(t *testing.T) {
	SetLocale("es-419")
	t.Cleanup(func() { SetLocale("en") })

	lp := &LiveProgram{}
	lp.SetAppName("Cuaderno")

	rec := httptest.NewRecorder()
	renderShell(rec, lp)

	body := rec.Body.String()
	if !strings.Contains(body, "<title>"+lp.Title()+"</title>") {
		t.Errorf("title did not land in <title>:\n%.300s", body)
	}
	if !strings.Contains(body, `<html lang="es-419">`) {
		t.Errorf("locale did not land in <html lang>:\n%.300s", body)
	}
}

// lang="" reads as "no idea what language this is", which is worse for
// assistive technology than a wrong guess. An empty tag never reaches
// here through the CLI, so the setter refuses it rather than storing
// it and letting the shell emit an empty attribute.
func TestEmptyLocaleIsRefused(t *testing.T) {
	SetLocale("pt-BR")
	t.Cleanup(func() { SetLocale("en") })

	SetLocale("")
	if got := currentLocale(); got != "pt-BR" {
		t.Errorf("empty tag overwrote the locale: got %q", got)
	}

	rec := httptest.NewRecorder()
	renderShell(rec, &LiveProgram{})
	if strings.Contains(rec.Body.String(), `<html lang="">`) {
		t.Error("shell emitted an empty lang attribute")
	}
}

// The admin panel is the framework's own UI, written in English. A
// Portuguese app does not make "Entities" a Portuguese word, so the
// panel declares its own language rather than borrowing the app's.
func TestAdminShellStaysEnglish(t *testing.T) {
	SetLocale("pt-BR")
	t.Cleanup(func() { SetLocale("en") })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_mar/admin", nil)
	handleAdminMarShell(rec, req)

	if !strings.Contains(rec.Body.String(), `<html lang="en">`) {
		t.Error("admin panel should declare English regardless of the app's locale")
	}
}
