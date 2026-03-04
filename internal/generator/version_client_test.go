package generator

import (
	"strings"
	"testing"

	"belm/internal/model"
)

func TestGenerateElmClientIncludesVersionEndpoint(t *testing.T) {
	app := &model.App{
		AppName:  "TodoApi",
		Port:     4100,
		Database: "todo.db",
	}

	out, err := GenerateElmClient(app)
	if err != nil {
		t.Fatalf("GenerateElmClient returned error: %v", err)
	}

	source := string(out.Source)
	if !strings.Contains(source, "getVersion : Config -> (Result Http.Error PublicVersion -> msg) -> Cmd msg") {
		t.Fatalf("generated Elm client must include getVersion signature, source:\n%s", source)
	}
	if !strings.Contains(source, "buildUrl config \"/_belm/version\"") {
		t.Fatalf("generated Elm client must call /_belm/version, source:\n%s", source)
	}
}

func TestGenerateTSClientIncludesVersionEndpoint(t *testing.T) {
	app := &model.App{
		AppName:  "TodoApi",
		Port:     4100,
		Database: "todo.db",
	}

	out, err := GenerateTSClient(app)
	if err != nil {
		t.Fatalf("GenerateTSClient returned error: %v", err)
	}

	source := string(out.Source)
	if !strings.Contains(source, "export async function getVersion(config: Config): Promise<PublicVersionResponse>") {
		t.Fatalf("generated TypeScript client must include getVersion signature, source:\n%s", source)
	}
	if !strings.Contains(source, "return requestJson<PublicVersionResponse>(config, \"GET\", \"/_belm/version\");") {
		t.Fatalf("generated TypeScript client must call /_belm/version, source:\n%s", source)
	}
}

