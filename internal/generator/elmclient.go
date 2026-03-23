package generator

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"mar/internal/model"
)

// ElmClientOutput defines where the generated Elm client should be written.
type ElmClientOutput struct {
	ModuleName string
	FileName   string
	Source     []byte
}

// GenerateElmClient builds an Elm module with helper functions for calling Mar APIs.
func GenerateElmClient(app *model.App) (*ElmClientOutput, error) {
	if app == nil {
		return nil, fmt.Errorf("nil app")
	}
	baseModule := sanitizeModuleName(app.AppName)
	if baseModule == "" {
		baseModule = "Mar"
	}
	moduleName := baseModule + "Client"

	buf := &bytes.Buffer{}
	writeLine(buf, "module "+moduleName+" exposing")
	writeLine(buf, "    ( Config")
	writeLine(buf, "    , EntityMeta")
	writeLine(buf, "    , FieldMeta")
	writeLine(buf, "    , PublicVersion")
	writeLine(buf, "    , Row")
	writeLine(buf, "    , SchemaMeta")
	writeLine(buf, "    , VersionApp")
	writeLine(buf, "    , getVersion")
	writeLine(buf, "    , schema")
	writeLine(buf, "    , rowDecoder")
	for _, entity := range app.Entities {
		title := toTitle(entity.Name)
		writeLine(buf, "    , list"+title)
		writeLine(buf, "    , get"+title)
		writeLine(buf, "    , create"+title)
		writeLine(buf, "    , update"+title)
		writeLine(buf, "    , delete"+title)
	}
	for _, action := range app.Actions {
		writeLine(buf, "    , run"+toTitle(action.Name))
	}
	if app.Auth != nil {
		writeLine(buf, "    , requestCode")
		writeLine(buf, "    , login")
		writeLine(buf, "    , logout")
		writeLine(buf, "    , me")
	}
	writeLine(buf, "    )")
	writeLine(buf, "")
	writeLine(buf, "import Dict exposing (Dict)")
	writeLine(buf, "import Http")
	writeLine(buf, "import Json.Decode as Decode exposing (Decoder)")
	writeLine(buf, "import Json.Encode as Encode")
	writeLine(buf, "import String")
	writeLine(buf, "import Url")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias Config =")
	writeLine(buf, "    { baseUrl : String")
	writeLine(buf, "    , token : String")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias FieldMeta =")
	writeLine(buf, "    { name : String")
	writeLine(buf, "    , fieldType : String")
	writeLine(buf, "    , primary : Bool")
	writeLine(buf, "    , auto : Bool")
	writeLine(buf, "    , optional : Bool")
	writeLine(buf, "    , defaultValue : Maybe Encode.Value")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias EntityMeta =")
	writeLine(buf, "    { name : String")
	writeLine(buf, "    , table : String")
	writeLine(buf, "    , resource : String")
	writeLine(buf, "    , primaryKey : String")
	writeLine(buf, "    , fields : List FieldMeta")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias SchemaMeta =")
	writeLine(buf, "    { appName : String")
	writeLine(buf, "    , portNumber : Int")
	writeLine(buf, "    , database : String")
	writeLine(buf, "    , entities : List EntityMeta")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias Row =")
	writeLine(buf, "    Dict String Decode.Value")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias VersionApp =")
	writeLine(buf, "    { name : String")
	writeLine(buf, "    , buildTime : String")
	writeLine(buf, "    , manifestHash : String")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "type alias PublicVersion =")
	writeLine(buf, "    { app : VersionApp")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "rowDecoder : Decoder Row")
	writeLine(buf, "rowDecoder =")
	writeLine(buf, "    Decode.dict Decode.value")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "versionAppDecoder : Decoder VersionApp")
	writeLine(buf, "versionAppDecoder =")
	writeLine(buf, "    Decode.map3 VersionApp")
	writeLine(buf, "        (Decode.field \"name\" Decode.string)")
	writeLine(buf, "        (Decode.field \"buildTime\" Decode.string)")
	writeLine(buf, "        (Decode.field \"manifestHash\" Decode.string)")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "publicVersionDecoder : Decoder PublicVersion")
	writeLine(buf, "publicVersionDecoder =")
	writeLine(buf, "    Decode.map PublicVersion")
	writeLine(buf, "        (Decode.field \"app\" versionAppDecoder)")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "rowsDecoder : Decoder (List Row)")
	writeLine(buf, "rowsDecoder =")
	writeLine(buf, "    Decode.list rowDecoder")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "schema : SchemaMeta")
	writeLine(buf, "schema =")
	writeLine(buf, "    { appName = "+elmString(app.AppName))
	writeLine(buf, "    , portNumber = "+fmt.Sprintf("%d", app.Port))
	writeLine(buf, "    , database = "+elmString(app.Database))
	writeLine(buf, "    , entities =")
	writeLine(buf, "        [")
	for i, entity := range app.Entities {
		comma := ","
		if i == len(app.Entities)-1 {
			comma = ""
		}
		writeLine(buf, "          { name = "+elmString(entity.Name))
		writeLine(buf, "          , table = "+elmString(entity.Table))
		writeLine(buf, "          , resource = "+elmString(entity.Resource))
		writeLine(buf, "          , primaryKey = "+elmString(entity.PrimaryKey))
		writeLine(buf, "          , fields =")
		writeLine(buf, "              [")
		for j, field := range entity.Fields {
			fieldComma := ","
			if j == len(entity.Fields)-1 {
				fieldComma = ""
			}
			writeLine(buf, "                { name = "+elmString(field.Name))
			writeLine(buf, "                , fieldType = "+elmString(field.Type))
			writeLine(buf, "                , primary = "+elmBool(field.Primary))
			writeLine(buf, "                , auto = "+elmBool(field.Auto))
			writeLine(buf, "                , optional = "+elmBool(field.Optional))
			writeLine(buf, "                , defaultValue = "+elmMaybeValue(field.Default))
			writeLine(buf, "                }"+fieldComma)
		}
		writeLine(buf, "              ]")
		writeLine(buf, "          }"+comma)
	}
	writeLine(buf, "        ]")
	writeLine(buf, "    }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "headers : Config -> List Http.Header")
	writeLine(buf, "headers config =")
	writeLine(buf, "    if String.trim config.token == \"\" then")
	writeLine(buf, "        []")
	writeLine(buf, "")
	writeLine(buf, "    else")
	writeLine(buf, "        [ Http.header \"Authorization\" (\"Bearer \" ++ String.trim config.token) ]")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "buildUrl : Config -> String -> String")
	writeLine(buf, "buildUrl config path =")
	writeLine(buf, "    String.trimRight config.baseUrl ++ path")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "httpGetRows : Config -> String -> (Result Http.Error (List Row) -> msg) -> Cmd msg")
	writeLine(buf, "httpGetRows config path toMsg =")
	writeLine(buf, "    Http.request")
	writeLine(buf, "        { method = \"GET\"")
	writeLine(buf, "        , headers = headers config")
	writeLine(buf, "        , url = buildUrl config path")
	writeLine(buf, "        , body = Http.emptyBody")
	writeLine(buf, "        , expect = Http.expectJson toMsg rowsDecoder")
	writeLine(buf, "        , timeout = Nothing")
	writeLine(buf, "        , tracker = Nothing")
	writeLine(buf, "        }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "httpGetRow : Config -> String -> (Result Http.Error Row -> msg) -> Cmd msg")
	writeLine(buf, "httpGetRow config path toMsg =")
	writeLine(buf, "    Http.request")
	writeLine(buf, "        { method = \"GET\"")
	writeLine(buf, "        , headers = headers config")
	writeLine(buf, "        , url = buildUrl config path")
	writeLine(buf, "        , body = Http.emptyBody")
	writeLine(buf, "        , expect = Http.expectJson toMsg rowDecoder")
	writeLine(buf, "        , timeout = Nothing")
	writeLine(buf, "        , tracker = Nothing")
	writeLine(buf, "        }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "httpWriteRow : String -> Config -> String -> Encode.Value -> (Result Http.Error Row -> msg) -> Cmd msg")
	writeLine(buf, "httpWriteRow method config path payload toMsg =")
	writeLine(buf, "    Http.request")
	writeLine(buf, "        { method = method")
	writeLine(buf, "        , headers = headers config")
	writeLine(buf, "        , url = buildUrl config path")
	writeLine(buf, "        , body = Http.jsonBody payload")
	writeLine(buf, "        , expect = Http.expectJson toMsg rowDecoder")
	writeLine(buf, "        , timeout = Nothing")
	writeLine(buf, "        , tracker = Nothing")
	writeLine(buf, "        }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "httpDelete : Config -> String -> (Result Http.Error () -> msg) -> Cmd msg")
	writeLine(buf, "httpDelete config path toMsg =")
	writeLine(buf, "    Http.request")
	writeLine(buf, "        { method = \"DELETE\"")
	writeLine(buf, "        , headers = headers config")
	writeLine(buf, "        , url = buildUrl config path")
	writeLine(buf, "        , body = Http.emptyBody")
	writeLine(buf, "        , expect = Http.expectWhatever toMsg")
	writeLine(buf, "        , timeout = Nothing")
	writeLine(buf, "        , tracker = Nothing")
	writeLine(buf, "        }")
	writeLine(buf, "")
	writeLine(buf, "")
	writeLine(buf, "getVersion : Config -> (Result Http.Error PublicVersion -> msg) -> Cmd msg")
	writeLine(buf, "getVersion config toMsg =")
	writeLine(buf, "    Http.request")
	writeLine(buf, "        { method = \"GET\"")
	writeLine(buf, "        , headers = headers config")
	writeLine(buf, "        , url = buildUrl config \"/_mar/version\"")
	writeLine(buf, "        , body = Http.emptyBody")
	writeLine(buf, "        , expect = Http.expectJson toMsg publicVersionDecoder")
	writeLine(buf, "        , timeout = Nothing")
	writeLine(buf, "        , tracker = Nothing")
	writeLine(buf, "        }")
	writeLine(buf, "")

	for _, entity := range app.Entities {
		title := toTitle(entity.Name)
		path := entity.Resource
		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "list"+title+" : Config -> (Result Http.Error (List Row) -> msg) -> Cmd msg")
		writeLine(buf, "list"+title+" config toMsg =")
		writeLine(buf, "    httpGetRows config "+elmString(path)+" toMsg")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "get"+title+" : Config -> String -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "get"+title+" config idValue toMsg =")
		writeLine(buf, "    httpGetRow config ("+elmString(path)+" ++ \"/\" ++ Url.percentEncode idValue) toMsg")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "create"+title+" : Config -> Encode.Value -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "create"+title+" config payload toMsg =")
		writeLine(buf, "    httpWriteRow \"POST\" config "+elmString(path)+" payload toMsg")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "update"+title+" : Config -> String -> Encode.Value -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "update"+title+" config idValue payload toMsg =")
		writeLine(buf, "    httpWriteRow \"PATCH\" config ("+elmString(path)+" ++ \"/\" ++ Url.percentEncode idValue) payload toMsg")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "delete"+title+" : Config -> String -> (Result Http.Error () -> msg) -> Cmd msg")
		writeLine(buf, "delete"+title+" config idValue toMsg =")
		writeLine(buf, "    httpDelete config ("+elmString(path)+" ++ \"/\" ++ Url.percentEncode idValue) toMsg")
	}

	if app.Auth != nil {
		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "requestCode : Config -> String -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "requestCode config email toMsg =")
		writeLine(buf, "    Http.request")
		writeLine(buf, "        { method = \"POST\"")
		writeLine(buf, "        , headers = headers config")
		writeLine(buf, "        , url = buildUrl config \"/auth/request-code\"")
		writeLine(buf, "        , body = Http.jsonBody (Encode.object [ ( \"email\", Encode.string email ) ])")
		writeLine(buf, "        , expect = Http.expectJson toMsg rowDecoder")
		writeLine(buf, "        , timeout = Nothing")
		writeLine(buf, "        , tracker = Nothing")
		writeLine(buf, "        }")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "login : Config -> { email : String, code : String } -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "login config payload toMsg =")
		writeLine(buf, "    Http.request")
		writeLine(buf, "        { method = \"POST\"")
		writeLine(buf, "        , headers = headers config")
		writeLine(buf, "        , url = buildUrl config \"/auth/login\"")
		writeLine(buf, "        , body =")
		writeLine(buf, "            Http.jsonBody")
		writeLine(buf, "                (Encode.object")
		writeLine(buf, "                    [ ( \"email\", Encode.string payload.email )")
		writeLine(buf, "                    , ( \"code\", Encode.string payload.code )")
		writeLine(buf, "                    ]")
		writeLine(buf, "                )")
		writeLine(buf, "        , expect = Http.expectJson toMsg rowDecoder")
		writeLine(buf, "        , timeout = Nothing")
		writeLine(buf, "        , tracker = Nothing")
		writeLine(buf, "        }")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "logout : Config -> (Result Http.Error () -> msg) -> Cmd msg")
		writeLine(buf, "logout config toMsg =")
		writeLine(buf, "    Http.request")
		writeLine(buf, "        { method = \"POST\"")
		writeLine(buf, "        , headers = headers config")
		writeLine(buf, "        , url = buildUrl config \"/auth/logout\"")
		writeLine(buf, "        , body = Http.emptyBody")
		writeLine(buf, "        , expect = Http.expectWhatever toMsg")
		writeLine(buf, "        , timeout = Nothing")
		writeLine(buf, "        , tracker = Nothing")
		writeLine(buf, "        }")

		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "me : Config -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "me config toMsg =")
		writeLine(buf, "    httpGetRow config \"/auth/me\" toMsg")
	}

	for _, action := range app.Actions {
		title := toTitle(action.Name)
		path := "/actions/" + action.Name
		writeLine(buf, "")
		writeLine(buf, "")
		writeLine(buf, "run"+title+" : Config -> Encode.Value -> (Result Http.Error Row -> msg) -> Cmd msg")
		writeLine(buf, "run"+title+" config payload toMsg =")
		writeLine(buf, "    httpWriteRow \"POST\" config "+elmString(path)+" payload toMsg")
	}

	out := &ElmClientOutput{
		ModuleName: moduleName,
		FileName:   moduleName + ".elm",
		Source:     buf.Bytes(),
	}
	return out, nil
}

// ClientOutputPath resolves the generated client location under dist/<app>/clients.
func ClientOutputPath(manifestPath, fileName string) string {
	dir := filepath.Dir(manifestPath)
	return filepath.Join(dir, "clients", fileName)
}

func writeLine(buf *bytes.Buffer, line string) {
	buf.WriteString(line)
	buf.WriteByte('\n')
}

func elmString(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}

func elmBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func elmMaybeValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "Nothing"
	case string:
		return "Just (Encode.string " + elmString(v) + ")"
	case bool:
		if v {
			return "Just (Encode.bool True)"
		}
		return "Just (Encode.bool False)"
	case int:
		return "Just (Encode.int " + fmt.Sprintf("%d", v) + ")"
	case int64:
		return "Just (Encode.float " + fmt.Sprintf("%d.0", v) + ")"
	case float64:
		return "Just (Encode.float " + fmt.Sprintf("%g", v) + ")"
	default:
		return "Nothing"
	}
}

// sanitizeModuleName converts arbitrary app names into valid Elm module identifiers.
func sanitizeModuleName(value string) string {
	cleaned := regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(value, " ")
	parts := strings.Fields(cleaned)
	if len(parts) == 0 {
		return ""
	}
	for i := range parts {
		parts[i] = toTitle(parts[i])
	}
	return strings.Join(parts, "")
}

func toTitle(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
