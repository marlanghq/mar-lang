package cli

import "testing"

func TestComputeEditorGitSigns(t *testing.T) {
	base := []string{
		"app Todo",
		"port 4100",
		"database \"todo.db\"",
		"entity Todo {",
		"  title: String",
		"}",
	}
	current := []string{
		"app Todo",
		"port 4100",
		"database \"todo.db\"",
		"entity Todo {",
		"  title: String",
		"  done: Bool",
		"}",
	}

	signs := computeEditorGitSigns(base, current)
	if got := signs[5]; got != '+' {
		t.Fatalf("expected line 6 to be marked as addition, got %q", got)
	}
}

func TestComputeEditorGitSignsModification(t *testing.T) {
	base := []string{
		"title: String",
	}
	current := []string{
		"title: String optional",
	}

	signs := computeEditorGitSigns(base, current)
	if got := signs[0]; got != '~' {
		t.Fatalf("expected modified line to be marked as ~, got %q", got)
	}
}

func TestComputeEditorGitSignsDeletion(t *testing.T) {
	base := []string{
		"one",
		"two",
		"three",
	}
	current := []string{
		"one",
		"three",
	}

	signs := computeEditorGitSigns(base, current)
	if got := signs[1]; got != '-' {
		t.Fatalf("expected deletion marker on line 2, got %q", got)
	}
}
