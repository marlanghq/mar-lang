port module Main exposing (main)

import Browser
import Browser.Navigation as Nav
import Element exposing (Attribute, Element, alignTop, below, centerX, column, el, fill, height, html, htmlAttribute, link, maximum, minimum, moveDown, newTabLink, none, padding, paddingEach, paragraph, px, rgb255, rgba255, row, scrollbarX, scrollbarY, spacing, text, width, wrappedRow)
import Element.Background as Background
import Element.Border as Border
import Element.Font as Font
import Element.Input as Input
import Html
import Html.Attributes as HtmlAttr
import Html.Events as HtmlEvents
import Json.Decode as Decode
import Svg exposing (path, svg)
import Svg.Attributes as SvgAttr
import Url exposing (Url)


type Route
    = Home
    | GettingStarted
    | AdvancedGuide
    | AdvancedFundamentals
    | AdvancedLanguageReference
    | AdvancedRuntime
    | AdvancedTooling
    | AdvancedDeploy
    | AdvancedCompiler
    | Examples


type alias Model =
    { key : Nav.Key
    , route : Route
    , copiedText : Maybe String
    , docsSearch : String
    , docsSearchOpen : Bool
    , docsSearchSelectedIndex : Maybe Int
    }


type Msg
    = LinkClicked Browser.UrlRequest
    | UrlChanged Url
    | CopyText String
    | UpdateDocsSearch String
    | FocusDocsSearch
    | BlurDocsSearch
    | MoveDocsSearchSelection Int
    | ActivateDocsSearchSelection
    | ClearDocsSearchSelection
    | HoverDocsSearchResult Int


type alias DocSearchEntry =
    { title : String
    , route : Route
    , sectionId : Maybe String
    , summary : String
    , keywords : List String
    }


port copyToClipboard : String -> Cmd msg


port scrollToTop : () -> Cmd msg


port scrollToElement : String -> Cmd msg


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
      , docsSearch = ""
      , docsSearchOpen = False
      , docsSearchSelectedIndex = Nothing
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
            let
                location =
                    routeLocationFromUrl url

                scrollCmd =
                    case location.sectionId of
                        Just sectionId ->
                            scrollToElement sectionId

                        Nothing ->
                            scrollToTop ()
            in
            ( { model | route = location.route, copiedText = Nothing, docsSearch = "", docsSearchOpen = False, docsSearchSelectedIndex = Nothing }, scrollCmd )

        CopyText source ->
            ( { model | copiedText = Just source }, copyToClipboard source )

        UpdateDocsSearch value ->
            ( { model | docsSearch = value, docsSearchOpen = String.trim value /= "", docsSearchSelectedIndex = Nothing }, Cmd.none )

        FocusDocsSearch ->
            ( { model | docsSearchOpen = String.trim model.docsSearch /= "", docsSearchSelectedIndex = Nothing }, Cmd.none )

        BlurDocsSearch ->
            ( { model | docsSearchOpen = False, docsSearchSelectedIndex = Nothing }, Cmd.none )

        MoveDocsSearchSelection direction ->
            let
                results =
                    currentDocSearchResults model
            in
            case List.length results of
                0 ->
                    ( model, Cmd.none )

                count ->
                    let
                        nextIndex =
                            case model.docsSearchSelectedIndex of
                                Just currentIndex ->
                                    modBy count (currentIndex + direction)

                                Nothing ->
                                    if direction < 0 then
                                        count - 1

                                    else
                                        0
                    in
                    ( { model | docsSearchOpen = True, docsSearchSelectedIndex = Just nextIndex }, Cmd.none )

        ActivateDocsSearchSelection ->
            let
                results =
                    currentDocSearchResults model

                maybeEntry =
                    case model.docsSearchSelectedIndex of
                        Just selectedIndex ->
                            getAt selectedIndex results

                        Nothing ->
                            List.head results
            in
            case maybeEntry of
                Just entry ->
                    ( { model | docsSearchOpen = False, docsSearchSelectedIndex = Nothing }
                    , Nav.pushUrl model.key (routeHrefWithSection entry.route entry.sectionId)
                    )

                Nothing ->
                    ( model, Cmd.none )

        ClearDocsSearchSelection ->
            ( { model | docsSearchOpen = False, docsSearchSelectedIndex = Nothing }, Cmd.none )

        HoverDocsSearchResult index ->
            ( { model | docsSearchSelectedIndex = Just index }, Cmd.none )


type alias RouteLocation =
    { route : Route
    , sectionId : Maybe String
    }


routeFromUrl : Url -> Route
routeFromUrl url =
    (routeLocationFromUrl url).route


routeLocationFromUrl : Url -> RouteLocation
routeLocationFromUrl url =
    let
        fragment =
            url.fragment
                |> Maybe.withDefault ""
                |> normalizeFragment

        ( routeFragment, sectionId ) =
            splitFragmentSection fragment
    in
    { route =
        case routeFragment of
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

            "advanced/deploy" ->
                AdvancedDeploy

            "advanced/compiler" ->
                AdvancedCompiler

            "examples" ->
                Examples

            _ ->
                Home
    , sectionId = sectionId
    }


normalizeFragment : String -> String
normalizeFragment fragment =
    if String.startsWith "/" fragment then
        String.dropLeft 1 fragment

    else
        fragment


splitFragmentSection : String -> ( String, Maybe String )
splitFragmentSection fragment =
    case String.split "::" fragment of
        routePart :: sectionPart :: _ ->
            ( routePart
            , if String.trim sectionPart == "" then
                Nothing

              else
                Just sectionPart
            )

        _ ->
            ( fragment, Nothing )


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

        AdvancedDeploy ->
            "#/advanced/deploy"

        AdvancedCompiler ->
            "#/advanced/compiler"

        Examples ->
            "#/examples"


routeHrefWithSection : Route -> Maybe String -> String
routeHrefWithSection route sectionId =
    case sectionId of
        Just value ->
            routeHref route ++ "::" ++ value

        Nothing ->
            routeHref route


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

        AdvancedDeploy ->
            "Mar - Deploy Guide"

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
        [ topBar model
        , column
            [ width fill
            , spacing 20
            ]
            [ warningBanner
            , routeView model
            , footer
            ]
        ]


topBar : Model -> Element Msg
topBar model =
    panel
        [ column [ width fill, spacing 12 ]
            [ wrappedRow [ width fill, spacing 12 ]
                [ el [ Font.size 28, Font.bold, Font.color (rgb255 22 57 96) ] (text "Mar")
                , el [ width fill ] none
                , topSearchArea model
                ]
            , wrappedRow [ width fill, spacing 8 ]
                [ navItem model.route Home "Home"
                , navItem model.route GettingStarted "Getting Started"
                , navItem model.route Examples "Examples"
                , navItem model.route AdvancedGuide "Advanced"
                ]
            ]
        ]


topSearchArea : Model -> Element Msg
topSearchArea model =
    el [ width (fill |> maximum 190), alignTop ]
        (searchFieldWithDropdown model)


searchFieldWithDropdown : Model -> Element Msg
searchFieldWithDropdown model =
    el
        [ width fill
        , below
            (if model.docsSearchOpen && String.trim model.docsSearch /= "" then
                el
                    [ width (fill |> minimum 420 |> maximum 560)
                    , htmlAttribute (HtmlAttr.style "position" "absolute")
                    , htmlAttribute (HtmlAttr.style "right" "0")
                    , moveDown 8
                    ]
                    (topSearchResults model)

             else
                none
            )
        ]
        (topSearch model)


githubRepoLink : Element Msg
githubRepoLink =
    newTabLink
        [ Font.color (rgb255 22 57 96)
        , htmlAttribute (HtmlAttr.style "line-height" "0")
        , htmlAttribute (HtmlAttr.style "opacity" "0.78")
        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
        , htmlAttribute (HtmlAttr.attribute "aria-label" "GitHub repository")
        ]
        { url = "https://github.com/marciofrayze/mar-lang"
        , label = html githubIcon
        }


githubIcon : Html.Html msg
githubIcon =
    svg
        [ SvgAttr.width "18"
        , SvgAttr.height "18"
        , SvgAttr.viewBox "0 0 16 16"
        , SvgAttr.fill "currentColor"
        , HtmlAttr.attribute "aria-hidden" "true"
        ]
        [ path [ SvgAttr.d "M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.5-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.65 7.65 0 0 1 4 0c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" ] [] ]


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

        AdvancedDeploy ->
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

        AdvancedDeploy ->
            advancedDeployPage model

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
                [ text "For now, Mar does not guarantee backward compatibility for language syntax or database schema." ]
            ]
        ]


topSearch : Model -> Element Msg
topSearch model =
    Input.text
        [ width fill
        , Background.color (rgb255 250 252 255)
        , Border.width 1
        , Border.color (rgb255 216 226 238)
        , Border.rounded 8
        , paddingEach { top = 7, right = 8, bottom = 7, left = 8 }
        , Font.size 16
        , htmlAttribute (HtmlEvents.onFocus FocusDocsSearch)
        , htmlAttribute (HtmlEvents.onBlur BlurDocsSearch)
        , htmlAttribute (HtmlEvents.preventDefaultOn "keydown" docsSearchKeyDecoder)
        ]
        { onChange = UpdateDocsSearch
        , text = model.docsSearch
        , placeholder = Just (Input.placeholder [] (text "Search"))
        , label = Input.labelHidden "Search"
        }


topSearchResults : Model -> Element Msg
topSearchResults model =
    let
        results =
            currentDocSearchResults model
    in
    if String.trim model.docsSearch == "" then
        none

    else
        column
            [ width fill
            , spacing 6
            , Background.color (rgb255 255 255 255)
            , Border.width 1
            , Border.color (rgb255 197 214 235)
            , Border.rounded 12
            , Border.shadow
                { offset = ( 0, 12 )
                , size = 0
                , blur = 28
                , color = rgba255 23 43 77 0.14
                }
            , paddingEach { top = 8, right = 8, bottom = 8, left = 8 }
            , htmlAttribute (HtmlEvents.preventDefaultOn "mousedown" (Decode.succeed ( FocusDocsSearch, True )))
            ]
            (if List.isEmpty results then
                [ paragraph [ Font.size 14, Font.color (rgb255 86 107 133), width fill ] [ text "Nothing matched your search." ] ]

             else
                List.indexedMap (docSearchResultView model.docsSearchSelectedIndex) results
            )


currentDocSearchResults : Model -> List DocSearchEntry
currentDocSearchResults model =
    let
        query =
            String.trim model.docsSearch
    in
    if query == "" then
        []

    else
        matchingDocSearchEntries query |> List.take 6


docsSearchKeyDecoder : Decode.Decoder ( Msg, Bool )
docsSearchKeyDecoder =
    Decode.field "key" Decode.string
        |> Decode.map docsSearchKeyToMessage


docsSearchKeyToMessage : String -> ( Msg, Bool )
docsSearchKeyToMessage key =
    case key of
        "ArrowDown" ->
            ( MoveDocsSearchSelection 1, True )

        "ArrowUp" ->
            ( MoveDocsSearchSelection -1, True )

        "Enter" ->
            ( ActivateDocsSearchSelection, True )

        "Escape" ->
            ( ClearDocsSearchSelection, True )

        _ ->
            ( FocusDocsSearch, False )


docSearchResultView : Maybe Int -> Int -> DocSearchEntry -> Element Msg
docSearchResultView selectedIndex index entry =
    let
        isSelected =
            selectedIndex == Just index
    in
    link
        [ width fill
        , Background.color
            (if isSelected then
                rgb255 232 243 255

             else
                rgb255 248 251 255
            )
        , Border.width 1
        , Border.color
            (if isSelected then
                rgb255 163 197 235

             else
                rgba255 255 255 255 0
            )
        , Border.rounded 8
        , paddingEach { top = 8, right = 10, bottom = 8, left = 10 }
        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
        , htmlAttribute (HtmlEvents.onMouseEnter (HoverDocsSearchResult index))
        , htmlAttribute (HtmlEvents.preventDefaultOn "mousedown" (Decode.succeed ( HoverDocsSearchResult index, True )))
        ]
        { url = routeHrefWithSection entry.route entry.sectionId
        , label =
            column [ width fill, spacing 2 ]
                [ paragraph
                    [ Font.size 16
                    , Font.bold
                    , Font.color
                        (if isSelected then
                            rgb255 20 58 99

                         else
                            rgb255 28 66 108
                        )
                    , width fill
                    ]
                    [ text entry.title ]
                , paragraph [ Font.size 14, Font.color (rgb255 86 107 133), width fill ] [ text entry.summary ]
                ]
        }


getAt : Int -> List a -> Maybe a
getAt index items =
    if index < 0 then
        Nothing

    else
        List.drop index items |> List.head


matchingDocSearchEntries : String -> List DocSearchEntry
matchingDocSearchEntries query =
    let
        normalizedQuery =
            String.toLower (String.trim query)

        searchableText entry =
            String.toLower
                (entry.title
                    ++ " "
                    ++ entry.summary
                    ++ " "
                    ++ String.join " " entry.keywords
                    ++ " "
                    ++ docSearchSectionText entry.sectionId
                )

        scoreText weight textValue =
            let
                normalizedText =
                    String.toLower textValue

                exactWordMatch =
                    List.member normalizedQuery (String.words normalizedText)
            in
            if normalizedText == normalizedQuery then
                weight + 8

            else if String.startsWith normalizedQuery normalizedText then
                weight + 4

            else if exactWordMatch then
                weight + 3

            else if String.contains normalizedQuery normalizedText then
                weight

            else
                0

        entryScore entry =
            scoreText 12 entry.title
                + scoreText 7 entry.summary
                + scoreText 5 (String.join " " entry.keywords)
                + scoreText 2 (searchableText entry)

        compareByScore left right =
            compare (entryScore right) (entryScore left)
    in
    docSearchEntries
        |> List.filter (\entry -> entryScore entry > 0)
        |> List.sortWith compareByScore


docSearchSectionText : Maybe String -> String
docSearchSectionText maybeSectionId =
    case maybeSectionId of
        Just sectionId ->
            case sectionId of
                "home-hero" ->
                    "Home hero. A simple declarative backend language. Mar compiles declarative source into a self-contained server executable with API, auth, admin panel, monitoring, and backups. Inspired by Elm, PocketBase, and Rails. Get Started. Advanced Guide."

                "why-mar" ->
                    "Why Mar. Less glue code. More backend. Declarative at its core. You describe the system at a higher level. Opinionated on purpose. Mar chooses a coherent runtime instead of exposing endless assembly decisions. Everything bundled. Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."

                "who-mar-is-for" ->
                    "Who Mar Is For. Mar is a strong fit for people who want the backend to stay boring in the best way: simple to run, easy to update, operational from day one, and without a lot of handwritten glue. Good fit for small teams, internal tools, MVPs, and product teams that want a coherent backend."

                "getting-started-intro" ->
                    "Getting Started. Install Mar, iterate quickly with hot reload, and deploy as a single executable."

                "install" ->
                    "Install. Download Mar from the GitHub releases page. Add mar to your PATH. Check mar version. Choose an editor. Try mar edit in the terminal for quick experiments. It is extremely experimental. For a fuller editing experience, use the VSCode extension. Install Mar Developer Tools from Visual Studio Marketplace. The VSCode extension requires mar on your PATH to work correctly."

                "quick-start" ->
                    "Quick Start. Create todo.mar. Develop. Runs the app locally with hot reload and opens the Admin UI while you edit todo.mar. Compile. Run mar compile to package self-contained executables for all supported platforms and generate frontend clients. Serve. Choose the target folder for your platform, start that executable, and open the printed Admin URL. Mar compile produces a single self-contained executable per target platform. Each one already includes API, auth, embedded Admin UI, monitoring dashboards, request logs, and SQLite backup tools. Ready for the next step. Next: Advanced Guide."

                "advanced-fundamentals" ->
                    "Advanced Guide. Core concepts of the language. Mar is a declarative backend DSL inspired by Elm, PocketBase, and Rails, implemented in Go with focus on readability, maintainability, and simple deployment."

                "syntax-model" ->
                    "Syntax model. Top-level statements: app, port, database, public, system, auth, entity, type alias, action. Fields use the form fieldName: Type with modifiers such as primary, auto, optional, and default. Built-in field types include Int, String, Bool, Float, and Posix. Posix follows Elm Time.Posix and stores Unix milliseconds. Comments use Elm-style line comments."

                "authentication-and-authorization" ->
                    "Authentication and authorization. Mar includes a built-in email-code login flow and per-operation authorization rules. Authentication endpoints are always available. Every Mar app includes a built-in User entity that you may extend. Entity access is deny-by-default unless you declare authorize rules. Admin always has read-only access to the built-in User entity, even without explicit authorize rules. authorize all when sets a default rule for list, get, create, update, and delete, and specific operations can still override it. System features use the same session and require role equals admin."

                "rules-and-typed-actions" ->
                    "Rules and typed actions. Rules are for validation close to the entity definition. Actions are for multi-step reads and writes that must succeed or fail together. Action steps can load, create, update, and delete rows inside the same transaction. Steps may bind aliases such as order = create Order or todo = load Todo, and later steps may reference alias fields like order.id. rule validates entity data and returns HTTP 422 when validation fails. Actions run in a single atomic transaction. Mar checks input types and assigned entity fields at compile time. Current limitations. Single .mar entry file per app. No multi-file projects or imports."

                "language-reference" ->
                    "Language Reference. Browse the current keywords, built-in names, functions, primitive types, and configuration options. Built-in primitive types include Int, String, Bool, Float, and Posix. Posix stores Unix milliseconds and follows Elm Time.Posix."

                "validation-and-authorization-reference" ->
                    "Validation and authorization reference. rule, expect, when, authorize, all, list, get, create, update, delete."

                "auth-config-reference" ->
                    "Auth config reference. User, code_ttl_minutes, session_ttl_hours, email_transport, email_from, email_subject, smtp_host, smtp_port, smtp_username, smtp_password_env, smtp_starttls."

                "system-config-reference" ->
                    "System config reference. request_logs_buffer, http_max_request_body_mb, auth_request_code_rate_limit_per_minute, auth_login_rate_limit_per_minute, admin_ui_session_ttl_hours, security_frame_policy, security_referrer_policy, security_content_type_nosniff, sqlite_journal_mode, sqlite_synchronous, sqlite_foreign_keys, sqlite_busy_timeout_ms, sqlite_wal_autocheckpoint, sqlite_journal_size_limit_mb, sqlite_mmap_size_mb, sqlite_cache_size_kb."

                "runtime" ->
                    "Runtime. The runtime generated by Mar is meant to be practical by default: HTTP endpoints, SQLite storage, authentication, admin tooling, and migrations come from the same source file."

                "system-configuration" ->
                    "System configuration. Use system when you need to tune runtime behavior. This is where request logging, body limits, auth rate limits, admin UI session lifetime, security headers, and SQLite pragmas are configured."

                "public-static-frontend" ->
                    "Public static frontend. Mar can embed static frontend files into the final executable and optionally serve an SPA fallback."

                "generated-endpoints" ->
                    "Generated endpoints. CRUD, actions, auth, health, schema, version, and admin-related endpoints are generated automatically. Each entity gets REST CRUD endpoints. Typed actions are exposed as POST /actions/<name>. System endpoints include /health, /_mar/admin, /_mar/schema, and /_mar/version. Admin-only system endpoints include /_mar/version/admin, /_mar/perf, /_mar/request-logs, and /_mar/backups."

                "migrations" ->
                    "Database Schema Migrations. Automatic migrations run on startup. Safe changes such as new optional columns and new required columns with literal defaults are applied. Unsafe changes are blocked with clear errors."

                "tooling" ->
                    "Tooling. The mar CLI supports the day-to-day developer workflow, while the generated clients and editor support help keep frontend and backend aligned."

                "compiler-and-runtime-commands" ->
                    "Compiler and runtime commands. mar init store-app. mar dev store.mar. Edit with code store.mar or mar edit store.mar. mar edit is extremely experimental. mar compile store.mar. mar fly init store.mar. mar fly provision store.mar. mar fly deploy store.mar. mar fly destroy store.mar. mar format store.mar. mar completion zsh. mar lsp."

                "shell-completion" ->
                    "Shell completion. Mar can generate shell completion for zsh, bash, and fish so commands like mar fly and mar compile are suggested as you type. Add the command below to your shell's startup file. Zsh add this line to ~/.zshrc. eval $(mar completion zsh). Bash add this line to ~/.bashrc or ~/.bash_profile. source <(mar completion bash). Fish add this line to ~/.config/fish/config.fish. mar completion fish | source."

                "generated-client-output" ->
                    "Generated client output. When you publish an app with mar compile, Mar also generates frontend clients for Elm and TypeScript. These clients wrap the generated HTTP API with named functions, so you do not need to hand-write fetch calls, URLs, or request payload shapes. Elm client. TypeScript client."

                "deploy" ->
                    "Deploy. Mar is intentionally simple to deploy. In production, you usually need just two things: the executable for your target platform and a persistent place to store the SQLite database."

                "email-delivery" ->
                    "Email delivery. If your app uses email login codes, configure a real SMTP provider before deploying. We currently recommend Resend because it is simple to set up and works well with Mar's SMTP configuration, but any SMTP-compatible provider should work. Set the SMTP password as an environment variable on your provider. In this example, smtp_password_env points to RESEND_API_KEY. That means your deploy environment must define a RESEND_API_KEY variable with the SMTP password before the app starts."

                "what-production-still-needs" ->
                    "What production still needs. Mar keeps deployment lightweight, but production still has a few practical requirements. A persistent disk for the SQLite database. A real email provider to send login codes."

                "where-you-can-deploy-it" ->
                    "Where you can deploy it. You can deploy a Mar app on any provider that can run a binary and give you persistent storage for the database file. Today, we recommend Fly.io because it fits that model well and keeps the setup small."

                "deploy-on-fly-io" ->
                    "Deploy on Fly.io. Mar already has a dedicated Fly.io workflow. Start with mar fly init, and Mar will prepare the Fly deployment files for your app. Choose the Fly app name you want to create on Fly.io. Choose the Fly region closest to your users. Let Mar generate the Fly deployment files for you. Then run mar fly provision to log in to Fly.io if needed, create the app, create its volume, and set the SMTP secret. Finally, run mar fly deploy. If you later want to remove the Fly.io app entirely, use mar fly destroy."

                "current-limitations" ->
                    "Current limitations. SQLite needs persistent disk storage so your data survives restarts and redeploys in production. A single-machine setup is the simplest path when using SQLite. If your provider cannot run a binary with persistent storage, it is not a good fit for Mar today."

                "compiler" ->
                    "Compiler. The compiler parses a single .mar file into a typed app model, validates it, generates clients, packages a manifest bundle with admin/public assets, and stamps that bundle into prebuilt runtime executables for all supported platforms."

                "compiler-pipeline" ->
                    "Compiler pipeline. Parse. Validate. Generate clients. Build bundle. Stamp prebuilt runtimes. Single executable per target platform. Typed clients. Packaged executables."

                "examples" ->
                    "Examples. Browse the Todo API and BookStore API examples."

                "todo-api-example" ->
                    "Todo API example. Minimal CRUD example using todo.mar."

                "bookstore-api-example" ->
                    "BookStore API example. Auth, roles, and transactional action example using store.mar. placeBookOrder."

                _ ->
                    ""

        Nothing ->
            ""


docSearchEntries : List DocSearchEntry
docSearchEntries =
    [ { title = "Home"
      , route = Home
      , sectionId = Just "home-hero"
      , summary = "A simple declarative backend language."
      , keywords = [ "simple", "declarative", "backend language", "home", "Elm", "PocketBase", "Rails", "Go" ]
      }
    , { title = "Why Mar"
      , route = Home
      , sectionId = Just "why-mar"
      , summary = "Less glue code. More backend. Declarative at its core, opinionated on purpose, and everything bundled."
      , keywords = [ "declarative", "opinionated", "everything bundled", "less glue code", "more backend", "auth", "admin tools", "logs", "monitoring", "backups" ]
      }
    , { title = "Who Mar Is For"
      , route = Home
      , sectionId = Just "who-mar-is-for"
      , summary = "A strong fit for teams that want the backend to stay boring in the best way: simple to run, easy to update, and operational from day one."
      , keywords = [ "boring", "backend", "mvp", "lean teams", "small teams", "simple deploy", "easy maintenance", "one binary" ]
      }
    , { title = "Getting Started"
      , route = GettingStarted
      , sectionId = Just "getting-started-intro"
      , summary = "Install Mar, create your first app with mar init, and open the Admin UI while developing."
      , keywords = [ "install", "quick start", "admin ui", "mar init", "mar dev", "starter app" ]
      }
    , { title = "Install"
      , route = GettingStarted
      , sectionId = Just "install"
      , summary = "Download Mar, add it to your PATH, and verify the installation."
      , keywords = [ "install", "download", "path", "mar version", "binary", "PATH", "VSCode", "extension" ]
      }
    , { title = "Quick Start"
      , route = GettingStarted
      , sectionId = Just "quick-start"
      , summary = "Create a starter app with mar init, then develop, compile, and run it."
      , keywords = [ "quick start", "mar init", "starter app", "admin", "admin ui", "_mar/admin", "browser", "dev", "serve" ]
      }
    , { title = "Choose an editor"
      , route = GettingStarted
      , sectionId = Just "install"
      , summary = "Try mar edit in the terminal for quick experiments. It is extremely experimental. For a fuller editing experience, use the VSCode extension."
      , keywords = [ "vscode", "editor", "extension", "formatting", "lsp", "syntax highlighting", "Mar Developer Tools", "marketplace", "mar edit", "terminal editor", "experimental" ]
      }
    , { title = "Advanced Fundamentals"
      , route = AdvancedFundamentals
      , sectionId = Just "advanced-fundamentals"
      , summary = "Understand the core syntax, built-in User model, rules, and authorization."
      , keywords = [ "language", "entities", "rules", "authorize", "user", "auth", "Posix", "timestamp", "Unix milliseconds", "default" ]
      }
    , { title = "Syntax model"
      , route = AdvancedFundamentals
      , sectionId = Just "syntax-model"
      , summary = "Top-level statements, fields, comments, and the basic shape of a Mar app."
      , keywords = [ "app", "port", "database", "public", "system", "auth", "entity", "type alias", "action", "comments", "Posix", "Int", "String", "Bool", "Float", "default" ]
      }
    , { title = "Authentication and authorization"
      , route = AdvancedFundamentals
      , sectionId = Just "authentication-and-authorization"
      , summary = "Built-in User entity, email-code login, deny-by-default access, and authorize rules."
      , keywords = [ "authentication", "authorization", "auth", "user", "authorize", "all", "list", "get", "create", "update", "delete" ]
      }
    , { title = "Rules and typed actions"
      , route = AdvancedFundamentals
      , sectionId = Just "rules-and-typed-actions"
      , summary = "Entity validation with rule/expect and multi-step transactional actions."
      , keywords = [ "rule", "expect", "validation", "typed actions", "transactions", "input", "load", "create", "update", "delete", "alias" ]
      }
    , { title = "Language Reference"
      , route = AdvancedLanguageReference
      , sectionId = Just "language-reference"
      , summary = "Browse the current keywords, built-in names, primitive types, functions, and configuration options."
      , keywords = [ "reference", "keywords", "functions", "system", "auth", "public", "Posix", "Int", "String", "Bool", "Float", "primitive types", "default" ]
      }
    , { title = "Validation and authorization reference"
      , route = AdvancedLanguageReference
      , sectionId = Just "validation-and-authorization-reference"
      , summary = "Reference for rule, expect, when, authorize, and CRUD operations."
      , keywords = [ "rule", "expect", "when", "authorize", "all", "list", "get", "create", "update", "delete" ]
      }
    , { title = "Auth config reference"
      , route = AdvancedLanguageReference
      , sectionId = Just "auth-config-reference"
      , summary = "Reference for User, code/session TTL, and SMTP email configuration."
      , keywords = [ "user", "code_ttl_minutes", "session_ttl_hours", "email_transport", "smtp_host", "smtp_port", "smtp_username", "smtp_password_env", "smtp_starttls" ]
      }
    , { title = "System config reference"
      , route = AdvancedLanguageReference
      , sectionId = Just "system-config-reference"
      , summary = "Reference for request logs, body limits, rate limits, admin UI session lifetime, and SQLite tuning."
      , keywords = [ "system", "request_logs_buffer", "http_max_request_body_mb", "admin_ui_session_ttl_hours", "sqlite", "security headers" ]
      }
    , { title = "Runtime"
      , route = AdvancedRuntime
      , sectionId = Just "runtime"
      , summary = "See generated endpoints, auth flow, system settings, migrations, and runtime behavior."
      , keywords = [ "runtime", "endpoints", "migrations", "system", "auth", "sqlite" ]
      }
    , { title = "System configuration"
      , route = AdvancedRuntime
      , sectionId = Just "system-configuration"
      , summary = "Tune runtime behavior such as request logging, body limits, rate limits, admin sessions, and SQLite settings."
      , keywords = [ "system", "runtime", "request logs", "body limits", "rate limits", "admin session", "sqlite" ]
      }
    , { title = "Public static frontend"
      , route = AdvancedRuntime
      , sectionId = Just "public-static-frontend"
      , summary = "Embed static frontend files into the final executable and optionally serve an SPA fallback."
      , keywords = [ "public", "frontend", "spa", "embedded", "static files", "mount", "dir", "spa_fallback" ]
      }
    , { title = "Generated endpoints"
      , route = AdvancedRuntime
      , sectionId = Just "generated-endpoints"
      , summary = "Understand generated CRUD, auth, action, health, schema, version, and admin endpoints."
      , keywords = [ "endpoints", "crud", "auth", "actions", "health", "schema", "version", "request logs", "backups" ]
      }
    , { title = "Database Schema Migrations"
      , route = AdvancedRuntime
      , sectionId = Just "migrations"
      , summary = "Automatic database schema migrations on startup with optional columns and defaulted required columns applied safely."
      , keywords = [ "migrations", "database schema", "startup", "schema changes", "blocked changes", "safe changes", "default", "optional columns" ]
      }
    , { title = "Tooling"
      , route = AdvancedTooling
      , sectionId = Just "tooling"
      , summary = "Use dev, compile, fly, format, completion, and LSP commands."
      , keywords = [ "tooling", "cli", "completion", "lsp", "fly", "format", "compile", "shell", "zsh", "bash", "fish", "autocomplete" ]
      }
    , { title = "Compiler and runtime commands"
      , route = AdvancedTooling
      , sectionId = Just "compiler-and-runtime-commands"
      , summary = "Use mar init, mar dev, code, mar edit, mar compile, mar fly init, mar fly provision, mar fly deploy, mar fly destroy, mar format, mar completion, and mar lsp."
      , keywords = [ "mar init", "mar edit", "mar dev", "code", "vscode", "mar compile", "mar fly init", "mar fly provision", "mar fly deploy", "mar fly destroy", "mar format", "mar completion", "mar lsp", "version", "editor", "terminal editor", "experimental editor" ]
      }
    , { title = "Shell completion"
      , route = AdvancedTooling
      , sectionId = Just "shell-completion"
      , summary = "Enable shell completion for zsh, bash, and fish."
      , keywords = [ "shell completion", "zsh", "bash", "fish", "autocomplete", "eval", "source", ".zshrc", ".bashrc", "config.fish" ]
      }
    , { title = "Generated client output"
      , route = AdvancedTooling
      , sectionId = Just "generated-client-output"
      , summary = "Generated Elm and TypeScript clients for CRUD, actions, auth, and version endpoints."
      , keywords = [ "clients", "elm", "typescript", "generated client", "frontend", "api client" ]
      }
    , { title = "Deploy"
      , route = AdvancedDeploy
      , sectionId = Just "deploy"
      , summary = "Deploy Mar apps with a single executable, persistent SQLite storage, SMTP, and Fly.io."
      , keywords = [ "deploy", "fly", "smtp", "resend", "sqlite", "volume" ]
      }
    , { title = "Email delivery"
      , route = AdvancedDeploy
      , sectionId = Just "email-delivery"
      , summary = "Configure a real SMTP provider and set the SMTP password as an environment variable before production deploys."
      , keywords = [ "email delivery", "smtp", "resend", "smtp_password_env", "RESEND_API_KEY", "login codes" ]
      }
    , { title = "Deploy on Fly.io"
      , route = AdvancedDeploy
      , sectionId = Just "deploy-on-fly-io"
      , summary = "Use mar fly init, mar fly provision, mar fly deploy, and mar fly destroy to prepare files, create the app, attach a volume, deploy, and remove the app when needed."
      , keywords = [ "fly", "fly.io", "fly init", "fly provision", "fly deploy", "fly destroy", "volume", "secret", "persistent disk", "region" ]
      }
    , { title = "Current limitations"
      , route = AdvancedDeploy
      , sectionId = Just "current-limitations"
      , summary = "SQLite needs persistent disk storage, and single-machine deployments are the simplest path today."
      , keywords = [ "limitations", "sqlite", "persistent disk", "restarts", "redeploys", "single machine" ]
      }
    , { title = "Compiler"
      , route = AdvancedCompiler
      , sectionId = Just "compiler"
      , summary = "Learn how Mar turns a single .mar file into typed clients and packaged executables."
      , keywords = [ "compiler", "clients", "bundle", "executables", "runtime stubs" ]
      }
    , { title = "Compiler pipeline"
      , route = AdvancedCompiler
      , sectionId = Just "compiler-pipeline"
      , summary = "Parse, validate, generate clients, build the bundle, and stamp prebuilt runtimes."
      , keywords = [ "parse", "validate", "generate clients", "bundle", "runtime executables", "prebuilt runtime" ]
      }
    , { title = "Examples"
      , route = Examples
      , sectionId = Just "examples"
      , summary = "Browse the Todo API and BookStore API examples."
      , keywords = [ "examples", "todo", "store", "bookstore", "sample apps" ]
      }
    , { title = "Todo API example"
      , route = Examples
      , sectionId = Just "todo-api-example"
      , summary = "Minimal CRUD example using todo.mar."
      , keywords = [ "todo", "todo.mar", "minimal crud", "example" ]
      }
    , { title = "BookStore API example"
      , route = Examples
      , sectionId = Just "bookstore-api-example"
      , summary = "Auth, roles, and transactional action example using store.mar."
      , keywords = [ "store", "store.mar", "bookstore", "auth", "roles", "transactional action", "placeBookOrder" ]
      }
    ]


footer : Element Msg
footer =
    el
        [ width fill
        , paddingEach { top = 0, right = 0, bottom = 0, left = 0 }
        ]
        (row
            [ width fill
            , Font.size 14
            , Font.color (rgb255 98 116 139)
            ]
            [ el [ width fill ] none
            , el [ centerX ]
                (row
                    [ spacing 4 ]
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
            , el [ width fill ] none
            , el [] githubRepoLink
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
        , homeGetStartedCta
        ]


homeGetStartedCta : Element Msg
homeGetStartedCta =
    panel
        [ column
            [ width fill
            , spacing 12
            ]
            [ paragraph
                [ Font.size 24
                , Font.bold
                , Font.color (rgb255 20 53 89)
                , width fill
                ]
                [ text "Ready to try Mar?" ]
            , paragraph
                [ Font.size 16
                , Font.color (rgb255 72 95 123)
                , width fill
                ]
                [ text "Start with the setup guide and run your first app locally." ]
            , row [ width fill ]
                [ el [ width fill ] none
                , link
                    (buttonAttributes
                        (rgb255 45 126 210)
                        (rgb255 245 250 255)
                        ++ [ width (fill |> maximum 320) ]
                    )
                    { url = routeHref GettingStarted
                    , label = text "Next: Getting Started"
                    }
                ]
            ]
        ]


gettingStartedPage : Model -> Element Msg
gettingStartedPage model =
    column
        [ width fill
        , spacing 20
        ]
        [ panel
            [ anchoredSection "getting-started-intro"
                [ sectionTitle "Getting Started"
                , paragraph [ Font.size 16, Font.color (rgb255 72 95 123), width fill ]
                    [ text "Install Mar, iterate quickly with hot reload, and deploy as a single executable." ]
                ]
            ]
        , install model
        , quickStart model
        , gettingStartedAdvancedCta
        ]


gettingStartedAdvancedCta : Element Msg
gettingStartedAdvancedCta =
    panel
        [ column
            [ width fill
            , spacing 12
            ]
            [ paragraph
                [ Font.size 24
                , Font.bold
                , Font.color (rgb255 20 53 89)
                ]
                [ text "Ready for the next step?" ]
            , paragraph
                [ Font.size 16
                , Font.color (rgb255 72 95 123)
                ]
                [ text "Continue to the Advanced Guide to understand the language, runtime, and compiler in more depth." ]
            , row [ width fill ]
                [ el [ width fill ] none
                , link
                    (buttonAttributes
                        (rgb255 45 126 210)
                        (rgb255 245 250 255)
                    )
                    { url = routeHref AdvancedGuide
                    , label = text "Next: Advanced Guide"
                    }
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
            [ anchoredSection "advanced-fundamentals"
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
                    , text ", "
                    , newTabLink
                        [ Font.color (rgb255 36 82 132)
                        , Font.semiBold
                        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                        ]
                        { url = "https://pocketbase.io"
                        , label = text "PocketBase"
                        }
                    , text ", and "
                    , newTabLink
                        [ Font.color (rgb255 36 82 132)
                        , Font.semiBold
                        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                        ]
                        { url = "https://rubyonrails.org"
                        , label = text "Rails"
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
                , bodyText "Mar reads top-to-bottom as a declarative app definition. A Mar app is centered around entities, rules, authorization, optional auth configuration, and typed actions. Built-in field types are Int, String, Bool, Float, and Posix."
                , docSubsectionTitle "Quick Examples"
                , codeFromString model "todo.mar" 450 todoExampleSource
                , codeFromString model "action.mar" 575 actionExampleSource
                ]
            , anchoredSection "syntax-model"
                [ docSubsectionTitle "Syntax Model"
                , docList
                    [ "Top-level statements: app, port, database, public, system, auth, entity, type alias, action."
                    , "Fields use the form fieldName: Type with optional modifiers such as primary, auto, optional, and default."
                    , "Built-in field types are Int, String, Bool, Float, and Posix. `Posix` follows Elm `Time.Posix` and stores Unix milliseconds."
                    , "Comments use Elm-style line comments: -- this is a comment."
                    ]
                ]
            , anchoredSection "authentication-and-authorization"
                [ docSubsectionTitle "Authentication and Authorization"
                , bodyText "Mar includes a built-in email-code login flow and per-operation authorization rules. The same auth model is also used by system-level tooling such as monitoring, logs, and backups."
                , codeFromString model "auth.mar" 272 authConfigSource
                , codeFromString model "authorize.mar" 300 authorizeExampleSource
                , docList
                    [ "Authentication endpoints are always available."
                    , "Every Mar app includes a built-in User entity that you may extend."
                    , "Entity access is deny-by-default unless you declare authorize rules."
                    , "Admin always has read-only access to the built-in User entity, even without explicit authorize rules."
                    , "`authorize all when ...` sets a default rule for list, get, create, update, and delete, and specific operations can still override it."
                    , "System features use the same session and require role == \"admin\"."
                    ]
                ]
            , anchoredSection "rules-and-typed-actions"
                [ docSubsectionTitle "Rules and Typed Actions"
                , paragraphWithEmphasis
                    [ el [ Font.bold, Font.color (rgb255 20 53 89) ] (text "Rules")
                    , text " are for validation close to the entity definition. "
                    , el [ Font.bold, Font.color (rgb255 20 53 89) ] (text "Actions")
                    , text " are for multi-step writes that must succeed or fail together."
                    ]
                , docList
                    [ "rule validates entity data and returns HTTP 422 with details when validation fails."
                    , "Actions run in a single atomic transaction."
                    , "Action steps can load, create, update, and delete rows."
                    , "Action steps may bind aliases like `todo = load Todo { ... }` or `order = create Order { ... }`, and later steps may reference alias fields such as `todo.id`."
                    , "Mar checks input types and assigned entity fields at compile time."
                    ]
                ]
            , anchoredSection "current-limitations-fundamentals"
                [ docSubsectionTitle "Current Limitations"
                , bodyText "Mar currently supports a single .mar entry file per app, without multi-file projects or imports."
                ]
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
            [ anchoredSection "language-reference"
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
                    , languageReferenceItem "default" "Assigns a literal default value to a field, such as `done: Bool default false`."
                    ]
                , languageReferenceGroup "Built-in primitive types"
                    [ languageReferenceItem "Int" "Whole-number field type."
                    , languageReferenceItem "String" "Text field type."
                    , languageReferenceItem "Bool" "Boolean field type."
                    , languageReferenceItem "Float" "Decimal-number field type."
                    , languageReferenceItem "Posix" "Timestamp field type stored as Unix milliseconds, aligned with Elm `Time.Posix`."
                    ]
                ]
            , anchoredSection "validation-and-authorization-reference"
                [ languageReferenceGroup "Validation and authorization"
                    [ languageReferenceItem "rule" "Adds entity validation."
                    , languageReferenceItem "expect" "Introduces the boolean expression enforced by a rule."
                    , languageReferenceItem "when" "Introduces the boolean expression used by an authorization clause."
                    , languageReferenceItem "authorize" "Declares per-operation authorization rules."
                    , languageReferenceItem "all, list, get, create, update, delete" "The supported operations for authorize clauses. `all` sets a default rule for every CRUD operation."
                    ]
                , languageReferenceGroup "Actions"
                    [ languageReferenceItem "input" "Declares the action input type and is also used in expressions such as input.userId."
                    , languageReferenceItem "load" "Loads one row inside an action. `load` must bind to an alias and must select by primary key."
                    , languageReferenceItem "create" "Adds a create step inside an action. Steps may bind aliases such as `order = create Order { ... }`."
                    , languageReferenceItem "update" "Adds an update step inside an action. Include the entity primary key plus the fields to change. Steps may bind aliases such as `updatedOrder = update Order { ... }`."
                    , languageReferenceItem "delete" "Adds a delete step inside an action. Include the entity primary key to select the row to remove. Steps may bind aliases such as `deletedOrder = delete Order { ... }`."
                    ]
                ]
            , anchoredSection "auth-config-reference"
                [ languageReferenceGroup "Auth config"
                    [ languageReferenceItem "User" "Built-in user entity present in every Mar app. You may extend it with extra fields and authorization rules. Admin always has read-only access to it."
                    , languageReferenceItem "code_ttl_minutes" "Sets how long login codes remain valid."
                    , languageReferenceItem "session_ttl_hours" "Sets the default session lifetime."
                    , languageReferenceItem "email_transport, email_from, email_subject, smtp_host, smtp_port, smtp_username, smtp_password_env, smtp_starttls" "Configure how login codes are delivered."
                    ]
                ]
            , anchoredSection "system-config-reference"
                [ languageReferenceGroup "System config"
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
            [ anchoredSection "runtime"
                [ sectionTitle "Advanced Guide"
                , docSubsectionTitle "Runtime"
                , bodyText "The runtime generated by Mar is meant to be practical by default: HTTP endpoints, SQLite storage, authentication, admin tooling, and migrations come from the same source file."
                ]
            , anchoredSection "system-configuration"
                [ docSubsectionTitle "System Configuration"
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
                ]
            , anchoredSection "public-static-frontend"
                [ docSubsectionTitle "Public Static Frontend"
                , bodyText "Mar can embed static frontend files into the final executable. This is useful when you want one deployable binary that serves both the backend and a compiled frontend."
                , codeFromString model "public.mar" 0 publicConfigSource
                ]
            , anchoredSection "generated-endpoints"
                [ docSubsectionTitle "Generated Endpoints"
                , bodyText "Mar turns the declarative app definition into a concrete HTTP surface. CRUD, actions, auth, health, version, and admin-related endpoints are generated automatically from the source file."
                , docList
                    [ "Each entity gets REST CRUD endpoints."
                    , "Typed actions are exposed as POST /actions/<name>."
                    , "System endpoints include /health, /_mar/admin, /_mar/schema, and /_mar/version."
                    , "Admin-only system endpoints include /_mar/version/admin, /_mar/perf, /_mar/request-logs, and /_mar/backups."
                    ]
                ]
            , anchoredSection "migrations"
                [ docSubsectionTitle "Database Schema Migrations"
                , bodyText "Mar applies schema migration logic automatically on startup. Safe changes are handled for you, while unsafe changes are blocked instead of being applied silently."
                , docList
                    [ "Migrations run automatically on startup."
                    , "Mar creates missing tables, adds new optional columns, adds new required columns when they define a literal default, and keeps auth/session storage ready."
                    , "Unsafe changes such as type changes, primary key changes, nullability changes, and new required fields without defaults are blocked."
                    , "When blocked, startup fails with a clear migration error."
                    ]
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
            [ anchoredSection "tooling"
                [ sectionTitle "Advanced Guide"
                , docSubsectionTitle "Tooling"
                , paragraphWithEmphasis
                    [ text "The "
                    , inlineCommand "mar"
                    , text " CLI supports the day-to-day developer workflow, while the generated clients and editor support help keep frontend and backend aligned."
                    ]
                ]
            , anchoredSection "compiler-and-runtime-commands"
                [ docSubsectionTitle "Compiler and Runtime Commands"
                , commandRow model "1" "Init" "Creates a new Mar project with a starter app, .gitignore, and README." "mar init store-app"
                , commandRow model "2" "Dev" "Runs the app in development mode with hot reload." "mar dev store.mar"
                , editCommandRow model "3"
                , commandRow model "4" "Compile" "Packages self-contained executables for all supported platforms and generates frontend clients." "mar compile store.mar"
                , commandRow model "5" "Fly init" "Prepares Fly.io deployment files for your app." "mar fly init store.mar"
                , commandRow model "6" "Fly provision" "Creates the Fly app, volume, and secrets from the generated Fly config." "mar fly provision store.mar"
                , commandRow model "7" "Fly deploy" "Rebuilds the Linux executable for the current app and runs fly deploy with the generated Fly config." "mar fly deploy store.mar"
                , commandRow model "8" "Fly destroy" "Permanently destroys the Fly.io app configured for this project." "mar fly destroy store.mar"
                , commandRow model "9" "Format" "Applies Mar's official formatting style to source files." "mar format store.mar"
                , commandRow model "10" "Completion" "Generates shell completion scripts for terminals such as zsh." "mar completion zsh"
                , commandRow model "11" "LSP" "Starts the language server used by the VSCode extension for diagnostics, hovers, and navigation. Usually started by the editor plugin." "mar lsp"
                , paragraphWithEmphasis
                    [ text "For a production walkthrough, see the "
                    , link [ Font.color (rgb255 36 82 132), Font.semiBold ] { url = routeHref AdvancedDeploy, label = text "Deploy" }
                    , text " guide."
                    ]
                ]
            , anchoredSection "shell-completion"
                [ docSubsectionTitle "Shell completion"
                , paragraphWithEmphasis
                    [ text "Mar can generate shell completion for "
                    , emphasisText "zsh"
                    , text ", "
                    , emphasisText "bash"
                    , text ", and "
                    , emphasisText "fish"
                    , text " so commands like "
                    , inlineCommand "mar fly"
                    , text " and "
                    , inlineCommand "mar compile"
                    , text " are suggested as you type."
                    ]
                , bodyText "Add the command below to your shell's startup file."
                , column
                    [ width fill
                    , spacing 16
                    ]
                    [ column
                        [ width fill
                        , spacing 8
                        ]
                        [ paragraphWithEmphasis
                            [ emphasisText "Zsh"
                            , text " — add this line to "
                            , inlineCommand "~/.zshrc"
                            , text "."
                            ]
                        , terminalFromString model "eval \"$(mar completion zsh)\""
                        ]
                    , column
                        [ width fill
                        , spacing 8
                        ]
                        [ paragraphWithEmphasis
                            [ emphasisText "Bash"
                            , text " — add this line to "
                            , inlineCommand "~/.bashrc"
                            , text " or "
                            , inlineCommand "~/.bash_profile"
                            , text "."
                            ]
                        , terminalFromString model "source <(mar completion bash)"
                        ]
                    , column
                        [ width fill
                        , spacing 8
                        ]
                        [ paragraphWithEmphasis
                            [ emphasisText "Fish"
                            , text " — add this line to "
                            , inlineCommand "~/.config/fish/config.fish"
                            , text "."
                            ]
                        , terminalFromString model "mar completion fish | source"
                        ]
                    ]
                ]
            , anchoredSection "generated-client-output"
                [ docSubsectionTitle "Generated Client Output"
                , bodyText "When you publish an app with mar compile, Mar also generates frontend clients for Elm and TypeScript. These clients wrap the generated HTTP API with named functions, so you do not need to hand-write fetch calls, URLs, or request payload shapes."
                , docList
                    [ "Elm client: dist/<name>/clients/<AppName>Client.elm"
                    , "TypeScript client: dist/<name>/clients/<AppName>Client.ts"
                    , "Both include CRUD functions, action functions, auth endpoints, and backend version access."
                    , "They reduce duplicated frontend code and keep frontend calls aligned with the backend generated from your .mar file."
                    , "This makes refactors safer, because the client surface is regenerated from the same source as the server."
                    ]
                ]
            ]
        , advancedPager (Just AdvancedRuntime) (Just AdvancedDeploy)
        ]


advancedDeployPage : Model -> Element Msg
advancedDeployPage model =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu AdvancedDeploy
        , panel
            [ anchoredSection "deploy"
                [ sectionTitle "Advanced Guide"
                , docSubsectionTitle "Deploy"
                , bodyText "Mar is intentionally simple to deploy. In production, you usually need just two things: the executable for your target platform and a persistent place to store the SQLite database."
                , docSubsectionTitle "Why deployment is simple"
                , docList
                    [ "Mar compile produces a single executable per target platform."
                    , "The runtime already includes the API, authentication, admin tools, logs, monitoring, and backup features."
                    , "SQLite keeps the data model simple: one database file, no separate database service required."
                    ]
                ]
            , anchoredSection "email-delivery"
                [ docSubsectionTitle "Email delivery"
                , paragraphWithEmphasis
                    [ text "If your app uses email login codes, configure a real SMTP provider before deploying. We currently recommend "
                    , newTabLink
                        [ Font.color (rgb255 36 82 132)
                        , Font.semiBold
                        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                        ]
                        { url = "https://resend.com"
                        , label = text "Resend"
                        }
                    , text " because it is simple to set up and works well with Mar's SMTP configuration, but any SMTP-compatible provider should work."
                    ]
                , docList
                    [ "Set the SMTP password as an environment variable on your provider."
                    ]
                , codeFromString model "app.mar" 0 smtpDeploySource
                , bodyText "In this example, smtp_password_env points to RESEND_API_KEY. That means your deploy environment must define a RESEND_API_KEY variable with the SMTP password before the app starts."
                ]
            , anchoredSection "what-production-still-needs"
                [ docSubsectionTitle "What production still needs"
                , bodyText "Mar keeps deployment lightweight, but production still has a few practical requirements."
                , docList
                    [ "A persistent disk for the SQLite database. An ephemeral disk will lose your data."
                    , "A real email provider to send login codes."
                    ]
                ]
            , anchoredSection "where-you-can-deploy-it"
                [ docSubsectionTitle "Where you can deploy it"
                , paragraphWithEmphasis
                    [ text "You can deploy a Mar app on any provider that can run a binary and give you persistent storage for the database file. Today, we recommend "
                    , newTabLink
                        [ Font.color (rgb255 36 82 132)
                        , Font.semiBold
                        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                        ]
                        { url = "https://fly.io"
                        , label = text "Fly.io"
                        }
                    , text " because it fits that model well and keeps the setup small."
                    ]
                ]
            , anchoredSection "deploy-on-fly-io"
                [ docSubsectionTitle "Deploy on Fly.io"
                , paragraphWithEmphasis
                    [ text "Mar already has a dedicated Fly.io workflow. Start with "
                    , inlineCommand "mar fly init"
                    , text ", and Mar will prepare the Fly deployment files for your app."
                    ]
                , terminalFromString model flyInitSource
                , docList
                    [ "Choose the Fly app name you want to create on Fly.io."
                    , "Choose the Fly region closest to your users."
                    , "Let Mar generate the Fly deployment files for you."
                    ]
                , bodyText "After that, run `mar fly provision` to log in to Fly if needed, create the Fly app, create its volume, and set the SMTP secret. Then run `mar fly deploy`."
                , terminalFromString model flyProvisionSource
                , terminalFromString model flyDeploySource
                ]
            , anchoredSection "current-limitations"
                [ docSubsectionTitle "Current limitations"
                , docList
                    [ "SQLite needs persistent disk storage so your data survives restarts and redeploys in production."
                    , "A single-machine setup is the simplest path when using SQLite."
                    , "If your provider cannot run a binary with persistent storage, it is not a good fit for Mar today."
                    ]
                ]
            ]
        , advancedPager (Just AdvancedTooling) (Just AdvancedCompiler)
        ]


advancedCompilerPage : Element Msg
advancedCompilerPage =
    column
        [ width fill
        , spacing 20
        ]
        [ advancedSubmenu AdvancedCompiler
        , panel
            [ anchoredSection "compiler"
                [ sectionTitle "Advanced Guide"
                , docSubsectionTitle "Compiler"
                , bodyText "The compiler parses a single .mar file into a typed app model, validates it, generates clients, packages a manifest bundle with admin/public assets, and stamps that bundle into prebuilt runtime executables for all supported platforms."
                ]
            , anchoredSection "compiler-pipeline"
                [ architectureDiagram ]
            ]
        , advancedPager (Just AdvancedDeploy) (Just AdvancedLanguageReference)
        ]


examplesPage : Model -> Element Msg
examplesPage model =
    column
        [ width fill
        , spacing 20
        ]
        [ panelWithAttributes [ htmlAttribute (HtmlAttr.id "examples"), htmlAttribute (HtmlAttr.id "todo-api-example") ]
            [ row [ width fill, spacing 12 ]
                [ column [ width fill, spacing 4 ]
                    [ paragraph [ Font.size 22, Font.bold, Font.color (rgb255 20 53 89) ] [ text "Todo API" ]
                    , paragraph [ Font.size 15, Font.color (rgb255 95 114 138) ] [ text "Minimal CRUD example" ]
                    ]
                ]
            , codeFromString model "todo.mar" 360 todoExampleSource
            ]
        , panelWithAttributes [ htmlAttribute (HtmlAttr.id "bookstore-api-example") ]
            [ row [ width fill, spacing 12 ]
                [ column [ width fill, spacing 4 ]
                    [ paragraph [ Font.size 22, Font.bold, Font.color (rgb255 20 53 89) ] [ text "BookStore API" ]
                    , paragraph [ Font.size 15, Font.color (rgb255 95 114 138) ] [ text "Auth, roles, and transactional action" ]
                    ]
                ]
            , codeFromString model "store.mar" 360 storeExampleSource
            ]
        ]


hero : Element Msg
hero =
    panelWithAttributes [ htmlAttribute (HtmlAttr.id "home-hero") ]
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
                , text ", "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://pocketbase.io"
                    , label = text "PocketBase"
                    }
                , text ", and "
                , newTabLink
                    [ Font.color (rgb255 36 82 132)
                    , Font.semiBold
                    , htmlAttribute (HtmlAttr.style "cursor" "pointer")
                    ]
                    { url = "https://rubyonrails.org"
                    , label = text "Rails"
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
    panelWithAttributes [ htmlAttribute (HtmlAttr.id "install") ]
        [ sectionTitle "Install"
        , downloadInstallRow
        , pathInstallRow model
        , installCommandRow model "3" "Check" "mar version"
        , pluginInstallRow model
        ]


quickStart : Model -> Element Msg
quickStart model =
    panel
        [ anchoredSection "quick-start"
            [ sectionTitle "Quick Start"
            , commandRow model "1" "Init" "Creates a new Todo starter app in a new folder with todo.mar, .gitignore, and README." "mar init todo"
            , commandRow model "2" "Develop" "Enter the project folder, run the app locally with hot reload, and open the Admin UI." """cd todo
mar dev todo.mar"""
            , commandRow model "3" "Compile" "Package self-contained executables for all supported platforms and generate frontend clients." """cd todo
mar compile todo.mar"""
            , commandRow model "4" "Serve" "Choose the target folder for your platform, start that executable, and open the printed Admin URL." """cd todo/dist/todo/darwin-arm64
./todo serve"""
            ]
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
        , paragraph [ Font.size 16, Font.color (rgb255 70 93 121), width fill ]
            [ text "Download the latest release from "
            , newTabLink [ Font.color (rgb255 28 66 108), Font.underline ]
                { url = "https://github.com/marciofrayze/mar-lang/releases"
                , label = text "GitHub Releases"
                }
            , text ", then extract the .zip file into a folder on your machine."
            ]
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
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text "Add to your PATH")
            ]
        , paragraph [ Font.size 16, Font.color (rgb255 70 93 121), width fill ]
            [ text "Move "
            , inlineCommand "mar"
            , text " to a directory in your PATH."
            ]
        , column
            [ width fill
            , spacing 8
            ]
            [ installSubitem model "macOS/Linux" """mv mar /usr/local/bin/mar
chmod +x /usr/local/bin/mar"""
            , column
                [ width fill
                , spacing 6
                , Background.color (rgb255 250 252 255)
                , Border.width 1
                , Border.color (rgb255 223 232 244)
                , Border.rounded 10
                , paddingEach { top = 10, right = 10, bottom = 10, left = 10 }
                ]
                [ el [ Font.size 13, Font.semiBold, Font.color (rgb255 70 93 121) ] (text "Windows")
                , column [ width fill, spacing 6 ]
                    [ paragraph [ Font.size 15, Font.color (rgb255 72 95 123), width fill ]
                        [ text "1. Extract "
                        , inlineCommand "mar.exe"
                        , text " to a folder such as "
                        , inlineCommand "C:\\Tools\\mar"
                        , text "."
                        ]
                    , paragraph [ Font.size 15, Font.color (rgb255 72 95 123), width fill ]
                        [ text "2. Open "
                        , el [ Font.color (rgb255 28 66 108), Font.semiBold ] (text "Windows Environment Variables")
                        , text ", edit "
                        , inlineCommand "Path"
                        , text ", and add that folder (for example, "
                        , inlineCommand "C:\\Tools\\mar"
                        , text ")."
                        ]
                    ]
                ]
            ]
        ]


pluginInstallRow : Model -> Element Msg
pluginInstallRow model =
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
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text "Choose an editor")
            ]
        , wrappedRow
            [ width fill
            , spacing 10
            ]
            [ el
                [ width (fill |> minimum 300 |> maximum 520)
                , alignTop
                ]
                (column
                    [ width fill
                    , spacing 10
                    ]
                    [ column
                        [ width fill
                        , spacing 8
                        , Background.color (rgb255 255 255 255)
                        , Border.width 1
                        , Border.color (rgb255 213 225 241)
                        , Border.rounded 10
                        , padding 12
                        ]
                        [ wrappedRow
                            [ width fill
                            , spacing 8
                            ]
                            [ editBadge "Recommended" False
                            , el [ Font.bold, Font.size 16, Font.color (rgb255 28 66 108) ] (text "VSCode")
                            ]
                        , wrappedRow
                            [ width fill
                            , spacing 8
                            ]
                            [ commandSnippet model "code todo.mar" ]
                        , paragraph
                            [ Font.size 14
                            , Font.color (rgb255 83 105 132)
                            , width fill
                            ]
                            [ text "Use the VSCode extension for the fuller editing experience." ]
                        ]
                    , column
                        [ width fill
                        , spacing 6
                        , Background.color (rgb255 250 253 255)
                        , Border.width 1
                        , Border.color (rgb255 222 232 244)
                        , Border.rounded 10
                        , padding 12
                        ]
                        [ paragraph [ Font.size 14, Font.color (rgb255 70 93 121), width fill ]
                            [ text "Install "
                            , newTabLink [ Font.semiBold, Font.color (rgb255 28 66 108), Font.underline ]
                                { url = "https://marketplace.visualstudio.com/items?itemName=mar-lang.mar-language-support"
                                , label = text "Mar Developer Tools"
                                }
                            , text " from Visual Studio Marketplace."
                            ]
                        , paragraph
                            [ Font.size 14
                            , Font.color (rgb255 72 95 123)
                            , width fill
                            ]
                            [ text "Make sure "
                            , inlineCommand "mar"
                            , text " is on your PATH so the extension works correctly."
                            ]
                        ]
                    ]
                )
            , el
                [ width (fill |> minimum 300 |> maximum 520)
                , alignTop
                ]
                (column
                    [ width fill
                    , spacing 10
                    ]
                    [ editOptionCard model "Experimental" "Terminal editor" [ text "Feeling adventurous? Try the built-in experimental terminal editor." ] "mar edit todo.mar" True ]
                )
            ]
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


editBadge : String -> Bool -> Element Msg
editBadge badge isExperimental =
    el
        [ Background.color
            (if isExperimental then
                rgb255 255 244 229

             else
                rgb255 232 243 255
            )
        , Font.color
            (if isExperimental then
                rgb255 128 85 24

             else
                rgb255 36 82 132
            )
        , Font.size 12
        , Font.semiBold
        , Border.rounded 999
        , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
        ]
        (text badge)


features : Element Msg
features =
    panelWithAttributes [ htmlAttribute (HtmlAttr.id "why-mar") ]
        [ sectionTitle "Why Mar"
        , whyLayoutChosen
        ]


audience : Element Msg
audience =
    panelWithAttributes [ htmlAttribute (HtmlAttr.id "who-mar-is-for") ]
        [ sectionTitle "Who Mar Is For"
        , audienceVariantOne
        ]


panel : List (Element Msg) -> Element Msg
panel children =
    panelWithAttributes [] children


panelWithAttributes : List (Attribute Msg) -> List (Element Msg) -> Element Msg
panelWithAttributes extraAttrs children =
    column
        ([ width (fill |> maximum 1040)
         , centerX
         , spacing 12
         , padding 16
         , Background.color (rgb255 255 255 255)
         , Border.width 1
         , Border.color (rgb255 209 222 239)
         , Border.rounded 12
         ]
            ++ extraAttrs
        )
        children


anchoredSection : String -> List (Element Msg) -> Element Msg
anchoredSection sectionId children =
    column
        [ width fill
        , spacing 12
        , htmlAttribute (HtmlAttr.id sectionId)
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


inlineCommand : String -> Element Msg
inlineCommand value =
    el
        [ Background.color (rgb255 233 242 252)
        , Border.rounded 6
        , paddingEach { top = 2, right = 6, bottom = 2, left = 6 }
        , Font.family [ Font.typeface "IBM Plex Mono", Font.monospace ]
        , Font.size 13
        , Font.color (rgb255 28 66 108)
        ]
        (text value)


docSubsectionTitle : String -> Element Msg
docSubsectionTitle label =
    paragraph
        [ Font.size 23
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


whyDeclarative : Element Msg
whyDeclarative =
    whyFeatureStrip "Declarative at its core" "You describe the system at a higher level."


whyOpinionated : Element Msg
whyOpinionated =
    whyFeatureStrip "Opinionated on purpose" "Mar chooses a coherent runtime instead of exposing endless assembly decisions."


whyBundled : Element Msg
whyBundled =
    whyFeatureStrip "Everything bundled" "Authentication, authorization, admin tools, logs, monitoring, and built-in database backups come together."


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
            [ commandSnippet model command ]
        , paragraph
            [ Font.size 14
            , Font.color (rgb255 83 105 132)
            , width fill
            ]
            [ text description ]
        ]


editCommandRow : Model -> String -> Element Msg
editCommandRow model number =
    column
        [ width fill
        , spacing 10
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
            , el [ Font.bold, Font.size 18, Font.color (rgb255 28 66 108) ] (text "Edit")
            ]
        , wrappedRow
            [ width fill
            , spacing 10
            ]
            [ el
                [ width (fill |> minimum 300 |> maximum 520)
                , alignTop
                ]
                (editOptionCard model "Recommended" "VSCode" [ text "Open the app in VSCode while ", inlineCommand "mar dev", text " keeps rebuilding in the background." ] "code store.mar" False)
            , el
                [ width (fill |> minimum 300 |> maximum 520)
                , alignTop
                ]
                (editOptionCard model "Experimental" "Terminal editor" [ text "Feeling adventurous? Try the built-in experimental terminal editor." ] "mar edit store.mar" True)
            ]
        ]


editOptionCard : Model -> String -> String -> List (Element Msg) -> String -> Bool -> Element Msg
editOptionCard model badge label description command isExperimental =
    column
        [ width fill
        , spacing 8
        , Background.color (rgb255 255 255 255)
        , Border.width 1
        , Border.color
            (if isExperimental then
                rgb255 237 214 175

             else
                rgb255 213 225 241
            )
        , Border.rounded 10
        , padding 12
        ]
        [ wrappedRow
            [ width fill
            , spacing 8
            ]
            [ el
                [ Background.color
                    (if isExperimental then
                        rgb255 255 244 229

                     else
                        rgb255 232 243 255
                    )
                , Font.color
                    (if isExperimental then
                        rgb255 128 85 24

                     else
                        rgb255 36 82 132
                    )
                , Font.size 12
                , Font.semiBold
                , Border.rounded 999
                , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
                ]
                (text badge)
            , el [ Font.bold, Font.size 16, Font.color (rgb255 28 66 108) ] (text label)
            ]
        , wrappedRow
            [ width fill
            , spacing 8
            ]
            [ commandSnippet model command ]
        , paragraph
            [ Font.size 14
            , Font.color (rgb255 83 105 132)
            , width fill
            ]
            description
        ]


commandSnippet : Model -> String -> Element Msg
commandSnippet model command =
    if String.contains "\n" command then
        terminalFromString model command

    else
        wrappedRow
            [ width fill
            , spacing 8
            ]
            [ codeInline command
            , copyLink model command
            ]


advancedSubmenu : Route -> Element Msg
advancedSubmenu current =
    panel
        [ wrappedRow [ width fill, spacing 8 ]
            [ sectionNavItem current AdvancedFundamentals "Fundamentals"
            , sectionNavItem current AdvancedRuntime "Runtime"
            , sectionNavItem current AdvancedTooling "Tooling"
            , sectionNavItem current AdvancedDeploy "Deploy"
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
                            ++ [ width (fill |> maximum 320) ]
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
                            ++ [ width (fill |> maximum 320), htmlAttribute (HtmlAttr.style "margin-left" "auto") ]
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

        AdvancedDeploy ->
            "Deploy"

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
        , installCommandSnippet model command
        ]


installCommandSnippet : Model -> String -> Element Msg
installCommandSnippet model command =
    if String.contains "\n" command then
        terminalFromString model command

    else
        wrappedRow [ width fill, spacing 8 ]
            [ codeInlineSmall command
            , copyLink model command
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
        [ el [ Element.alignTop, moveDown -2, Font.color (rgb255 93 107 126), Font.bold ] (text "•")
        , paragraph
            [ Element.alignTop
            , Font.size 15
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
        [ el [ Element.alignTop, moveDown -2, Font.color (rgb255 93 107 126), Font.bold ] (text "•")
        , paragraph
            [ Element.alignTop
            , Font.size 15
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
                (highlightCLICommandLine source)
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
                (highlightCLICommandLine source)
            )
        )


terminalFromString : Model -> String -> Element Msg
terminalFromString model source =
    let
        lines =
            source
                |> String.split "\n"
                |> trimTrailingEmptyLine
    in
    wrappedRow
        [ width fill
        , spacing 8
        ]
        [ el
            [ width fill
            , scrollbarX
            , Background.color (rgb255 16 34 54)
            , Border.width 1
            , Border.color (rgb255 38 70 105)
            , Border.rounded 10
            , paddingEach { top = 12, right = 14, bottom = 12, left = 14 }
            ]
            (html
                (Html.pre
                    [ HtmlAttr.style "margin" "0"
                    , HtmlAttr.style "white-space" "pre"
                    , HtmlAttr.style "font-family" "IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
                    , HtmlAttr.style "font-size" "14px"
                    , HtmlAttr.style "line-height" "1.55"
                    , HtmlAttr.style "color" "#D8E7F8"
                    ]
                    (List.map terminalLineView lines)
                )
            )
        , copyLink model source
        ]


terminalLineView : String -> Html.Html msg
terminalLineView lineText =
    Html.div
        [ HtmlAttr.style "min-height" "22px" ]
        (if String.isEmpty lineText then
            [ Html.text " " ]

         else
            highlightCLICommandLine lineText
        )


terminalHighlightWords : List String
terminalHighlightWords =
    [ "eval"
    , "source"
    , "cd"
    , "mv"
    , "chmod"
    , "mar"
    , "code"
    , "zsh"
    , "bash"
    , "fish"
    , "todo.mar"
    , "./todo"
    , "./todo.exe"
    , "todo/dist/todo/darwin-arm64"
    , "dist/todo/darwin-arm64"
    , "dist/todo/linux-<your-arch>"
    , "dist/todo/windows-amd64"
    , "dist/todo/<your-target-folder>"
    , "/usr/local/bin/mar"
    ]


terminalHighlightColor : String -> String
terminalHighlightColor value =
    case value of
        "mar" ->
            "#A6E3A1"

        "code" ->
            "#A6E3A1"

        "./todo" ->
            "#A6E3A1"

        "./todo.exe" ->
            "#A6E3A1"

        "zsh" ->
            "#C792EA"

        "bash" ->
            "#C792EA"

        "fish" ->
            "#C792EA"

        "todo.mar" ->
            "#F6C177"

        "todo/dist/todo/darwin-arm64" ->
            "#F6C177"

        "dist/todo/darwin-arm64" ->
            "#F6C177"

        "dist/todo/linux-<your-arch>" ->
            "#F6C177"

        "dist/todo/windows-amd64" ->
            "#F6C177"

        "dist/todo/<your-target-folder>" ->
            "#F6C177"

        "/usr/local/bin/mar" ->
            "#F6C177"

        _ ->
            "#8BE9FD"


highlightCLICommandLine : String -> List (Html.Html msg)
highlightCLICommandLine lineText =
    lineText
        |> tokenizeTerminalLine
        |> List.map terminalChunkView


terminalChunkView : TerminalChunk -> Html.Html msg
terminalChunkView chunk =
    case chunk of
        TerminalWhitespace value ->
            Html.text value

        TerminalToken value ->
            Html.span [ HtmlAttr.style "color" (terminalTokenColor value) ] [ Html.text value ]


terminalTokenColor : String -> String
terminalTokenColor chunk =
    let
        normalized =
            String.trim chunk
    in
    if isMarBinaryToken normalized then
        "#A6E3A1"

    else if String.endsWith ".mar" normalized then
        "#F6C177"

    else if List.member normalized terminalHighlightWords then
        terminalHighlightColor normalized

    else if isCLICommandToken normalized then
        "#8BE9FD"

    else
        "#D8E7F8"


isMarBinaryToken : String -> Bool
isMarBinaryToken chunk =
    let
        normalized =
            chunk
                |> String.trim
                |> String.split "/"
                |> List.reverse
                |> List.head
                |> Maybe.withDefault chunk
    in
    List.member normalized [ "mar", "code" ]


isCLICommandToken : String -> Bool
isCLICommandToken chunk =
    List.member chunk
        [ "dev"
        , "compile"
        , "fly"
        , "init"
        , "deploy"
        , "format"
        , "completion"
        , "lsp"
        , "version"
        ]


type TerminalChunk
    = TerminalWhitespace String
    | TerminalToken String


tokenizeTerminalLine : String -> List TerminalChunk
tokenizeTerminalLine value =
    tokenizeTerminalChars [] [] False (String.toList value)
        |> List.reverse


tokenizeTerminalChars : List TerminalChunk -> List Char -> Bool -> List Char -> List TerminalChunk
tokenizeTerminalChars chunks current isWhitespace remaining =
    case remaining of
        [] ->
            flushTerminalChunk chunks current isWhitespace

        char :: rest ->
            let
                charIsWhitespace =
                    isTerminalWhitespace char
            in
            if List.isEmpty current then
                tokenizeTerminalChars chunks [ char ] charIsWhitespace rest

            else if charIsWhitespace == isWhitespace then
                tokenizeTerminalChars chunks (char :: current) isWhitespace rest

            else
                tokenizeTerminalChars
                    (terminalChunkFromChars isWhitespace current :: chunks)
                    [ char ]
                    charIsWhitespace
                    rest


flushTerminalChunk : List TerminalChunk -> List Char -> Bool -> List TerminalChunk
flushTerminalChunk chunks current isWhitespace =
    if List.isEmpty current then
        chunks

    else
        terminalChunkFromChars isWhitespace current :: chunks


terminalChunkFromChars : Bool -> List Char -> TerminalChunk
terminalChunkFromChars isWhitespace chars =
    let
        value =
            chars
                |> List.reverse
                |> String.fromList
    in
    if isWhitespace then
        TerminalWhitespace value

    else
        TerminalToken value


isTerminalWhitespace : Char -> Bool
isTerminalWhitespace char =
    char == ' ' || char == '\t'


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

        hasHeader =
            String.trim fileName /= ""

        codeBox topLeft topRight =
            el
                [ width fill
                , height (px autoHeight)
                , scrollbarX
                , scrollbarY
                , Background.color (rgb255 18 38 61)
                , Border.widthEach { top = 1, right = 1, bottom = 1, left = 1 }
                , Border.color (rgb255 38 70 105)
                , Border.roundEach
                    { topLeft = topLeft
                    , topRight = topRight
                    , bottomLeft = 10
                    , bottomRight = 10
                    }
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
    in
    if hasHeader then
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
            , codeBox 0 0
            ]

    else
        codeBox 10 10


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
    Char.isAlphaNum char || char == '_' || char == '.'


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
    if word == "input" || String.startsWith "input." word || String.contains "." word then
        token "#93D7FF" word

    else if List.member word [ "all", "list", "get", "load", "create", "update", "delete" ] then
        token "#93D7FF" word

    else if List.member word [ "app", "port", "database", "entity", "rule", "expect", "when", "authorize", "auth", "type", "alias", "action", "input", "load", "create", "public", "system", "dir", "mount", "spa_fallback", "code_ttl_minutes", "session_ttl_hours", "email_transport", "email_from", "email_subject", "smtp_host", "smtp_port", "smtp_username", "smtp_password_env", "smtp_starttls", "request_logs_buffer", "http_max_request_body_mb", "auth_request_code_rate_limit_per_minute", "auth_login_rate_limit_per_minute", "admin_ui_session_ttl_hours", "security_frame_policy", "security_referrer_policy", "security_content_type_nosniff", "sqlite_journal_mode", "sqlite_synchronous", "sqlite_foreign_keys", "sqlite_busy_timeout_ms", "sqlite_wal_autocheckpoint", "sqlite_journal_size_limit_mb", "sqlite_mmap_size_mb", "sqlite_cache_size_kb" ] then
        token "#7AB8FF" word

    else if List.member word [ "Int", "String", "Bool", "Float", "Posix" ] then
        token "#4FD1C5" word

    else if List.member word [ "primary", "auto", "optional", "default" ] then
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


token : String -> String -> Html.Html msg
token color value =
    Html.span [ HtmlAttr.style "color" color ] [ Html.text value ]


todoExampleSource : String
todoExampleSource =
    """-- A minimal CRUD application.
-- This example shows the basic Mar structure:
-- app, entity, rule, and authorization.

-- Application
app TodoApi

-- Entity
entity Todo {
  title: String
  done: Bool

  rule "Title must have at least 3 chars" expect len(title) >= 3
  authorize all when auth_authenticated
}
"""


actionExampleSource : String
actionExampleSource =
    """-- A transactional action example.
-- This example shows how one action can load,
-- create, update, and delete in a single atomic operation.

-- Action input
type alias PlaceOrderInput =
  { userId : Int
  , inventoryId : Int
  , cartId : Int
  , total : Float
  }

-- Atomic action
action placeOrder {
  input: PlaceOrderInput

  inventory = load Inventory {
    id: input.inventoryId
  }

  order = create Order {
    userId: input.userId
    total: input.total
    status: "created"
  }

  updatedInventory = update Inventory {
    id: inventory.id
    stock: inventory.stock - 1
  }

  deletedCart = delete Cart {
    id: input.cartId
  }

  audit = create AuditLog {
    userId: order.userId
    event: "order created"
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
  dir "./frontend/dist"      -- required; resolved relative to the .mar file.
  mount "/"                  -- defaults to /.
  spa_fallback "index.html"  -- serves the frontend entry file for SPA-style routes.
}
"""


authConfigSource : String
authConfigSource =
    """-- Email-code authentication with direct SMTP delivery
auth {
  code_ttl_minutes 10
  session_ttl_hours 24
  email_transport smtp
  email_from "no-reply@store.example"
  email_subject "Your StoreApi login code"
  smtp_host "smtp.example.com"
  smtp_port 587
  smtp_username "username"
  smtp_password_env "SMTP_PASSWORD"
  smtp_starttls true
}
"""


flyInitSource : String
flyInitSource =
    """mar fly init app.mar
"""


flyProvisionSource : String
flyProvisionSource =
    """mar fly provision app.mar
"""


flyDeploySource : String
flyDeploySource =
    """mar fly deploy app.mar
"""


smtpDeploySource : String
smtpDeploySource =
    """-- In your app.mar file
auth {
  email_transport smtp
  email_from "no-reply@yourdomain.com"
  email_subject "Your login code"
  smtp_host "smtp.resend.com"
  smtp_port 587
  smtp_username "resend"
  smtp_password_env "RESEND_API_KEY"
  smtp_starttls true
}
"""


authorizeExampleSource : String
authorizeExampleSource =
    """-- Per-operation authorization inside the built-in User entity
entity User {
  displayName: String optional

  -- Admin always has read-only access to User, even without explicit rules.
  -- These rules are still useful when non-admin user access should be allowed.
  authorize all when isRole("admin")
  authorize get when auth_authenticated and (id == auth_user_id or isRole("admin"))
  authorize delete when isRole("admin")
}
"""


storeExampleSource : String
storeExampleSource =
    """app BookStoreApi
database "bookstore.db"

auth {
  code_ttl_minutes 10
  session_ttl_hours 24
  email_transport console
  email_from "no-reply@bookstore.local"
  email_subject "Your BookStore login code"
}

entity User {
  displayName: String optional

  authorize all when isRole("admin")
  authorize get when auth_authenticated and (id == auth_user_id or isRole("admin"))
  authorize update when auth_authenticated and ((id == auth_user_id and role == auth_role) or isRole("admin"))
  authorize delete when isRole("admin")
}

entity Book {
  title: String
  authorName: String
  isbn: String
  price: Float
  stock: Int

  rule "Book title cannot be empty" expect title != ""
  rule "Price must be greater than zero" expect price > 0

  authorize all when true
  authorize create when auth_authenticated
  authorize update when isRole("admin")
  authorize delete when isRole("admin")
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

  order = create Order {
    orderRef: input.orderRef
    userId: input.userId
    status: "confirmed"
    total: input.orderTotal
    currency: "BRL"
    notes: input.notes
  }

  orderItem = create OrderItem {
    orderRef: order.orderRef
    userId: order.userId
    bookId: input.bookId
    quantity: input.quantity
    unitPrice: input.unitPrice
    lineTotal: input.lineTotal
  }

  auditLog = create AuditLog {
    userId: orderItem.userId
    event: "book order created"
    orderRef: orderItem.orderRef
  }
}
"""
