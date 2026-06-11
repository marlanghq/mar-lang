package scaffold

import "fmt"

// fullstackAuthFiles returns the file set for `mar init` when the
// operator picks the fullstack-with-auth kind. Layout mirrors the
// canonical multipage examples (notes-auth-multipage, team-notes):
//
//	Main.mar              — Auth.config + App.fullstack wiring
//	Shared.mar            — types + Service contracts
//	Backend.mar           — users + entries entities, Auth.protected services
//	Frontend/Routes.mar   — centralized typed Paths
//	Frontend/SignIn.mar   — email + code flow with inline errors
//	Frontend/Home.mar     — protected entries page
//
// Splitting pages and using Page.protected from day one matches how
// real auth-gated apps grow.
func fullstackAuthFiles(name string) map[string]string {
	files := sharedFiles(name)
	files["mar.json"] = fmt.Sprintf(`{
  "name": "%s"
}
`, name)
	files["Main.mar"] = `module Main exposing (main)


-- Entry point. Sign-in uses a one-time email code.
--
-- In dev mode the codes are printed to the terminal running mar dev,
-- so you can sign in without configuring SMTP. For real email in
-- production, add a "mail" block to mar.json.


import Backend
import Frontend.SignIn
import Frontend.Home


auth : Auth { id : Int, email : String }
auth =
    Auth.config
        { entity     = Backend.users
        , identify   = \u -> u.email
        , signInPage = Frontend.SignIn.page
        , email      = { subject = "Sign in" }
        , signup     = \email -> { email = email }
        , sessionDuration = Time.days 30
        }


main : Effect ()
main =
    App.fullstack
        { services = Backend.services
        , pages    =
            [ Frontend.SignIn.page
            , Frontend.Home.page
            ]
        -- Usually api is empty, unless you need webhooks or custom REST routes.
        , api      = []
        }
`
	files["Shared.mar"] = `module Shared exposing (..)


-- Types and Service contracts shared between Frontend (caller) and
-- Backend (implementation).


type alias User =
    { id    : Int
    , email : String
    }


type alias Entry =
    { id       : Int
    , body     : String
    , authorId : Int
    }


type alias NewEntry =
    { body : String
    }


-- Two services. Backend.mar implements them, Frontend.Home calls them.
-- Auth.protect (in Backend.mar) injects the signed-in User as the
-- handler's second arg, so unauthenticated requests get 401 before
-- the handler runs.
listMine : Service () (List Entry)
listMine = Service.declare


createMine : Service NewEntry Entry
createMine = Service.declare
`
	files["Backend.mar"] = `module Backend exposing (users, services)


import Shared


-- Auth's User entity. Main.mar passes this to Auth.config.
users : Entity Shared.User
users =
    Entity.define
        { name = "users"
        , columns =
            { id    = Entity.serial
            , email = Entity.text Entity.notNull
            }
        , uniques = []
        }


entries : Entity Shared.Entry
entries =
    Entity.define
        { name = "entries"
        , columns =
            { id       = Entity.serial
            , body     = Entity.text Entity.notNull
            , authorId = Entity.int Entity.notNull
            }
        , uniques = []
        }


-- Service handlers. Each takes the authenticated User as its second
-- arg. Auth.protect (below) injects it; missing/expired sessions
-- return 401 before the handler runs.
listMine : () -> Shared.User -> Effect (List Shared.Entry)
listMine _ user =
    Repo.findBy entries { authorId = user.id }


createMine : Shared.NewEntry -> Shared.User -> Effect Shared.Entry
createMine input user =
    Repo.create entries
        { body     = input.body
        , authorId = user.id
        }


services =
    [ Auth.protect Shared.listMine   listMine
    , Auth.protect Shared.createMine createMine
    ]
`
	files["Frontend/Routes.mar"] = `module Frontend.Routes exposing (signIn, home)


-- Centralized URL surface. Renaming a path here surfaces every
-- navigationLink and Nav.pushTo call site as a compile-time error.
-- Page.create takes the raw String separately (the typechecker needs
-- a String literal for its route-pattern parser).


signIn : Path {}
signIn = "/sign-in"


home : Path {}
home = "/"
`
	files["Frontend/SignIn.mar"] = `module Frontend.SignIn exposing (page)


import Shared
import UI exposing
    ( navigationStack, navigationTitle
    , form, section
    , text, textField, button, empty
    , email, numericCode, submit, width, chars
    )


-- Local state machine for the sign-in flow. The Maybe String slot
-- on each input state carries the last server error so the user
-- sees why their attempt failed.
type Model
    = AskEmail String (Maybe String)
    | Submitting String
    | AskCode String String (Maybe String)


type Msg
    = DraftChanged String
    | Submitted
    | CodeRequested (Result String ())
    | CodeVerified (Result String Shared.User)


init : (Model, Effect Msg)
init =
    ( AskEmail "" Nothing, Effect.none )


update : Msg -> Model -> (Model, Effect Msg)
update msg model =
    case msg of
        -- Typing clears any pending error.
        DraftChanged value ->
            case model of
                AskEmail _ _ ->
                    ( AskEmail value Nothing, Effect.none )

                AskCode email _ _ ->
                    ( AskCode email value Nothing, Effect.none )

                Submitting _ ->
                    ( model, Effect.none )

        Submitted ->
            case model of
                AskEmail email _ ->
                    ( Submitting email
                    , Auth.requestCode { email = email } CodeRequested
                    )

                AskCode email code _ ->
                    ( Submitting email
                    , Auth.verifyCode { email = email, code = code } CodeVerified
                    )

                Submitting _ ->
                    ( model, Effect.none )

        CodeRequested (Ok ()) ->
            case model of
                Submitting email ->
                    ( AskCode email "" Nothing, Effect.none )

                _ ->
                    ( model, Effect.none )

        CodeRequested (Err why) ->
            case model of
                Submitting email ->
                    ( AskEmail email (Just why), Effect.none )

                _ ->
                    ( model, Effect.none )

        CodeVerified (Ok _) ->
            -- Auth.completeSignIn redirects to wherever a 401 sent
            -- the user from (or "/" by default), replacing history
            -- so back-button doesn't return to sign-in.
            ( AskEmail "" Nothing, Auth.completeSignIn )

        CodeVerified (Err why) ->
            case model of
                Submitting email ->
                    ( AskCode email "" (Just why), Effect.none )

                _ ->
                    ( model, Effect.none )


view : Model -> View Msg
view model =
    case model of
        AskEmail draft maybeErr ->
            navigationStack [ navigationTitle "Sign in" ]
                [ form
                    [ section []
                        [ text "Enter your email and we'll send a one-time code."
                        , textField [ email, submit Submitted, width (chars 30) ]
                            "Email" draft DraftChanged
                        , errorView maybeErr
                        ]
                    , section []
                        [ button [] Submitted "Send me a code" ]
                    ]
                ]

        AskCode emailAddr draft maybeErr ->
            navigationStack [ navigationTitle "Enter your code" ]
                [ form
                    [ section []
                        [ text ("We sent a 6-digit code to " ++ emailAddr ++ ".")
                        , textField [ numericCode, submit Submitted ]
                            "Code" draft DraftChanged
                        , errorView maybeErr
                        ]
                    , section []
                        [ button [] Submitted "Verify" ]
                    ]
                ]

        Submitting _ ->
            navigationStack [ navigationTitle "Sign in" ]
                [ form
                    [ section []
                        [ text "Working…" ]
                    ]
                ]


-- Renders the error if present, otherwise an empty no-op view so the
-- layout doesn't jump between success and failure cases.
errorView : Maybe String -> View Msg
errorView maybeErr =
    case maybeErr of
        Nothing ->
            empty

        Just msg ->
            text msg


page : Page
page =
    Page.create
        { path = "/sign-in"
        , title = "Sign in"
        , init = init
        , update = update
        , view = view
        }
`
	files["Frontend/Home.mar"] = fmt.Sprintf(`module Frontend.Home exposing (page)


import Shared
import UI exposing
    ( navigationStack, navigationTitle, topBarTrailing
    , list, section, header
    , hstack
    , text, textField, button, errorText
    , submit, disabled
    )


-- Protected page. Page.protected runs Auth.me on entry. If no
-- session, redirects to the signInPage from Auth.config. If logged
-- in, threads the User into init/update/view as the first arg.


-- Three-state machine for the entries list. The bare List Entry
-- would force init to start with [] (rendering "0 entries" while
-- the fetch is in flight); the explicit states keep the UI honest.
type Entries
    = Loading
    | Loaded (List Shared.Entry)
    | Failed String


type alias Model =
    { entries : Entries
    , draft   : String
    }


type Msg
    = EntriesLoaded (Result String (List Shared.Entry))
    | DraftChanged String
    | AddClicked
    | EntryCreated (Result String Shared.Entry)
    | SignOutClicked
    | SignedOut (Result String ())


init : Shared.User -> (Model, Effect Msg)
init =
    ( { entries = Loading, draft = "" }
    , Service.call Shared.listMine () EntriesLoaded
    )


update : Shared.User -> Msg -> Model -> (Model, Effect Msg)
update _ msg model =
    case msg of
        EntriesLoaded (Ok loaded) ->
            ( { model | entries = Loaded loaded }, Effect.none )

        EntriesLoaded (Err why) ->
            ( { model | entries = Failed why }, Effect.none )

        DraftChanged value ->
            ( { model | draft = value }, Effect.none )

        AddClicked ->
            if String.trim model.draft == "" then
                ( model, Effect.none )
            else
                ( model
                , Service.call Shared.createMine { body = model.draft } EntryCreated
                )

        EntryCreated (Ok _) ->
            ( { model | draft = "" }
            , Service.call Shared.listMine () EntriesLoaded
            )

        EntryCreated (Err _) ->
            ( model, Effect.none )

        SignOutClicked ->
            ( model, Auth.logout SignedOut )

        SignedOut _ ->
            ( { entries = Loading, draft = "" }
            , Nav.replace "/sign-in"
            )


view : Shared.User -> Model -> View Msg
view user model =
    navigationStack
        [ navigationTitle ("Hi, " ++ user.email)
        , topBarTrailing (button [] SignOutClicked "Sign out")
        ]
        [ list []
            [ section []
                [ hstack []
                    [ textField [ submit AddClicked ]
                        "New entry" model.draft DraftChanged
                    , button
                        [ disabled (String.trim model.draft == "") ]
                        AddClicked "Add"
                    ]
                ]
            , entriesSection model.entries
            ]
        ]


entriesSection : Entries -> View Msg
entriesSection state =
    case state of
        Loading ->
            section [] [ text "Loading…" ]

        Failed why ->
            section [] [ errorText ("Couldn't load entries: " ++ why) ]

        Loaded items ->
            section [ header (String.fromInt (List.length items) ++ " entries") ]
                (List.map renderEntry (List.reverse items))


renderEntry : Shared.Entry -> View Msg
renderEntry entry =
    text entry.body


page : Page
page =
    Page.protected
        { path = "/"
        , title = "%s"
        , init = init
        , update = update
        , view = view
        }
`, name)
	return files
}
