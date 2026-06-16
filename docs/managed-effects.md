# Managed Effects

This document describes the architectural model for Mar applications.

The goal is to make it accurate to say:

> Mar uses a managed side-effect system.

More precisely:

- backend reads and writes go through explicit, typed services
- frontend pages follow a Model-View-Update architecture
- `view` stays pure
- side effects are explicit values returned by `init` and `update`
- only the runtime is allowed to execute those effects

For the full API reference, see [mar.md](./mar.md). This document focuses on the **why**.

## Core idea

Mar separates backend and frontend cleanly while keeping them in the same language and codebase.

**Backend** defines:

- entities (the data shape)
- services (typed request and response contracts)
- handlers (effectful functions the runtime invokes per request)

**Frontend** defines:

- pages (MVU programs)
- views (pure descriptions)
- messages (state transitions)
- effects (descriptions of work for the runtime)

The boundary is clear:

- backend exposes typed capabilities as `Service` values
- frontend invokes them through `Service.call`, getting back `Effect` descriptions
- the runtime executes effects and feeds results back into `update`

## Effects as values

Mar treats effects as **data**, not as arbitrary code execution.

This means:

- `view` cannot perform effects: it returns `View Msg`, a pure description
- helper functions stay pure
- `init` returns an initial `Effect Msg`
- `update` returns effects for the runtime to execute
- effects belong to a closed, runtime-managed set

The single effect type is:

```elm
Effect a
```

One type parameter, like Elm's `Cmd`. For frontend MVU the runtime expects `Effect Msg`, an effect that produces a `Msg`. Effects carry no error type: failures are values, encoded into `Msg` variants (a `Result` in the payload), never a bypass of the message stream.

Navigation effects:

```elm
Nav.pushTo    : route -> Effect msg
Nav.replaceTo : route -> Effect msg
```

Backend calls from the frontend (the failure rides in the `Result` the `toMsg` receives, as a `Service.Error` union):

```elm
Service.call : Service req resp -> req -> (Result Service.Error resp -> msg) -> Effect msg
Effect.batch : List (Effect msg) -> Effect msg
Effect.none  : Effect msg
```

Example:

```elm
init : Shared.User -> (Model, Effect Msg)
init user =
    ( { tasks = [], loading = True }
    , Service.call Shared.listTasks () TasksLoaded
    )
```

```elm
update : Msg -> Model -> (Model, Effect Msg)
update msg model =
    case msg of
        RetryClicked ->
            ( { model | loading = True }
            , Service.call Shared.listTasks () TasksLoaded
            )

        BackClicked ->
            ( model, Nav.replaceTo Frontend.Routes.home )
```

The key claim:

- application code **describes** effects
- application code does not **execute** effects directly

## Backend services

Backend access is explicit and typed. A `Service req resp` is the contract; a handler turns a request into an effect that produces the response.

**Contracts** are declared once, in the shared module, each with a verb and a path:

```elm
addTask : Service { name : String } AddTaskOutcome
addTask = Service.declare POST "/tasks"
```

The contract names the request and response types, and `Service.declare VERB "/path"` fixes the HTTP method and route (a path may carry typed `{name:Type}` params naming fields of the request). Both backend (`Service.implement`, or `Auth.protect` when the call needs a signed-in user) and frontend (`Service.call`) reference the same value, so a contract change breaks both sides at compile time. The verb and path are transparent to the caller: `Service.call` is identical whatever method a service uses.

**Handlers** are effectful functions the runtime invokes per request:

```elm
addTaskImpl : { name : String } -> User -> Effect AddTaskOutcome
addTaskImpl input user =
    if String.trim input.name == "" then
        Effect.succeed NameEmpty
    else
        Repo.create tasks { name = input.name, done = False, userId = user.id }
            |> Effect.map Added
```

The handler is pure to call: it returns a description. The runtime executes the resulting effect, reading and writing rows through `Repo.*`. A handler returns its declared response, so the outcomes it can produce are fixed by the type and the frontend's `case` over them is checked for exhaustiveness. There is no separate query language: composition is ordinary Mar over the rows `Repo.*` returns.

The verb a service was declared with constrains its handler: a `GET` is read-only, and the compiler rejects one whose handler reaches `Repo.create`, `Repo.update`, or `Repo.deleteById`. A call that mutates is declared `POST`, `PUT`, `PATCH`, or `DELETE` (`addTask` above is a `POST`, so it may write).

Frontend code reaches backend capabilities only through `Service.call`, never by embedding database behavior in a view.

## No UI CRUD sugar

Mar does not provide special view items like `create`, `edit`, or `delete` shortcuts.

Such forms hide too much:

- the label shown to the user
- the message emitted by the interaction
- the state transition
- the effect being requested
- the success and failure paths

That works against a clean MVU story.

Instead, UI is built from explicit elements: `button`, `textField`, `list`, `row` / `column` / `section`, `toggle`, and the rest of the view vocabulary (see [mar.md](./mar.md) section 5.3).

The flow is uniform:

1. the user interacts with the view
2. the view emits a `Msg`
3. `update` returns a new model plus effects
4. the runtime executes those effects
5. the runtime sends a new `Msg` with the result

This makes "add task", "toggle", "delete", and "reload" all the same kind of thing.

## Shape of a page

A page is a pure state machine plus a pure view.

```elm
module Frontend.Home exposing (page)

import Shared


-- MODEL

type alias Model =
    { tasks : List Shared.Task
    , draft : String
    , error : Maybe String
    }


-- MSG

type Msg
    = TasksLoaded (Result Service.Error (List Shared.Task))
    | DraftChanged String
    | AddClicked
    | Added (Result Service.Error Shared.AddTaskOutcome)


-- INIT

init : Shared.User -> (Model, Effect Msg)
init user =
    ( { tasks = [], draft = "", error = Nothing }
    , Service.call Shared.listTasks () TasksLoaded
    )


-- UPDATE

update : Msg -> Model -> (Model, Effect Msg)
update msg model =
    case msg of
        TasksLoaded (Ok tasks) ->
            ( { model | tasks = tasks }, Effect.none )

        TasksLoaded (Err why) ->
            ( { model | error = Just (Service.errorToString why) }, Effect.none )

        DraftChanged value ->
            ( { model | draft = value }, Effect.none )

        AddClicked ->
            ( model, Service.call Shared.addTask { name = model.draft } Added )

        Added (Ok (Shared.Added task)) ->
            ( { model | tasks = model.tasks ++ [ task ], draft = "" }, Effect.none )

        Added (Ok Shared.NameEmpty) ->
            ( { model | error = Just "Name can't be empty." }, Effect.none )

        Added (Err why) ->
            ( { model | error = Just (Service.errorToString why) }, Effect.none )


-- VIEW

view : Model -> View Msg
view model =
    navigationStack [ navigationTitle "Today" ]
        [ form
            [ section []
                [ textField [] "New task" model.draft DraftChanged
                , button [] AddClicked "Add"
                ]
            , section [] (List.map taskRow model.tasks)
            ]
        ]


-- PAGE

page : Page
page =
    Page.protected
        { path   = "/"
        , title  = "Today"
        , init   = init
        , update = update
        , view   = view
        }
```

Important properties:

- `Msg` is explicit and typed
- the `Model` shape is explicit
- `update` is exhaustive (compiler-enforced)
- effects are explicit in the return value `(Model, Effect Msg)`
- `view` is pure and only describes elements

Create and edit flows are just pages with their own `Msg` and `update`: there is no `create` shortcut that hides the state machine. Both the domain outcome (`Shared.Added` / `Shared.NameEmpty`) and the transport failure (`Service.Error`) arrive as ordinary values the page matches on.

## Effect contexts

The compiler distinguishes two contexts:

| Context | Can call |
|---|---|
| Pure (top-level definitions, helpers, `view`) | only pure functions, including declaring `Service` contracts and building entities |
| Effectful (handlers, `init`, `update`) | pure functions plus `Repo.*`, `Service.call`, `Nav.*`, and the rest of the `Effect` API |

A handler returns an `Effect`: building it is a description, and only the runtime executes it. Because `view` and helpers stay pure, the only place work happens is the effects that `init`, `update`, and handlers hand back to the runtime.

## Language summary

The language story:

- Mar backend defines entities, service contracts, and handlers
- Mar frontend is a collection of independent MVU pages
- all end-user interactions happen through explicit messages
- all side effects are managed by the runtime
- purity is the default, effects are explicit values
- the type system tracks which functions are effectful

This is the model Mar is built around.
