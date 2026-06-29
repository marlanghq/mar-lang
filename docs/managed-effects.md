# Managed Effects

This document describes the architectural model for Mar applications.

The goal is to make it accurate to say:

> Mar uses a managed side-effect system.

More precisely:

- backend reads and writes go through explicit, typed services
- frontend pages follow a Model-View-Update architecture
- `view` stays pure
- side effects are explicit values: backend work is a `Task`, frontend commands are a `Cmd`
- only the runtime is allowed to run those values

For the full API reference, see [mar.md](./mar.md). This document focuses on the **why**.

## Core idea

Mar separates backend and frontend cleanly while keeping them in the same language and codebase.

**Backend** defines:

- entities (the data shape)
- services (typed request and response contracts)
- handlers (functions the runtime invokes per request, each returning a `Task`)

**Frontend** defines:

- pages (MVU programs)
- views (pure descriptions)
- messages (state transitions)
- commands (descriptions of work for the runtime)

The boundary is clear:

- backend exposes typed capabilities as `Service` values, implemented by handlers that return a `Task`
- frontend invokes them through `Service.call`, getting back a `Cmd` that dispatches a `Msg`
- the runtime runs the command and feeds the result back into `update`

## Two algebras: Task and Cmd

Mar treats side effects as **data**, not as arbitrary code execution. Two types carry that data, one per side of the app:

- **`Task a`** is the backend's value-monad: a computation that, when run, produces an `a` (or aborts). You chain Tasks to compute a value: read a row, then read another, then return a response. `Repo.*` return `Task`, `Time.now : Task Time`, and a service handler returns `Task resp`.
- **`Cmd msg`** is the frontend's message-monoid (Mar's `Cmd`, like Elm's): a description of work the MVU runtime should do, whose result comes back as a `Msg`. `init` and `update` return `(Model, Cmd Msg)`; `Service.call` and `Nav.*` return `Cmd msg`.
- **`Sub msg`** is the frontend's *subscription* type: a standing declaration of what the runtime should listen to over time (a timer today; a frame loop later), reconciled against the model after every update. A page declares `subscriptions : Model -> Sub Msg`. See [Subscriptions](#subscriptions-sub).

They used to be one type. `Effect a` carried both algebras at once, a value-monad *and* a message-monoid, and the overlap let a real bug compile: a value-producing effect like `Time.now` returned from a frontend `update` silently did nothing, because the loop only knew how to deliver messages, not values. Splitting them makes that impossible: `Task` is a value and `Cmd` is a message, so you cannot return a `Task` where a `Cmd Msg` is expected. The compiler rejects it.

This means:

- `view` cannot perform effects: it returns `View Msg`, a pure description
- helper functions stay pure
- `init` returns an initial `(Model, Cmd Msg)`
- `update` returns commands for the runtime to run
- both `Task` and `Cmd` belong to a closed, runtime-managed set

Neither type carries an error index. On the backend a `Task` aborts with `Task.fail`. On the frontend failures are values, encoded into `Msg` variants (a `Result` in the payload), never a bypass of the message stream.

Frontend commands. `Service.call` and `Nav.*` build a `Cmd`; the failure of a call rides in the `Result` the `toMsg` receives, as a `Service.Error` union:

```elm
Service.call : Service req resp -> req -> (Result Service.Error resp -> msg) -> Cmd msg
Nav.pushTo   : route -> Cmd msg
Nav.replaceTo : route -> Cmd msg
Cmd.batch    : List (Cmd msg) -> Cmd msg
Cmd.none     : Cmd msg
```

### Cmd.perform: the Task to Cmd bridge

A `Task` produces a value; the frontend loop only consumes messages. `Cmd.perform` bridges the two, exactly like Elm's `Task.perform`:

```elm
Cmd.perform : (a -> msg) -> Task a -> Cmd msg
```

It runs the `Task` and delivers the produced value to `update` as a `Msg`. This is the only way a Task's result reaches the frontend. For example, `Time.now : Task Time` is the same name on both sides; the frontend reaches the loop with `Cmd.perform GotNow Time.now`.

Example:

```elm
init : Shared.User -> (Model, Cmd Msg)
init user =
    ( { tasks = [], loading = True }
    , Service.call Shared.listTasks () TasksLoaded
    )
```

```elm
update : Msg -> Model -> (Model, Cmd Msg)
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

- application code **describes** work, as a `Task` or a `Cmd`
- application code does not **run** it directly

## Subscriptions: Sub

`Cmd` is a one-shot — the runtime runs it once and the result comes back as a `Msg`. Some inputs aren't one-shot: a clock that ticks every second, polling, a countdown. Those are **subscriptions** — standing declarations of what the runtime should listen to, for as long as the model says so.

A page declares them in a `subscriptions` field, a pure function of the model:

```elm
subscriptions : Model -> Sub Msg
```

The runtime re-evaluates it after every `update` and reconciles: a source newly returned is started, a source no longer returned is stopped, a survivor keeps running. So you turn a subscription off by returning a model where `subscriptions` no longer lists it, and leaving a page stops its subscriptions for free. Identity is structural — the *data* of the subscription, never the tagger function — so two `Time.every` at the same interval share one timer.

Like `Cmd`, `Sub` is frontend-only, and it is its own algebra: there is no bridge from `Task`.

```elm
Time.every : Duration -> (Time -> msg) -> Sub msg
Sub.batch  : List (Sub msg) -> Sub msg
Sub.none   : Sub msg
```

`Time.every` is the v1 source: it fires every `Duration` and hands the tagger the current `Time`. Like Elm's `Time.every`, the first tick is *after* one interval, not at zero — seed the immediate value in `init` with `Cmd.perform`:

```elm
type Msg = Tick Time

init : (Model, Cmd Msg)
init =
    ( { now = Nothing }, Cmd.perform Tick Time.now )

subscriptions : Model -> Sub Msg
subscriptions model =
    Time.every (Time.seconds 1) Tick
```

A page that subscribes to nothing returns `Sub.none` — the common case, so most pages write `subscriptions = \_ -> Sub.none` (and protected/dynamic pages thread the same leading args as `view`, e.g. `\_ _ -> Sub.none`).

## Backend services

Backend access is explicit and typed. A `Service req resp` is the contract; a handler turns a request into a `Task` that produces the response.

**Contracts** are declared once, in the shared module, each with a verb and a path:

```elm
addTask : Service { name : String } AddTaskOutcome
addTask = Service.declare POST "/tasks"
```

The contract names the request and response types, and `Service.declare VERB "/path"` fixes the HTTP method and route (a path may carry typed `{name:Type}` params naming fields of the request). Both backend (`Service.implement`, or `Auth.protect` when the call needs a signed-in user) and frontend (`Service.call`) reference the same value, so a contract change breaks both sides at compile time. The verb and path are transparent to the caller: `Service.call` is identical whatever method a service uses.

**Handlers** are functions the runtime invokes per request; each returns a `Task`:

```elm
addTaskImpl : { name : String } -> User -> Task AddTaskOutcome
addTaskImpl input user =
    if String.trim input.name == "" then
        Task.succeed NameEmpty
    else
        Repo.create tasks { name = input.name, done = False, userId = user.id }
            |> Task.map Added
```

The handler is pure to call: it returns a description. The runtime runs the resulting `Task`, reading and writing rows through `Repo.*`. A handler returns its declared response, so the outcomes it can produce are fixed by the type and the frontend's `case` over them is checked for exhaustiveness. There is no separate query language: composition is ordinary Mar over the rows `Repo.*` returns.

The verb a service was declared with constrains its handler: a `GET` is read-only, and the compiler rejects one whose handler reaches `Repo.create`, `Repo.update`, or `Repo.deleteById`. A call that mutates is declared `POST`, `PUT`, `PATCH`, or `DELETE` (`addTask` above is a `POST`, so it may write).

Frontend code reaches backend capabilities only through `Service.call`, never by embedding database behavior in a view.

## No UI CRUD sugar

Mar does not provide special view items like `create`, `edit`, or `delete` shortcuts.

Such forms hide too much:

- the label shown to the user
- the message emitted by the interaction
- the state transition
- the command being requested
- the success and failure paths

That works against a clean MVU story.

Instead, UI is built from explicit elements: `button`, `textField`, `list`, `row` / `column` / `section`, `toggle`, and the rest of the view vocabulary (see [mar.md](./mar.md) section 5.3).

The flow is uniform:

1. the user interacts with the view
2. the view emits a `Msg`
3. `update` returns a new model plus a `Cmd`
4. the runtime runs that command
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

init : Shared.User -> (Model, Cmd Msg)
init user =
    ( { tasks = [], draft = "", error = Nothing }
    , Service.call Shared.listTasks () TasksLoaded
    )


-- UPDATE

update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
    case msg of
        TasksLoaded (Ok tasks) ->
            ( { model | tasks = tasks }, Cmd.none )

        TasksLoaded (Err why) ->
            ( { model | error = Just (Service.errorToString why) }, Cmd.none )

        DraftChanged value ->
            ( { model | draft = value }, Cmd.none )

        AddClicked ->
            ( model, Service.call Shared.addTask { name = model.draft } Added )

        Added (Ok (Shared.Added task)) ->
            ( { model | tasks = model.tasks ++ [ task ], draft = "" }, Cmd.none )

        Added (Ok Shared.NameEmpty) ->
            ( { model | error = Just "Name can't be empty." }, Cmd.none )

        Added (Err why) ->
            ( { model | error = Just (Service.errorToString why) }, Cmd.none )


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
        , subscriptions = \_ _ -> Sub.none
        }
```

Important properties:

- `Msg` is explicit and typed
- the `Model` shape is explicit
- `update` is exhaustive (compiler-enforced)
- commands are explicit in the return value `(Model, Cmd Msg)`
- `view` is pure and only describes elements

Create and edit flows are just pages with their own `Msg` and `update`: there is no `create` shortcut that hides the state machine. Both the domain outcome (`Shared.Added` / `Shared.NameEmpty`) and the transport failure (`Service.Error`) arrive as ordinary values the page matches on.

## Effect contexts

The compiler distinguishes two contexts:

| Context | Can call |
|---|---|
| Pure (top-level definitions, helpers, `view`) | only pure functions, including declaring `Service` contracts and building entities |
| Effectful (handlers, `init`, `update`) | pure functions plus `Repo.*`, the rest of the `Task` API (backend), and `Service.call`, `Nav.*`, `Cmd.*` (frontend) |

A handler returns a `Task`, and `init` / `update` return a `Cmd`: building either is a description, and only the runtime runs it. Because `view` and helpers stay pure, the only place work happens is the `Task` and `Cmd` values that `init`, `update`, and handlers hand back to the runtime.

## Language summary

The language story:

- Mar backend defines entities, service contracts, and handlers
- Mar frontend is a collection of independent MVU pages
- all end-user interactions happen through explicit messages
- all side effects are managed by the runtime
- purity is the default; backend work is a `Task` value, frontend commands are a `Cmd` value, and recurring inputs (timers) are a `Sub` value
- the type system tracks which functions are effectful, and keeps backend `Task`, frontend `Cmd`, and `Sub` distinct

This is the model Mar is built around.
