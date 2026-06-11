# Managed Effects

This document describes the architectural model for Mar applications.

The goal is to make it accurate to say:

> Mar uses a managed side-effect system.

More precisely:

- backend reads and writes are explicit, typed services
- frontend screens follow a Model-View-Update architecture
- `view` stays pure
- side effects are explicit values returned by `init` and `update`
- only the runtime is allowed to execute those effects

For full API reference, see [mar.md](./mar.md). This document focuses on the **why**.

## Core idea

Mar separates backend and frontend cleanly while keeping them in the same language and codebase.

**Backend** defines:

- entities (data shape)
- queries (read-only descriptions)
- endpoints (typed contracts for HTTP routes)
- handlers (effectful functions invoked by the runtime)

**Frontend** defines:

- screens (MVU programs)
- views (pure descriptions)
- messages (state transitions)
- effects (descriptions of work for the runtime)

The boundary is clear:

- backend exposes typed capabilities through `Endpoint` values
- frontend invokes those endpoints through `Endpoint.call`, getting back `Effect` descriptions
- the runtime executes effects and feeds results back into `update`

## Effects as values

Mar treats effects as **data**, not as arbitrary code execution.

This means:

- `view` cannot perform effects — it returns `View Msg`, a pure description
- helper functions remain pure
- `init` may return an initial `Effect Never Msg`
- `update` may return effects for the runtime to execute
- effects belong to a closed, runtime-managed set

The single effect type is:

```elm
Effect a
```

One type parameter, like Elm's `Cmd`. For frontend MVU the runtime expects
`Effect Msg` — an effect that produces a `Msg`. Effects don't carry an error
type: failures are values, encoded into `Msg` variants (a `Result` in the
payload), never a bypass of the message stream.

The minimum effect family in `Navigation`:

```elm
Navigation.push : NavigateTarget -> Effect Msg
Navigation.back : Effect Msg
```

And for backend calls from the frontend (the failure rides in the `Result` the
`toMsg` receives, as a `Service.Error` union):

```elm
Service.call : Service req resp -> req -> (Result Service.Error resp -> msg) -> Effect msg
Effect.batch : List (Effect msg) -> Effect msg
Effect.none  : Effect msg
```

Example:

```elm
init : (Model, Effect Never Msg)
init =
    ( { profile = Nothing, loading = True }
    , Endpoint.call loadProfile ()
        |> Effect.toMsg ProfileLoaded
    )
```

```elm
update : Msg -> Model -> (Model, Effect Never Msg)
update msg model =
    case msg of
        RetryClicked ->
            ( { model | loading = True }
            , Endpoint.call loadProfile ()
                |> Effect.toMsg ProfileLoaded
            )

        CloseClicked ->
            ( model, Navigation.back )
```

The key claim:

- application code **describes** effects
- application code does not **execute** effects directly

## Backend services

Backend access is explicit and typed.

**Queries** are read-only descriptions:

```elm
timelinePostsQuery : Query Post
timelinePostsQuery =
    Db.from posts
        |> Db.where (Db.eq .published True)
        |> Db.orderBy .createdAt Desc
        |> Db.limit 20
```

A query is a value. Constructing one performs no I/O. The runtime executes it via `Db.list`, `Db.first`, etc.

**Endpoints** are typed contracts:

```elm
followUser : Endpoint.Post FollowInput Follow FollowField
followUser =
    Endpoint.post "/follows"
        |> Endpoint.validateWithUser validateFollow
```

The endpoint declares: HTTP method, path, input type, output type, and validation tag. Both backend (`Endpoint.implement`) and frontend (`Endpoint.call`) reference the same value, so contract changes break both sides at compile time.

**Handlers** are effectful functions invoked by the runtime:

```elm
createFollow : FollowInput -> User -> Effect (ResponseError FollowField) Follow
createFollow input user =
    Db.insert follows { follower = user.id, followed = input.followed }
```

The handler is pure to call (returns a description). The runtime instantiates it on each request and executes the resulting effect.

Frontend code calls backend capabilities through `Endpoint.call`, never by embedding ad hoc database behavior in the view.

## No UI CRUD sugar

Mar does not provide special screen items like `create`, `edit`, or `delete` shortcuts.

Such forms hide too much:

- the label shown to the user
- the message emitted by the interaction
- the state transition
- the effect being requested
- the success and failure paths

That works against a clean MVU story.

Instead, application UI is built from explicit elements:

- `button`
- `field` (with `multiline` for textareas)
- `list`
- `row`, `column`, `section`
- typed inputs via `field` attributes (`onChange`, `value`, `disabled`, etc.)

The flow is uniform:

1. user interacts with the view
2. the view emits a `Msg`
3. `update` returns a new model plus effects
4. the runtime executes those effects
5. the runtime sends a new `Msg` with the result

This makes "create post", "follow", "like", "save profile", and "load more" all the same kind of thing.

## Entity, endpoint, route

Mar keeps the data layer and the HTTP layer separate. An entity is only the data schema; endpoints are explicit typed contracts; handlers are explicit effectful functions; routes are explicit lists, organized by access policy. Every endpoint is written by hand — the cost is more code, the benefit is no hidden behavior.

## Shape of a screen

A screen is a pure state machine plus a pure view.

```elm
module Screens.Timeline exposing (..)

import Endpoint
import Effect exposing (Effect)
import Navigation
import View exposing (..)
import Posts exposing (Post, PostInput, PostField, timelinePosts, publishPost)
import Screen exposing (Screen)
import Screens.PostDetail


-- MODEL

type alias Model =
    { posts : List Post
    , body : String
    , submitting : Bool
    , error : Maybe String
    , fieldErrors : List (FieldError PostField)
    }


-- MSG

type Msg
    = TimelineLoaded (Result (ResponseError ()) (List Post))
    | BodyChanged String
    | SubmitClicked
    | Published (Result (ResponseError PostField) Post)


-- INIT

init : (Model, Effect Never Msg)
init =
    ( { posts = [], body = "", submitting = False, error = Nothing, fieldErrors = [] }
    , Endpoint.call timelinePosts ()
        |> Effect.toMsg TimelineLoaded
    )


-- UPDATE

update : Msg -> Model -> (Model, Effect Never Msg)
update msg model =
    case msg of
        TimelineLoaded (Ok posts) ->
            ( { model | posts = posts }, Effect.none )

        TimelineLoaded (Err err) ->
            ( { model | error = Just (errorToString err) }, Effect.none )

        BodyChanged value ->
            ( { model | body = value, fieldErrors = [] }, Effect.none )

        SubmitClicked ->
            ( { model | submitting = True }
            , Endpoint.call publishPost () { body = model.body }
                |> Effect.toMsg Published
            )

        Published (Ok _) ->
            ( { model | body = "", submitting = False }
            , Endpoint.call timelinePosts ()
                |> Effect.toMsg TimelineLoaded
            )

        Published (Err (Validation errors)) ->
            ( { model | submitting = False, fieldErrors = errors }, Effect.none )

        Published (Err err) ->
            ( { model | submitting = False, error = Just (errorToString err) }, Effect.none )


-- VIEW

view : Model -> View Msg
view model =
    section []
        [ title "Timeline"
        , field
            [ label "What's happening?"
            , multiline True
            , value model.body
            , onChange BodyChanged
            , disabled model.submitting
            , errorMessage (FieldError.firstFor Posts.Body model.fieldErrors)
            ]
        , button
            [ onClick SubmitClicked
            , disabled (model.submitting || String.isEmpty model.body)
            , intent Primary
            ]
            [ text (if model.submitting then "Posting…" else "Post") ]
        , list (List.map postRow model.posts)
        ]


postRow : Post -> View Msg
postRow post =
    row [ Navigation.target (Screens.PostDetail.to post.id) ]
        [ text post.body ]


-- SCREEN

screen : Screen () Model Msg
screen =
    Screen.with
        { path = "/timeline"
        , init = init
        , update = update
        , view = view
        }

to : NavigateTarget
to = Screen.to screen
```

Important properties:

- `Msg` is explicit and typed
- `Model` shape is explicit
- `update` is exhaustive (compiler-enforced)
- effects are explicit in the return value `(Model, Effect Never Msg)`
- `view` is pure and only describes elements

## Creating and editing without sugar

Create and edit flows are explicit screens — there is no `create` shortcut that hides the state machine. The "compose post" example below shows the full shape:

```elm
module Screens.ComposePost exposing (..)

-- ... imports

type alias Model =
    { body : String
    , submitting : Bool
    , fieldErrors : List (FieldError PostField)
    , error : Maybe String
    }

type Msg
    = BodyChanged String
    | SubmitClicked
    | Submitted (Result (ResponseError PostField) Post)
    | CancelClicked

init : (Model, Effect Never Msg)
init =
    ( { body = "", submitting = False, fieldErrors = [], error = Nothing }
    , Effect.none
    )

update : Msg -> Model -> (Model, Effect Never Msg)
update msg model =
    case msg of
        BodyChanged text ->
            ( { model | body = text, fieldErrors = [] }, Effect.none )

        SubmitClicked ->
            ( { model | submitting = True }
            , Endpoint.call publishPost () { body = model.body }
                |> Effect.toMsg Submitted
            )

        Submitted (Ok post) ->
            ( model, Navigation.push (Screens.PostDetail.to post.id) )

        Submitted (Err (Validation errors)) ->
            ( { model | submitting = False, fieldErrors = errors }, Effect.none )

        Submitted (Err err) ->
            ( { model | submitting = False, error = Just (errorToString err) }, Effect.none )

        CancelClicked ->
            ( model, Navigation.back )

view : Model -> View Msg
view model =
    section []
        [ title "New post"
        , field
            [ label "What's happening?"
            , multiline True
            , value model.body
            , onChange BodyChanged
            , errorMessage (FieldError.firstFor Posts.Body model.fieldErrors)
            ]
        , button [ onClick SubmitClicked, intent Primary ] [ text "Post" ]
        , button [ onClick CancelClicked, intent Subtle ] [ text "Cancel" ]
        ]
```

The important point is not the exact input widget syntax. The important point is the architecture:

- no hidden create behavior in the view
- no hidden label generation
- no hidden success/failure handling
- everything passes through the same MVU pipeline

## Effect contexts

The compiler distinguishes two contexts:

| Context | Can call |
|---|---|
| Pure (top-level definitions, helpers, view) | only pure functions, including constructing `Query` and `Endpoint` values |
| Effectful (handlers, `init`, `update`) | pure + `Db.*`, `Endpoint.call`, `Navigation.*`, etc. |

A `Query` is a description; constructing it is pure. `Db.list query` returns an `Effect` — invoking it is effectful, and only allowed in handler context.

This means top-level reusable queries are safe:

```elm
-- Top-level, pure:
recentPostsQuery : Query Post
recentPostsQuery =
    Db.from posts
        |> Db.where (Db.gte .createdAt yesterday)
        |> Db.orderBy .createdAt Desc
        |> Db.limit 20

-- Handler, effectful:
listRecentPosts : Effect (ResponseError ()) (List Post)
listRecentPosts =
    Db.list recentPostsQuery
```

## Language summary

The target language story:

- Mar backend defines entities, queries, endpoints, and handlers
- Mar frontend is a collection of independent MVU screens
- all end-user interactions happen through explicit messages
- all side effects are managed by the runtime
- purity is the default, effects are explicit values
- the type system tracks which functions are effectful

This is the model Mar is built around.
