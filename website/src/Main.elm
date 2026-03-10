port module Main exposing (main)

import Browser
import Browser.Navigation as Nav
import Char
import Element exposing (Attribute, Element, centerX, column, el, fill, height, html, htmlAttribute, link, maximum, minimum, newTabLink, padding, paddingEach, paragraph, px, rgb255, row, scrollbarX, scrollbarY, spacing, text, width, wrappedRow)
import Element.Background as Background
import Element.Border as Border
import Element.Font as Font
import Html
import Html.Attributes as HtmlAttr
import Html.Events as HtmlEvents
import String
import Url exposing (Url)


type Route
    = Home
    | GettingStarted
    | AdvancedGuide
    | AdvancedFundamentals
    | AdvancedLanguageReference
    | AdvancedRuntime
    | AdvancedTooling
    | AdvancedCompiler
    | Examples


type alias Model =
    { key : Nav.Key
    , route : Route
    , copiedText : Maybe String
    }


type Msg
    = LinkClicked Browser.UrlRequest
    | UrlChanged Url
    | CopyText String


port copyToClipboard : String -> Cmd msg


main : Program () Model Msg
main =
    Browser.application
        { init = init
        , update = update
        , subscriptions = \_ -> Sub.none
        , view = view
        , onUrlRequest = LinkClicked
        , onUrlChange = UrlChanged
        }


init : () -> Url -> Nav.Key -> ( Model, Cmd Msg )
init _ url key =
    ( { key = key
      , route = routeFromUrl url
      , copiedText = Nothing
      }
    , Cmd.none
    )


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        LinkClicked urlRequest ->
            case urlRequest of
                Browser.Internal url ->
                    ( model, Nav.pushUrl model.key (Url.toString url) )

                Browser.External href ->
                    ( model, Nav.load href )

        UrlChanged url ->
            ( { model | route = routeFromUrl url, copiedText = Nothing }, Cmd.none )

        CopyText source ->
            ( { model | copiedText = Just source }, copyToClipboard source )


routeFromUrl : Url -> Route
routeFromUrl url =
    let
        fragment =
            url.fragment
                |> Maybe.withDefault ""
                |> normalizeFragment
    in
    case fragment of
        "" ->
            Home

        "getting-started" ->
            GettingStarted

        "advanced" ->
            AdvancedGuide

        "advanced/fundamentals" ->
            AdvancedFundamentals

        "advanced/reference" ->
            AdvancedLanguageReference

        "advanced/runtime" ->
            AdvancedRuntime

        "advanced/tooling" ->
            AdvancedTooling

        "advanced/compiler" ->
            AdvancedCompiler

        "examples" ->
            Examples

        _ ->
            Home


normalizeFragment : String -> String
normalizeFragment fragment =
    if String.startsWith "/" fragment then
        String.dropLeft 1 fragment

    else
        fragment


routeHref : Route -> String
routeHref route =
    case route of
        Home ->
            "#/"

        GettingStarted ->
            "#/getting-started"

        AdvancedGuide ->
            "#/advanced"

        AdvancedFundamentals ->
            "#/advanced/fundamentals"

        AdvancedLanguageReference ->
            "#/advanced/reference"

        AdvancedRuntime ->
            "#/advanced/runtime"

        AdvancedTooling ->
            "#/advanced/tooling"

        AdvancedCompiler ->
            "#/advanced/compiler"

        Examples ->
            "#/examples"


pageTitle : Route -> String
pageTitle route =
    case route of
        Home ->
            "Mar"

        GettingStarted ->
            "Mar - Getting Started"

        AdvancedGuide ->
            "Mar - Advanced Guide"

        AdvancedFundamentals ->
            "Mar - Fundamentals Guide"

        AdvancedLanguageReference ->
            "Mar - Language Reference"

        AdvancedRuntime ->
            "Mar - Runtime Guide"

        AdvancedTooling ->
            "Mar - Tooling Guide"

        AdvancedCompiler ->
            "Mar - Compiler Guide"

        Examples ->
            "Mar - Examples"


view : Model -> Browser.Document Msg
view model =
    { title = pageTitle model.route
    , body =
        [ Element.layout
            [ Background.color (rgb255 244 248 255)
            , Font.family
                [ Font.typeface "IBM Plex Sans"
                , Font.typeface "Helvetica Neue"
                , Font.sansSerif
                ]
            , Font.color (rgb255 26 41 59)
            ]
            (page model)
        ]
    }


page : Model -> Element Msg
page model =
    column
        [ width fill
        , spacing 20
        , paddingEach { top = 20, right = 20, bottom = 28, left = 20 }
        ]
        [ topBar model.route
        , warningBanner
        , routeView model
        , footer
        ]


topBar : Route -> Element Msg
topBar route =
    panel
        [ column [ width fill, spacing 12 ]
            [ el [ Font.size 28, Font.bold, Font.color (rgb255 22 57 96) ] (text "Mar")
            , wrappedRow [ width fill, spacing 8 ]
                [ navItem route Home "Home"
                , navItem route GettingStarted "Getting Started"
                , navItem route AdvancedGuide "Advanced"
                , navItem route Examples "Examples"
                ]
            ]
        ]


navItem : Route -> Route -> String -> Element Msg
navItem current target label =
    navLink label (routeHref target) (topLevelRoute current == topLevelRoute target)


topLevelRoute : Route -> Route
topLevelRoute route =
    case route of
        AdvancedFundamentals ->
            AdvancedGuide

        AdvancedLanguageReference ->
            AdvancedGuide

        AdvancedRuntime ->
            AdvancedGuide

        AdvancedTooling ->
            AdvancedGuide

        AdvancedCompiler ->
            AdvancedGuide

        _ ->
            route


routeView : Model -> Element Msg
routeView model =
    case model.route of
        Home ->
            homePage model

        GettingStarted ->
            gettingStartedPage model

        AdvancedGuide ->
            advancedLanguagePage model

        AdvancedFundamentals ->
            advancedLanguagePage model

        AdvancedLanguageReference ->
            advancedLanguageReferencePage

        AdvancedRuntime ->
            advancedRuntimePage model

        AdvancedTooling ->
            advancedToolingPage model

        AdvancedCompiler ->
            advancedCompilerPage

        Examples ->
            examplesPage model


warningBanner : Element Msg
warningBanner =
    column
        [ width (fill |> maximum 1040)
        , centerX
        , spacing 8
        , padding 16
        , Background.color (rgb255 255 247 224)
        , Border.width 1
        , Border.color (rgb255 244 210 133)
        , Border.rounded 12
        ]
        [ column [ spacing 8, width fill ]
            [ paragraph [ Font.size 22, Font.bold, Font.color (rgb255 121 66 0) ]
                [ text "Warning" ]
            , paragraph [ Font.size 16, Font.color (rgb255 107 62 0), width fill ]
                [ text "Mar is still at a very early stage and is "
                , el [ Font.bold ] (text "not recommended for production use yet")
                , text "."
                ]
            , paragraph [ Font.size 16, Font.color (rgb255 107 62 0), width fill ]
                [ text "For now, Mar does not guarantee backward compatibility for language syntax or database schema. That guarantee is planned for a future stable release." ]
            ]
        ]


footer : Element Msg
footer =
    el
        [ width fill
        , paddingEach { top = 0, right = 0, bottom = 0, left = 0 }
        ]
        (row
            [ centerX
            , spacing 4
            , Font.size 14
            , Font.color (rgb255 98 116 139)
            ]
            [ text "Copyright © 2026"
            , newTabLink
                [ Font.color (rgb255 36 82 132)
                , Font.semiBold
                , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                ]
                { url = "https://segunda.tech/about"
                , label = text "Marcio Frayze David"
                }
            ]
        )


homePage : Model -> Element Msg
homePage model =
    column
        [ width fill
        , spacing 20
        ]
        [ hero
        , codeExample model
        , features
        , audience
        ]


gettingStartedPage : Model -> Element Msg
gettingStartedPage model =
    column
        [ width fill
        , spacing 20
        ]
        [ panel
            [ sectionTitle "Getting Started"
            , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                [ text "Install Mar, iterate quickly with hot reload, and deploy as a single executable." ]
            ]
        , install model
        , quickStart model
        , panel
            [ sectionTitle "Use the Admin UI while developing"
            , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                [ text "Admin UI URL: "
                , newTabLink
                    [ Font.bold
                    , Font.family [ Font.typeface "IBM Plex Mono", Font.monospace ]
                    , Font.color (rgb255 36 82 132)
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "http://localhost:4100/_mar/admin"
                    , label = text "http://localhost:4100/_mar/admin"
                    }
                ]
            , bulletList
                [ "Sign in through Authentication."
                , "Navigate entities from the left sidebar."
                , "Manage records with the built-in CRUD actions."
                , "Access monitoring, logs, and database tools with an admin account."
                ]
            ]
        ]


advancedLanguagePage : Model -> Element Msg
advancedLanguagePage model =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu model.route
        , panel
            [ sectionTitle "Advanced Guide"
            , paragraph
                [ Font.size 16
                , Font.color (rgb255 72 95 123)
                , width fill
                ]
                [ text "Mar is a declarative backend DSL inspired by "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://elm-lang.org"
                    , label = text "Elm"
                    }
                , text " and "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://pocketbase.io"
                    , label = text "PocketBase"
                    }
                , text ", implemented in "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://go.dev"
                    , label = text "Go"
                    }
                , text " with focus on readability, maintainability, and simple deployment."
                ]
            , docSubsectionTitle "Fundamentals"
            , bodyText "Mar reads top-to-bottom as a declarative app definition. A Mar app is centered around entities, rules, authorization, optional auth configuration, and typed actions."
            , docSubsectionTitle "Quick Examples"
            , codeFromString model "todo.mar" 450 todoExampleSource
            , codeFromString model "action.mar" 575 actionExampleSource
            , docSubsectionTitle "Syntax Model"
            , docList
                [ "Top-level statements: app, port, database, public, system, auth, entity, type alias, action."
                , "Fields use the form fieldName: Type with optional modifiers such as primary, auto, and optional."
                , "Comments use Elm-style line comments: -- this is a comment."
                ]
            , docSubsectionTitle "Authentication and Authorization"
            , bodyText "Mar includes a built-in email-code login flow and per-operation authorization rules. The same auth model is also used by system-level tooling such as monitoring, logs, and backups."
            , codeFromString model "auth.mar" 272 authConfigSource
            , codeFromString model "authorize.mar" 300 authorizeExampleSource
            , docList
                [ "Authentication endpoints are always available."
                , "When auth { ... } is defined, Mar uses your configured user entity and fields."
                , "When auth { ... } is omitted, Mar still provides a built-in auth user store."
                , "System features use the same session and require role == \"admin\"."
                ]
            , docSubsectionTitle "Rules and Typed Actions"
            , bodyText "Rules are for validation close to the entity definition. Actions are for multi-step writes that must succeed or fail together."
            , docList
                [ "rule validates entity data and returns HTTP 422 with details when validation fails."
                , "Actions run in a single atomic transaction."
                , "Mar checks input types and assigned entity fields at compile time."
                ]
            , docSubsectionTitle "Current Limitations"
            , bodyText "Mar currently supports a single .mar entry file per app, without multi-file projects or imports."
            ]
        , advancedPager Nothing (Just AdvancedRuntime)
        ]


advancedLanguageReferencePage : Element Msg
advancedLanguageReferencePage =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu AdvancedLanguageReference
        , panel
            [ sectionTitle "Advanced Guide"
            , docSubsectionTitle "Language Reference"
            , bodyText "This reference lists the current keywords and built-in names used by the language."
            , languageReferenceGroup "Top-level declarations"
                [ languageReferenceItem "app" "Declares the app name."
                , languageReferenceItem "port" "Sets the HTTP port."
                , languageReferenceItem "database" "Sets the SQLite database file."
                , languageReferenceItem "public" "Declares embedded static frontend files."
                , languageReferenceItem "system" "Declares runtime and security settings."
                , languageReferenceItem "auth" "Declares email-code authentication settings."
                , languageReferenceItem "entity" "Declares an entity and its generated CRUD surface."
                , languageReferenceItem "type alias" "Declares a record type, typically for action input."
                , languageReferenceItem "action" "Declares a custom action endpoint."
                ]
            , languageReferenceGroup "Entity fields and modifiers"
                [ languageReferenceItem "primary" "Marks a field as the primary key."
                , languageReferenceItem "auto" "Marks a field as auto-generated."
                , languageReferenceItem "optional" "Marks a field as nullable."
                ]
            , languageReferenceGroup "Validation and authorization"
                [ languageReferenceItem "rule" "Adds entity validation."
                , languageReferenceItem "when" "Introduces the boolean expression used by a rule or authorization clause."
                , languageReferenceItem "authorize" "Declares per-operation authorization rules."
                , languageReferenceItem "list, get, create, update, delete" "The supported CRUD operations for authorize clauses."
                ]
            , languageReferenceGroup "Actions"
                [ languageReferenceItem "input" "Declares the action input type and is also used in expressions such as input.userId."
                , languageReferenceItem "create" "Adds a create step inside an action."
                ]
            , languageReferenceGroup "Auth config"
                [ languageReferenceItem "user_entity" "Sets which entity stores authenticated users."
                , languageReferenceItem "email_field" "Sets which user field is used as the login email."
                , languageReferenceItem "role_field" "Sets which user field is used for role checks."
                , languageReferenceItem "code_ttl_minutes" "Sets how long login codes remain valid."
                , languageReferenceItem "session_ttl_hours" "Sets the default session lifetime."
                , languageReferenceItem "email_transport, email_from, email_subject, sendmail_path" "Configure how login codes are delivered."
                ]
            , languageReferenceGroup "System config"
                [ languageReferenceItem "request_logs_buffer" "Sets how many recent requests stay in memory for monitoring."
                , languageReferenceItem "http_max_request_body_mb" "Limits request body size."
                , languageReferenceItem "auth_request_code_rate_limit_per_minute, auth_login_rate_limit_per_minute" "Configure auth rate limits."
                , languageReferenceItem "admin_ui_session_ttl_hours" "Sets a separate session lifetime for the embedded admin UI."
                , languageReferenceItem "security_frame_policy, security_referrer_policy, security_content_type_nosniff" "Configure security response headers."
                , languageReferenceItem "sqlite_journal_mode, sqlite_synchronous, sqlite_foreign_keys" "Configure core SQLite behavior."
                , languageReferenceItem "sqlite_busy_timeout_ms, sqlite_wal_autocheckpoint, sqlite_journal_size_limit_mb, sqlite_mmap_size_mb, sqlite_cache_size_kb" "Configure SQLite performance tuning."
                ]
            , languageReferenceGroup "Public frontend config"
                [ languageReferenceItem "dir" "Sets the source directory of embedded static files."
                , languageReferenceItem "mount" "Sets where embedded static files are served."
                , languageReferenceItem "spa_fallback" "Sets the fallback file used for SPA-style routes."
                ]
            , languageReferenceGroup "Built-in functions and values"
                [ languageReferenceItem "len, contains, startsWith, endsWith, matches" "Built-in helpers available inside rule and authorize expressions."
                , languageReferenceItem "isRole" "Checks the authenticated user role inside authorize expressions."
                , languageReferenceItem "auth_authenticated, auth_email, auth_user_id, auth_role" "Built-in authentication values available in expressions."
                , languageReferenceItem "true, false, null" "Built-in literals."
                ]
            ]
        , advancedPager (Just AdvancedCompiler) Nothing
        ]


advancedRuntimePage : Model -> Element Msg
advancedRuntimePage model =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu model.route
        , panel
            [ sectionTitle "Advanced Guide"
            , docSubsectionTitle "Runtime"
            , bodyText "The runtime generated by Mar is meant to be practical by default: HTTP endpoints, SQLite storage, authentication, admin tooling, and migrations come from the same source file."
            , docSubsectionTitle "System Configuration"
            , paragraphWithEmphasis
                [ text "Use "
                , emphasisText "system"
                , text " when you need to tune runtime behavior. This is where request logging, body limits, auth rate limits, admin UI session lifetime, security headers, and SQLite pragmas are configured."
                ]
            , codeFromString model "system.mar" 0 systemConfigSource
            , docList
                [ "request_logs_buffer controls how many recent requests stay in memory for monitoring."
                , "http_max_request_body_mb limits request body size and returns HTTP 413 when exceeded."
                , "Auth rate limits control request-code and login attempts per minute."
                , "admin_ui_session_ttl_hours can shorten the embedded admin UI session without changing REST client sessions."
                , "Security settings apply response headers such as frame policy, referrer policy, and nosniff."
                , "SQLite settings are performance-first by default and can be overridden per app."
                ]
            , docSubsectionTitle "Public Static Frontend"
            , bodyText "Mar can embed static frontend files into the final executable. This is useful when you want one deployable binary that serves both the backend and a compiled frontend."
            , codeFromString model "public.mar" 260 publicConfigSource
            , docSubsectionTitle "Generated Endpoints"
            , bodyText "Mar turns the declarative app definition into a concrete HTTP surface. CRUD, actions, auth, health, version, and admin-related endpoints are generated automatically from the source file."
            , docList
                [ "Each entity gets REST CRUD endpoints."
                , "Typed actions are exposed as POST /actions/<name>."
                , "System endpoints include /health, /_mar/admin, /_mar/schema, and /_mar/version."
                , "Admin-only system endpoints include /_mar/version/admin, /_mar/perf, /_mar/request-logs, and /_mar/backups."
                ]
            , docSubsectionTitle "Migrations"
            , bodyText "Mar applies schema migration logic automatically on startup. Safe changes are handled for you, while unsafe changes are blocked instead of being applied silently."
            , docList
                [ "Migrations run automatically on startup."
                , "Mar creates missing tables, adds new optional columns, and keeps auth/session storage ready."
                , "Unsafe changes such as type changes, primary key changes, nullability changes, and new required fields are blocked."
                , "When blocked, startup fails with a clear migration error."
                ]
            ]
        , advancedPager (Just AdvancedFundamentals) (Just AdvancedTooling)
        ]


advancedToolingPage : Model -> Element Msg
advancedToolingPage model =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu model.route
        , panel
            [ sectionTitle "Advanced Guide"
            , docSubsectionTitle "Tooling"
            , paragraphWithEmphasis
                [ emphasisText "mar"
                , text " hosts the day-to-day developer workflow, while the generated clients and editor support help keep frontend and backend aligned."
                ]
            , docSubsectionTitle "Compiler and Runtime Commands"
            , commandRow model "1" "Dev" "Runs the app in development mode with hot reload when the .mar file changes." "mar dev store.mar"
            , commandRow model "2" "Compile" "Packages self-contained executables for all supported platforms and generates frontend clients." "mar compile store.mar"
            , commandRow model "3" "Format" "Applies Mar's official formatting style to source files." "mar format store.mar"
            , commandRow model "4" "LSP" "Starts the language server used by the VSCode extension for diagnostics, hovers, and navigation. Usually started by the editor plugin." "mar lsp"
            , docSubsectionTitle "Generated Client Output"
            , bodyText "When you compile an app, Mar also generates frontend clients for Elm and TypeScript. These clients wrap the generated HTTP API with named functions, so you do not need to hand-write fetch calls, URLs, or request payload shapes."
            , docList
                [ "Elm client: dist/<name>/clients/<AppName>Client.elm"
                , "TypeScript client: dist/<name>/clients/<AppName>Client.ts"
                , "Both include CRUD functions, action functions, auth endpoints, and backend version access."
                , "They reduce duplicated frontend code and keep frontend calls aligned with the backend generated from your .mar file."
                , "This makes refactors safer, because the client surface is regenerated from the same source as the server."
                ]
            , docSubsectionTitle "Admin UI and Editor Support"
            , bodyText "Mar ships with an embedded Admin UI for operating the app you compiled. The editor tooling focuses on making the DSL easier to author and safer to change."
            , docList
                [ "The embedded Admin UI uses schema discovery from GET /_mar/schema."
                , "It supports CRUD browsing, auth flows, monitoring, request logs, and database tooling."
                , "The VSCode extension provides syntax highlighting, hover docs, go to definition, references, rename, formatting, and LSP diagnostics."
                ]
            ]
        , advancedPager (Just AdvancedRuntime) (Just AdvancedCompiler)
        ]


advancedCompilerPage : Element Msg
advancedCompilerPage =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu AdvancedCompiler
        , panel
            [ sectionTitle "Advanced Guide"
            , docSubsectionTitle "Compiler"
            , bodyText "The compiler parses a single .mar file into a typed app model, validates it, generates clients, packages a manifest bundle with admin/public assets, and stamps that bundle into prebuilt runtime executables for all supported platforms."
            , architectureDiagram
            ]
        , advancedPager (Just AdvancedTooling) (Just AdvancedLanguageReference)
        ]


examplesPage : Model -> Element Msg
examplesPage model =
    column
        [ width fill
        , spacing 20
        ]
        [ exampleCard model "Todo API" "Minimal CRUD example" "todo.mar" todoExampleSource
        , exampleCard model "BookStore API" "Auth, roles, and transactional action" "store.mar" storeExampleSource
        ]


exampleCard : Model -> String -> String -> String -> String -> Element Msg
exampleCard model name subtitle fileName source =
    panel
        [ row [ width fill, spacing 12 ]
            [ column [ width fill, spacing 4 ]
                [ paragraph [ Font.size 22, Font.bold, Font.color (rgb255 20 53 89) ] [ text name ]
                , paragraph [ Font.size 15, Font.color (rgb255 95 114 138) ] [ text subtitle ]
                ]
            ]
        , codeFromString model fileName 360 source
        ]


hero : Element Msg
hero =
    panel
        [ column [ spacing 10, width fill ]
            [ paragraph [ Font.size 38, Font.bold, Font.color (rgb255 16 44 79), width (fill |> maximum 900) ]
                [ text "A simple declarative backend language." ]
            , paragraph [ Font.size 18, Font.color (rgb255 72 95 123), width fill ]
                [ text "Mar compiles declarative source into a self-contained server executable with API, auth, admin panel, monitoring, and backups." ]
            , paragraph [ Font.size 16, Font.color (rgb255 96 116 140), width (fill |> maximum 880) ]
                [ text "Inspired by "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://elm-lang.org"
                    , label = text "Elm"
                    }
                , text " and "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://pocketbase.io"
                    , label = text "PocketBase"
                    }
                , text "."
                ]
            , wrappedRow [ width fill, spacing 10, paddingEach { top = 6, right = 0, bottom = 0, left = 0 }, centerX ]
                [ link
                    (buttonAttributes
                        (rgb255 45 126 210)
                        (rgb255 245 250 255)
                        ++ [ width (px 220) ]
                    )
                    { url = routeHref GettingStarted
                    , label = text "Get Started"
                    }
                , link
                    (buttonAttributes
                        (rgb255 230 239 250)
                        (rgb255 36 82 132)
                        ++ [ width (px 220) ]
                    )
                    { url = routeHref AdvancedGuide
                    , label = text "Advanced Guide"
                    }
                ]
            ]
        ]


codeExample : Model -> Element Msg
codeExample model =
    panel
        [ sectionTitle "Mar Syntax Example"
        , codeBlock model
        ]


install : Model -> Element Msg
install model =
    panel
        [ sectionTitle "Install"
        , downloadInstallRow
        , pathInstallRow model
        , installCommandRow model "3" "Check" "mar version"
        , pluginInstallRow
        ]


quickStart : Model -> Element Msg
quickStart model =
    panel
        [ sectionTitle "Quick Start"
        , quickStartCreateCard model "1" "Create" "todo.mar" todoExampleSource
        , commandRow model "2" "Develop" "Runs the app locally with hot reload while you edit todo.mar." "mar dev todo.mar"
        , commandRow model "3" "Compile" "Packages production executables for all supported platforms and generates the frontend clients." "mar compile todo.mar"
        , commandRow model "4" "Deploy" "Choose the target folder for your platform and start that executable." "cd dist/todo/darwin-arm64 && ./todo serve"
        , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
            [ text "Mar compile produces a single self-contained executable per target platform. Each one already includes API, auth, embedded Admin UI, monitoring dashboards, request logs, and SQLite backup tools." ]
        ]


quickStartCreateCard : Model -> String -> String -> String -> String -> Element Msg
quickStartCreateCard model number label fileName source =
    column
        [ width fill
        , spacing 10
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 12, left = 12 }
        ]
        [ row [ width fill, spacing 10 ]
            [ stepBadge number
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text label)
            ]
        , codeFromString model fileName 320 source
        ]


installCommandRow : Model -> String -> String -> String -> Element Msg
installCommandRow model number label command =
    column
        [ width fill
        , spacing 10
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
        ]
        [ wrappedRow [ width fill, spacing 10 ]
            [ stepBadge number
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text label)
            ]
        , wrappedRow [ width fill, spacing 8 ]
            [ codeInline command
            , copyLink model command
            ]
        ]


downloadInstallRow : Element Msg
downloadInstallRow =
    column
        [ width fill
        , spacing 10
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
        ]
        [ wrappedRow [ width fill, spacing 10 ]
            [ stepBadge "1"
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text "Download")
            ]
        , instructionText "Mar is currently in a closed alpha stage and is not available to the general public yet."
        ]


pathInstallRow : Model -> Element Msg
pathInstallRow model =
    column
        [ spacing 10
        , width fill
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
        ]
        [ wrappedRow [ width fill, spacing 10 ]
            [ stepBadge "2"
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text "Path")
            , instructionText "Move mar to a directory in your PATH."
            ]
        , column
            [ width fill
            , spacing 8
            ]
            [ installSubitem model "macOS/Linux" "mv mar /usr/local/bin/mar && chmod +x /usr/local/bin/mar"
            , installSubitem model "Windows" "setx PATH \"%PATH%;C:\\Tools\\mar\""
            ]
        ]


pluginInstallRow : Element Msg
pluginInstallRow =
    column
        [ spacing 10
        , width fill
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
        ]
        [ wrappedRow [ width fill, spacing 10 ]
            [ stepBadge "4"
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text "Code editor")
            , paragraph [ Font.size 16, Font.color (rgb255 70 93 121), width fill ]
                [ text "Currently, Mar supports only "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://code.visualstudio.com/"
                    , label = text "VSCode"
                    }
                , text "."
                ]
            ]
        , column
            [ spacing 6
            , width fill
            ]
            [ paragraph [ Font.size 14, Font.color (rgb255 70 93 121) ]
                [ text "Open VSCode Extensions (Cmd+Shift+X on macOS, Ctrl+Shift+X on Windows/Linux)." ]
            , paragraph [ Font.size 14, Font.color (rgb255 70 93 121) ]
                [ text "Search for "
                , el [ Font.semiBold ] (text "\"Mar Language Support\"")
                , text " and click Install."
                ]
            ]
        , paragraph
            [ Font.size 14
            , Font.color (rgb255 72 95 123)
            , width fill
            ]
            [ text "The VSCode extension requires mar on your PATH to start LSP and formatting." ]
        ]


stepBadge : String -> Element Msg
stepBadge value =
    el
        [ Font.bold
        , Font.size 15
        , Font.color (rgb255 34 76 122)
        , Background.color (rgb255 224 236 252)
        , Border.rounded 999
        , paddingEach { top = 3, right = 8, bottom = 3, left = 8 }
        ]
        (text value)


features : Element Msg
features =
    panel
        [ sectionTitle "Why Mar"
        , whyLayoutChosen
        ]


audience : Element Msg
audience =
    panel
        [ sectionTitle "Who Mar Is For"
        , audienceVariantOne
        ]


panel : List (Element Msg) -> Element Msg
panel children =
    column
        [ width (fill |> maximum 1040)
        , centerX
        , spacing 12
        , padding 16
        , Background.color (rgb255 255 255 255)
        , Border.width 1
        , Border.color (rgb255 209 222 239)
        , Border.rounded 12
        ]
        children


sectionTitle : String -> Element Msg
sectionTitle label =
    paragraph [ Font.size 26, Font.bold, Font.color (rgb255 20 53 89) ] [ text label ]


bodyText : String -> Element Msg
bodyText value =
    paragraph
        [ Font.size 16
        , Font.color (rgb255 72 95 123)
        , width fill
        ]
        [ text value ]


paragraphWithEmphasis : List (Element Msg) -> Element Msg
paragraphWithEmphasis children =
    paragraph
        [ Font.size 16
        , Font.color (rgb255 72 95 123)
        , width fill
        ]
        children


emphasisText : String -> Element Msg
emphasisText value =
    el
        [ Font.semiBold
        , Font.color (rgb255 28 66 108)
        ]
        (text value)


subsectionLabel : String -> Element Msg
subsectionLabel label =
    el
        [ Font.size 14
        , Font.semiBold
        , Font.color (rgb255 61 91 125)
        , Background.color (rgb255 242 247 255)
        , Border.width 1
        , Border.color (rgb255 215 226 241)
        , Border.rounded 999
        , paddingEach { top = 5, right = 10, bottom = 5, left = 10 }
        ]
        (text label)


docSubsectionTitle : String -> Element Msg
docSubsectionTitle label =
    paragraph
        [ Font.size 20
        , Font.bold
        , Font.color (rgb255 28 66 108)
        , paddingEach { top = 6, right = 0, bottom = 0, left = 0 }
        ]
        [ text label ]


architectureDiagram : Element Msg
architectureDiagram =
    column
        [ width (fill |> maximum 760)
        , centerX
        , spacing 10
        ]
        [ architectureNode "Source" ".mar file"
        , architectureArrow
        , architectureNode "Parser" "AST + expressions"
        , architectureArrow
        , architectureNode "Model" "typed app definition"
        , architectureArrow
        , architectureNode "Validation" "entities, auth, actions"
        , architectureArrow
        , wrappedRow
            [ width fill
            , spacing 10
            ]
            [ architectureNode "Generated clients" "Elm + TypeScript client code"
            , architectureNode "App bundle" "Manifest + admin UI + optional public files"
            ]
        , architectureArrow
        , architectureNode "Runtime stubs" "Prebuilt executables for supported OS/arch targets"
        , architectureArrow
        , architectureNode "Packager" "Inject app bundle into each runtime stub"
        , architectureArrow
        , architectureNode "Executables" "Self-contained binaries for all supported platforms"
        ]


architectureNode : String -> String -> Element Msg
architectureNode title detail =
    column
        [ width (fill |> maximum 360)
        , centerX
        , spacing 4
        , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
        , Background.color (rgb255 246 250 255)
        , Border.width 1
        , Border.color (rgb255 211 224 241)
        , Border.rounded 10
        ]
        [ paragraph [ Font.size 16, Font.bold, Font.color (rgb255 28 66 108) ] [ text title ]
        , paragraph [ Font.size 14, Font.color (rgb255 86 107 133) ] [ text detail ]
        ]


architectureArrow : Element Msg
architectureArrow =
    el
        [ centerX
        , Font.size 24
        , Font.bold
        , Font.color (rgb255 128 150 178)
        , paddingEach { top = 6, right = 2, bottom = 6, left = 2 }
        ]
        (text "↓")


whyOptionLabel : String -> Element Msg
whyOptionLabel label =
    el
        [ Font.size 13
        , Font.semiBold
        , Font.color (rgb255 84 108 136)
        , Background.color (rgb255 239 245 252)
        , Border.rounded 999
        , paddingEach { top = 4, right = 10, bottom = 4, left = 10 }
        ]
        (text label)


whyHeadline : Element Msg
whyHeadline =
    paragraph [ Font.size 30, Font.bold, Font.color (rgb255 19 49 84), width fill ]
        [ text "Less glue code. More backend." ]


whyDeclarative : Element Msg
whyDeclarative =
    whyFeatureStrip "Declarative at its core" "You describe the system at a higher level."


whyOpinionated : Element Msg
whyOpinionated =
    whyFeatureStrip "Opinionated on purpose" "Mar chooses a coherent runtime instead of exposing endless assembly decisions."


whyBundled : Element Msg
whyBundled =
    whyFeatureStrip "Everything bundled" "Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."


whyLayoutOne : Element Msg
whyLayoutOne =
    wrappedRow [ width fill, spacing 18 ]
        [ column
            [ width (fill |> maximum 360)
            , spacing 12
            , padding 20
            , Background.color (rgb255 22 49 82)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 30, Font.bold, Font.color (rgb255 246 250 255), width fill ]
                [ text "Less glue code. More backend." ]
            ]
        , column
            [ width fill
            , spacing 10
            ]
            [ whyDeclarative
            , whyOpinionated
            , whyBundled
            ]
        ]


whyLayoutChosen : Element Msg
whyLayoutChosen =
    wrappedRow [ width fill, spacing 18 ]
        [ column
            [ width (fill |> maximum 380)
            , spacing 14
            , padding 22
            , Background.color (rgb255 22 49 82)
            , Border.width 1
            , Border.color (rgb255 35 74 117)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 32, Font.bold, Font.color (rgb255 246 250 255), width fill ]
                [ text "Less glue code. More backend." ]
            , paragraph [ Font.size 16, Font.color (rgb255 205 220 238), width fill ]
                [ text "Declarative at its core. Opinionated on purpose. Everything bundled." ]
            ]
        , column
            [ width fill
            , spacing 10
            ]
            [ whyDeclarative
            , whyOpinionated
            , whyBundled
            ]
        ]


whyLayoutTwo : Element Msg
whyLayoutTwo =
    column
        [ width fill
        , spacing 12
        , padding 18
        , Background.color (rgb255 247 250 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 16
        ]
        [ whyHeadline
        , wrappedRow [ width fill, spacing 12 ]
            [ whyGridCard "Declarative by default" "You describe the system at a higher level."
            , whyGridCard "Opinionated on purpose" "Mar chooses a coherent runtime instead of exposing endless assembly decisions."
            , whyGridCard "Everything bundled" "Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."
            ]
        ]


whyLayoutThree : Element Msg
whyLayoutThree =
    column
        [ width fill
        , spacing 12
        ]
        [ whyHeadline
        , whyBand "Declarative by default" "You describe the system at a higher level."
        , whyBand "Opinionated on purpose" "Mar chooses a coherent runtime instead of exposing endless assembly decisions."
        , whyBand "Everything bundled" "Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."
        ]


whyGridCard : String -> String -> Element Msg
whyGridCard title description =
    column
        [ width (fill |> maximum 500)
        , spacing 8
        , padding 16
        , Background.color (rgb255 248 251 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 14
        ]
        [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 31 51 76), width fill ]
            [ text title ]
        , paragraph [ Font.size 15, Font.color (rgb255 83 101 124), width fill ]
            [ text description ]
        ]


whyLayoutFour : Element Msg
whyLayoutFour =
    wrappedRow [ width fill, spacing 12 ]
        [ whyMiniCard "Less glue code. More backend." " "
        , whyMiniCard "Declarative by default" "You describe the system at a higher level."
        , whyMiniCard "Opinionated on purpose" "Mar chooses a coherent runtime instead of exposing endless assembly decisions."
        , whyMiniCard "Everything bundled" "Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."
        ]


whyLayoutFive : Element Msg
whyLayoutFive =
    column [ width fill, spacing 10 ]
        [ whyHeadline
        , wrappedRow [ width fill, spacing 10 ]
            [ whyMinimalCard "Declarative by default"
            , whyMinimalCard "Opinionated on purpose"
            , whyMinimalCard "Everything bundled"
            ]
        ]


whyLayoutSix : Element Msg
whyLayoutSix =
    wrappedRow [ width fill, spacing 18 ]
        [ column [ width (fill |> maximum 320), spacing 10 ]
            [ whyHeadline
            , bodyText "A backend should not require a long trail of handwritten infrastructure before it starts to feel whole."
            ]
        , column [ width fill, spacing 8 ]
            [ whyChecklistItem "Declarative by default"
            , whyChecklistItem "Opinionated on purpose"
            , whyChecklistItem "Everything bundled"
            ]
        ]


whyLayoutSeven : Element Msg
whyLayoutSeven =
    column
        [ width fill
        , spacing 12
        , padding 22
        , Background.color (rgb255 22 49 82)
        , Border.width 1
        , Border.color (rgb255 35 74 117)
        , Border.rounded 16
        ]
        [ paragraph [ Font.size 32, Font.bold, Font.color (rgb255 246 250 255), width fill, centerX ]
            [ text "Less glue code. More backend." ]
        , paragraph [ Font.size 16, Font.color (rgb255 205 220 238), width fill, centerX ]
            [ text "Declarative by default. Opinionated on purpose. Everything bundled." ]
        ]


whyLayoutEight : Element Msg
whyLayoutEight =
    column [ width fill, spacing 10 ]
        [ whyHeadline
        , wrappedRow [ width fill, spacing 10 ]
            [ whyFeatureStrip "Declarative by default" "You describe the system at a higher level."
            , whyFeatureStrip "Opinionated on purpose" "Mar chooses a coherent runtime instead of exposing endless assembly decisions."
            ]
        , whyFeatureStrip "Everything bundled" "Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."
        ]


whyLayoutNine : Element Msg
whyLayoutNine =
    wrappedRow [ width fill, spacing 14 ]
        [ column
            [ width (fill |> maximum 420)
            , spacing 12
            ]
            [ whyHeadline
            , bodyText "Mar is most compelling when you want the backend stack to make stronger decisions for you."
            ]
        , column
            [ width fill
            , spacing 8
            ]
            [ whySimpleBullet "Declarative by default"
            , whySimpleBullet "Opinionated on purpose"
            , whySimpleBullet "Everything bundled"
            ]
        ]


whyLayoutTen : Element Msg
whyLayoutTen =
    column [ width fill, spacing 12 ]
        [ whyHeadline
        , column
            [ width fill
            , spacing 8
            , padding 18
            , Background.color (rgb255 248 251 255)
            , Border.width 1
            , Border.color (rgb255 214 225 239)
            , Border.rounded 16
            ]
            [ whySimpleBullet "Declarative by default"
            , whySimpleBullet "Opinionated on purpose"
            , whySimpleBullet "Everything bundled"
            ]
        ]


whyMiniCard : String -> String -> Element Msg
whyMiniCard title description =
    column
        [ width (fill |> maximum 240)
        , spacing 6
        , padding 14
        , Background.color (rgb255 248 251 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 14
        ]
        [ paragraph [ Font.size 17, Font.bold, Font.color (rgb255 30 48 72), width fill ]
            [ text title ]
        , paragraph [ Font.size 14, Font.color (rgb255 83 101 124), width fill ]
            [ text description ]
        ]


whyChecklistItem : String -> Element Msg
whyChecklistItem value =
    row [ width fill, spacing 10 ]
        [ el
            [ Font.size 14
            , Font.bold
            , Font.color (rgb255 28 122 84)
            , Background.color (rgb255 232 247 240)
            , Border.rounded 999
            , paddingEach { top = 3, right = 7, bottom = 3, left = 7 }
            ]
            (text "✓")
        , paragraph [ width fill, Font.size 15, Font.color (rgb255 76 93 117) ] [ text value ]
        ]


whySimpleBullet : String -> Element Msg
whySimpleBullet value =
    row [ width fill, spacing 10 ]
        [ el [ Font.color (rgb255 101 122 148), Font.bold ] (text "•")
        , paragraph [ width fill, Font.size 15, Font.color (rgb255 76 93 117) ] [ text value ]
        ]


whyBand : String -> String -> Element Msg
whyBand title description =
    column
        [ width fill
        , spacing 4
        , padding 16
        , Background.color (rgb255 247 250 255)
        , Border.width 1
        , Border.color (rgb255 216 226 239)
        , Border.rounded 14
        ]
        [ paragraph [ Font.size 21, Font.bold, Font.color (rgb255 30 48 72), width fill ]
            [ text title ]
        , paragraph [ Font.size 15, Font.color (rgb255 83 101 124), width fill ]
            [ text description ]
        ]


whyMinimalCard : String -> Element Msg
whyMinimalCard title =
    el
        [ Background.color (rgb255 246 250 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 999
        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
        , Font.size 15
        , Font.semiBold
        , Font.color (rgb255 33 56 84)
        ]
        (text title)


whyFeatureStrip : String -> String -> Element Msg
whyFeatureStrip title description =
    row
        [ width fill
        , spacing 14
        , padding 14
        , Background.color (rgb255 248 251 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 14
        ]
        [ el
            [ Font.size 22
            , Font.bold
            , Font.color (rgb255 92 126 168)
            , paddingEach { top = 2, right = 6, bottom = 0, left = 2 }
            ]
            (text "→")
        , column [ width fill, spacing 4 ]
            [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 31 51 76), width fill ]
                [ text title ]
            , paragraph [ Font.size 15, Font.color (rgb255 83 101 124), width fill ]
                [ text description ]
            ]
        ]


useCaseRow : String -> String -> String -> Element Msg
useCaseRow audienceTitle pain solution =
    column
        [ width fill
        , spacing 14
        , padding 12
        , Background.color (rgb255 246 250 255)
        , Border.width 1
        , Border.color (rgb255 211 224 241)
        , Border.rounded 10
        ]
        [ el
            [ Font.size 18
            , Font.bold
            , Font.color (rgb255 42 58 77)
            ]
            (text audienceTitle)
        , column [ width fill, spacing 4 ]
            [ paragraph [ Font.size 16, Font.color (rgb255 93 107 126) ] [ text pain ]
            , paragraph [ Font.size 16, Font.color (rgb255 68 86 108), Font.semiBold ] [ text solution ]
            ]
        ]


audienceOptionBlock : String -> List (Element Msg) -> Element Msg
audienceOptionBlock label children =
    column
        [ width fill
        , spacing 12
        , padding 16
        , Background.color (rgb255 250 252 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 14
        ]
        (el
            [ Font.size 13
            , Font.semiBold
            , Font.color (rgb255 34 76 122)
            , Background.color (rgb255 226 237 252)
            , Border.rounded 999
            , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
            ]
            (text label)
            :: children
        )


audiencePersonaCard : String -> String -> String -> Element Msg
audiencePersonaCard title summary fit =
    column
        [ width (fill |> minimum 220)
        , spacing 8
        , padding 14
        , Background.color (rgb255 245 249 255)
        , Border.width 1
        , Border.color (rgb255 211 224 241)
        , Border.rounded 12
        ]
        [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 28 66 108), width fill ]
            [ text title ]
        , paragraph [ Font.size 15, Font.color (rgb255 78 101 130), width fill ]
            [ text summary ]
        , paragraph [ Font.size 14, Font.color (rgb255 58 84 115), Font.semiBold, width fill ]
            [ text fit ]
        ]


audienceTag : String -> Element Msg
audienceTag value =
    el
        [ Font.size 14
        , Font.semiBold
        , Font.color (rgb255 34 76 122)
        , Background.color (rgb255 232 241 252)
        , Border.rounded 999
        , paddingEach { top = 8, right = 12, bottom = 8, left = 12 }
        ]
        (text value)


audienceStatement : String -> String -> Element Msg
audienceStatement title description =
    column
        [ width fill
        , spacing 4
        ]
        [ paragraph [ Font.size 18, Font.bold, Font.color (rgb255 31 51 76), width fill ]
            [ text title ]
        , paragraph [ Font.size 15, Font.color (rgb255 83 101 124), width fill ]
            [ text description ]
        ]


audienceOptionOne : Element Msg
audienceOptionOne =
    audienceOptionBlock "Option 1"
        [ paragraph [ Font.size 28, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "For small teams that want the backend to feel complete early." ]
        , wrappedRow [ width fill, spacing 12 ]
            [ audiencePersonaCard "Solo founders" "You need to move from idea to working product without wiring five tools first." "Mar gives you auth, admin tools, logs, monitoring, and backups in one place."
            , audiencePersonaCard "Product engineers" "You want a declarative backend that still ships as one real server binary." "Mar keeps the moving parts small and the result operational."
            , audiencePersonaCard "Small teams" "You want conventions that remove backend glue code instead of multiplying it." "Mar stays opinionated so the system remains easier to maintain."
            ]
        ]


audienceOptionTwo : Element Msg
audienceOptionTwo =
    audienceOptionBlock "Option 2"
        [ wrappedRow [ width fill, spacing 18 ]
            [ column [ width (fill |> maximum 320), spacing 10 ]
                [ paragraph [ Font.size 29, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                    [ text "A strong fit when simplicity matters more than endless backend assembly." ]
                , bodyText "Mar works best when you want to describe the system at a higher level and still end up with something you can run, observe, and deploy easily."
                ]
            , column [ width fill, spacing 10 ]
                [ whyChecklistItem "You prefer declarative backend code over framework sprawl."
                , whyChecklistItem "You want one binary instead of a trail of infrastructure glue."
                , whyChecklistItem "You want auth, admin tools, logs, and backups built in."
                ]
            ]
        ]


audienceOptionThree : Element Msg
audienceOptionThree =
    audienceOptionBlock "Option 3"
        [ paragraph [ Font.size 27, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Who usually gets value from Mar?" ]
        , wrappedRow [ width fill, spacing 10 ]
            [ audienceTag "Declarative-first developers"
            , audienceTag "Internal tools"
            , audienceTag "Lean MVPs"
            , audienceTag "Small product teams"
            , audienceTag "One-person backends"
            ]
        , bodyText "If you want the backend to stay coherent, operational, and easy to update without a lot of handwritten glue, Mar is aimed at that shape of work."
        ]


audienceOptionFour : Element Msg
audienceOptionFour =
    audienceOptionBlock "Option 4"
        [ wrappedRow [ width fill, spacing 12 ]
            [ column
                [ width fill
                , spacing 8
                ]
                [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 28 66 108), width fill ]
                    [ text "Best when" ]
                , whySimpleBullet "You want one runtime with clear conventions."
                , whySimpleBullet "You care about easier maintenance over maximum flexibility."
                , whySimpleBullet "You want built-in operational features from day one."
                ]
            , column
                [ width fill
                , spacing 8
                ]
                [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 28 66 108), width fill ]
                    [ text "Less ideal when" ]
                , whySimpleBullet "You want a highly unopinionated backend framework."
                , whySimpleBullet "You expect every decision to stay fully pluggable."
                , whySimpleBullet "You want to assemble the stack piece by piece yourself."
                ]
            ]
        ]


audienceOptionFive : Element Msg
audienceOptionFive =
    audienceOptionBlock "Option 5"
        [ audienceStatement "For developers who like declarative systems" "Mar keeps the core of the backend readable and intention-first."
        , audienceStatement "For teams shipping early versions of products" "Mar gives you auth, admin tools, logs, monitoring, and backups without building them one by one."
        , audienceStatement "For products that should stay easy to operate" "Mar compiles to one server executable that is easier to move, run, update, and reason about."
        ]


audienceOptionSix : Element Msg
audienceOptionSix =
    audienceOptionBlock "Option 6"
        [ paragraph [ Font.size 30, Font.bold, Font.color (rgb255 20 53 89), centerX ]
            [ text "Built for small backend surfaces that still need real operational depth." ]
        , wrappedRow [ width fill, spacing 12, centerX ]
            [ whyMiniCard "Lean teams" "Move faster without spending early time wiring the usual backend pieces."
            , whyMiniCard "Opinionated builders" "Prefer coherent defaults to a long list of assembly choices."
            , whyMiniCard "Shipping-focused developers" "Want development to feel simple and deploy to feel even simpler."
            ]
        ]


audienceOptionSeven : Element Msg
audienceOptionSeven =
    audienceOptionBlock "Option 7"
        [ useCaseRow "Founders building the first version" "You want the first backend to feel complete fast." "Mar shortens the path from schema to working product."
        , useCaseRow "Engineers replacing repetitive backend setup" "You are tired of re-creating auth, admin, and operations over and over." "Mar turns those into built-in parts of the runtime."
        , useCaseRow "Teams maintaining small products for a long time" "You care about keeping complexity under control after launch." "Mar keeps the system easier to update and reason about."
        ]


audienceOptionEight : Element Msg
audienceOptionEight =
    audienceOptionBlock "Option 8"
        [ wrappedRow [ width fill, spacing 14 ]
            [ whyBand "Declarative teams" "You prefer describing the system instead of hand-assembling each layer."
            , whyBand "Operationally minded teams" "You want logs, monitoring, admin tools, and backups to exist from the beginning."
            , whyBand "Maintenance-conscious teams" "You want a backend that stays understandable after the first release."
            ]
        ]


audienceOptionNine : Element Msg
audienceOptionNine =
    audienceOptionBlock "Option 9"
        [ paragraph [ Font.size 26, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Mar is for people who want the backend to feel like a product, not a pile of setup." ]
        , wrappedRow [ width fill, spacing 12 ]
            [ audiencePersonaCard "Indie products" "You need to ship with a small team and keep the stack tight." "One binary. Fewer moving parts."
            , audiencePersonaCard "Internal tools" "You want admin and operational visibility without building a frontend first." "The runtime already includes those tools."
            , audiencePersonaCard "Long-lived small systems" "You want updates to stay manageable over time." "Opinionated structure helps avoid entropy."
            ]
        ]


audienceOptionTen : Element Msg
audienceOptionTen =
    audienceOptionBlock "Option 10"
        [ paragraph [ Font.size 24, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "A good match if these sound familiar:" ]
        , whyChecklistItem "“I want less backend glue code.”"
        , whyChecklistItem "“I want auth and operations built in.”"
        , whyChecklistItem "“I want the first deploy to be simple.”"
        , whyChecklistItem "“I want the backend to stay easy to maintain.”"
        ]


audienceOptionEleven : Element Msg
audienceOptionEleven =
    audienceOptionBlock "Option 11"
        [ wrappedRow [ width fill, spacing 16 ]
            [ column [ width (fill |> maximum 340), spacing 10 ]
                [ paragraph [ Font.size 30, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                    [ text "Who tends to like Mar?" ]
                , bodyText "People who value a coherent backend more than total framework flexibility."
                ]
            , column [ width fill, spacing 8 ]
                [ audienceStatement "Declarative builders" "You want backend code to express intent clearly."
                , audienceStatement "Small teams" "You want one tool to cover more of the backend surface."
                , audienceStatement "Maintenance-minded teams" "You want upgrades and operations to stay straightforward."
                ]
            ]
        ]


audienceOptionTwelve : Element Msg
audienceOptionTwelve =
    audienceOptionBlock "Option 12"
        [ paragraph [ Font.size 28, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Mar fits best when speed and coherence matter at the same time." ]
        , wrappedRow [ width fill, spacing 10 ]
            [ audienceTag "Ship early"
            , audienceTag "Keep one binary"
            , audienceTag "Reduce glue code"
            , audienceTag "Stay easy to operate"
            , audienceTag "Keep maintenance light"
            ]
        ]


audienceOptionThirteen : Element Msg
audienceOptionThirteen =
    audienceOptionBlock "Option 13"
        [ commandRowStyle "1" "Describe the system" "You prefer a declarative source of truth."
        , commandRowStyle "2" "Run a complete backend" "You want auth, admin tools, logs, monitoring, and backups already in place."
        , commandRowStyle "3" "Keep operating simply" "You want updates and deployments to stay low-friction."
        ]


audienceOptionFourteen : Element Msg
audienceOptionFourteen =
    audienceOptionBlock "Option 14"
        [ wrappedRow [ width fill, spacing 12 ]
            [ audiencePersonaCard "Backend generalists" "You do not want to spend another week stitching routine pieces together." "Mar narrows the assembly work."
            , audiencePersonaCard "Founders wearing many hats" "You need the backend to come with product and operational basics." "Mar ships with the fundamentals built in."
            , audiencePersonaCard "Teams avoiding backend sprawl" "You want fewer systems to keep in sync." "Mar favors one coherent runtime."
            ]
        ]


audienceOptionFifteen : Element Msg
audienceOptionFifteen =
    audienceOptionBlock "Option 15"
        [ paragraph [ Font.size 25, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "If your ideal backend is small, declarative, and already operational, Mar is probably for you." ]
        , bodyText "It works especially well when you want to spend more effort on product logic and less on backend plumbing."
        ]


audienceOptionSixteen : Element Msg
audienceOptionSixteen =
    audienceOptionBlock "Option 16"
        [ wrappedRow [ width fill, spacing 12 ]
            [ whyGridCard "One-person products" "You need the backend to cover more ground with less setup."
            , whyGridCard "Small customer-facing apps" "You want a complete backend shape without building every layer."
            , whyGridCard "Internal systems" "You want admin tools and observability without building them from scratch."
            ]
        ]


audienceOptionSeventeen : Element Msg
audienceOptionSeventeen =
    audienceOptionBlock "Option 17"
        [ audienceStatement "Mar is for teams that like opinions" "Not because flexibility is bad, but because coherent defaults make small systems faster to build and easier to keep healthy."
        , audienceStatement "Mar is for teams that value operational clarity" "Logs, monitoring, admin tools, and backups are part of the runtime story, not postponed work."
        ]


audienceOptionEighteen : Element Msg
audienceOptionEighteen =
    audienceOptionBlock "Option 18"
        [ paragraph [ Font.size 24, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Use Mar if you want the first backend to already feel structured." ]
        , whySimpleBullet "The language stays declarative."
        , whySimpleBullet "The runtime stays opinionated."
        , whySimpleBullet "The deploy stays small."
        , whySimpleBullet "The system stays easier to maintain."
        ]


audienceOptionNineteen : Element Msg
audienceOptionNineteen =
    audienceOptionBlock "Option 19"
        [ wrappedRow [ width fill, spacing 14 ]
            [ column [ width fill, spacing 8 ]
                [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 28 66 108), width fill ]
                    [ text "For" ]
                , whySimpleBullet "Founders shipping MVPs"
                , whySimpleBullet "Teams building focused internal tools"
                , whySimpleBullet "Developers who want fewer moving parts"
                ]
            , column [ width fill, spacing 8 ]
                [ paragraph [ Font.size 19, Font.bold, Font.color (rgb255 28 66 108), width fill ]
                    [ text "Because" ]
                , whySimpleBullet "Mar removes common backend repetition."
                , whySimpleBullet "Mar bundles the operational essentials."
                , whySimpleBullet "Mar compiles to one deployable executable."
                ]
            ]
        ]


audienceOptionTwenty : Element Msg
audienceOptionTwenty =
    audienceOptionBlock "Option 20"
        [ paragraph [ Font.size 28, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Not every backend needs Mar. The right ones usually want clarity, speed, and fewer seams." ]
        , wrappedRow [ width fill, spacing 12 ]
            [ audienceTag "Declarative source"
            , audienceTag "Single executable"
            , audienceTag "Built-in operations"
            , audienceTag "Opinionated runtime"
            ]
        ]


audienceOptionTwentyOne : Element Msg
audienceOptionTwentyOne =
    audienceOptionBlock "Option 21"
        [ audienceStatement "Founders and solo builders" "Mar helps when the backend needs to feel complete before you have time to build every missing operational piece."
        , audienceStatement "Small product teams" "Mar helps when you want conventions that make it easier to move and easier to revisit later."
        , audienceStatement "Developers who dislike backend sprawl" "Mar helps when you would rather operate one coherent server than a loose collection of parts."
        ]


audienceOptionTwentyTwo : Element Msg
audienceOptionTwentyTwo =
    audienceOptionBlock "Option 22"
        [ paragraph [ Font.size 29, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "For developers who would rather describe the system than assemble it all manually." ]
        , bodyText "Mar favors that kind of work: declarative language, one runtime, one binary, and operational features already present."
        ]


audienceOptionTwentyThree : Element Msg
audienceOptionTwentyThree =
    audienceOptionBlock "Option 23"
        [ wrappedRow [ width fill, spacing 10 ]
            [ whyMiniCard "MVPs" "Ship a first version without postponing auth and operations."
            , whyMiniCard "Internal tools" "Get a usable backend surface early, including admin tools."
            , whyMiniCard "Small products" "Keep the backend compact enough to maintain comfortably."
            , whyMiniCard "Focused teams" "Trade endless configurability for stronger defaults."
            ]
        ]


audienceOptionTwentyFour : Element Msg
audienceOptionTwentyFour =
    audienceOptionBlock "Option 24"
        [ whyFeatureStrip "For teams that want less wiring" "Mar reduces the hand-built backend glue that usually grows around small systems."
        , whyFeatureStrip "For teams that want deploys to stay boring" "One executable is easier to move, run, and update."
        , whyFeatureStrip "For teams that want observability early" "Logs, monitoring, and admin tools are already there."
        ]


audienceOptionTwentyFive : Element Msg
audienceOptionTwentyFive =
    audienceOptionBlock "Option 25"
        [ paragraph [ Font.size 27, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Mar is especially appealing when the backend should stay small without feeling incomplete." ]
        , wrappedRow [ width fill, spacing 12 ]
            [ audiencePersonaCard "First product versions" "You need a backend that is ready to use, not just ready to extend." "Built-ins matter here."
            , audiencePersonaCard "Pragmatic teams" "You want decisions made well enough once, not reopened every sprint." "Opinionated defaults help."
            ]
        ]


audienceOptionTwentySix : Element Msg
audienceOptionTwentySix =
    audienceOptionBlock "Option 26"
        [ audienceStatement "Best for backends that should be quick to build" "Mar keeps the language high level and the runtime complete."
        , audienceStatement "Best for backends that should be quick to deploy" "One executable keeps the operational footprint smaller."
        , audienceStatement "Best for backends that should stay easy to update" "Coherent structure helps the system resist drift."
        ]


audienceOptionTwentySeven : Element Msg
audienceOptionTwentySeven =
    audienceOptionBlock "Option 27"
        [ paragraph [ Font.size 24, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Use Mar if these are the words you want associated with the backend:" ]
        , wrappedRow [ width fill, spacing 10 ]
            [ audienceTag "Declarative"
            , audienceTag "Opinionated"
            , audienceTag "Traceable"
            , audienceTag "Easy to deploy"
            , audienceTag "Easy to maintain"
            ]
        ]


audienceOptionTwentyEight : Element Msg
audienceOptionTwentyEight =
    audienceOptionBlock "Option 28"
        [ wrappedRow [ width fill, spacing 16 ]
            [ column [ width fill, spacing 10 ]
                [ paragraph [ Font.size 28, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                    [ text "Who is it really for?" ]
                , bodyText "People who want a backend with stronger opinions and fewer seams."
                ]
            , column [ width fill, spacing 8 ]
                [ whyChecklistItem "You want auth and authorization built in."
                , whyChecklistItem "You want logs and monitoring from the start."
                , whyChecklistItem "You want deployment to stay straightforward."
                ]
            ]
        ]


audienceOptionTwentyNine : Element Msg
audienceOptionTwentyNine =
    audienceOptionBlock "Option 29"
        [ paragraph [ Font.size 26, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text "Mar is for projects where the backend should stay boring in the best way." ]
        , bodyText "Clear language, one runtime, built-in operations, and less assembly work tend to matter most in small teams, internal tools, and early-stage products."
        ]


audienceOptionThirty : Element Msg
audienceOptionThirty =
    audienceOptionBlock "Option 30"
        [ wrappedRow [ width fill, spacing 12 ]
            [ audiencePersonaCard "If you want declarative backend code" "Mar keeps the main source of truth higher level." "Less imperative glue."
            , audiencePersonaCard "If you want a complete runtime" "Mar bundles auth, admin tools, logs, monitoring, and backups." "Less missing infrastructure."
            , audiencePersonaCard "If you want simpler operations" "Mar compiles to one executable that is easier to move and update." "Less deployment overhead."
            ]
        ]


commandRowStyle : String -> String -> String -> Element Msg
commandRowStyle number label description =
    column
        [ width fill
        , spacing 6
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
        ]
        [ wrappedRow [ width fill, spacing 10 ]
            [ stepBadge number
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text label)
            ]
        , paragraph [ Font.size 14, Font.color (rgb255 83 105 132), width fill ]
            [ text description ]
        ]


audienceHeadlineCopy : String
audienceHeadlineCopy =
    "For teams that want the backend to feel boring in the best way."


audienceSummaryCopy : String
audienceSummaryCopy =
    "Predictable to run. Easy to reason about. Small enough to maintain without losing operational depth."


audienceNeedOne : String
audienceNeedOne =
    "You want declarative backend code."


audienceNeedTwo : String
audienceNeedTwo =
    "You want built-in auth, admin tools, logs, monitoring, and backups."


audienceNeedThree : String
audienceNeedThree =
    "You want deployment to stay simple."


audienceNeedCards : List (Element Msg)
audienceNeedCards =
    [ audienceNeedStrip audienceNeedOne
    , audienceNeedStrip audienceNeedTwo
    , audienceNeedStrip audienceNeedThree
    ]


audienceNeedChecks : List (Element Msg)
audienceNeedChecks =
    [ whyChecklistItem audienceNeedOne
    , whyChecklistItem audienceNeedTwo
    , whyChecklistItem audienceNeedThree
    ]


audienceVariantOne : Element Msg
audienceVariantOne =
    wrappedRow [ width fill, spacing 18 ]
        [ column
            [ width (fill |> maximum 380)
            , spacing 10
            , padding 22
            , Background.color (rgb255 52 88 133)
            , Border.width 1
            , Border.color (rgb255 77 114 161)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 30, Font.bold, Font.color (rgb255 246 250 255), width fill ]
                [ text audienceHeadlineCopy ]
            , paragraph [ Font.size 16, Font.color (rgb255 211 224 241), width fill ]
                [ text audienceSummaryCopy ]
            ]
        , column [ width fill, spacing 10 ]
            audienceNeedCards
        ]


audienceNeedStrip : String -> Element Msg
audienceNeedStrip value =
    row
        [ width fill
        , spacing 14
        , padding 14
        , Background.color (rgb255 248 251 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 14
        ]
        [ el
            [ Font.size 14
            , Font.bold
            , Font.color (rgb255 28 122 84)
            , Background.color (rgb255 232 247 240)
            , Border.rounded 999
            , paddingEach { top = 3, right = 7, bottom = 3, left = 7 }
            ]
            (text "✓")
        , paragraph [ Font.size 17, Font.color (rgb255 31 51 76), width fill ]
            [ text value ]
        ]


audienceVariantTwo : Element Msg
audienceVariantTwo =
    column
        [ width fill
        , spacing 12
        , padding 20
        , Background.color (rgb255 245 249 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 16
        ]
        [ paragraph [ Font.size 29, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text audienceHeadlineCopy ]
        , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
            [ text audienceSummaryCopy ]
        , wrappedRow [ width fill, spacing 12 ]
            [ whyMiniCard "Declarative" audienceNeedOne
            , whyMiniCard "Built-in runtime" audienceNeedTwo
            , whyMiniCard "Simple deploy" audienceNeedThree
            ]
        ]


audienceVariantThree : Element Msg
audienceVariantThree =
    wrappedRow [ width fill, spacing 18 ]
        [ column
            [ width (fill |> maximum 360)
            , spacing 10
            , padding 20
            , Background.color (rgb255 237 244 252)
            , Border.width 1
            , Border.color (rgb255 203 218 238)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 28, Font.bold, Font.color (rgb255 25 58 96), width fill ]
                [ text audienceHeadlineCopy ]
            , paragraph [ Font.size 16, Font.color (rgb255 74 97 124), width fill ]
                [ text audienceSummaryCopy ]
            ]
        , column [ width fill, spacing 8 ]
            audienceNeedChecks
        ]


audienceVariantFour : Element Msg
audienceVariantFour =
    wrappedRow [ width fill, spacing 18 ]
        [ column [ width fill, spacing 10 ]
            [ paragraph [ Font.size 29, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                [ text audienceHeadlineCopy ]
            , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                [ text audienceSummaryCopy ]
            ]
        , column
            [ width (fill |> maximum 360)
            , spacing 10
            , padding 18
            , Background.color (rgb255 28 55 88)
            , Border.width 1
            , Border.color (rgb255 44 82 126)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 16, Font.color (rgb255 230 240 251), width fill ]
                [ text audienceNeedOne ]
            , paragraph [ Font.size 16, Font.color (rgb255 230 240 251), width fill ]
                [ text audienceNeedTwo ]
            , paragraph [ Font.size 16, Font.color (rgb255 230 240 251), width fill ]
                [ text audienceNeedThree ]
            ]
        ]


audienceVariantFive : Element Msg
audienceVariantFive =
    column
        [ width fill
        , spacing 14
        , padding 20
        , Background.color (rgb255 249 251 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 16
        ]
        [ paragraph [ Font.size 30, Font.bold, Font.color (rgb255 20 53 89), width fill ]
            [ text audienceHeadlineCopy ]
        , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
            [ text audienceSummaryCopy ]
        , column [ width fill, spacing 10 ]
            audienceNeedCards
        ]


audienceVariantSix : Element Msg
audienceVariantSix =
    wrappedRow [ width fill, spacing 16 ]
        [ column
            [ width (fill |> maximum 320)
            , spacing 8
            ]
            [ el
                [ Font.size 14
                , Font.semiBold
                , Font.color (rgb255 34 76 122)
                , Background.color (rgb255 226 237 252)
                , Border.rounded 999
                , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
                ]
                (text "A good fit when")
            , paragraph [ Font.size 28, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                [ text audienceHeadlineCopy ]
            , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                [ text audienceSummaryCopy ]
            ]
        , wrappedRow [ width fill, spacing 10 ]
            [ whyBand "Declarative" audienceNeedOne
            , whyBand "Complete runtime" audienceNeedTwo
            , whyBand "Simple deploy" audienceNeedThree
            ]
        ]


audienceVariantSeven : Element Msg
audienceVariantSeven =
    column
        [ width fill
        , spacing 12
        ]
        [ row
            [ width fill
            , spacing 14
            , padding 18
            , Background.color (rgb255 22 49 82)
            , Border.width 1
            , Border.color (rgb255 35 74 117)
            , Border.rounded 16
            ]
            [ el [ Font.size 26, Font.color (rgb255 180 206 237), Font.bold ] (text "→")
            , column [ width fill, spacing 8 ]
                [ paragraph [ Font.size 28, Font.bold, Font.color (rgb255 246 250 255), width fill ]
                    [ text audienceHeadlineCopy ]
                , paragraph [ Font.size 16, Font.color (rgb255 205 220 238), width fill ]
                    [ text audienceSummaryCopy ]
                ]
            ]
        , wrappedRow [ width fill, spacing 12 ]
            [ whyMiniCard "Declarative backend code" "A higher-level way to describe the system."
            , whyMiniCard "Built-in operational depth" "Auth, admin tools, logs, monitoring, and backups."
            , whyMiniCard "Simple deployment" "One executable and fewer moving parts."
            ]
        ]


audienceVariantEight : Element Msg
audienceVariantEight =
    wrappedRow [ width fill, spacing 18 ]
        [ column
            [ width (fill |> maximum 400)
            , spacing 12
            , padding 18
            , Background.color (rgb255 246 250 255)
            , Border.width 1
            , Border.color (rgb255 214 225 239)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 29, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                [ text audienceHeadlineCopy ]
            , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                [ text audienceSummaryCopy ]
            ]
        , column
            [ width fill
            , spacing 10
            ]
            [ audienceNeedStrip audienceNeedOne
            , audienceNeedStrip audienceNeedTwo
            , audienceNeedStrip audienceNeedThree
            ]
        ]


audienceVariantNine : Element Msg
audienceVariantNine =
    column
        [ width fill
        , spacing 12
        , padding 20
        , Background.color (rgb255 250 252 255)
        , Border.width 1
        , Border.color (rgb255 214 225 239)
        , Border.rounded 16
        ]
        [ wrappedRow [ width fill, spacing 18 ]
            [ column [ width (fill |> maximum 420), spacing 8 ]
                [ paragraph [ Font.size 30, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                    [ text audienceHeadlineCopy ]
                , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                    [ text audienceSummaryCopy ]
                ]
            , el
                [ width fill
                , height fill
                ]
                (text "")
            ]
        , wrappedRow [ width fill, spacing 10 ]
            [ audienceTag "Declarative"
            , audienceTag "Built-in operations"
            , audienceTag "Simple deploy"
            ]
        , column [ width fill, spacing 8 ]
            audienceNeedChecks
        ]


audienceVariantTen : Element Msg
audienceVariantTen =
    wrappedRow [ width fill, spacing 16 ]
        [ column
            [ width (fill |> maximum 360)
            , spacing 10
            , padding 20
            , Border.width 1
            , Border.color (rgb255 214 225 239)
            , Border.rounded 16
            ]
            [ paragraph [ Font.size 29, Font.bold, Font.color (rgb255 20 53 89), width fill ]
                [ text audienceHeadlineCopy ]
            , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                [ text audienceSummaryCopy ]
            ]
        , column
            [ width fill
            , spacing 10
            , padding 18
            , Background.color (rgb255 242 248 255)
            , Border.width 1
            , Border.color (rgb255 206 222 242)
            , Border.rounded 16
            ]
            audienceNeedChecks
        ]


commandRow : Model -> String -> String -> String -> String -> Element Msg
commandRow model number label description command =
    column
        [ width fill
        , spacing 8
        , Background.color (rgb255 245 250 255)
        , Border.width 1
        , Border.color (rgb255 213 225 241)
        , Border.rounded 10
        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
        ]
        [ wrappedRow
            [ width fill
            , spacing 10
            ]
            [ stepBadge number
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text label)
            ]
        , wrappedRow
            [ width fill
            , spacing 8
            ]
            [ codeInline command
            , copyLink model command
            ]
        , paragraph
            [ Font.size 14
            , Font.color (rgb255 83 105 132)
            , width fill
            ]
            [ text description ]
        ]


resourceCard : String -> String -> Element Msg
resourceCard label target =
    link
        [ width (fill |> minimum 220)
        , Background.color (rgb255 242 248 255)
        , Border.width 1
        , Border.color (rgb255 206 222 242)
        , Border.rounded 10
        , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
        , Font.size 17
        , Font.color (rgb255 28 71 116)
        , Font.semiBold
        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
        ]
        { url = target
        , label = text label
        }


guideCard : String -> String -> Route -> Element Msg
guideCard title summary target =
    link
        [ width (fill |> minimum 220)
        , Background.color (rgb255 246 250 255)
        , Border.width 1
        , Border.color (rgb255 211 224 241)
        , Border.rounded 10
        , paddingEach { top = 14, right = 14, bottom = 14, left = 14 }
        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
        ]
        { url = routeHref target
        , label =
            column [ spacing 6, width fill ]
                [ paragraph [ Font.size 18, Font.bold, Font.color (rgb255 28 66 108) ] [ text title ]
                , paragraph [ Font.size 15, Font.color (rgb255 86 107 133), width fill ] [ text summary ]
                ]
        }


advancedSubmenu : Route -> Element Msg
advancedSubmenu current =
    panel
        [ wrappedRow [ width fill, spacing 8 ]
            [ sectionNavItem current AdvancedFundamentals "Fundamentals"
            , sectionNavItem current AdvancedRuntime "Runtime"
            , sectionNavItem current AdvancedTooling "Tooling"
            , sectionNavItem current AdvancedCompiler "Compiler"
            , sectionNavItem current AdvancedLanguageReference "Language Reference"
            ]
        ]


sectionNavItem : Route -> Route -> String -> Element Msg
sectionNavItem current target label =
    let
        isCurrent =
            case ( current, target ) of
                ( AdvancedGuide, AdvancedFundamentals ) ->
                    True

                _ ->
                    current == target
    in
    navLink label (routeHref target) isCurrent


advancedPager : Maybe Route -> Maybe Route -> Element Msg
advancedPager previous next =
    panel
        [ wrappedRow
            [ width fill
            , spacing 10
            ]
            (case ( previous, next ) of
                ( Just previousRoute, Just nextRoute ) ->
                    [ link
                        (buttonAttributes
                            (rgb255 230 239 250)
                            (rgb255 36 82 132)
                            ++ [ width (fill |> maximum 320) ]
                        )
                        { url = routeHref previousRoute
                        , label = text ("Previous: " ++ routeLabel previousRoute)
                        }
                    , el [ width fill ] (text "")
                    , link
                        (buttonAttributes
                            (rgb255 45 126 210)
                            (rgb255 245 250 255)
                            ++ [ width (fill |> maximum 320) ]
                        )
                        { url = routeHref nextRoute
                        , label = text ("Next: " ++ routeLabel nextRoute)
                        }
                    ]

                ( Just previousRoute, Nothing ) ->
                    [ link
                        (buttonAttributes
                            (rgb255 230 239 250)
                            (rgb255 36 82 132)
                            ++ [ width (fill |> maximum 320), centerX ]
                        )
                        { url = routeHref previousRoute
                        , label = text ("Previous: " ++ routeLabel previousRoute)
                        }
                    ]

                ( Nothing, Just nextRoute ) ->
                    [ link
                        (buttonAttributes
                            (rgb255 45 126 210)
                            (rgb255 245 250 255)
                            ++ [ width (fill |> maximum 320), centerX ]
                        )
                        { url = routeHref nextRoute
                        , label = text ("Next: " ++ routeLabel nextRoute)
                        }
                    ]

                ( Nothing, Nothing ) ->
                    []
            )
        ]


routeLabel : Route -> String
routeLabel route =
    case route of
        AdvancedFundamentals ->
            "Fundamentals"

        AdvancedLanguageReference ->
            "Language Reference"

        AdvancedRuntime ->
            "Runtime"

        AdvancedTooling ->
            "Tooling"

        AdvancedCompiler ->
            "Compiler"

        AdvancedGuide ->
            "Advanced Guide"

        GettingStarted ->
            "Getting Started"

        Examples ->
            "Examples"

        Home ->
            "Home"


primaryButton : String -> String -> Element Msg
primaryButton label target =
    link
        (buttonAttributes
            (rgb255 45 126 210)
            (rgb255 245 250 255)
        )
        { url = target
        , label = text label
        }


secondaryButton : String -> String -> Element Msg
secondaryButton label target =
    link
        (buttonAttributes
            (rgb255 230 239 250)
            (rgb255 36 82 132)
        )
        { url = target
        , label = text label
        }


buttonAttributes : Element.Color -> Element.Color -> List (Attribute Msg)
buttonAttributes bg fg =
    [ Background.color bg
    , Font.color fg
    , Border.rounded 10
    , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
    , Font.semiBold
    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
    ]


navLink : String -> String -> Bool -> Element Msg
navLink label target isCurrent =
    link
        (if isCurrent then
            [ Font.size 14
            , Font.semiBold
            , Font.color (rgb255 24 73 126)
            , Background.color (rgb255 226 238 253)
            , Border.width 1
            , Border.color (rgb255 171 200 235)
            , Border.rounded 999
            , paddingEach { top = 6, right = 10, bottom = 6, left = 10 }
            , htmlAttribute (HtmlAttr.style "cursor" "pointer")
            ]

         else
            [ Font.size 14
            , Font.semiBold
            , Font.color (rgb255 64 88 118)
            , Border.width 1
            , Border.color (rgb255 230 238 248)
            , Border.rounded 999
            , paddingEach { top = 6, right = 10, bottom = 6, left = 10 }
            , htmlAttribute (HtmlAttr.style "cursor" "pointer")
            ]
        )
        { url = target
        , label = text label
        }


instructionText : String -> Element Msg
instructionText value =
    paragraph [ Font.size 16, Font.color (rgb255 70 93 121), width fill ] [ text value ]


installSubitem : Model -> String -> String -> Element Msg
installSubitem model platform command =
    column
        [ width fill
        , spacing 6
        , Background.color (rgb255 250 252 255)
        , Border.width 1
        , Border.color (rgb255 223 232 244)
        , Border.rounded 10
        , paddingEach { top = 10, right = 10, bottom = 10, left = 10 }
        ]
        [ el [ Font.size 13, Font.semiBold, Font.color (rgb255 70 93 121) ] (text platform)
        , wrappedRow [ width fill, spacing 8 ]
            [ codeInlineSmall command
            , copyLink model command
            ]
        ]


bulletList : List String -> Element Msg
bulletList items =
    column [ spacing 8, width fill ] (List.map bulletItem items)


bulletItem : String -> Element Msg
bulletItem value =
    row [ spacing 8, width fill ]
        [ el [ Font.color (rgb255 93 107 126), Font.bold ] (text "•")
        , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ] [ text value ]
        ]


docList : List String -> Element Msg
docList items =
    column
        [ spacing 8
        , width fill
        , paddingEach { top = 0, right = 0, bottom = 0, left = 16 }
        ]
        (List.map docListItem items)


docListItem : String -> Element Msg
docListItem value =
    row
        [ spacing 8
        , width fill
        ]
        [ el [ Font.color (rgb255 93 107 126), Font.bold ] (text "•")
        , paragraph
            [ Font.size 15
            , Font.color (rgb255 72 95 123)
            , width fill
            ]
            [ text value ]
        ]


languageReferenceGroup : String -> List (Element Msg) -> Element Msg
languageReferenceGroup label items =
    column
        [ spacing 8
        , width fill
        , paddingEach { top = 2, right = 0, bottom = 6, left = 0 }
        ]
        (paragraph
            [ Font.size 16
            , Font.semiBold
            , Font.color (rgb255 39 72 110)
            ]
            [ text label ]
            :: items
        )


languageReferenceItem : String -> String -> Element Msg
languageReferenceItem keyword description =
    row
        [ spacing 8
        , width fill
        , paddingEach { top = 0, right = 0, bottom = 0, left = 16 }
        ]
        [ el [ Font.color (rgb255 93 107 126), Font.bold ] (text "•")
        , paragraph
            [ Font.size 15
            , Font.color (rgb255 72 95 123)
            , width fill
            ]
            [ languageKeywordText keyword
            , text " "
            , text description
            ]
        ]


languageKeywordText : String -> Element Msg
languageKeywordText value =
    el
        [ Font.semiBold
        , Font.color (rgb255 28 66 108)
        ]
        (text value)


codeInline : String -> Element Msg
codeInline source =
    el
        [ width fill
        , scrollbarX
        , Background.color (rgb255 22 43 67)
        , Border.rounded 7
        , paddingEach { top = 7, right = 9, bottom = 7, left = 9 }
        ]
        (html
            (Html.div
                [ HtmlAttr.style "white-space" "pre"
                , HtmlAttr.style "font-family" "IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
                , HtmlAttr.style "font-size" "14px"
                , HtmlAttr.style "color" "#D8E7F8"
                ]
                [ Html.text source ]
            )
        )


codeInlineSmall : String -> Element Msg
codeInlineSmall source =
    el
        [ width fill
        , scrollbarX
        , Background.color (rgb255 22 43 67)
        , Border.rounded 7
        , paddingEach { top = 6, right = 8, bottom = 6, left = 8 }
        ]
        (html
            (Html.div
                [ HtmlAttr.style "white-space" "pre"
                , HtmlAttr.style "font-family" "IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
                , HtmlAttr.style "font-size" "12px"
                , HtmlAttr.style "color" "#D8E7F8"
                ]
                [ Html.text source ]
            )
        )


copyLink : Model -> String -> Element Msg
copyLink model source =
    let
        isCopied =
            model.copiedText == Just source

        label =
            if isCopied then
                "✓"

            else
                "⎘"

        fontSize =
            if isCopied then
                "18px"

            else
                "20px"

        titleText =
            if isCopied then
                "Copied"

            else
                "Copy code"

        color =
            if isCopied then
                "rgb(34, 122, 85)"

            else
                "rgb(88, 115, 146)"
    in
    html
        (Html.button
            [ HtmlAttr.type_ "button"
            , HtmlAttr.class "copy-link"
            , HtmlAttr.style "cursor" "pointer"
            , HtmlAttr.style "font-size" fontSize
            , HtmlAttr.style "font-weight" "600"
            , HtmlAttr.style "color" color
            , HtmlAttr.style "line-height" "1"
            , HtmlAttr.style "padding" "4px 6px"
            , HtmlAttr.style "width" "30px"
            , HtmlAttr.style "height" "30px"
            , HtmlAttr.style "display" "inline-flex"
            , HtmlAttr.style "align-items" "center"
            , HtmlAttr.style "justify-content" "center"
            , HtmlAttr.style "background" "transparent"
            , HtmlAttr.style "border" "none"
            , HtmlAttr.style "outline" "none"
            , HtmlAttr.style "box-shadow" "none"
            , HtmlAttr.style "appearance" "none"
            , HtmlAttr.style "-webkit-appearance" "none"
            , HtmlAttr.style "border-radius" "6px"
            , HtmlAttr.title titleText
            , HtmlAttr.attribute "aria-label" titleText
            , HtmlEvents.onClick (CopyText source)
            ]
            [ Html.text label ]
        )


codeFromString : Model -> String -> Int -> String -> Element Msg
codeFromString model fileName boxHeight source =
    let
        lines =
            source
                |> String.split "\n"
                |> trimTrailingEmptyLine
                |> ensureAtLeastOneLine

        autoHeight =
            max boxHeight ((List.length lines * 22) + 30)
    in
    column
        [ width fill
        , spacing 0
        ]
        [ row
            [ width fill
            , paddingEach { top = 0, right = 4, bottom = 0, left = 0 }
            ]
            [ el
                [ Background.color (rgb255 24 47 73)
                , Border.widthEach { top = 1, right = 1, bottom = 0, left = 1 }
                , Border.color (rgb255 38 70 105)
                , Border.roundEach { topLeft = 10, topRight = 10, bottomLeft = 0, bottomRight = 0 }
                , paddingEach { top = 8, right = 12, bottom = 8, left = 12 }
                , Font.family [ Font.typeface "IBM Plex Mono", Font.monospace ]
                , Font.size 13
                , Font.color (rgb255 176 199 225)
                ]
                (text fileName)
            , el [ width fill ] (text "")
            , copyLink model source
            ]
        , el
            [ width fill
            , height (px autoHeight)
            , scrollbarX
            , scrollbarY
            , Background.color (rgb255 18 38 61)
            , Border.widthEach { top = 1, right = 1, bottom = 1, left = 1 }
            , Border.color (rgb255 38 70 105)
            , Border.roundEach { topLeft = 0, topRight = 0, bottomLeft = 10, bottomRight = 10 }
            , paddingEach { top = 12, right = 14, bottom = 12, left = 14 }
            ]
            (html
                (Html.pre
                    [ HtmlAttr.style "margin" "0"
                    , HtmlAttr.style "white-space" "pre"
                    , HtmlAttr.style "overflow-wrap" "break-word"
                    , HtmlAttr.style "font-family" "IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
                    , HtmlAttr.style "font-size" "14px"
                    , HtmlAttr.style "line-height" "1.55"
                    , HtmlAttr.style "color" "#D8E9FF"
                    ]
                    [ Html.div
                        [ HtmlAttr.style "min-width" "max-content" ]
                        (List.indexedMap codeEditorLineView lines)
                    ]
                )
            )
        ]


trimTrailingEmptyLine : List String -> List String
trimTrailingEmptyLine lines =
    case List.reverse lines of
        "" :: rest ->
            List.reverse rest

        _ ->
            lines


ensureAtLeastOneLine : List String -> List String
ensureAtLeastOneLine lines =
    if List.isEmpty lines then
        [ "" ]

    else
        lines


codeEditorLineView : Int -> String -> Html.Html msg
codeEditorLineView index lineText =
    Html.div
        [ HtmlAttr.style "display" "flex"
        , HtmlAttr.style "align-items" "flex-start"
        , HtmlAttr.style "min-height" "22px"
        ]
        [ Html.span
            [ HtmlAttr.style "width" "42px"
            , HtmlAttr.style "flex" "0 0 42px"
            , HtmlAttr.style "text-align" "right"
            , HtmlAttr.style "padding-right" "12px"
            , HtmlAttr.style "color" "#7F96B3"
            , HtmlAttr.style "user-select" "none"
            ]
            [ Html.text (String.fromInt (index + 1)) ]
        , Html.code
            [ HtmlAttr.style "white-space" "pre"
            , HtmlAttr.style "color" "#D8E9FF"
            ]
            (if String.isEmpty lineText then
                [ Html.text " " ]

             else
                highlightMarSource lineText
            )
        ]


highlightMarSource : String -> List (Html.Html msg)
highlightMarSource source =
    highlightMarHelp source []
        |> List.reverse


highlightMarHelp : String -> List (Html.Html msg) -> List (Html.Html msg)
highlightMarHelp remaining acc =
    case String.uncons remaining of
        Nothing ->
            acc

        Just ( firstChar, rest ) ->
            case String.uncons rest of
                Just ( '-', restAfterDash ) ->
                    if firstChar == '-' then
                        let
                            ( commentText, afterComment ) =
                                takeUntilNewline restAfterDash
                        in
                        highlightMarHelp afterComment (commentToken ("--" ++ commentText) :: acc)

                    else
                        tokenizeSingle firstChar rest acc

                _ ->
                    if firstChar == '"' then
                        let
                            ( stringLiteral, afterString ) =
                                takeStringLiteral rest "\""
                        in
                        highlightMarHelp afterString (token "#F7C97F" stringLiteral :: acc)

                    else if Char.isDigit firstChar then
                        let
                            ( numberTail, afterNumber ) =
                                takeWhile isNumberChar rest

                            numberText =
                                String.fromChar firstChar ++ numberTail
                        in
                        highlightMarHelp afterNumber (token "#F5A97F" numberText :: acc)

                    else if isIdentifierStart firstChar then
                        let
                            ( identifierTail, afterIdentifier ) =
                                takeWhile isIdentifierChar rest

                            word =
                                String.fromChar firstChar ++ identifierTail
                        in
                        highlightMarHelp afterIdentifier (wordToken word :: acc)

                    else if isTwoCharOperator firstChar rest then
                        let
                            secondChar =
                                rest
                                    |> String.left 1

                            afterOperator =
                                String.dropLeft 1 rest
                        in
                        highlightMarHelp afterOperator (token "#D8E9FF" (String.fromChar firstChar ++ secondChar) :: acc)

                    else if isOperatorChar firstChar then
                        highlightMarHelp rest (token "#D8E9FF" (String.fromChar firstChar) :: acc)

                    else if isPunctuationChar firstChar then
                        highlightMarHelp rest (token "#AFC7E6" (String.fromChar firstChar) :: acc)

                    else
                        highlightMarHelp rest (Html.text (String.fromChar firstChar) :: acc)


tokenizeSingle : Char -> String -> List (Html.Html msg) -> List (Html.Html msg)
tokenizeSingle firstChar rest acc =
    if firstChar == '"' then
        let
            ( stringLiteral, afterString ) =
                takeStringLiteral rest "\""
        in
        highlightMarHelp afterString (token "#F7C97F" stringLiteral :: acc)

    else if Char.isDigit firstChar then
        let
            ( numberTail, afterNumber ) =
                takeWhile isNumberChar rest

            numberText =
                String.fromChar firstChar ++ numberTail
        in
        highlightMarHelp afterNumber (token "#F5A97F" numberText :: acc)

    else if isIdentifierStart firstChar then
        let
            ( identifierTail, afterIdentifier ) =
                takeWhile isIdentifierChar rest

            word =
                String.fromChar firstChar ++ identifierTail
        in
        highlightMarHelp afterIdentifier (wordToken word :: acc)

    else if isTwoCharOperator firstChar rest then
        let
            secondChar =
                rest
                    |> String.left 1

            afterOperator =
                String.dropLeft 1 rest
        in
        highlightMarHelp afterOperator (token "#D8E9FF" (String.fromChar firstChar ++ secondChar) :: acc)

    else if isOperatorChar firstChar then
        highlightMarHelp rest (token "#D8E9FF" (String.fromChar firstChar) :: acc)

    else if isPunctuationChar firstChar then
        highlightMarHelp rest (token "#AFC7E6" (String.fromChar firstChar) :: acc)

    else
        highlightMarHelp rest (Html.text (String.fromChar firstChar) :: acc)


takeUntilNewline : String -> ( String, String )
takeUntilNewline input =
    case String.uncons input of
        Nothing ->
            ( "", "" )

        Just ( char, rest ) ->
            if char == '\n' then
                ( "", input )

            else
                let
                    ( tailText, remaining ) =
                        takeUntilNewline rest
                in
                ( String.fromChar char ++ tailText, remaining )


takeStringLiteral : String -> String -> ( String, String )
takeStringLiteral remaining built =
    case String.uncons remaining of
        Nothing ->
            ( built, "" )

        Just ( char, rest ) ->
            if char == '"' then
                ( built ++ "\"", rest )

            else if char == '\\' then
                case String.uncons rest of
                    Nothing ->
                        ( built ++ "\\\\", "" )

                    Just ( escaped, afterEscaped ) ->
                        takeStringLiteral afterEscaped (built ++ "\\" ++ String.fromChar escaped)

            else
                takeStringLiteral rest (built ++ String.fromChar char)


takeWhile : (Char -> Bool) -> String -> ( String, String )
takeWhile predicate input =
    case String.uncons input of
        Nothing ->
            ( "", "" )

        Just ( char, rest ) ->
            if predicate char then
                let
                    ( tailText, remaining ) =
                        takeWhile predicate rest
                in
                ( String.fromChar char ++ tailText, remaining )

            else
                ( "", input )


isIdentifierStart : Char -> Bool
isIdentifierStart char =
    Char.isAlpha char || char == '_'


isIdentifierChar : Char -> Bool
isIdentifierChar char =
    Char.isAlphaNum char || char == '_'


isNumberChar : Char -> Bool
isNumberChar char =
    Char.isDigit char || char == '.'


isTwoCharOperator : Char -> String -> Bool
isTwoCharOperator firstChar rest =
    let
        secondChar =
            rest
                |> String.left 1
    in
    List.member (String.fromChar firstChar ++ secondChar)
        [ ">="
        , "<="
        , "=="
        , "!="
        , "&&"
        , "||"
        , "->"
        ]


isOperatorChar : Char -> Bool
isOperatorChar char =
    List.member char [ '=', '+', '-', '*', '/', '%', '!', '<', '>', '&', '|' ]


isPunctuationChar : Char -> Bool
isPunctuationChar char =
    List.member char [ '{', '}', '(', ')', '[', ']', ':', ',', '.' ]


wordToken : String -> Html.Html msg
wordToken word =
    if List.member word [ "list", "get", "create", "update", "delete" ] then
        token "#93D7FF" word

    else if List.member word [ "app", "port", "database", "entity", "rule", "when", "authorize", "auth", "type", "alias", "action", "input", "create", "public", "system", "dir", "mount", "spa_fallback", "user_entity", "email_field", "role_field", "code_ttl_minutes", "session_ttl_hours", "email_transport", "email_from", "email_subject", "sendmail_path", "request_logs_buffer", "http_max_request_body_mb", "auth_request_code_rate_limit_per_minute", "auth_login_rate_limit_per_minute", "admin_ui_session_ttl_hours", "security_frame_policy", "security_referrer_policy", "security_content_type_nosniff", "sqlite_journal_mode", "sqlite_synchronous", "sqlite_foreign_keys", "sqlite_busy_timeout_ms", "sqlite_wal_autocheckpoint", "sqlite_journal_size_limit_mb", "sqlite_mmap_size_mb", "sqlite_cache_size_kb" ] then
        token "#7AB8FF" word

    else if List.member word [ "Int", "String", "Bool", "Float" ] then
        token "#4FD1C5" word

    else if List.member word [ "primary", "auto", "optional" ] then
        token "#B7C5D9" word

    else if List.member word [ "len", "contains", "startsWith", "endsWith", "matches", "isRole" ] then
        token "#82E0AA" word

    else if List.member word [ "input", "auth_authenticated", "auth_email", "auth_user_id", "auth_role", "true", "false", "null" ] then
        token "#C3D7FF" word

    else
        case String.uncons word of
            Just ( firstChar, _ ) ->
                if Char.isUpper firstChar then
                    token "#92C4FF" word

                else
                    token "#DCE8F8" word

            Nothing ->
                Html.text word


commentToken : String -> Html.Html msg
commentToken value =
    Html.span
        [ HtmlAttr.style "color" "#7F96B3"
        , HtmlAttr.style "font-style" "italic"
        ]
        [ Html.text value ]


codeBlock : Model -> Element Msg
codeBlock model =
    codeFromString model "todo.mar" 340 todoExampleSource


codeSnippet : List (Html.Html msg)
codeSnippet =
    [ codeKeyword "app"
    , Html.text " "
    , codeEntity "TodoApi"
    , Html.text "\n"
    , codeKeyword "port"
    , Html.text " "
    , codeNumber "4100"
    , Html.text "\n"
    , codeKeyword "database"
    , Html.text " "
    , codeString "\"todo.db\""
    , Html.text "\n\n"
    , codeKeyword "entity"
    , Html.text " "
    , codeEntity "Todo"
    , Html.text " "
    , codePunctuation "{"
    , Html.text "\n"
    , Html.text "  "
    , codeField "id"
    , codePunctuation ":"
    , Html.text " "
    , codeType "Int"
    , Html.text " "
    , codeModifier "primary"
    , Html.text " "
    , codeModifier "auto"
    , Html.text "\n"
    , Html.text "  "
    , codeField "title"
    , codePunctuation ":"
    , Html.text " "
    , codeType "String"
    , Html.text "\n"
    , Html.text "  "
    , codeField "done"
    , codePunctuation ":"
    , Html.text " "
    , codeType "Bool"
    , Html.text "\n\n"
    , Html.text "  "
    , codeKeyword "rule"
    , Html.text " "
    , codeString "\"Title must have at least 3 chars\""
    , Html.text " "
    , codeKeyword "when"
    , Html.text " "
    , codeFunction "len"
    , codePunctuation "("
    , codeField "title"
    , codePunctuation ")"
    , Html.text " "
    , codeOperator ">="
    , Html.text " "
    , codeNumber "3"
    , Html.text "\n"
    , Html.text "  "
    , codeKeyword "authorize"
    , Html.text " "
    , codeCrud "list"
    , Html.text " "
    , codeKeyword "when"
    , Html.text " "
    , codeContext "auth_authenticated"
    , Html.text "\n"
    , Html.text "  "
    , codeKeyword "authorize"
    , Html.text " "
    , codeCrud "create"
    , Html.text " "
    , codeKeyword "when"
    , Html.text " "
    , codeContext "auth_authenticated"
    , Html.text "\n"
    , codePunctuation "}"
    , Html.text "\n"
    ]


token : String -> String -> Html.Html msg
token color value =
    Html.span [ HtmlAttr.style "color" color ] [ Html.text value ]


codeKeyword : String -> Html.Html msg
codeKeyword value =
    token "#7AB8FF" value


codeType : String -> Html.Html msg
codeType value =
    token "#4FD1C5" value


codeModifier : String -> Html.Html msg
codeModifier value =
    token "#B7C5D9" value


codeField : String -> Html.Html msg
codeField value =
    token "#DCE8F8" value


codeEntity : String -> Html.Html msg
codeEntity value =
    token "#92C4FF" value


codeCrud : String -> Html.Html msg
codeCrud value =
    token "#93D7FF" value


codeString : String -> Html.Html msg
codeString value =
    token "#F7C97F" value


codeNumber : String -> Html.Html msg
codeNumber value =
    token "#F5A97F" value


codeFunction : String -> Html.Html msg
codeFunction value =
    token "#82E0AA" value


codeContext : String -> Html.Html msg
codeContext value =
    token "#C3D7FF" value


codeOperator : String -> Html.Html msg
codeOperator value =
    token "#D8E9FF" value


codePunctuation : String -> Html.Html msg
codePunctuation value =
    token "#AFC7E6" value


todoExampleSource : String
todoExampleSource =
    """-- A minimal CRUD application.
-- This example shows the basic Mar structure:
-- app, port, database, entity, rule, and authorization.

-- Application
app TodoApi
port 4100
database \"todo.db\"

-- Entity
entity Todo {
  id: Int primary auto
  title: String
  done: Bool

  rule \"Title must have at least 3 chars\" when len(title) >= 3
  authorize list when auth_authenticated
  authorize create when auth_authenticated
}
"""


actionExampleSource : String
actionExampleSource =
    """-- A transactional action example.
-- This example shows how one action can write to
-- multiple entities in a single atomic operation.

-- Action input
type alias PlaceOrderInput =
  { userId : Int
  , total : Float
  }

-- Atomic action
action placeOrder {
  input: PlaceOrderInput

  create Order {
    userId: input.userId
    total: input.total
    status: \"created\"
  }

  create AuditLog {
    userId: input.userId
    event: \"order created\"
  }
}
"""


systemConfigSource : String
systemConfigSource =
    """-- Runtime configuration
system {
  request_logs_buffer 500
  http_max_request_body_mb 1
  auth_request_code_rate_limit_per_minute 5
  auth_login_rate_limit_per_minute 10
  admin_ui_session_ttl_hours 2
  security_frame_policy sameorigin
  security_referrer_policy strict-origin-when-cross-origin
  security_content_type_nosniff true
  sqlite_journal_mode wal
  sqlite_synchronous normal
  sqlite_foreign_keys true
  sqlite_busy_timeout_ms 5000
  sqlite_wal_autocheckpoint 1000
  sqlite_journal_size_limit_mb 64
  sqlite_mmap_size_mb 128
  sqlite_cache_size_kb 2000
}
"""


publicConfigSource : String
publicConfigSource =
    """-- Public files are embedded into the final executable.
public {
  dir \"./frontend/dist\"      -- required; resolved relative to the .mar file.
  mount \"/\"                  -- defaults to /.
  spa_fallback \"index.html\"  -- serves the frontend entry file for SPA-style routes.
}
"""


authConfigSource : String
authConfigSource =
    """-- Email-code authentication
auth {
  user_entity User
  email_field email
  role_field role
  code_ttl_minutes 10
  session_ttl_hours 24
  email_transport console
  email_from \"no-reply@store.local\"
  email_subject \"Your StoreApi login code\"
}
"""


authorizeExampleSource : String
authorizeExampleSource =
    """-- Per-operation authorization inside an entity
entity User {
  id: Int primary auto
  email: String
  role: String

  authorize list when isRole(\"admin\")
  authorize get when auth_authenticated and (id == auth_user_id or isRole(\"admin\"))
  authorize create when true
  authorize update when auth_authenticated and (id == auth_user_id or isRole(\"admin\"))
  authorize delete when isRole(\"admin\")
}
"""


storeExampleSource : String
storeExampleSource =
    """app BookStoreApi
port 4100
database \"bookstore.db\"

auth {
  user_entity User
  email_field email
  role_field role
  code_ttl_minutes 10
  session_ttl_hours 24
  email_transport console
  email_from \"no-reply@bookstore.local\"
  email_subject \"Your BookStore login code\"
}

entity User {
  id: Int primary auto
  email: String
  role: String
  displayName: String optional

  authorize list when isRole(\"admin\")
  authorize get when auth_authenticated and (id == auth_user_id or isRole(\"admin\"))
  authorize create when isRole(\"admin\")
  authorize update when auth_authenticated and ((id == auth_user_id and role == auth_role) or isRole(\"admin\"))
  authorize delete when isRole(\"admin\")
}

entity Book {
  id: Int primary auto
  title: String
  authorName: String
  isbn: String
  price: Float
  stock: Int

  rule \"Book title cannot be empty\" when title != \"\"
  rule \"Price must be greater than zero\" when price > 0

  authorize list when true
  authorize get when true
  authorize create when auth_authenticated
  authorize update when isRole(\"admin\")
  authorize delete when isRole(\"admin\")
}

type alias PlaceBookOrderInput =
  { orderRef : String
  , userId : Int
  , bookId : Int
  , quantity : Int
  , unitPrice : Float
  , lineTotal : Float
  , orderTotal : Float
  , notes : String
  }

action placeBookOrder {
  input: PlaceBookOrderInput

  create Order {
    orderRef: input.orderRef
    userId: input.userId
    status: \"confirmed\"
    total: input.orderTotal
    currency: \"BRL\"
    notes: input.notes
  }

  create OrderItem {
    orderRef: input.orderRef
    userId: input.userId
    bookId: input.bookId
    quantity: input.quantity
    unitPrice: input.unitPrice
    lineTotal: input.lineTotal
  }

  create AuditLog {
    userId: input.userId
    event: \"book order created\"
    orderRef: input.orderRef
  }
}
"""
