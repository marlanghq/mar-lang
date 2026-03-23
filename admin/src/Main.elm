port module Main exposing (main)

import Browser
import Browser.Events
import Browser.Navigation as Nav
import Dict exposing (Dict)
import Element exposing (Element, alignLeft, centerX, centerY, column, el, fill, fillPortion, height, htmlAttribute, inFront, maximum, minimum, none, padding, paddingEach, paragraph, px, rgb255, rgba255, row, scrollbarY, spacing, text, width, wrappedRow)
import Element.Background as Background
import Element.Border as Border
import Element.Font as Font
import Element.Input as Input
import Html.Attributes as HtmlAttr
import Html.Events as HtmlEvents
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Mar.Api exposing (ActionInfo, AuthInfo, Entity, Field, FieldType(..), InputAliasField, InputAliasInfo, Row, Schema, decodeRows, decodeSchema, encodePayload, fieldTypeLabel, rowDecoder, valueToDisplayString, valueToString)
import Url exposing (Url)


type alias Flags =
    { apiBase : String
    , authToken : String
    , systemAuthToken : String
    , viewportWidth : Int
    }


port saveSession : Encode.Value -> Cmd msg


type Remote a
    = NotAsked
    | Loading
    | Loaded a
    | Failed String


type FormMode
    = FormHidden
    | FormCreate
    | FormEdit Row


type BooleanFieldState
    = BooleanUnset
    | BooleanFalse
    | BooleanTrue


type AuthScope
    = AppAuthScope


type AuthStage
    = AuthStageEmail
    | AuthStageCode
    | AuthStageSession


type AuthSubmitState
    = AuthSubmitSendingCode
    | AuthSubmitSigningIn


type WorkspaceMode
    = AppWorkspace
    | AdminWorkspace


type Route
    = RouteDefault WorkspaceMode
    | RouteAuthTools WorkspaceMode
    | RouteEntity WorkspaceMode String
    | RouteEntityCreate WorkspaceMode String
    | RouteEntityDetail WorkspaceMode String String
    | RouteEntityEdit WorkspaceMode String String
    | RouteAction WorkspaceMode String
    | RoutePerformance
    | RouteRequestLogs
    | RouteDatabase


type alias Model =
    { apiBase : String
    , navKey : Nav.Key
    , currentUrl : Url
    , currentRoute : Route
    , authToken : String
    , systemAuthToken : String
    , currentEmail : Maybe String
    , currentRole : Maybe String
    , currentSystemEmail : Maybe String
    , currentSystemRole : Maybe String
    , authEmail : String
    , authCode : String
    , authStage : AuthStage
    , authSubmitting : Maybe AuthSubmitState
    , sessionRestorePending : Bool
    , firstAdminCodeRequested : Bool
    , authToolsOpen : Bool
    , workspace : WorkspaceMode
    , schema : Remote Schema
    , selectedEntity : Maybe Entity
    , selectedAction : Maybe ActionInfo
    , rows : Remote (List Row)
    , selectedRow : Maybe Row
    , formMode : FormMode
    , formValues : Dict String String
    , actionFormValues : Dict String String
    , actionResult : Maybe Row
    , perf : Remote PerfPayload
    , adminVersion : Remote AdminVersionPayload
    , monitoringVersionDetailsOpen : Bool
    , requestLogs : Remote RequestLogsPayload
    , backups : Remote BackupsPayload
    , performanceMode : Bool
    , requestLogsMode : Bool
    , databaseMode : Bool
    , lastBackup : Maybe BackupResponse
    , pendingDelete : Maybe PendingDelete
    , authInlineMessage : Maybe String
    , flash : Maybe String
    , viewportWidth : Int
    , mobileSidebarOpen : Bool
    , keepMobileSidebarOpenOnNextRoute : Bool
    }


type alias PendingDelete =
    { entity : Entity
    , idValue : String
    , message : String
    }


type alias MobileNavEntry =
    { label : String
    , onPress : Msg
    , selected : Bool
    }


type Msg
    = UrlRequested Browser.UrlRequest
    | UrlChanged Url
    | GotSchema (Result ApiHttpError Schema)
    | SelectEntity String
    | SelectAction String
    | ReloadRows
    | GotRows (Result ApiHttpError (List Row))
    | SelectPerformance
    | SelectRequestLogs
    | SelectDatabase
    | ReloadDatabase
    | ReloadPerformance
    | ToggleMonitoringVersionDetails
    | ReloadRequestLogs
    | GotPerformance (Result ApiHttpError PerfPayload)
    | GotAdminVersion (Result ApiHttpError AdminVersionPayload)
    | GotRequestLogs (Result ApiHttpError RequestLogsPayload)
    | GotBackups (Result ApiHttpError BackupsPayload)
    | TriggerBackup
    | GotBackup (Result ApiHttpError BackupResponse)
    | SetAuthEmail String
    | SetAuthCode String
    | BackToAuthEmail
    | SwitchWorkspace WorkspaceMode
    | SetActionField String String
    | RequestAuthCode
    | GotRequestAuthCode AuthScope (Result ApiHttpError RequestCodeResponse)
    | BootstrapFirstAdmin
    | GotBootstrapFirstAdmin AuthScope (Result ApiHttpError RequestCodeResponse)
    | LoginWithCode
    | GotLoginWithCode AuthScope (Result ApiHttpError LoginResponse)
    | GotAuthMe AuthScope (Result ApiHttpError AuthMeResponse)
    | LogoutSession
    | GotLogoutSession AuthScope (Result ApiHttpError ())
    | ToggleAuthTools
    | SelectRow Row
    | StartCreate
    | StartEdit Row
    | CloseSelectedRow
    | CancelForm
    | SetFormField String String
    | SubmitForm
    | GotCreate (Result ApiHttpError Row)
    | GotUpdate (Result ApiHttpError Row)
    | RequestDeleteRow Row
    | ConfirmDelete
    | CancelDelete
    | GotDelete (Result ApiHttpError ())
    | RunAction
    | GotRunAction (Result ApiHttpError Row)
    | ClearFlash
    | ViewportResized Int Int
    | ToggleMobileSidebar
    | CloseMobileSidebar


type ApiHttpError
    = ApiBadUrl String
    | ApiTimeout
    | ApiNetworkError
    | ApiBadResponse ApiErrorPayload
    | ApiBadBody String


type alias ApiErrorPayload =
    { statusCode : Int
    , errorCode : Maybe String
    , message : String
    }


type alias RequestCodeResponse =
    { message : String }


type alias LoginResponse =
    { token : String
    , role : Maybe String
    , email : Maybe String
    }


type alias AuthMeResponse =
    { email : String
    , role : Maybe String
    }


type alias BackupResponse =
    { path : String
    , backupDir : String
    , removed : List String
    }


type alias BackupFile =
    { path : String
    , name : String
    , sizeBytes : Float
    , createdAt : String
    }


type alias BackupsPayload =
    { backupDir : String
    , backups : List BackupFile
    }


type alias PerfPayload =
    { uptimeSeconds : Float
    , goroutines : Int
    , memoryBytes : Float
    , sqliteBytes : Float
    , http : PerfHttp
    }


type alias PerfHttp =
    { totalRequests : Int
    , success2xx : Int
    , errors4xx : Int
    , errors5xx : Int
    , routes : List PerfRoute
    }


type alias AdminVersionPayload =
    { app : VersionApp
    , mar : VersionMar
    , runtimeInfo : VersionRuntime
    }


type alias VersionApp =
    { name : String
    , buildTime : String
    , manifestHash : String
    }


type alias VersionMar =
    { version : String
    , commit : String
    , buildTime : String
    }


type alias VersionRuntime =
    { goVersion : String
    , platform : String
    }


type alias PerfRoute =
    { method : String
    , route : String
    , count : Int
    , errors4xx : Int
    , errors5xx : Int
    , avgMs : Float
    }


type alias RequestLogsPayload =
    { buffer : Int
    , totalCaptured : Int
    , logs : List RequestLogEntry
    }


type alias RequestLogEntry =
    { id : String
    , method : String
    , path : String
    , route : String
    , status : Int
    , durationMs : Float
    , timestamp : String
    , queryCount : Int
    , queryTimeMs : Float
    , errorMessage : String
    , queries : List RequestLogQuery
    }


type alias RequestLogQuery =
    { sql : String
    , reason : Maybe String
    , durationMs : Float
    , rowCount : Int
    , error : Maybe String
    }


routeFromUrl : Url -> Route
routeFromUrl url =
    case fragmentSegments url of
        [] ->
            RouteDefault AppWorkspace

        [ "app" ] ->
            RouteDefault AppWorkspace

        [ "app", "auth" ] ->
            RouteAuthTools AppWorkspace

        [ "app", "entity", entityName ] ->
            RouteEntity AppWorkspace entityName

        [ "app", "entity", entityName, "new" ] ->
            RouteEntityCreate AppWorkspace entityName

        [ "app", "entity", entityName, rowKey ] ->
            RouteEntityDetail AppWorkspace entityName rowKey

        [ "app", "entity", entityName, rowKey, "edit" ] ->
            RouteEntityEdit AppWorkspace entityName rowKey

        [ "app", "action", actionName ] ->
            RouteAction AppWorkspace actionName

        [ "admin" ] ->
            RouteDefault AdminWorkspace

        [ "admin", "auth" ] ->
            RouteAuthTools AdminWorkspace

        [ "admin", "entity", entityName ] ->
            RouteEntity AdminWorkspace entityName

        [ "admin", "entity", entityName, "new" ] ->
            RouteEntityCreate AdminWorkspace entityName

        [ "admin", "entity", entityName, rowKey ] ->
            RouteEntityDetail AdminWorkspace entityName rowKey

        [ "admin", "entity", entityName, rowKey, "edit" ] ->
            RouteEntityEdit AdminWorkspace entityName rowKey

        [ "admin", "action", actionName ] ->
            RouteAction AdminWorkspace actionName

        [ "admin", "monitoring" ] ->
            RoutePerformance

        [ "admin", "logs" ] ->
            RouteRequestLogs

        [ "admin", "database" ] ->
            RouteDatabase

        _ ->
            RouteDefault AppWorkspace


fragmentSegments : Url -> List String
fragmentSegments url =
    url.fragment
        |> Maybe.withDefault ""
        |> String.split "/"
        |> List.filter (\segment -> segment /= "")
        |> List.filterMap Url.percentDecode


routeHref : Model -> Route -> String
routeHref model route =
    let
        base =
            model.currentUrl.path
                ++ (case model.currentUrl.query of
                        Just query ->
                            "?" ++ query

                        Nothing ->
                            ""
                   )

        fragment =
            routeFragment route
    in
    if fragment == "" then
        base

    else
        base ++ "#" ++ fragment


routeFragment : Route -> String
routeFragment route =
    case route of
        RouteDefault workspace ->
            workspaceSegment workspace

        RouteAuthTools workspace ->
            String.join "/" [ workspaceSegment workspace, "auth" ]

        RouteEntity workspace entityName ->
            String.join "/" [ workspaceSegment workspace, "entity", Url.percentEncode entityName ]

        RouteEntityCreate workspace entityName ->
            String.join "/" [ workspaceSegment workspace, "entity", Url.percentEncode entityName, "new" ]

        RouteEntityDetail workspace entityName rowKey ->
            String.join "/" [ workspaceSegment workspace, "entity", Url.percentEncode entityName, Url.percentEncode rowKey ]

        RouteEntityEdit workspace entityName rowKey ->
            String.join "/" [ workspaceSegment workspace, "entity", Url.percentEncode entityName, Url.percentEncode rowKey, "edit" ]

        RouteAction workspace actionName ->
            String.join "/" [ workspaceSegment workspace, "action", Url.percentEncode actionName ]

        RoutePerformance ->
            "admin/monitoring"

        RouteRequestLogs ->
            "admin/logs"

        RouteDatabase ->
            "admin/database"


workspaceSegment : WorkspaceMode -> String
workspaceSegment workspace =
    case workspace of
        AppWorkspace ->
            "app"

        AdminWorkspace ->
            "admin"


main : Program Flags Model Msg
main =
    Browser.application
        { init = init
        , update = update
        , subscriptions = \_ -> Browser.Events.onResize ViewportResized
        , view = view
        , onUrlRequest = UrlRequested
        , onUrlChange = UrlChanged
        }


init : Flags -> Url -> Nav.Key -> ( Model, Cmd Msg )
init flags url navKey =
    let
        initialModel =
            { apiBase = flags.apiBase
            , navKey = navKey
            , currentUrl = url
            , currentRoute = routeFromUrl url
            , authToken = String.trim flags.authToken
            , systemAuthToken = String.trim flags.systemAuthToken
            , currentEmail = Nothing
            , currentRole = Nothing
            , currentSystemEmail = Nothing
            , currentSystemRole = Nothing
            , authEmail = ""
            , authCode = ""
            , authStage = AuthStageEmail
            , authSubmitting = Nothing
            , sessionRestorePending = True
            , firstAdminCodeRequested = False
            , authToolsOpen = String.trim flags.authToken == "" && String.trim flags.systemAuthToken == ""
            , workspace = AppWorkspace
            , schema = Loading
            , selectedEntity = Nothing
            , selectedAction = Nothing
            , rows = NotAsked
            , selectedRow = Nothing
            , formMode = FormHidden
            , formValues = Dict.empty
            , actionFormValues = Dict.empty
            , actionResult = Nothing
            , perf = NotAsked
            , adminVersion = NotAsked
            , monitoringVersionDetailsOpen = False
            , requestLogs = NotAsked
            , backups = NotAsked
            , performanceMode = False
            , requestLogsMode = False
            , databaseMode = False
            , lastBackup = Nothing
            , pendingDelete = Nothing
            , authInlineMessage = Nothing
            , flash = Nothing
            , viewportWidth = max 320 flags.viewportWidth
            , mobileSidebarOpen = False
            , keepMobileSidebarOpenOnNextRoute = False
            }

        restoreAppAuthCmd =
            loadAuthMe AppAuthScope initialModel
    in
    ( initialModel
    , Cmd.batch
        [ loadSchema flags.apiBase
        , restoreAppAuthCmd
        ]
    )


type EntityRouteMode
    = EntityRouteList
    | EntityRouteCreate
    | EntityRouteDetail String
    | EntityRouteEdit String


pushRoute : Route -> Model -> Cmd Msg
pushRoute route model =
    if route == model.currentRoute then
        Cmd.none

    else
        Nav.pushUrl model.navKey (routeHref model route)


replaceRoute : Route -> Model -> Cmd Msg
replaceRoute route model =
    if route == model.currentRoute then
        Cmd.none

    else
        Nav.replaceUrl model.navKey (routeHref model route)


backRoute : Model -> Cmd Msg
backRoute model =
    Nav.back model.navKey 1


applyCurrentRoute : Model -> ( Model, Cmd Msg )
applyCurrentRoute model =
    case model.currentRoute of
        RouteDefault AppWorkspace ->
            case model.schema of
                Loaded schema ->
                    case preferredInitialEntity schema of
                        Just entity ->
                            applyEntityRoute AppWorkspace entity.name EntityRouteList model

                        Nothing ->
                            ( resetForRoute AppWorkspace model, Cmd.none )

                _ ->
                    ( resetForRoute AppWorkspace model, Cmd.none )

        RouteDefault AdminWorkspace ->
            applyAuthToolsRoute AdminWorkspace model

        RouteAuthTools workspace ->
            applyAuthToolsRoute workspace model

        RouteEntity workspace entityName ->
            applyEntityRoute workspace entityName EntityRouteList model

        RouteEntityCreate workspace entityName ->
            applyEntityRoute workspace entityName EntityRouteCreate model

        RouteEntityDetail workspace entityName rowKey ->
            applyEntityRoute workspace entityName (EntityRouteDetail rowKey) model

        RouteEntityEdit workspace entityName rowKey ->
            applyEntityRoute workspace entityName (EntityRouteEdit rowKey) model

        RouteAction workspace actionName ->
            applyActionRoute workspace actionName model

        RoutePerformance ->
            applySystemRoute RoutePerformance model

        RouteRequestLogs ->
            applySystemRoute RouteRequestLogs model

        RouteDatabase ->
            applySystemRoute RouteDatabase model


resetForRoute : WorkspaceMode -> Model -> Model
resetForRoute workspace model =
    { model
        | workspace = workspace
        , authToolsOpen = False
        , performanceMode = False
        , requestLogsMode = False
        , databaseMode = False
        , selectedEntity = Nothing
        , selectedAction = Nothing
        , rows = NotAsked
        , selectedRow = Nothing
        , formMode = FormHidden
        , formValues = Dict.empty
        , actionFormValues = Dict.empty
        , actionResult = Nothing
        , pendingDelete = Nothing
        , authInlineMessage = Nothing
        , flash = Nothing
        , mobileSidebarOpen = isCompactLayout model && model.keepMobileSidebarOpenOnNextRoute
        , keepMobileSidebarOpenOnNextRoute = False
    }


applyAuthToolsRoute : WorkspaceMode -> Model -> ( Model, Cmd Msg )
applyAuthToolsRoute workspace model =
    let
        baseModel =
            resetForRoute workspace model
    in
    ( { baseModel | authToolsOpen = True }, Cmd.none )


applyActionRoute : WorkspaceMode -> String -> Model -> ( Model, Cmd Msg )
applyActionRoute workspace actionName model =
    case findAction actionName model of
        Nothing ->
            let
                baseModel =
                    resetForRoute workspace model
            in
            ( { baseModel | flash = Just "Action not found" }, Cmd.none )

        Just actionInfo ->
            let
                baseModel =
                    resetForRoute workspace model
            in
            ( { baseModel
                | selectedAction = Just actionInfo
                , actionFormValues = actionFormDefaults model actionInfo
              }
            , Cmd.none
            )


applySystemRoute : Route -> Model -> ( Model, Cmd Msg )
applySystemRoute route model =
    if not (isAdminProfile model) then
        let
            baseModel =
                resetForRoute AppWorkspace model
        in
        ( { baseModel
            | flash =
                Just
                    (case route of
                        RoutePerformance ->
                            "Admin role required to access monitoring tools."

                        RouteRequestLogs ->
                            "Admin role required to access request logs."

                        RouteDatabase ->
                            "Admin role required to access database tools."

                        _ ->
                            "Admin role required."
                    )
          }
        , Cmd.none
        )

    else
        let
            baseModel =
                resetForRoute AdminWorkspace model
        in
        case route of
            RoutePerformance ->
                let
                    nextModel =
                        { baseModel | performanceMode = True, perf = Loading, adminVersion = Loading, monitoringVersionDetailsOpen = False }
                in
                ( nextModel, Cmd.batch [ loadPerformance nextModel, loadAdminVersion nextModel ] )

            RouteRequestLogs ->
                let
                    nextModel =
                        { baseModel | requestLogsMode = True, requestLogs = Loading }
                in
                ( nextModel, loadRequestLogs nextModel )

            RouteDatabase ->
                let
                    nextModel =
                        { baseModel | databaseMode = True, perf = Loading, backups = Loading }
                in
                ( nextModel, Cmd.batch [ loadPerformance nextModel, loadBackups nextModel ] )

            _ ->
                ( baseModel, Cmd.none )


applyEntityRoute : WorkspaceMode -> String -> EntityRouteMode -> Model -> ( Model, Cmd Msg )
applyEntityRoute workspace entityName routeMode model =
    case findEntity entityName model of
        Nothing ->
            let
                baseModel =
                    resetForRoute workspace model
            in
            ( { baseModel | flash = Just "Entity not found" }, Cmd.none )

        Just entity ->
            let
                sameEntity =
                    case model.selectedEntity of
                        Just current ->
                            current.name == entity.name

                        Nothing ->
                            False

                existingRows =
                    if sameEntity then
                        model.rows

                    else
                        NotAsked

                shouldLoadRows =
                    case existingRows of
                        NotAsked ->
                            True

                        Failed _ ->
                            True

                        _ ->
                            not sameEntity

                baseModel =
                    let
                        clearedModel =
                            resetForRoute workspace model
                    in
                    { clearedModel
                        | selectedEntity = Just entity
                        , rows =
                            if shouldLoadRows then
                                Loading

                            else
                                existingRows
                    }

                nextModel =
                    applyEntityRouteMode routeMode baseModel
            in
            if shouldLoadRows then
                ( nextModel, loadRows nextModel )

            else
                ( nextModel, Cmd.none )


applyEntityRouteMode : EntityRouteMode -> Model -> Model
applyEntityRouteMode routeMode model =
    case routeMode of
        EntityRouteList ->
            { model | selectedRow = Nothing, formMode = FormHidden, formValues = Dict.empty }

        EntityRouteCreate ->
            let
                nextModel =
                    { model | selectedRow = Nothing, formMode = FormCreate }
            in
            { nextModel | formValues = formDefaults nextModel }

        EntityRouteDetail rowKey ->
            syncRowRoute rowKey False model

        EntityRouteEdit rowKey ->
            syncRowRoute rowKey True model


syncRouteSelection : Model -> Model
syncRouteSelection model =
    case model.currentRoute of
        RouteEntity workspace entityName ->
            if routeMatchesSelectedEntity workspace entityName model then
                applyEntityRouteMode EntityRouteList model

            else
                model

        RouteEntityCreate workspace entityName ->
            if routeMatchesSelectedEntity workspace entityName model then
                applyEntityRouteMode EntityRouteCreate model

            else
                model

        RouteEntityDetail workspace entityName rowKey ->
            if routeMatchesSelectedEntity workspace entityName model then
                applyEntityRouteMode (EntityRouteDetail rowKey) model

            else
                model

        RouteEntityEdit workspace entityName rowKey ->
            if routeMatchesSelectedEntity workspace entityName model then
                applyEntityRouteMode (EntityRouteEdit rowKey) model

            else
                model

        _ ->
            model


syncRowRoute : String -> Bool -> Model -> Model
syncRowRoute rowKey editing model =
    case ( model.selectedEntity, model.rows ) of
        ( Just entity, Loaded rows ) ->
            case findRowById entity rowKey rows of
                Just rowValue ->
                    if editing then
                        let
                            nextModel =
                                { model | selectedRow = Just rowValue, formMode = FormEdit rowValue }
                        in
                        { nextModel | formValues = formFromRow nextModel rowValue }

                    else
                        { model | selectedRow = Just rowValue, formMode = FormHidden, formValues = Dict.empty }

                Nothing ->
                    { model | selectedRow = Nothing, formMode = FormHidden, formValues = Dict.empty, flash = Just "Record not found" }

        _ ->
            { model | selectedRow = Nothing, formMode = FormHidden, formValues = Dict.empty }


routeMatchesSelectedEntity : WorkspaceMode -> String -> Model -> Bool
routeMatchesSelectedEntity workspace entityName model =
    currentWorkspace model
        == workspace
        && (case model.selectedEntity of
                Just entity ->
                    entity.name == entityName

                Nothing ->
                    False
           )


findRowById : Entity -> String -> List Row -> Maybe Row
findRowById entity rowKey rows =
    rows
        |> List.filter
            (\rowValue ->
                rowId entity rowValue == Just rowKey
            )
        |> List.head


routeForWorkspaceSwitch : WorkspaceMode -> Model -> Route
routeForWorkspaceSwitch workspace model =
    case model.currentRoute of
        RouteAuthTools _ ->
            RouteAuthTools workspace

        RouteEntity _ entityName ->
            RouteEntity workspace entityName

        RouteEntityCreate _ entityName ->
            RouteEntityCreate workspace entityName

        RouteEntityDetail _ entityName rowKey ->
            RouteEntityDetail workspace entityName rowKey

        RouteEntityEdit _ entityName rowKey ->
            RouteEntityEdit workspace entityName rowKey

        RouteAction _ actionName ->
            RouteAction workspace actionName

        RoutePerformance ->
            if workspace == AppWorkspace then
                RouteDefault AppWorkspace

            else
                RoutePerformance

        RouteRequestLogs ->
            if workspace == AppWorkspace then
                RouteDefault AppWorkspace

            else
                RouteRequestLogs

        RouteDatabase ->
            if workspace == AppWorkspace then
                RouteDefault AppWorkspace

            else
                RouteDatabase

        RouteDefault _ ->
            RouteDefault workspace


routeForCurrentEntityList : Model -> Maybe Route
routeForCurrentEntityList model =
    model.selectedEntity
        |> Maybe.map (\entity -> RouteEntity (currentWorkspace model) entity.name)


rowIdForCurrentSelection : Model -> Row -> Maybe String
rowIdForCurrentSelection model rowValue =
    case model.selectedEntity of
        Just entity ->
            rowId entity rowValue

        Nothing ->
            Nothing


cancelFormRoute : Model -> Maybe Route
cancelFormRoute model =
    case ( model.selectedEntity, model.formMode ) of
        ( Just entity, FormEdit rowValue ) ->
            rowId entity rowValue
                |> Maybe.map (\rowKey -> RouteEntityDetail (currentWorkspace model) entity.name rowKey)

        ( Just entity, FormCreate ) ->
            Just (RouteEntity (currentWorkspace model) entity.name)

        _ ->
            routeForCurrentEntityList model


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        UrlRequested request ->
            case request of
                Browser.Internal url ->
                    ( model, Nav.pushUrl model.navKey (Url.toString url) )

                Browser.External href ->
                    ( model, Nav.load href )

        UrlChanged url ->
            applyCurrentRoute { model | currentUrl = url, currentRoute = routeFromUrl url }

        GotSchema result ->
            case result of
                Ok schema ->
                    let
                        nextModel =
                            { model
                                | schema = Loaded schema
                                , authStage =
                                    if hasActiveSession model then
                                        AuthStageSession

                                    else if model.authStage == AuthStageCode || model.firstAdminCodeRequested then
                                        AuthStageCode

                                    else
                                        AuthStageEmail
                                , firstAdminCodeRequested =
                                    model.firstAdminCodeRequested
                                        && String.trim model.authToken
                                        == ""
                            }
                    in
                    applyCurrentRoute nextModel

                Err httpError ->
                    ( { model | schema = Failed (httpErrorToString httpError), rows = Failed "schema unavailable" }, Cmd.none )

        SelectEntity entityName ->
            let
                nextModel =
                    closeMobileSidebarForNavigation model
            in
            ( nextModel, pushRoute (RouteEntity (currentWorkspace nextModel) entityName) nextModel )

        SelectAction actionName ->
            let
                nextModel =
                    closeMobileSidebarForNavigation model
            in
            ( nextModel, pushRoute (RouteAction (currentWorkspace nextModel) actionName) nextModel )

        ReloadRows ->
            let
                nextModel =
                    { model | rows = Loading, flash = Nothing }
            in
            ( nextModel, loadRows nextModel )

        GotRows result ->
            case result of
                Ok rows ->
                    ( syncRouteSelection { model | rows = Loaded rows }, Cmd.none )

                Err httpError ->
                    ( { model | rows = Failed (httpErrorToString httpError) }, Cmd.none )

        SelectPerformance ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Admin role required to access monitoring tools." }, Cmd.none )

            else
                let
                    nextModel =
                        closeMobileSidebarForNavigation model
                in
                ( nextModel, pushRoute RoutePerformance nextModel )

        SelectRequestLogs ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Admin role required to access request logs." }, Cmd.none )

            else
                let
                    nextModel =
                        closeMobileSidebarForNavigation model
                in
                ( nextModel, pushRoute RouteRequestLogs nextModel )

        SelectDatabase ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Admin role required to access database tools." }, Cmd.none )

            else
                let
                    nextModel =
                        closeMobileSidebarForNavigation model
                in
                ( nextModel, pushRoute RouteDatabase nextModel )

        ReloadDatabase ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Only admin can refresh database tools" }, Cmd.none )

            else
                let
                    nextModel =
                        { model | perf = Loading, backups = Loading, flash = Nothing }
                in
                ( nextModel, Cmd.batch [ loadPerformance nextModel, loadBackups nextModel ] )

        ReloadPerformance ->
            let
                nextModel =
                    { model | perf = Loading, adminVersion = Loading, flash = Nothing }
            in
            ( nextModel, Cmd.batch [ loadPerformance nextModel, loadAdminVersion nextModel ] )

        ToggleMonitoringVersionDetails ->
            ( { model | monitoringVersionDetailsOpen = not model.monitoringVersionDetailsOpen }, Cmd.none )

        ReloadRequestLogs ->
            let
                nextModel =
                    { model | requestLogs = Loading, flash = Nothing }
            in
            ( nextModel, loadRequestLogs nextModel )

        GotPerformance result ->
            case result of
                Ok perf ->
                    ( { model | perf = Loaded perf }, Cmd.none )

                Err httpError ->
                    ( { model | perf = Failed (httpErrorToString httpError) }, Cmd.none )

        GotAdminVersion result ->
            case result of
                Ok payload ->
                    ( { model | adminVersion = Loaded payload }, Cmd.none )

                Err httpError ->
                    ( { model | adminVersion = Failed (httpErrorToString httpError) }, Cmd.none )

        GotRequestLogs result ->
            case result of
                Ok payload ->
                    ( { model | requestLogs = Loaded payload }, Cmd.none )

                Err httpError ->
                    ( { model | requestLogs = Failed (httpErrorToString httpError) }, Cmd.none )

        GotBackups result ->
            case result of
                Ok backups ->
                    ( { model | backups = Loaded backups }, Cmd.none )

                Err httpError ->
                    ( { model | backups = Failed (httpErrorToString httpError) }, Cmd.none )

        SetAuthEmail email ->
            ( { model | authEmail = email, authInlineMessage = Nothing }, Cmd.none )

        SetAuthCode code ->
            ( { model | authCode = code }, Cmd.none )

        BackToAuthEmail ->
            ( { model | authStage = AuthStageEmail, authCode = "", authSubmitting = Nothing, authInlineMessage = Nothing, flash = Nothing, mobileSidebarOpen = False }, Cmd.none )

        SwitchWorkspace workspace ->
            let
                nextModel =
                    closeMobileSidebarForNavigation model
            in
            ( nextModel, pushRoute (routeForWorkspaceSwitch workspace nextModel) nextModel )

        SetActionField fieldName value ->
            ( { model | actionFormValues = Dict.insert fieldName value model.actionFormValues }, Cmd.none )

        RequestAuthCode ->
            if String.trim model.authEmail == "" then
                ( { model | flash = Just "Email is required for request-code" }, Cmd.none )

            else if authEmailValidationMessage model.authEmail /= Nothing then
                ( { model | authInlineMessage = authEmailValidationMessage model.authEmail, flash = Nothing }, Cmd.none )

            else
                let
                    scope =
                        activeAuthScope
                in
                ( { model | authInlineMessage = Nothing, flash = Nothing, authSubmitting = Just AuthSubmitSendingCode }, requestAuthCode scope model )

        GotRequestAuthCode _ result ->
            case result of
                Ok _ ->
                    ( { model
                        | authStage = AuthStageCode
                        , authSubmitting = Nothing
                        , authInlineMessage = Nothing
                        , flash = Nothing
                      }
                    , Cmd.none
                    )

                Err httpError ->
                    ( { model | authSubmitting = Nothing, flash = Just (authRequestCodeErrorToString httpError) }, Cmd.none )

        BootstrapFirstAdmin ->
            if String.trim model.authEmail == "" then
                ( { model | flash = Just "Email is required to create the first admin" }, Cmd.none )

            else if authEmailValidationMessage model.authEmail /= Nothing then
                ( { model | authInlineMessage = authEmailValidationMessage model.authEmail, flash = Nothing }, Cmd.none )

            else
                let
                    scope =
                        activeAuthScope
                in
                ( { model | authInlineMessage = Nothing, flash = Nothing, authSubmitting = Just AuthSubmitSendingCode }, bootstrapFirstAdmin scope model )

        GotBootstrapFirstAdmin _ result ->
            case result of
                Ok _ ->
                    ( { model | authStage = AuthStageCode, authSubmitting = Nothing, firstAdminCodeRequested = True, authInlineMessage = Nothing, flash = Nothing }, loadSchema model.apiBase )

                Err httpError ->
                    ( { model | authSubmitting = Nothing, flash = Just (authRequestCodeErrorToString httpError) }, Cmd.none )

        LoginWithCode ->
            if String.trim model.authEmail == "" || String.trim model.authCode == "" then
                ( { model | flash = Just "Email and code are required for login" }, Cmd.none )

            else if authEmailValidationMessage model.authEmail /= Nothing then
                ( { model | authInlineMessage = authEmailValidationMessage model.authEmail, flash = Nothing }, Cmd.none )

            else
                let
                    scope =
                        activeAuthScope
                in
                ( { model | authInlineMessage = Nothing, flash = Nothing, authSubmitting = Just AuthSubmitSigningIn }, loginWithCode scope model )

        GotLoginWithCode scope result ->
            case result of
                Ok response ->
                    case scope of
                        AppAuthScope ->
                            let
                                nextWorkspace =
                                    workspaceForCurrentRoute response.role model.currentRoute

                                nextModel =
                                    { model
                                        | authToken = ""
                                        , currentRole = response.role
                                        , currentEmail = response.email
                                        , authEmail = ""
                                        , authCode = ""
                                        , authStage = AuthStageSession
                                        , authSubmitting = Nothing
                                        , sessionRestorePending = False
                                        , firstAdminCodeRequested = False
                                        , authToolsOpen = False
                                        , workspace = nextWorkspace
                                        , flash = Just "Login successful."
                                    }

                                meCmd =
                                    loadAuthMe AppAuthScope nextModel

                                schemaCmd =
                                    loadSchema model.apiBase

                                saveSessionCmd =
                                    saveSessionFromModel nextModel
                            in
                            if shouldReloadCrudAfterLogin model then
                                let
                                    loadingModel =
                                        { nextModel | rows = Loading }
                                in
                                ( loadingModel, Cmd.batch [ loadRows loadingModel, meCmd, schemaCmd, saveSessionCmd ] )

                            else
                                ( nextModel, Cmd.batch [ meCmd, schemaCmd, saveSessionCmd ] )

                Err httpError ->
                    ( { model | authSubmitting = Nothing, flash = Just (authLoginErrorToString httpError) }, Cmd.none )

        GotAuthMe scope result ->
            case result of
                Ok response ->
                    case scope of
                        AppAuthScope ->
                            let
                                nextWorkspace =
                                    workspaceForCurrentRoute response.role model.currentRoute

                                preferredEntity =
                                    case model.schema of
                                        Loaded schema ->
                                            preferredInitialEntity schema

                                        _ ->
                                            Nothing

                                nextSelectedEntity =
                                    if model.selectedEntity == Nothing then
                                        preferredEntity

                                    else
                                        model.selectedEntity

                                shouldLoadRows =
                                    nextWorkspace
                                        == AppWorkspace
                                        && nextSelectedEntity
                                        /= Nothing
                                        && (model.selectedEntity
                                                == Nothing
                                                || model.rows
                                                == NotAsked
                                           )

                                nextModel =
                                    { model
                                        | currentEmail = Just response.email
                                        , currentRole = response.role
                                        , authStage = AuthStageSession
                                        , sessionRestorePending = False
                                        , authToolsOpen = False
                                        , workspace = nextWorkspace
                                        , selectedEntity = nextSelectedEntity
                                        , rows =
                                            if shouldLoadRows then
                                                Loading

                                            else
                                                model.rows
                                        , flash = Nothing
                                    }
                            in
                            if shouldLoadRows then
                                ( nextModel, loadRows nextModel )

                            else
                                ( nextModel, Cmd.none )

                Err httpError ->
                    if isUnauthorizedError httpError then
                        case scope of
                            AppAuthScope ->
                                let
                                    nextModel =
                                        { model
                                            | authToken = ""
                                            , currentEmail = Nothing
                                            , currentRole = Nothing
                                            , authStage = AuthStageEmail
                                            , sessionRestorePending = False
                                            , authToolsOpen = True
                                            , flash =
                                                if model.sessionRestorePending then
                                                    Nothing

                                                else
                                                    Just "Session expired. Please login again."
                                        }
                                in
                                ( nextModel, saveSessionFromModel nextModel )

                    else
                        ( { model
                            | sessionRestorePending = False
                            , flash =
                                if model.sessionRestorePending then
                                    Nothing

                                else
                                    Just (httpErrorToString httpError)
                          }
                        , Cmd.none
                        )

        LogoutSession ->
            let
                scope =
                    activeAuthScope

                nextModel =
                    clearLocalSession model
            in
            ( nextModel
            , Cmd.batch
                [ saveSessionFromModel nextModel
                , logoutSession scope model
                ]
            )

        GotLogoutSession scope result ->
            case result of
                Ok _ ->
                    case scope of
                        AppAuthScope ->
                            ( model, Cmd.none )

                Err _ ->
                    ( model, Cmd.none )

        TriggerBackup ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Only admin can create backups" }, Cmd.none )

            else
                ( { model | flash = Nothing }, triggerBackup model )

        GotBackup result ->
            case result of
                Ok response ->
                    let
                        removedCount =
                            List.length response.removed

                        removedText =
                            if removedCount > 0 then
                                " Removed " ++ String.fromInt removedCount ++ " old backup(s)."

                            else
                                ""

                        nextModel =
                            { model | lastBackup = Just response, flash = Just ("Backup created at " ++ response.path ++ "." ++ removedText), backups = Loading }
                    in
                    ( nextModel, Cmd.batch [ loadBackups nextModel, loadPerformance nextModel ] )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        ToggleAuthTools ->
            let
                nextModel =
                    closeMobileSidebarForNavigation model
            in
            ( nextModel, pushRoute (RouteAuthTools (currentWorkspace nextModel)) nextModel )

        SelectRow rowValue ->
            case ( model.selectedEntity, rowIdForCurrentSelection model rowValue ) of
                ( Just entity, Just rowKey ) ->
                    ( model, pushRoute (RouteEntityDetail (currentWorkspace model) entity.name rowKey) model )

                _ ->
                    ( { model | flash = Just "Could not open record details" }, Cmd.none )

        StartCreate ->
            case routeForCurrentEntityList model of
                Just (RouteEntity workspace entityName) ->
                    ( model, pushRoute (RouteEntityCreate workspace entityName) model )

                _ ->
                    ( { model | flash = Just "Select an entity first" }, Cmd.none )

        StartEdit rowValue ->
            case ( model.selectedEntity, rowIdForCurrentSelection model rowValue ) of
                ( Just entity, Just rowKey ) ->
                    ( model, pushRoute (RouteEntityEdit (currentWorkspace model) entity.name rowKey) model )

                _ ->
                    ( { model | flash = Just "Could not open edit mode" }, Cmd.none )

        CloseSelectedRow ->
            case model.currentRoute of
                RouteEntityDetail _ _ _ ->
                    ( model, backRoute model )

                _ ->
                    case routeForCurrentEntityList model of
                        Just route ->
                            ( model, replaceRoute route model )

                        Nothing ->
                            ( { model | selectedRow = Nothing, flash = Nothing }, Cmd.none )

        CancelForm ->
            case model.currentRoute of
                RouteEntityCreate _ _ ->
                    ( model, backRoute model )

                RouteEntityEdit _ _ _ ->
                    ( model, backRoute model )

                _ ->
                    case cancelFormRoute model of
                        Just route ->
                            ( model, replaceRoute route model )

                        Nothing ->
                            ( { model | formMode = FormHidden, formValues = Dict.empty, flash = Nothing }, Cmd.none )

        SetFormField key value ->
            ( { model | formValues = Dict.insert key value model.formValues }, Cmd.none )

        SubmitForm ->
            case model.selectedEntity of
                Nothing ->
                    ( { model | flash = Just "Select an entity first" }, Cmd.none )

                Just entity ->
                    let
                        forUpdate =
                            case model.formMode of
                                FormEdit _ ->
                                    True

                                _ ->
                                    False
                    in
                    case encodePayload { forUpdate = forUpdate } entity.fields model.formValues of
                        Err message ->
                            ( { model | flash = Just message }, Cmd.none )

                        Ok payload ->
                            case model.formMode of
                                FormCreate ->
                                    ( model, createRow model entity payload )

                                FormEdit rowValue ->
                                    case rowId entity rowValue of
                                        Nothing ->
                                            ( { model | flash = Just "Could not find row id" }, Cmd.none )

                                        Just idValue ->
                                            ( model, updateRow model entity idValue payload )

                                FormHidden ->
                                    ( { model | flash = Just "Nothing to save" }, Cmd.none )

        GotCreate result ->
            case result of
                Ok createdRow ->
                    let
                        nextRows =
                            case model.rows of
                                Loaded items ->
                                    Loaded (createdRow :: items)

                                _ ->
                                    model.rows

                        nextModel =
                            { model | rows = nextRows, formMode = FormHidden, formValues = Dict.empty, flash = Nothing }
                    in
                    case ( model.selectedEntity, rowIdForCurrentSelection nextModel createdRow ) of
                        ( Just entity, Just rowKey ) ->
                            ( nextModel, replaceRoute (RouteEntityDetail (currentWorkspace nextModel) entity.name rowKey) nextModel )

                        _ ->
                            ( nextModel, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        GotUpdate result ->
            case result of
                Ok updatedRow ->
                    let
                        nextRows =
                            case ( model.selectedEntity, model.rows ) of
                                ( Just entity, Loaded items ) ->
                                    Loaded (replaceRow entity updatedRow items)

                                _ ->
                                    model.rows

                        nextModel =
                            { model | rows = nextRows, selectedRow = Just updatedRow, formMode = FormHidden, formValues = Dict.empty, flash = Nothing }
                    in
                    case ( model.selectedEntity, rowIdForCurrentSelection nextModel updatedRow ) of
                        ( Just entity, Just rowKey ) ->
                            ( nextModel, replaceRoute (RouteEntityDetail (currentWorkspace nextModel) entity.name rowKey) nextModel )

                        _ ->
                            ( nextModel, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        RequestDeleteRow rowValue ->
            case model.selectedEntity of
                Nothing ->
                    ( { model | flash = Just "Select an entity first" }, Cmd.none )

                Just entity ->
                    case rowId entity rowValue of
                        Nothing ->
                            ( { model | flash = Just "Could not find row id" }, Cmd.none )

                        Just idValue ->
                            let
                                message =
                                    deleteConfirmationMessage entity rowValue

                                nextModel =
                                    { model | pendingDelete = Just { entity = entity, idValue = idValue, message = message } }
                            in
                            ( nextModel, Cmd.none )

        ConfirmDelete ->
            case model.pendingDelete of
                Just pendingDelete ->
                    ( { model | pendingDelete = Nothing }, deleteRowRequest model pendingDelete.entity pendingDelete.idValue )

                Nothing ->
                    ( { model | pendingDelete = Nothing }, Cmd.none )

        CancelDelete ->
            ( { model | pendingDelete = Nothing }, Cmd.none )

        GotDelete result ->
            case result of
                Ok _ ->
                    let
                        nextModel =
                            { model | flash = Nothing, selectedRow = Nothing, formMode = FormHidden, formValues = Dict.empty, pendingDelete = Nothing, rows = NotAsked }
                    in
                    case routeForCurrentEntityList nextModel of
                        Just route ->
                            ( nextModel, replaceRoute route nextModel )

                        Nothing ->
                            ( nextModel, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError), pendingDelete = Nothing }, Cmd.none )

        RunAction ->
            case model.selectedAction of
                Nothing ->
                    ( { model | flash = Just "Select an action first" }, Cmd.none )

                Just actionInfo ->
                    case actionPayloadFromForm model actionInfo of
                        Err message ->
                            ( { model | flash = Just message }, Cmd.none )

                        Ok payload ->
                            ( { model | flash = Nothing, actionResult = Nothing }, runAction model actionInfo payload )

        GotRunAction result ->
            case result of
                Ok response ->
                    ( { model | actionResult = Just response, flash = Just "Action executed successfully" }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        ClearFlash ->
            ( { model | flash = Nothing }, Cmd.none )

        ViewportResized widthPx _ ->
            ( { model
                | viewportWidth = max 320 widthPx
                , mobileSidebarOpen =
                    if widthPx >= 900 then
                        False

                    else
                        model.mobileSidebarOpen
                , keepMobileSidebarOpenOnNextRoute =
                    if widthPx >= 900 then
                        False

                    else
                        model.keepMobileSidebarOpenOnNextRoute
              }
            , Cmd.none
            )

        ToggleMobileSidebar ->
            ( { model | mobileSidebarOpen = not model.mobileSidebarOpen, keepMobileSidebarOpenOnNextRoute = False }, Cmd.none )

        CloseMobileSidebar ->
            ( { model | mobileSidebarOpen = False, keepMobileSidebarOpenOnNextRoute = False }, Cmd.none )


loadSchema : String -> Cmd Msg
loadSchema apiBase =
    Http.get
        { url = apiBase ++ "/_mar/schema"
        , expect = expectJsonWithApiError GotSchema decodeSchema
        }


loadRows : Model -> Cmd Msg
loadRows model =
    case model.selectedEntity of
        Nothing ->
            Cmd.none

        Just entity ->
            Http.request
                { method = "GET"
                , headers = appAuthHeaders model
                , url = model.apiBase ++ entity.resource
                , body = Http.emptyBody
                , expect = expectJsonWithApiError GotRows decodeRows
                , timeout = Nothing
                , tracker = Nothing
                }


loadPerformance : Model -> Cmd Msg
loadPerformance model =
    Http.request
        { method = "GET"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ "/_mar/perf"
        , body = Http.emptyBody
        , expect = expectJsonWithApiError GotPerformance perfPayloadDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loadAdminVersion : Model -> Cmd Msg
loadAdminVersion model =
    Http.request
        { method = "GET"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ "/_mar/version/admin"
        , body = Http.emptyBody
        , expect = expectJsonWithApiError GotAdminVersion adminVersionDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loadRequestLogs : Model -> Cmd Msg
loadRequestLogs model =
    Http.request
        { method = "GET"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ "/_mar/request-logs?limit=30"
        , body = Http.emptyBody
        , expect = expectJsonWithApiError GotRequestLogs requestLogsPayloadDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loadBackups : Model -> Cmd Msg
loadBackups model =
    Http.request
        { method = "GET"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ "/_mar/backups"
        , body = Http.emptyBody
        , expect = expectJsonWithApiError GotBackups backupsDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


triggerBackup : Model -> Cmd Msg
triggerBackup model =
    Http.request
        { method = "POST"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ "/_mar/backups"
        , body = Http.emptyBody
        , expect = expectJsonWithApiError GotBackup backupResponseDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


createRow : Model -> Entity -> Encode.Value -> Cmd Msg
createRow model entity payload =
    Http.request
        { method = "POST"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ entity.resource
        , body = Http.jsonBody payload
        , expect = expectJsonWithApiError GotCreate rowDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


updateRow : Model -> Entity -> String -> Encode.Value -> Cmd Msg
updateRow model entity idValue payload =
    Http.request
        { method = "PATCH"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ entity.resource ++ "/" ++ idValue
        , body = Http.jsonBody payload
        , expect = expectJsonWithApiError GotUpdate rowDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


deleteRowRequest : Model -> Entity -> String -> Cmd Msg
deleteRowRequest model entity idValue =
    Http.request
        { method = "DELETE"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ entity.resource ++ "/" ++ idValue
        , body = Http.emptyBody
        , expect = expectUnitWithApiError GotDelete
        , timeout = Nothing
        , tracker = Nothing
        }


appAuthHeaders : Model -> List Http.Header
appAuthHeaders model =
    if String.trim model.authToken == "" then
        []

    else
        [ Http.header "Authorization" ("Bearer " ++ String.trim model.authToken) ]


requestAuthCode : AuthScope -> Model -> Cmd Msg
requestAuthCode scope model =
    let
        endpoint =
            "/auth/request-code"
    in
    Http.request
        { method = "POST"
        , headers = []
        , url = model.apiBase ++ endpoint
        , body =
            Http.jsonBody
                (Encode.object
                    [ ( "email", Encode.string (String.trim model.authEmail) )
                    ]
                )
        , expect = expectJsonWithApiError (GotRequestAuthCode scope) requestCodeDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


bootstrapFirstAdmin : AuthScope -> Model -> Cmd Msg
bootstrapFirstAdmin scope model =
    let
        endpoint =
            "/_mar/bootstrap-admin"
    in
    Http.request
        { method = "POST"
        , headers = []
        , url = model.apiBase ++ endpoint
        , body =
            Http.jsonBody
                (Encode.object
                    [ ( "email", Encode.string (String.trim model.authEmail) )
                    ]
                )
        , expect = expectJsonWithApiError (GotBootstrapFirstAdmin scope) requestCodeDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loginWithCode : AuthScope -> Model -> Cmd Msg
loginWithCode scope model =
    let
        endpoint =
            "/auth/login"
    in
    Http.request
        { method = "POST"
        , headers = [ Http.header "X-Mar-Admin-UI" "true" ]
        , url = model.apiBase ++ endpoint
        , body =
            Http.jsonBody
                (Encode.object
                    [ ( "email", Encode.string (String.trim model.authEmail) )
                    , ( "code", Encode.string (String.trim model.authCode) )
                    ]
                )
        , expect = expectJsonWithApiError (GotLoginWithCode scope) loginResponseDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loadAuthMe : AuthScope -> Model -> Cmd Msg
loadAuthMe scope model =
    let
        headers =
            appAuthHeaders model

        endpoint =
            "/auth/me"
    in
    Http.request
        { method = "GET"
        , headers = headers
        , url = model.apiBase ++ endpoint
        , body = Http.emptyBody
        , expect = expectJsonWithApiError (GotAuthMe scope) authMeResponseDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


logoutSession : AuthScope -> Model -> Cmd Msg
logoutSession scope model =
    let
        headers =
            appAuthHeaders model

        endpoint =
            "/auth/logout"
    in
    Http.request
        { method = "POST"
        , headers = headers
        , url = model.apiBase ++ endpoint
        , body = Http.emptyBody
        , expect = expectUnitWithApiError (GotLogoutSession scope)
        , timeout = Nothing
        , tracker = Nothing
        }


runAction : Model -> ActionInfo -> Encode.Value -> Cmd Msg
runAction model actionInfo payload =
    Http.request
        { method = "POST"
        , headers = appAuthHeaders model
        , url = model.apiBase ++ "/actions/" ++ actionInfo.name
        , body = Http.jsonBody payload
        , expect = expectJsonWithApiError GotRunAction rowDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


expectJsonWithApiError : (Result ApiHttpError a -> msg) -> Decode.Decoder a -> Http.Expect msg
expectJsonWithApiError toMsg decoder =
    Http.expectStringResponse toMsg
        (\response ->
            case response of
                Http.GoodStatus_ _ body ->
                    case Decode.decodeString decoder body of
                        Ok value ->
                            Ok value

                        Err decodeError ->
                            Err (ApiBadBody ("Failed to decode response: " ++ Decode.errorToString decodeError))

                Http.BadStatus_ metadata body ->
                    Err (ApiBadResponse (apiErrorPayload metadata.statusCode body))

                Http.BadUrl_ url ->
                    Err (ApiBadUrl url)

                Http.Timeout_ ->
                    Err ApiTimeout

                Http.NetworkError_ ->
                    Err ApiNetworkError
        )


expectUnitWithApiError : (Result ApiHttpError () -> msg) -> Http.Expect msg
expectUnitWithApiError toMsg =
    Http.expectStringResponse toMsg
        (\response ->
            case response of
                Http.GoodStatus_ _ _ ->
                    Ok ()

                Http.BadStatus_ metadata body ->
                    Err (ApiBadResponse (apiErrorPayload metadata.statusCode body))

                Http.BadUrl_ url ->
                    Err (ApiBadUrl url)

                Http.Timeout_ ->
                    Err ApiTimeout

                Http.NetworkError_ ->
                    Err ApiNetworkError
        )


apiErrorPayload : Int -> String -> ApiErrorPayload
apiErrorPayload statusCode body =
    let
        fallback =
            { statusCode = statusCode
            , errorCode = Nothing
            , message = "HTTP error: " ++ String.fromInt statusCode
            }
    in
    case Decode.decodeString apiErrorDecoder body of
        Ok payload ->
            { payload | statusCode = statusCode }

        Err _ ->
            fallback


apiErrorDecoder : Decode.Decoder ApiErrorPayload
apiErrorDecoder =
    Decode.map3 ApiErrorPayload
        (Decode.succeed 0)
        (Decode.oneOf
            [ Decode.field "errorCode" (Decode.map Just Decode.string)
            , Decode.field "errorCode" (Decode.null Nothing)
            , Decode.succeed Nothing
            ]
        )
        (Decode.oneOf
            [ Decode.field "error" Decode.string
            , Decode.field "message" Decode.string
            ]
        )


requestCodeDecoder : Decode.Decoder RequestCodeResponse
requestCodeDecoder =
    Decode.map RequestCodeResponse
        (Decode.field "message" Decode.string)


loginResponseDecoder : Decode.Decoder LoginResponse
loginResponseDecoder =
    Decode.map3 LoginResponse
        (Decode.field "token" Decode.string)
        (Decode.oneOf
            [ Decode.field "role" (Decode.map Just Decode.string)
            , Decode.field "role" (Decode.null Nothing)
            , Decode.at [ "user", "role" ] (Decode.map Just Decode.string)
            , Decode.at [ "user", "role" ] (Decode.null Nothing)
            , Decode.succeed Nothing
            ]
        )
        (Decode.oneOf
            [ Decode.field "email" (Decode.map Just Decode.string)
            , Decode.field "email" (Decode.null Nothing)
            , Decode.at [ "user", "email" ] (Decode.map Just Decode.string)
            , Decode.at [ "user", "email" ] (Decode.null Nothing)
            , Decode.succeed Nothing
            ]
        )


authMeResponseDecoder : Decode.Decoder AuthMeResponse
authMeResponseDecoder =
    Decode.map2 AuthMeResponse
        (Decode.field "email" Decode.string)
        (Decode.oneOf
            [ Decode.field "role" (Decode.map Just Decode.string)
            , Decode.field "role" (Decode.null Nothing)
            , Decode.succeed Nothing
            ]
        )


perfPayloadDecoder : Decode.Decoder PerfPayload
perfPayloadDecoder =
    Decode.map5 PerfPayload
        (Decode.field "uptimeSeconds" Decode.float)
        (Decode.field "goroutines" Decode.int)
        (Decode.field "memoryBytes" Decode.float)
        (Decode.field "sqliteBytes" Decode.float)
        (Decode.field "http" perfHttpDecoder)


perfHttpDecoder : Decode.Decoder PerfHttp
perfHttpDecoder =
    Decode.map5 PerfHttp
        (Decode.field "totalRequests" Decode.int)
        (Decode.oneOf
            [ Decode.field "success2xx" Decode.int
            , Decode.succeed 0
            ]
        )
        (Decode.field "errors4xx" Decode.int)
        (Decode.field "errors5xx" Decode.int)
        (Decode.field "routes" (Decode.list perfRouteDecoder))


perfRouteDecoder : Decode.Decoder PerfRoute
perfRouteDecoder =
    Decode.map6 PerfRoute
        (Decode.field "method" Decode.string)
        (Decode.field "route" Decode.string)
        (Decode.field "count" Decode.int)
        (Decode.field "errors4xx" Decode.int)
        (Decode.field "errors5xx" Decode.int)
        (Decode.field "avgMs" Decode.float)


adminVersionDecoder : Decode.Decoder AdminVersionPayload
adminVersionDecoder =
    Decode.map3 AdminVersionPayload
        (Decode.field "app" versionAppDecoder)
        (Decode.field "mar" versionMarDecoder)
        (Decode.field "runtime" versionRuntimeDecoder)


versionAppDecoder : Decode.Decoder VersionApp
versionAppDecoder =
    Decode.map3 VersionApp
        (Decode.field "name" Decode.string)
        (Decode.field "buildTime" Decode.string)
        (Decode.field "manifestHash" Decode.string)


versionMarDecoder : Decode.Decoder VersionMar
versionMarDecoder =
    Decode.map3 VersionMar
        (Decode.field "version" Decode.string)
        (Decode.field "commit" Decode.string)
        (Decode.field "buildTime" Decode.string)


versionRuntimeDecoder : Decode.Decoder VersionRuntime
versionRuntimeDecoder =
    Decode.map2 VersionRuntime
        (Decode.field "goVersion" Decode.string)
        (Decode.field "platform" Decode.string)


requestLogsPayloadDecoder : Decode.Decoder RequestLogsPayload
requestLogsPayloadDecoder =
    Decode.map3 RequestLogsPayload
        (Decode.field "buffer" Decode.int)
        (Decode.field "totalCaptured" Decode.int)
        (Decode.field "logs" (Decode.list requestLogEntryDecoder))


requestLogEntryDecoder : Decode.Decoder RequestLogEntry
requestLogEntryDecoder =
    Decode.map8
        (\id method path route status durationMs timestamp queryCount ->
            { id = id
            , method = method
            , path = path
            , route = route
            , status = status
            , durationMs = durationMs
            , timestamp = timestamp
            , queryCount = queryCount
            , queryTimeMs = 0
            , errorMessage = ""
            , queries = []
            }
        )
        (Decode.field "id" Decode.string)
        (Decode.field "method" Decode.string)
        (Decode.field "path" Decode.string)
        (Decode.field "route" Decode.string)
        (Decode.field "status" Decode.int)
        (Decode.field "durationMs" Decode.float)
        (Decode.field "timestamp" Decode.string)
        (Decode.field "queryCount" Decode.int)
        |> Decode.andThen
            (\base ->
                Decode.map3
                    (\queryTimeMs errorMessage queries ->
                        { base
                            | queryTimeMs = queryTimeMs
                            , errorMessage = errorMessage
                            , queries = queries
                        }
                    )
                    (Decode.field "queryTimeMs" Decode.float)
                    (Decode.oneOf
                        [ Decode.field "errorMessage" Decode.string
                        , Decode.succeed ""
                        ]
                    )
                    (Decode.field "queries" (Decode.list requestLogQueryDecoder))
            )


requestLogQueryDecoder : Decode.Decoder RequestLogQuery
requestLogQueryDecoder =
    Decode.map5 RequestLogQuery
        (Decode.field "sql" Decode.string)
        (Decode.oneOf
            [ Decode.field "reason" (Decode.map Just Decode.string)
            , Decode.field "reason" (Decode.null Nothing)
            , Decode.succeed Nothing
            ]
        )
        (Decode.field "durationMs" Decode.float)
        (Decode.field "rowCount" Decode.int)
        (Decode.oneOf
            [ Decode.field "error" (Decode.map Just Decode.string)
            , Decode.field "error" (Decode.null Nothing)
            , Decode.succeed Nothing
            ]
        )


backupResponseDecoder : Decode.Decoder BackupResponse
backupResponseDecoder =
    Decode.map3 BackupResponse
        (Decode.field "path" Decode.string)
        (Decode.field "backupDir" Decode.string)
        (Decode.oneOf
            [ Decode.field "removed" (Decode.list Decode.string)
            , Decode.field "removed" (Decode.null [])
            , Decode.succeed []
            ]
        )


backupsDecoder : Decode.Decoder BackupsPayload
backupsDecoder =
    Decode.map2 BackupsPayload
        (Decode.oneOf
            [ Decode.field "backupDir" Decode.string
            , Decode.succeed ""
            ]
        )
        (Decode.field "backups" (Decode.list backupFileDecoder))


backupFileDecoder : Decode.Decoder BackupFile
backupFileDecoder =
    Decode.map4 BackupFile
        (Decode.field "path" Decode.string)
        (Decode.field "name" Decode.string)
        (Decode.field "sizeBytes" Decode.float)
        (Decode.field "createdAt" Decode.string)


findEntity : String -> Model -> Maybe Entity
findEntity entityName model =
    case model.schema of
        Loaded schema ->
            List.filter (\entity -> entity.name == entityName) schema.entities
                |> List.head

        _ ->
            Nothing


findAction : String -> Model -> Maybe ActionInfo
findAction actionName model =
    case model.schema of
        Loaded schema ->
            List.filter (\actionInfo -> actionInfo.name == actionName) schema.actions
                |> List.head

        _ ->
            Nothing


findInputAlias : String -> Model -> Maybe InputAliasInfo
findInputAlias aliasName model =
    case model.schema of
        Loaded schema ->
            List.filter (\aliasInfo -> aliasInfo.name == aliasName) schema.inputAliases
                |> List.head

        _ ->
            Nothing


preferredInitialEntity : Schema -> Maybe Entity
preferredInitialEntity schema =
    let
        authEntityName =
            schema.auth |> Maybe.map .userEntity

        nonAuthEntities =
            case authEntityName of
                Just entityName ->
                    List.filter (\entity -> entity.name /= entityName) schema.entities

                Nothing ->
                    schema.entities
    in
    case List.head nonAuthEntities of
        Just entity ->
            Just entity

        Nothing ->
            List.head schema.entities


splitEntitiesForSidebar : Model -> List Entity -> ( List Entity, List Entity )
splitEntitiesForSidebar model entities =
    case authInfoFromModel model of
        Just authInfo ->
            ( List.filter (\entity -> entity.name == authInfo.userEntity) entities
            , List.filter (\entity -> entity.name /= authInfo.userEntity) entities
            )

        Nothing ->
            ( [], entities )


actionFormDefaults : Model -> ActionInfo -> Dict String String
actionFormDefaults model actionInfo =
    case findInputAlias actionInfo.inputAlias model of
        Nothing ->
            Dict.empty

        Just aliasInfo ->
            aliasInfo.fields
                |> List.map
                    (\field ->
                        ( field.name
                        , if field.fieldType == "Bool" then
                            "false"

                          else
                            ""
                        )
                    )
                |> Dict.fromList


actionPayloadFromForm : Model -> ActionInfo -> Result String Encode.Value
actionPayloadFromForm model actionInfo =
    case findInputAlias actionInfo.inputAlias model of
        Nothing ->
            Err ("Input alias not found: " ++ actionInfo.inputAlias)

        Just aliasInfo ->
            aliasInfo.fields
                |> List.foldl (encodeActionField model.actionFormValues) (Ok [])
                |> Result.map Encode.object


encodeActionField : Dict String String -> InputAliasField -> Result String (List ( String, Encode.Value )) -> Result String (List ( String, Encode.Value ))
encodeActionField valuesByName field partialResult =
    case partialResult of
        Err message ->
            Err message

        Ok items ->
            let
                rawValue =
                    Dict.get field.name valuesByName
                        |> Maybe.withDefault ""
                        |> String.trim
            in
            if rawValue == "" then
                Err (fieldLabel field.name ++ " is required")

            else
                case field.fieldType of
                    "String" ->
                        Ok (( field.name, Encode.string rawValue ) :: items)

                    "Int" ->
                        case String.toInt rawValue of
                            Just value ->
                                Ok (( field.name, Encode.int value ) :: items)

                            Nothing ->
                                Err (fieldLabel field.name ++ " expects a whole number")

                    "Float" ->
                        case String.toFloat rawValue of
                            Just value ->
                                Ok (( field.name, Encode.float value ) :: items)

                            Nothing ->
                                Err (fieldLabel field.name ++ " expects a decimal number")

                    "Posix" ->
                        case parsePosixMillis rawValue of
                            Just value ->
                                Ok (( field.name, Encode.float value ) :: items)

                            Nothing ->
                                Err (fieldLabel field.name ++ " expects Unix milliseconds")

                    "Bool" ->
                        let
                            lowered =
                                String.toLower rawValue
                        in
                        if lowered == "true" || lowered == "1" || lowered == "yes" then
                            Ok (( field.name, Encode.bool True ) :: items)

                        else if lowered == "false" || lowered == "0" || lowered == "no" then
                            Ok (( field.name, Encode.bool False ) :: items)

                        else
                            Err (fieldLabel field.name ++ " expects a yes or no value")

                    _ ->
                        Err ("Unsupported input type " ++ field.fieldType ++ " for " ++ fieldLabel field.name)


rowId : Entity -> Row -> Maybe String
rowId entity rowValue =
    Dict.get entity.primaryKey rowValue
        |> Maybe.map valueToString


deleteConfirmationMessage : Entity -> Row -> String
deleteConfirmationMessage entity rowValue =
    case rowDisplayLabel entity rowValue of
        Just label ->
            "Delete " ++ entityLabel entity ++ " \"" ++ label ++ "\"? This action cannot be undone."

        Nothing ->
            "Delete this " ++ entityLabel entity ++ " entry? This action cannot be undone."


rowDisplayLabel : Entity -> Row -> Maybe String
rowDisplayLabel entity rowValue =
    let
        preferredFields =
            [ "name", "title", "email", "slug", "id", entity.primaryKey ]
    in
    preferredFields
        |> uniqueStrings
        |> List.filterMap (\fieldName -> rowFieldLabel entity fieldName rowValue)
        |> List.head


rowFieldLabel : Entity -> String -> Row -> Maybe String
rowFieldLabel entity fieldName rowValue =
    rowFieldValue entity fieldName rowValue
        |> Maybe.map String.trim
        |> Maybe.andThen
            (\value ->
                if value == "" || value == "null" then
                    Nothing

                else
                    Just value
            )


entityLabel : Entity -> String
entityLabel entity =
    entity.name
        |> String.trim
        |> String.toLower


fieldLabel : String -> String
fieldLabel =
    humanizeIdentifier


fieldPlaceholder : String -> String
fieldPlaceholder fieldName =
    "Enter " ++ String.toLower (fieldLabel fieldName)


fieldDefaultText : Field -> String
fieldDefaultText field =
    case field.defaultValue of
        Just defaultValue ->
            valueToString defaultValue

        Nothing ->
            case field.fieldType of
                BoolType ->
                    if field.optional then
                        ""

                    else
                        "false"

                _ ->
                    ""


placeholderForType : String -> String -> String
placeholderForType fieldName fieldType =
    if fieldType == "Posix" then
        "Unix milliseconds since epoch"

    else
        fieldPlaceholder fieldName


parsePosixMillis : String -> Maybe Float
parsePosixMillis rawValue =
    String.toFloat rawValue
        |> Maybe.andThen
            (\value ->
                if isWholeNumber value then
                    Just value

                else
                    Nothing
            )


isWholeNumber : Float -> Bool
isWholeNumber value =
    toFloat (round value) == value


rowFieldValue : Entity -> String -> Row -> Maybe String
rowFieldValue entity fieldName rowValue =
    let
        displayValue value =
            case List.filter (\field -> field.name == fieldName) entity.fields |> List.head of
                Just field ->
                    valueToDisplayString field.fieldType value

                Nothing ->
                    valueToString value
    in
    Dict.get fieldName rowValue
        |> Maybe.map displayValue


entityDisplayName : Entity -> String
entityDisplayName entity =
    humanizeIdentifier entity.name


humanizeIdentifier : String -> String
humanizeIdentifier identifier =
    let
        words =
            splitIdentifierWords identifier
                |> List.map String.toLower
    in
    case words of
        [] ->
            String.trim identifier

        first :: rest ->
            String.join " " (capitalizeWord first :: rest)


splitIdentifierWords : String -> List String
splitIdentifierWords identifier =
    let
        flushWord acc =
            if List.isEmpty acc.current then
                acc

            else
                { acc
                    | current = []
                    , words = String.fromList (List.reverse acc.current) :: acc.words
                }

        pushChar char acc =
            { acc
                | current = char :: acc.current
                , prevWasLowerOrDigit = Char.isLower char || Char.isDigit char
            }

        step char acc =
            if char == '_' || char == '-' || char == ' ' || char == '\n' || char == '\t' || char == '\u{000D}' then
                flushWord acc
                    |> (\state -> { state | prevWasLowerOrDigit = False })

            else if Char.isUpper char && acc.prevWasLowerOrDigit then
                flushWord acc
                    |> pushChar char

            else
                pushChar char acc

        finalState =
            String.toList (String.trim identifier)
                |> List.foldl step { current = [], words = [], prevWasLowerOrDigit = False }
                |> flushWord
    in
    List.reverse finalState.words


capitalizeWord : String -> String
capitalizeWord word =
    case String.uncons word of
        Just ( first, rest ) ->
            String.fromChar (Char.toUpper first) ++ rest

        Nothing ->
            ""


uniqueStrings : List String -> List String
uniqueStrings values =
    values
        |> List.foldl
            (\value acc ->
                if List.member value acc then
                    acc

                else
                    value :: acc
            )
            []
        |> List.reverse


replaceRow : Entity -> Row -> List Row -> List Row
replaceRow entity updated rows =
    case rowId entity updated of
        Nothing ->
            rows

        Just targetId ->
            rows
                |> List.map
                    (\rowValue ->
                        case rowId entity rowValue of
                            Just currentId ->
                                if currentId == targetId then
                                    updated

                                else
                                    rowValue

                            Nothing ->
                                rowValue
                    )


formDefaults : Model -> Dict String String
formDefaults model =
    case model.selectedEntity of
        Nothing ->
            Dict.empty

        Just entity ->
            entity.fields
                |> List.filter (\field -> not field.primary)
                |> List.map
                    (\field ->
                        ( field.name
                        , fieldDefaultText field
                        )
                    )
                |> Dict.fromList


formFromRow : Model -> Row -> Dict String String
formFromRow model rowValue =
    case model.selectedEntity of
        Nothing ->
            Dict.empty

        Just entity ->
            entity.fields
                |> List.filter (\field -> not field.primary)
                |> List.map
                    (\field ->
                        let
                            valueText =
                                Dict.get field.name rowValue
                                    |> Maybe.map valueToString
                                    |> Maybe.withDefault ""
                        in
                        ( field.name, valueText )
                    )
                |> Dict.fromList


view : Model -> Browser.Document Msg
view model =
    { title = currentAppName model
    , body =
        [ Element.layout
            [ Background.color (rgb255 244 245 247)
            , Font.family
                [ Font.typeface "Space Grotesk"
                , Font.typeface "IBM Plex Sans"
                , Font.sansSerif
                ]
            , Font.color (rgb255 29 36 44)
            , inFront (viewDeleteConfirmation model)
            ]
            (viewLayout model)
        ]
    }


viewLayout : Model -> Element Msg
viewLayout model =
    if model.sessionRestorePending then
        viewLoadingGate

    else if hasActiveSession model then
        if isCompactLayout model then
            column
                [ width fill
                , height fill
                , htmlAttribute (HtmlAttr.style "height" "100vh")
                , htmlAttribute (HtmlAttr.style "min-height" "100vh")
                , htmlAttribute (HtmlAttr.style "overflow" "hidden")
                ]
                [ viewMobileTopBar model
                , el
                    [ width fill
                    , height fill
                    , htmlAttribute (HtmlAttr.style "min-height" "0")
                    , inFront
                        (if model.mobileSidebarOpen then
                            viewMobileMoreSheet model

                         else
                            none
                        )
                    ]
                    (viewContent model)
                , viewMobileBottomNav model
                ]

        else
            row
                [ width fill
                , height fill
                , htmlAttribute (HtmlAttr.style "height" "100vh")
                , htmlAttribute (HtmlAttr.style "overflow" "hidden")
                ]
                [ viewSidebar model
                , viewContent model
                ]

    else
        viewAuthGate model


viewLoadingGate : Element Msg
viewLoadingGate =
    el
        [ width fill
        , height fill
        ]
        (el [ centerX, centerY ] (text "Loading..."))


viewMobileTopBar : Model -> Element Msg
viewMobileTopBar model =
    column
        [ width fill
        , Background.color (rgb255 18 22 28)
        , padding 16
        , spacing 4
        ]
        [ el [ Font.size 22, Font.bold, Font.color (rgb255 240 245 250) ] (text (currentAppName model)) ]


mobileNavEntries : Model -> List MobileNavEntry
mobileNavEntries model =
    let
        workspace =
            currentWorkspace model

        ( authEntities, crudEntities, actions ) =
            case model.schema of
                Loaded schema ->
                    splitEntitiesForSidebar model schema.entities
                        |> (\( authOnly, crudOnly ) -> ( authOnly, crudOnly, schema.actions ))

                _ ->
                    ( [], [], [] )

        entityEntry entity =
            { label = entityDisplayName entity
            , onPress = SelectEntity entity.name
            , selected =
                not model.authToolsOpen
                    && not model.performanceMode
                    && not model.requestLogsMode
                    && not model.databaseMode
                    && (case model.selectedEntity of
                            Just current ->
                                current.name == entity.name

                            Nothing ->
                                False
                       )
            }

        actionEntry actionInfo =
            { label = humanizeIdentifier actionInfo.name
            , onPress = SelectAction actionInfo.name
            , selected =
                not model.authToolsOpen
                    && not model.performanceMode
                    && not model.requestLogsMode
                    && not model.databaseMode
                    && (case model.selectedAction of
                            Just current ->
                                current.name == actionInfo.name

                            Nothing ->
                                False
                       )
            }

        authEntries =
            if hasAnyAuthInfo model then
                { label =
                    if workspace == AppWorkspace then
                        "Account"

                    else
                        "Authorization"
                , onPress = ToggleAuthTools
                , selected = model.authToolsOpen
                }
                    :: (if workspace == AdminWorkspace then
                            List.map entityEntry authEntities

                        else
                            []
                       )

            else
                []

        systemEntries =
            if isAdminProfile model && workspace == AdminWorkspace then
                [ { label = "Monitoring", onPress = SelectPerformance, selected = model.performanceMode && not model.authToolsOpen }
                , { label = "Logs", onPress = SelectRequestLogs, selected = model.requestLogsMode && not model.authToolsOpen }
                , { label = "Database", onPress = SelectDatabase, selected = model.databaseMode && not model.authToolsOpen }
                ]

            else
                []
    in
    List.map entityEntry crudEntities
        ++ List.map actionEntry actions
        ++ systemEntries
        ++ authEntries


mobileWorkspaceEntries : Model -> List MobileNavEntry
mobileWorkspaceEntries model =
    if isAdminProfile model then
        [ { label = "App"
          , onPress = SwitchWorkspace AppWorkspace
          , selected = currentWorkspace model == AppWorkspace
          }
        , { label = "Admin"
          , onPress = SwitchWorkspace AdminWorkspace
          , selected = currentWorkspace model == AdminWorkspace
          }
        ]

    else
        []


mobileVisibleNavEntries : Model -> List MobileNavEntry
mobileVisibleNavEntries model =
    let
        entries =
            mobileNavEntries model

        shouldShowMore =
            isAdminProfile model || List.length entries > 5
    in
    if shouldShowMore then
        List.take 4 entries

    else
        entries


mobileOverflowNavEntries : Model -> List MobileNavEntry
mobileOverflowNavEntries model =
    let
        entries =
            mobileNavEntries model

        shouldShowMore =
            isAdminProfile model || List.length entries > 5
    in
    if shouldShowMore then
        List.drop 4 entries

    else
        []


viewMobileBottomNav : Model -> Element Msg
viewMobileBottomNav model =
    let
        visibleEntries =
            mobileVisibleNavEntries model

        overflowEntries =
            mobileOverflowNavEntries model

        shouldShowMore =
            not (List.isEmpty overflowEntries) || isAdminProfile model

        moreSelected =
            if model.mobileSidebarOpen then
                True

            else
                List.any .selected overflowEntries

        navButton entry =
            let
                buttonSelected =
                    not model.mobileSidebarOpen && entry.selected
            in
            Input.button
                [ width fill
                , Background.color
                    (if buttonSelected then
                        rgb255 236 243 255

                     else
                        rgba255 255 255 0 0
                    )
                , Font.color
                    (if buttonSelected then
                        rgb255 53 84 138

                     else
                        rgb255 104 116 134
                    )
                , Border.rounded 14
                , paddingEach { top = 9, right = 8, bottom = 9, left = 8 }
                , cupertinoFocusRing
                , htmlAttribute (HtmlAttr.style "outline" "none")
                , htmlAttribute (HtmlAttr.style "box-shadow" "none")
                , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
                ]
                { onPress =
                    Just
                        entry.onPress
                , label =
                    el
                        [ width fill
                        , centerX
                        , Font.center
                        , Font.size 11
                        , Font.semiBold
                        ]
                        (text entry.label)
                }

        moreButton =
            Input.button
                [ width fill
                , Background.color
                    (if moreSelected then
                        rgb255 236 243 255

                     else
                        rgba255 255 255 0 0
                    )
                , Font.color
                    (if moreSelected then
                        rgb255 53 84 138

                     else
                        rgb255 104 116 134
                    )
                , Border.rounded 14
                , paddingEach { top = 9, right = 8, bottom = 9, left = 8 }
                , cupertinoFocusRing
                , htmlAttribute (HtmlAttr.style "outline" "none")
                , htmlAttribute (HtmlAttr.style "box-shadow" "none")
                , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
                ]
                { onPress = Just ToggleMobileSidebar
                , label =
                    el
                        [ width fill
                        , centerX
                        , Font.center
                        , Font.size 11
                        , Font.semiBold
                        ]
                        (text "More")
                }
    in
    if List.isEmpty visibleEntries && not shouldShowMore then
        none

    else
        row
            [ width fill
            , paddingEach { top = 8, right = 14, bottom = 14, left = 14 }
            , htmlAttribute (HtmlAttr.style "padding-bottom" "calc(34px + env(safe-area-inset-bottom))")
            ]
            [ row
                [ width fill
                , spacing 6
                , Background.color (rgba255 250 252 255 226)
                , Border.rounded 22
                , Border.width 1
                , Border.color (rgba255 226 234 244 220)
                , paddingEach { top = 8, right = 8, bottom = 8, left = 8 }
                , htmlAttribute (HtmlAttr.style "backdrop-filter" "blur(24px)")
                , htmlAttribute (HtmlAttr.style "-webkit-backdrop-filter" "blur(24px)")
                , htmlAttribute (HtmlAttr.style "box-shadow" "0 12px 30px rgba(15,23,42,0.08), inset 0 1px 0 rgba(255,255,255,0.72)")
                ]
                (List.map navButton visibleEntries
                    ++ (if shouldShowMore then
                            [ moreButton ]

                        else
                            []
                       )
                )
            ]


viewMobileMoreSheet : Model -> Element Msg
viewMobileMoreSheet model =
    let
        overflowEntries =
            mobileOverflowNavEntries model

        workspaceEntries =
            mobileWorkspaceEntries model

        sheetButton entry =
            Input.button
                [ width fill
                , Border.rounded 14
                , Background.color
                    (if entry.selected then
                        rgb255 229 238 255

                     else
                        rgb255 244 247 252
                    )
                , Font.color
                    (if entry.selected then
                        rgb255 45 97 209

                     else
                        rgb255 43 56 74
                    )
                , paddingEach { top = 13, right = 14, bottom = 13, left = 14 }
                , cupertinoFocusRing
                , htmlAttribute (HtmlAttr.style "outline" "none")
                , htmlAttribute (HtmlAttr.style "box-shadow" "none")
                , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
                ]
                { onPress =
                    Just
                        (if entry.selected then
                            CloseMobileSidebar

                         else
                            entry.onPress
                        )
                , label = paragraph [ centerX ] [ text entry.label ]
                }

        workspaceButton entry =
            Input.button
                [ width fill
                , Border.rounded 14
                , Border.width 2
                , Border.color
                    (if entry.selected then
                        rgb255 225 232 242

                     else
                        rgba255 255 255 0 0
                    )
                , Background.color
                    (if entry.selected then
                        rgb255 248 251 255

                     else
                        rgba255 255 255 0 0
                    )
                , Font.color
                    (if entry.selected then
                        rgb255 53 84 138

                     else
                        rgb255 78 92 112
                    )
                , paddingEach { top = 11, right = 10, bottom = 11, left = 10 }
                , cupertinoFocusRing
                , htmlAttribute (HtmlAttr.style "outline" "none")
                , htmlAttribute
                    (HtmlAttr.style
                        "box-shadow"
                        (if entry.selected then
                            "0 1px 3px rgba(31,41,55,0.10), inset 0 1px 0 rgba(255,255,255,0.72)"

                         else
                            "none"
                        )
                    )
                ]
                { onPress =
                    Just
                        (if entry.selected then
                            CloseMobileSidebar

                         else
                            entry.onPress
                        )
                , label =
                    paragraph
                        [ centerX
                        , Font.size 12
                        , Font.semiBold
                        ]
                        [ text entry.label ]
                }
    in
    el
        [ width fill
        , height fill
        , Background.color (rgba255 24 29 36 138)
        ]
        (column
            [ width fill
            , height fill
            ]
            [ el [ width fill, height fill ] none
            , column
                [ width fill
                , spacing 12
                , Background.color (rgba255 250 252 255 240)
                , padding 16
                , Border.roundEach { topLeft = 24, topRight = 24, bottomLeft = 0, bottomRight = 0 }
                , Border.width 1
                , Border.color (rgba255 226 234 244 236)
                , htmlAttribute (HtmlAttr.style "backdrop-filter" "blur(28px)")
                , htmlAttribute (HtmlAttr.style "-webkit-backdrop-filter" "blur(28px)")
                , htmlAttribute (HtmlAttr.style "box-shadow" "0 -10px 30px rgba(15,23,42,0.10), inset 0 1px 0 rgba(255,255,255,0.72)")
                ]
                (row [ width fill, spacing 12 ]
                    [ el [ Font.size 20, Font.bold, Font.color (rgb255 34 47 64) ] (text "More")
                    , el [ width fill ] none
                    , Input.button
                        [ Background.color (rgb255 236 240 246)
                        , Font.color (rgb255 62 74 92)
                        , Border.rounded 12
                        , paddingEach { top = 8, right = 12, bottom = 8, left = 12 }
                        , cupertinoFocusRing
                        , htmlAttribute (HtmlAttr.style "outline" "none")
                        , htmlAttribute (HtmlAttr.style "box-shadow" "none")
                        , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
                        ]
                        { onPress = Just CloseMobileSidebar
                        , label = text "Close"
                        }
                    ]
                    :: (if List.isEmpty workspaceEntries then
                            []

                        else
                            [ column
                                [ width fill
                                , spacing 10
                                , Background.color (rgb255 233 239 247)
                                , Border.rounded 18
                                , Border.width 1
                                , Border.color (rgb255 219 228 240)
                                , padding 12
                                , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.72)")
                                ]
                                [ el [ Font.size 11, Font.bold, Font.color (rgb255 109 121 139) ] (text "WORKSPACE")
                                , row
                                    [ width fill
                                    , spacing 8
                                    , Background.color (rgb255 246 249 253)
                                    , Border.rounded 16
                                    , Border.width 1
                                    , Border.color (rgb255 228 235 244)
                                    , padding 6
                                    ]
                                    (List.map workspaceButton workspaceEntries)
                                ]
                            ]
                       )
                    ++ (if List.isEmpty overflowEntries then
                            []

                        else
                            el [ Font.size 11, Font.bold, Font.color (rgb255 124 136 154) ] (text "MORE")
                                :: List.map sheetButton overflowEntries
                       )
                )
            ]
        )


viewAuthGate : Model -> Element Msg
viewAuthGate model =
    let
        compact =
            isCompactLayout model

        authDisplayAppName =
            currentAppName model
                |> humanizeIdentifier

        firstAdminMode =
            authFirstAdminMode model

        authStage =
            currentAuthStage model

        ( stageTitle, stageSubtitle ) =
            case authStage of
                AuthStageEmail ->
                    if firstAdminMode then
                        ( "Set up the first admin"
                        , "Enter the email that should receive the first access code."
                        )

                    else
                        ( "Sign in"
                        , "We will send you a 6-digit access code."
                        )

                AuthStageCode ->
                    ( "Check your email"
                    , ""
                    )

                AuthStageSession ->
                    ( "Session ready"
                    , "Your admin session is active."
                    )

        authCardBody =
            case authStage of
                AuthStageEmail ->
                    viewAuthEmailStage model
                        firstAdminMode
                        (if firstAdminMode then
                            "Send access code"

                         else
                            "Continue"
                        )
                        (if firstAdminMode then
                            BootstrapFirstAdmin

                         else
                            RequestAuthCode
                        )
                        (model.authSubmitting == Just AuthSubmitSendingCode)

                AuthStageCode ->
                    viewAuthCodeStage model
                        firstAdminMode
                        "Send code again"
                        (if firstAdminMode then
                            BootstrapFirstAdmin

                         else
                            RequestAuthCode
                        )
                        (model.authSubmitting == Just AuthSubmitSigningIn)
                        (model.authSubmitting == Just AuthSubmitSendingCode)

                AuthStageSession ->
                    viewAuthSessionStage model
    in
    el
        [ width fill
        , height fill
        , padding
            (if compact then
                16

             else
                24
            )
        ]
        (el
            [ centerX
            , centerY
            , width (fill |> maximum 460)
            ]
            (column
                [ width fill
                , spacing 14
                ]
                [ column
                    (cupertinoPanelAttrs 18 28
                        ++ [ htmlAttribute (HtmlAttr.class "auth-stage auth-gate-card") ]
                    )
                    [ column
                        [ width fill
                        , spacing 8
                        ]
                        ([ el
                            [ Font.size 28
                            , Font.bold
                            , centerX
                            ]
                            (text authDisplayAppName)
                         , el
                            [ Font.size 12
                            , Font.color (rgb255 84 121 224)
                            , centerX
                            ]
                            (text
                                (case authStage of
                                    AuthStageEmail ->
                                        "Step 1 of 2"

                                    AuthStageCode ->
                                        "Step 2 of 2"

                                    AuthStageSession ->
                                        "Ready"
                                )
                            )
                         , paragraph
                            [ centerX
                            , Font.size 22
                            , Font.bold
                            ]
                            [ text stageTitle ]
                         ]
                            ++ (if String.isEmpty stageSubtitle then
                                    []

                                else
                                    [ paragraph
                                        [ centerX
                                        , Font.size 14
                                        , Font.color (rgb255 93 103 120)
                                        ]
                                        [ text stageSubtitle ]
                                    ]
                               )
                        )
                    , authCardBody
                    ]
                , viewAuthFlashSlot model
                ]
            )
        )


viewAuthFlashSlot : Model -> Element Msg
viewAuthFlashSlot model =
    let
        minHeightPx =
            if isCompactLayout model then
                92

            else
                78
    in
    el
        [ width fill
        , htmlAttribute (HtmlAttr.style "min-height" (String.fromInt minHeightPx ++ "px"))
        ]
        (viewFlash model)


viewSidebar : Model -> Element Msg
viewSidebar model =
    let
        compact =
            isCompactLayout model

        workspace =
            currentWorkspace model

        sidebarBackground =
            if compact then
                rgb255 18 22 28

            else
                rgb255 232 237 245

        sidebarTitleColor =
            if compact then
                rgb255 240 245 250

            else
                rgb255 28 38 54

        sidebarSubtitleColor =
            if compact then
                rgb255 144 158 179

            else
                rgb255 112 124 144

        sidebarSectionColor =
            if compact then
                rgb255 118 136 160

            else
                rgb255 132 143 159

        sidebarItemBackground selected =
            if compact then
                if selected then
                    rgb255 54 94 217

                else
                    rgb255 24 29 36

            else if selected then
                rgb255 238 244 255

            else
                rgb255 247 249 252

        sidebarItemTextColor selected =
            if compact then
                rgb255 244 246 248

            else if selected then
                rgb255 45 77 136

            else
                rgb255 45 57 75

        sidebarItemSubtitleColor =
            if compact then
                rgb255 170 181 196

            else
                rgb255 126 138 156

        workspaceToggleBackground selected =
            if compact then
                sidebarItemBackground selected

            else if selected then
                rgb255 233 241 255

            else
                rgba255 255 255 0 0

        workspaceToggleTextColor selected =
            if compact then
                sidebarItemTextColor selected

            else if selected then
                rgb255 45 77 136

            else
                rgb255 78 92 112

        workspaceToggleBorderColor =
            rgb255 220 228 240

        sidebarButtonAttrs selected backgroundColor textColor paddingValues =
            [ width fill
            , Border.rounded 10
            , Border.width 1
            , Border.color
                (if selected then
                    rgb255 227 235 246

                 else
                    rgba255 255 255 0 0
                )
            , Background.color backgroundColor
            , Font.color textColor
            , paddingEach paddingValues
            , cupertinoFocusRing
            , htmlAttribute (HtmlAttr.style "outline" "none")
            , htmlAttribute
                (HtmlAttr.style
                    "box-shadow"
                    (if selected && not compact then
                        "0 8px 20px rgba(110, 139, 196, 0.10), inset 0 1px 0 rgba(255,255,255,0.55)"

                     else
                        "none"
                    )
                )
            ]

        workspaceToggleAttrs backgroundColor textColor paddingValues =
            [ width fill
            , Border.rounded 10
            , Border.width 1
            , Border.color workspaceToggleBorderColor
            , Background.color backgroundColor
            , Font.color textColor
            , paddingEach paddingValues
            , cupertinoFocusRing
            , htmlAttribute (HtmlAttr.style "outline" "none")
            , htmlAttribute
                (HtmlAttr.style
                    "box-shadow"
                    (if backgroundColor == rgb255 233 241 255 then
                        "0 1px 3px rgba(31, 41, 55, 0.10), inset 0 1px 0 rgba(255,255,255,0.72)"

                     else
                        "none"
                    )
                )
            ]

        ( authEntities, crudEntities, actions ) =
            case model.schema of
                Loaded schema ->
                    let
                        ( authOnly, crudOnly ) =
                            splitEntitiesForSidebar model schema.entities
                    in
                    ( authOnly, crudOnly, schema.actions )

                _ ->
                    ( [], [], [] )

        sidebarItemLabel : String -> Maybe String -> Element Msg
        sidebarItemLabel title maybeSubtitle =
            if workspace == AppWorkspace then
                paragraph [ alignLeft ] [ text title ]

            else
                column [ width fill, spacing 4 ]
                    (paragraph [ alignLeft ] [ text title ]
                        :: (case maybeSubtitle of
                                Just subtitle ->
                                    [ paragraph [ alignLeft, Font.size 11, Font.color sidebarItemSubtitleColor ] [ text subtitle ] ]

                                Nothing ->
                                    []
                           )
                    )

        workspaceSwitch : List (Element Msg)
        workspaceSwitch =
            if isAdminProfile model then
                [ el
                    [ width fill
                    , Background.color (rgb255 229 236 246)
                    , Border.rounded 14
                    , Border.width 1
                    , Border.color (rgb255 210 220 235)
                    , padding 3
                    , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.7)")
                    ]
                    (row [ width fill, spacing 4 ]
                        [ Input.button
                            (workspaceToggleAttrs
                                (workspaceToggleBackground (workspace == AppWorkspace))
                                (workspaceToggleTextColor (workspace == AppWorkspace))
                                { top = 3, right = 10, bottom = 3, left = 10 }
                            )
                            { onPress = Just (SwitchWorkspace AppWorkspace)
                            , label = paragraph [ centerX, Font.size 14 ] [ text "App" ]
                            }
                        , Input.button
                            (workspaceToggleAttrs
                                (workspaceToggleBackground (workspace == AdminWorkspace))
                                (workspaceToggleTextColor (workspace == AdminWorkspace))
                                { top = 3, right = 10, bottom = 3, left = 10 }
                            )
                            { onPress = Just (SwitchWorkspace AdminWorkspace)
                            , label = paragraph [ centerX, Font.size 14 ] [ text "Admin" ]
                            }
                        ]
                    )
                ]

            else
                []

        entityButton entity =
            let
                selected =
                    not model.authToolsOpen
                        && not model.performanceMode
                        && not model.requestLogsMode
                        && not model.databaseMode
                        && (case model.selectedEntity of
                                Just current ->
                                    current.name == entity.name

                                Nothing ->
                                    False
                           )
            in
            Input.button
                (sidebarButtonAttrs
                    selected
                    (sidebarItemBackground selected)
                    (sidebarItemTextColor selected)
                    { top = 12, right = 12, bottom = 12, left = 12 }
                )
                { onPress = Just (SelectEntity entity.name)
                , label =
                    sidebarItemLabel entity.name (Just entity.resource)
                }

        actionEndpointCard : ActionInfo -> Element Msg
        actionEndpointCard actionInfo =
            let
                selected =
                    not model.authToolsOpen
                        && not model.performanceMode
                        && not model.requestLogsMode
                        && not model.databaseMode
                        && (case model.selectedAction of
                                Just current ->
                                    current.name == actionInfo.name

                                Nothing ->
                                    False
                           )
            in
            Input.button
                (sidebarButtonAttrs
                    selected
                    (sidebarItemBackground selected)
                    (sidebarItemTextColor selected)
                    { top = 12, right = 12, bottom = 12, left = 12 }
                )
                { onPress = Just (SelectAction actionInfo.name)
                , label =
                    sidebarItemLabel actionInfo.name (Just ("/actions/" ++ actionInfo.name))
                }

        performanceButton : Element Msg
        performanceButton =
            let
                selected =
                    model.performanceMode && not model.authToolsOpen
            in
            Input.button
                (sidebarButtonAttrs
                    selected
                    (sidebarItemBackground selected)
                    (sidebarItemTextColor selected)
                    { top = 12, right = 12, bottom = 12, left = 12 }
                )
                { onPress = Just SelectPerformance
                , label =
                    sidebarItemLabel "Monitoring" (Just "/_mar/perf")
                }

        requestLogsButton : Element Msg
        requestLogsButton =
            let
                selected =
                    model.requestLogsMode && not model.authToolsOpen
            in
            Input.button
                (sidebarButtonAttrs
                    selected
                    (sidebarItemBackground selected)
                    (sidebarItemTextColor selected)
                    { top = 12, right = 12, bottom = 12, left = 12 }
                )
                { onPress = Just SelectRequestLogs
                , label =
                    sidebarItemLabel "Logs" (Just "/_mar/request-logs")
                }

        databaseButton : Element Msg
        databaseButton =
            let
                selected =
                    model.databaseMode && not model.authToolsOpen
            in
            Input.button
                (sidebarButtonAttrs
                    selected
                    (sidebarItemBackground selected)
                    (sidebarItemTextColor selected)
                    { top = 12, right = 12, bottom = 12, left = 12 }
                )
                { onPress = Just SelectDatabase
                , label =
                    sidebarItemLabel "Database" (Just "/_mar/backups")
                }

        authToolsButton : Element Msg
        authToolsButton =
            let
                selected =
                    model.authToolsOpen
            in
            Input.button
                (sidebarButtonAttrs
                    selected
                    (sidebarItemBackground selected)
                    (sidebarItemTextColor selected)
                    { top = 12, right = 12, bottom = 12, left = 12 }
                )
                { onPress = Just ToggleAuthTools
                , label =
                    sidebarItemLabel
                        (if workspace == AppWorkspace then
                            "Account"

                         else
                            "Authorization"
                        )
                        (Just "/auth")
                }
    in
    column
        ([ width
            (if compact then
                fill

             else
                px 280
            )
         , Background.color sidebarBackground
         , padding
            (if compact then
                16

             else
                18
            )
         , spacing 12
         ]
            ++ (if compact then
                    [ width fill ]

                else
                    [ height fill
                    , scrollbarY
                    , Border.widthEach { top = 0, right = 1, bottom = 0, left = 0 }
                    , Border.color (rgb255 214 221 233)
                    ]
               )
        )
        (List.concat
            [ if compact then
                []

              else
                [ row [ width fill, spacing 8 ]
                    [ el [ Font.size 24, Font.bold, Font.color sidebarTitleColor ] (text (currentAppName model)) ]
                , if workspace == AppWorkspace && not (isAdminProfile model) then
                    none

                  else
                    el [ Font.size 13, Font.color sidebarSubtitleColor ]
                        (text
                            (if workspace == AppWorkspace then
                                "App workspace"

                             else
                                "System workspace"
                            )
                        )
                ]
            , workspaceSwitch
            , if hasAnyAuthInfo model then
                el (cupertinoSectionHeaderAttrs ++ [ Font.color sidebarSectionColor ])
                    (text
                        (if workspace == AppWorkspace then
                            "ACCOUNT"

                         else
                            "AUTH"
                        )
                    )
                    :: authToolsButton
                    :: (if workspace == AdminWorkspace then
                            List.map entityButton authEntities

                        else
                            []
                       )

              else
                []
            , if List.isEmpty crudEntities then
                []

              else
                el
                    ([ paddingEach { top = 4, right = 0, bottom = 0, left = 0 }
                     , Font.color sidebarSectionColor
                     ]
                        ++ cupertinoSectionHeaderAttrs
                    )
                    (text
                        (if workspace == AppWorkspace then
                            "EXPLORE"

                         else
                            "CRUD"
                        )
                    )
                    :: List.map entityButton crudEntities
            , if List.isEmpty actions then
                []

              else
                el
                    ([ paddingEach { top = 4, right = 0, bottom = 0, left = 0 }
                     , Font.color sidebarSectionColor
                     ]
                        ++ cupertinoSectionHeaderAttrs
                    )
                    (text
                        (if workspace == AppWorkspace then
                            "FLOWS"

                         else
                            "ACTIONS"
                        )
                    )
                    :: List.map actionEndpointCard actions
            , if isAdminProfile model && workspace == AdminWorkspace then
                [ el
                    ([ paddingEach { top = 4, right = 0, bottom = 0, left = 0 }
                     , Font.color sidebarSectionColor
                     ]
                        ++ cupertinoSectionHeaderAttrs
                    )
                    (text "SYSTEM")
                , performanceButton
                , requestLogsButton
                , databaseButton
                ]

              else
                []
            ]
        )


viewContent : Model -> Element Msg
viewContent model =
    let
        compact =
            isCompactLayout model

        showInspectorAsScreen =
            compact
                && (model.formMode /= FormHidden || model.selectedRow /= Nothing)
    in
    column
        ([ width fill
         , height fill
         , paddingEach
            { top =
                if compact then
                    16

                else
                    12
            , right =
                if compact then
                    16

                else
                    24
            , bottom =
                if compact then
                    16

                else
                    12
            , left =
                if compact then
                    16

                else
                    12
            }
         , spacing
            (if compact then
                16

             else
                12
            )
         , htmlAttribute (HtmlAttr.style "min-height" "0")
         ]
            ++ (if compact then
                    [ scrollbarY ]

                else
                    []
               )
        )
        [ viewAuthToolsPanel model
        , viewFlash model
        , if model.authToolsOpen then
            none

          else if model.performanceMode then
            viewPerformancePanel model

          else if model.requestLogsMode then
            viewRequestLogsPanel model

          else if model.databaseMode then
            viewDatabasePanel model

          else
            case model.selectedAction of
                Just _ ->
                    viewDataPanel model

                Nothing ->
                    if showInspectorAsScreen then
                        viewInspector model

                    else if compact then
                        viewDataPanel model

                    else
                        row
                            [ width fill
                            , height fill
                            , spacing 12
                            , htmlAttribute (HtmlAttr.style "min-height" "0")
                            ]
                            [ viewDataPanel model
                            , viewInspector model
                            ]
        ]


viewAuthToolsPanel : Model -> Element Msg
viewAuthToolsPanel model =
    if not model.authToolsOpen then
        none

    else
        let
            workspace =
                currentWorkspace model

            maybeAppAuth =
                authInfoFromModel model

            activeScope =
                activeAuthScope

            authStage =
                currentAuthStage model

            activeBadgeText =
                if workspace == AppWorkspace then
                    if firstAdminMode then
                        [ badge "First admin setup" ]

                    else
                        []

                else
                    case activeScope of
                        AppAuthScope ->
                            [ badge "POST /auth/request-code"
                            , badge "POST /auth/login"
                            , badge "GET /auth/me"
                            , badge "POST /auth/logout"
                            ]

            transportText =
                if workspace == AppWorkspace then
                    ""

                else
                    case activeScope of
                        AppAuthScope ->
                            case maybeAppAuth of
                                Just appAuth ->
                                    "Transport: " ++ appAuth.emailTransport ++ " | User entity: " ++ appAuth.userEntity

                                Nothing ->
                                    "User authentication is not enabled."

            appHasNoUsers =
                case maybeAppAuth of
                    Just appAuth ->
                        appAuth.needsBootstrap

                    Nothing ->
                        False

            firstAdminMode =
                case activeScope of
                    AppAuthScope ->
                        appHasNoUsers || model.firstAdminCodeRequested

            authFlowTitle =
                if workspace == AppWorkspace then
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                "Set up first admin"

                            else
                                "Sign in"

                        AuthStageCode ->
                            if firstAdminMode then
                                "Confirm first admin"

                            else
                                "Enter code"

                        AuthStageSession ->
                            "Your account"

                else
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                "Set up first admin"

                            else
                                "Login"

                        AuthStageCode ->
                            if firstAdminMode then
                                "Confirm first admin"

                            else
                                "Enter code"

                        AuthStageSession ->
                            "Session"

            authFlowSteps =
                if workspace == AppWorkspace then
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                [ "This email will become the first administrator for this app."
                                , "Mar will send a 6-digit verification code to finish setup."
                                ]

                            else
                                [ "We will send a 6-digit code to your email." ]

                        AuthStageCode ->
                            if firstAdminMode then
                                [ "Enter the verification code sent to the first admin email."
                                , "After a successful login, Mar will open the admin interface."
                                ]

                            else
                                [ "Enter the code we sent to continue." ]

                        AuthStageSession ->
                            []

                else
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                [ "Enter the email for the first administrator."
                                , "Mar will send a 6-digit verification code for the first admin setup."
                                ]

                            else
                                [ "Enter your email to receive a 6-digit login code."
                                , "You will confirm the code on the next screen."
                                ]

                        AuthStageCode ->
                            if firstAdminMode then
                                [ "Use the verification code sent to the first admin email."
                                , "After a successful login, Mar will open the admin interface."
                                ]

                            else
                                [ "Enter the login code sent to your email."
                                , "After a successful login, Mar will open the admin interface."
                                ]

                        AuthStageSession ->
                            []

            authFlowStepRow : Int -> String -> Element Msg
            authFlowStepRow index description =
                row [ width fill, spacing 8 ]
                    [ el [ Font.bold, Font.size 12, Font.color (rgb255 70 89 120) ] (text (String.fromInt (index + 1) ++ "."))
                    , paragraph [ width fill, Font.size 12, Font.color (rgb255 93 103 120) ] [ text description ]
                    ]

            authPanelTitle =
                if firstAdminMode then
                    "First admin setup"

                else if workspace == AppWorkspace then
                    "Account"

                else
                    "Authorization"

            firstAdminNotice =
                if firstAdminMode then
                    Just
                        (column
                            [ width fill
                            , spacing 6
                            , Background.color (rgb255 255 244 214)
                            , Border.rounded 12
                            , Border.width 1
                            , Border.color (rgb255 240 204 104)
                            , padding 12
                            ]
                            [ el [ Font.bold, Font.size 13, Font.color (rgb255 94 71 15) ] (text "Important setup step")
                            , paragraph
                                [ width fill
                                , Font.size 12
                                , Font.color (rgb255 120 91 23)
                                ]
                                [ text "You are creating the first administrator for this app. This is a one-time setup before normal sign-in starts." ]
                            ]
                        )

                else
                    Nothing

            emailActionLabel =
                if firstAdminMode then
                    "Create first admin"

                else
                    "Send code"

            resendActionLabel =
                if firstAdminMode then
                    "Send code again"

                else
                    "Resend code"

            emailSubmitMsg =
                if firstAdminMode then
                    BootstrapFirstAdmin

                else
                    RequestAuthCode

            sendButtonLoading =
                model.authSubmitting == Just AuthSubmitSendingCode

            loginButtonLoading =
                model.authSubmitting == Just AuthSubmitSigningIn

            authPanelBody =
                case authStage of
                    AuthStageEmail ->
                        viewAuthEmailStage model firstAdminMode emailActionLabel emailSubmitMsg sendButtonLoading

                    AuthStageCode ->
                        viewAuthCodeStage model firstAdminMode resendActionLabel emailSubmitMsg loginButtonLoading sendButtonLoading

                    AuthStageSession ->
                        viewAuthSessionStage model
        in
        if not (hasAnyAuthInfo model) then
            none

        else
            column
                (cupertinoPanelAttrs 12 16)
                [ viewPanelHeader (isCompactLayout model)
                    authPanelTitle
                    (if String.trim transportText == "" then
                        []

                     else
                        [ paragraph
                            [ width fill
                            , Font.size 13
                            , Font.color (rgb255 93 103 120)
                            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
                            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
                            ]
                            [ text transportText ]
                        ]
                    )
                    []
                , Maybe.withDefault none firstAdminNotice
                , if List.isEmpty activeBadgeText then
                    none

                  else
                    wrappedRow [ width fill, spacing 8 ] activeBadgeText
                , if List.isEmpty authFlowSteps then
                    none

                  else
                    column
                        [ width fill
                        , spacing 6
                        , Background.color
                            (if firstAdminMode then
                                rgb255 255 249 234

                             else
                                rgb255 245 248 255
                            )
                        , Border.rounded 10
                        , Border.width 1
                        , Border.color
                            (if firstAdminMode then
                                rgb255 243 221 156

                             else
                                rgb255 199 214 242
                            )
                        , padding 10
                        ]
                        (el [ Font.bold, Font.size 13, Font.color (rgb255 70 89 120) ] (text authFlowTitle)
                            :: List.indexedMap authFlowStepRow authFlowSteps
                        )
                , authPanelBody
                ]


viewAuthEmailStage : Model -> Bool -> String -> Msg -> Bool -> Element Msg
viewAuthEmailStage model firstAdminMode actionLabel submitMsg isLoading =
    let
        emailPlaceholder =
            if firstAdminMode then
                "admin@email.com"

            else
                "user@email.com"
    in
    column
        [ width fill
        , spacing 12
        , htmlAttribute (HtmlAttr.class "auth-stage auth-stage-email")
        ]
        [ Input.text
            (cupertinoTextInputAttrs
                ++ [ htmlAttribute (HtmlAttr.type_ "email")
                   , htmlAttribute (HtmlAttr.attribute "autocomplete" "email")
                   , htmlAttribute (HtmlAttr.attribute "inputmode" "email")
                   ]
                ++ (if isLoading then
                        []

                    else
                        [ onEnter submitMsg ]
                   )
            )
            { onChange = SetAuthEmail
            , text = model.authEmail
            , placeholder = Just (Input.placeholder [] (text emailPlaceholder))
            , label = Input.labelAbove [ Font.size 12 ] (text "Email")
            }
        , el [ width fill ]
            (authActionButton
                (if firstAdminMode then
                    rgb255 242 180 42

                 else
                    rgb255 84 121 224
                )
                (if firstAdminMode then
                    rgb255 40 33 16

                 else
                    rgb255 246 248 252
                )
                (if isLoading then
                    Nothing

                 else
                    Just submitMsg
                )
                actionLabel
            )
        , authStatusLine
            model.authInlineMessage
            (if isLoading then
                Just "Sending code..."

             else
                Nothing
            )
        ]


viewAuthCodeStage : Model -> Bool -> String -> Msg -> Bool -> Bool -> Element Msg
viewAuthCodeStage model firstAdminMode resendLabel resendMsg loginLoading resendLoading =
    let
        emailText =
            String.trim model.authEmail
    in
    column
        [ width fill
        , spacing 12
        , htmlAttribute (HtmlAttr.class "auth-stage auth-stage-code")
        ]
        [ column
            (cupertinoInsetCardAttrs 10 ++ [ width fill, spacing 6 ])
            [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text "Code sent to")
            , el [ Font.bold, Font.size 14, Font.color (rgb255 44 56 72) ] (text emailText)
            ]
        , Input.text
            (cupertinoTextInputAttrs
                ++ (if loginLoading || resendLoading then
                        []

                    else
                        [ onEnter LoginWithCode ]
                   )
            )
            { onChange = SetAuthCode
            , text = model.authCode
            , placeholder = Just (Input.placeholder [] (text "6-digit code"))
            , label = Input.labelAbove [ Font.size 12 ] (text "Login code")
            }
        , wrappedRow [ width fill, spacing 10 ]
            [ if firstAdminMode then
                none

              else
                authSecondaryButton
                    (if loginLoading || resendLoading then
                        Nothing

                     else
                        Just BackToAuthEmail
                    )
                    "Use another email"
            , authSecondaryButton
                (if loginLoading || resendLoading then
                    Nothing

                 else
                    Just resendMsg
                )
                resendLabel
            ]
        , el [ width fill ]
            (authActionButton
                (rgb255 34 124 95)
                (rgb255 246 251 248)
                (if loginLoading || resendLoading then
                    Nothing

                 else
                    Just LoginWithCode
                )
                "Login"
            )
        , authStatusLine
            model.authInlineMessage
            (if loginLoading then
                Just "Signing in..."

             else if resendLoading then
                Just "Sending code..."

             else
                Nothing
            )
        ]


viewAuthSessionStage : Model -> Element Msg
viewAuthSessionStage model =
    let
        emailText =
            currentAuthSessionEmailText model

        roleText =
            currentAuthSessionRoleText model
    in
    column
        [ width fill
        , spacing 12
        , htmlAttribute (HtmlAttr.class "auth-stage auth-stage-session")
        ]
        [ column
            (cupertinoInsetCardAttrs 10 ++ [ width fill, spacing 6 ])
            [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text "Authenticated as")
            , el [ Font.bold, Font.size 14, Font.color (rgb255 44 56 72) ] (text emailText)
            , if roleText == "" then
                none

              else
                el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text ("Role: " ++ roleText))
            ]
        , wrappedRow [ width fill, spacing 10 ] [ authDangerButton (Just LogoutSession) "Logout" ]
        ]


authActionButton : Element.Color -> Element.Color -> Maybe Msg -> String -> Element Msg
authActionButton backgroundColor textColor onPress labelText =
    let
        isDisabled =
            onPress == Nothing
    in
    Input.button
        ([ width fill
         , Background.color
            (if isDisabled then
                rgb255 232 237 245

             else
                backgroundColor
            )
         , Font.color
            (if isDisabled then
                rgb255 110 120 136

             else
                textColor
            )
         , Border.rounded 12
         , Border.width 1
         , Border.color
            (if isDisabled then
                rgb255 214 222 233

             else
                rgba255 255 255 0 0
            )
         , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
         , htmlAttribute (HtmlAttr.style "outline" "none")
         , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
         , htmlAttribute
            (HtmlAttr.style
                "box-shadow"
                (if isDisabled then
                    "none"

                 else
                    "0 10px 24px rgba(84, 121, 224, 0.14), inset 0 1px 0 rgba(255,255,255,0.30)"
                )
            )
         ]
        )
        { onPress = onPress
        , label = text labelText
        }


authStatusLine : Maybe String -> Maybe String -> Element Msg
authStatusLine maybeErrorMessage maybeStatusMessage =
    let
        ( maybeMessage, messageColor ) =
            case maybeErrorMessage of
                Just errorMessage ->
                    ( Just errorMessage, rgb255 176 60 46 )

                Nothing ->
                    ( maybeStatusMessage, rgb255 93 103 120 )
    in
    el
        [ width fill
        , height (px 20)
        , centerX
        ]
        (case maybeMessage of
            Just message ->
                paragraph [ Font.size 13, Font.color messageColor, centerX ] [ text message ]

            Nothing ->
                none
        )


authSecondaryButton : Maybe Msg -> String -> Element Msg
authSecondaryButton onPress labelText =
    cupertinoNeutralButton onPress labelText


authDangerButton : Maybe Msg -> String -> Element Msg
authDangerButton onPress labelText =
    cupertinoDangerButton onPress labelText


authUtilityButton : Element.Color -> Element.Color -> Maybe Msg -> String -> Element Msg
authUtilityButton backgroundColor textColor onPress labelText =
    cupertinoButton backgroundColor textColor (rgba255 255 255 0 0) onPress labelText


cupertinoPrimaryButton : Maybe Msg -> String -> Element Msg
cupertinoPrimaryButton onPress labelText =
    cupertinoButton
        (rgb255 231 239 255)
        (rgb255 47 97 209)
        (rgb255 205 220 244)
        onPress
        labelText


cupertinoNeutralButton : Maybe Msg -> String -> Element Msg
cupertinoNeutralButton onPress labelText =
    cupertinoButton
        (rgb255 240 244 250)
        (rgb255 62 74 92)
        (rgb255 220 228 240)
        onPress
        labelText


cupertinoDangerButton : Maybe Msg -> String -> Element Msg
cupertinoDangerButton onPress labelText =
    cupertinoButton
        (rgb255 255 239 239)
        (rgb255 176 60 46)
        (rgb255 245 210 210)
        onPress
        labelText


cupertinoButton : Element.Color -> Element.Color -> Element.Color -> Maybe Msg -> String -> Element Msg
cupertinoButton backgroundColor textColor borderColor onPress labelText =
    Input.button
        [ Background.color backgroundColor
        , Font.color textColor
        , Border.rounded 12
        , Border.width 1
        , Border.color borderColor
        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
        , cupertinoFocusRing
        , htmlAttribute (HtmlAttr.style "outline" "none")
        , htmlAttribute (HtmlAttr.style "box-shadow" "0 1px 3px rgba(31,41,55,0.08), inset 0 1px 0 rgba(255,255,255,0.58)")
        , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
        ]
        { onPress = onPress
        , label = text labelText
        }


cupertinoFocusRing : Element.Attribute msg
cupertinoFocusRing =
    Element.focused
        [ Border.glow (rgba255 94 135 218 0.45) 3
        ]


cupertinoPanelAttrs : Int -> Int -> List (Element.Attribute msg)
cupertinoPanelAttrs spacingValue paddingValue =
    [ width fill
    , spacing spacingValue
    , Background.color (rgb255 252 253 255)
    , Border.rounded 18
    , Border.width 1
    , Border.color (rgb255 232 238 246)
    , padding paddingValue
    , htmlAttribute (HtmlAttr.style "box-shadow" "0 14px 32px rgba(15,23,42,0.06), inset 0 1px 0 rgba(255,255,255,0.72)")
    ]


cupertinoInsetCardAttrs : Int -> List (Element.Attribute msg)
cupertinoInsetCardAttrs paddingValue =
    [ Background.color (rgb255 247 250 254)
    , Border.rounded 14
    , Border.width 1
    , Border.color (rgb255 232 238 246)
    , padding paddingValue
    , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.7)")
    ]


cupertinoSectionHeaderAttrs : List (Element.Attribute msg)
cupertinoSectionHeaderAttrs =
    [ Font.size 10
    , Font.bold
    , Font.letterSpacing 0.8
    , Font.color (rgb255 132 143 159)
    ]


cupertinoTextInputAttrs : List (Element.Attribute msg)
cupertinoTextInputAttrs =
    [ width fill
    , Background.color (rgb255 248 250 254)
    , Border.rounded 12
    , Border.width 1
    , Border.color (rgb255 222 230 241)
    , padding 12
    , cupertinoFocusRing
    , htmlAttribute (HtmlAttr.style "outline" "none")
    , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.72)")
    ]


formBooleanField : String -> Bool -> String -> (String -> Msg) -> Element Msg
formBooleanField labelText isOptional rawValue toMsg =
    let
        state =
            booleanFieldState rawValue
    in
    column
        [ width fill
        , spacing 8
        ]
        [ el [ Font.size 12 ] (text labelText)
        , row [ width fill, spacing 10, centerY ]
            (List.concat
                [ [ boolToggleButton state (Just (toMsg (nextBooleanRawValue rawValue))) ]
                , if isOptional then
                    [ boolUnsetButton (state == BooleanUnset) (Just (toMsg "")) ]

                  else
                    []
                ]
            )
        ]


booleanFieldState : String -> BooleanFieldState
booleanFieldState rawValue =
    case rawValue of
        "true" ->
            BooleanTrue

        "false" ->
            BooleanFalse

        _ ->
            BooleanUnset


nextBooleanRawValue : String -> String
nextBooleanRawValue rawValue =
    case booleanFieldState rawValue of
        BooleanTrue ->
            "false"

        BooleanFalse ->
            "true"

        BooleanUnset ->
            "true"


boolToggleButton : BooleanFieldState -> Maybe Msg -> Element Msg
boolToggleButton state onPress =
    Input.button
        []
        { onPress = onPress
        , label =
            row
                [ width (px 54)
                , height (px 30)
                , centerY
                , Background.color
                    (case state of
                        BooleanTrue ->
                            rgb255 207 222 252

                        BooleanFalse ->
                            rgb255 232 238 246

                        BooleanUnset ->
                            rgb255 242 245 250
                    )
                , Border.width 1
                , Border.color
                    (case state of
                        BooleanTrue ->
                            rgb255 182 203 242

                        BooleanFalse ->
                            rgb255 214 223 235

                        BooleanUnset ->
                            rgb255 224 230 239
                    )
                , Border.rounded 999
                , paddingEach { top = 3, right = 3, bottom = 3, left = 3 }
                , cupertinoFocusRing
                , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.72)")
                ]
                (case state of
                    BooleanTrue ->
                        [ el [ width fill ] none
                        , boolToggleKnob (rgb255 255 255 255)
                        ]

                    BooleanFalse ->
                        [ boolToggleKnob (rgb255 255 255 255)
                        , el [ width fill ] none
                        ]

                    BooleanUnset ->
                        [ el [ width fill ] none
                        , boolToggleKnob (rgb255 244 246 250)
                        , el [ width fill ] none
                        ]
                )
        }


boolToggleKnob : Element.Color -> Element Msg
boolToggleKnob color =
    el
        [ width (px 22)
        , height (px 22)
        , Background.color color
        , Border.width 1
        , Border.color (rgb255 208 214 224)
        , Border.rounded 999
        ]
        none


boolUnsetButton : Bool -> Maybe Msg -> Element Msg
boolUnsetButton selected onPress =
    Input.button
        [ Background.color
            (if selected then
                rgb255 232 239 251

             else
                rgb255 247 249 252
            )
        , Font.color
            (if selected then
                rgb255 63 86 130

             else
                rgb255 109 121 138
            )
        , Border.width 1
        , Border.color
            (if selected then
                rgb255 206 220 242

             else
                rgb255 225 230 237
            )
        , Border.rounded 999
        , paddingEach { top = 8, right = 12, bottom = 8, left = 12 }
        , cupertinoFocusRing
        , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.72)")
        ]
        { onPress = onPress
        , label = text "Unset"
        }


authInfoFromModel : Model -> Maybe AuthInfo
authInfoFromModel model =
    case model.schema of
        Loaded schema ->
            schema.auth

        _ ->
            Nothing


hasAnyAuthInfo : Model -> Bool
hasAnyAuthInfo model =
    authInfoFromModel model /= Nothing


currentAuthStage : Model -> AuthStage
currentAuthStage model =
    if hasActiveSession model then
        AuthStageSession

    else
        model.authStage


authFirstAdminMode : Model -> Bool
authFirstAdminMode model =
    let
        maybeAppAuth =
            authInfoFromModel model

        needsBootstrap =
            case activeAuthScope of
                AppAuthScope ->
                    case maybeAppAuth of
                        Just appAuth ->
                            appAuth.needsBootstrap

                        Nothing ->
                            False
    in
    needsBootstrap || model.firstAdminCodeRequested


currentWorkspace : Model -> WorkspaceMode
currentWorkspace model =
    if isAdminProfile model && model.workspace == AdminWorkspace then
        AdminWorkspace

    else
        AppWorkspace


workspaceForCurrentRoute : Maybe String -> Route -> WorkspaceMode
workspaceForCurrentRoute maybeRole route =
    case route of
        RouteDefault workspace ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteAuthTools workspace ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteEntity workspace _ ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteEntityCreate workspace _ ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteEntityDetail workspace _ _ ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteEntityEdit workspace _ _ ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteAction workspace _ ->
            if workspace == AdminWorkspace && isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RoutePerformance ->
            if isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteRequestLogs ->
            if isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace

        RouteDatabase ->
            if isAdminRole maybeRole then
                AdminWorkspace

            else
                AppWorkspace


currentAppName : Model -> String
currentAppName model =
    case model.schema of
        Loaded schema ->
            String.trim schema.appName

        _ ->
            "Mar"


activeAuthScope : AuthScope
activeAuthScope =
    AppAuthScope


isAdminProfile : Model -> Bool
isAdminProfile model =
    isAdminRole model.currentRole || isAdminRole model.currentSystemRole


isAdminRole : Maybe String -> Bool
isAdminRole maybeRole =
    case maybeRole of
        Just role ->
            String.toLower (String.trim role) == "admin"

        Nothing ->
            False


currentAuthSessionEmailText : Model -> String
currentAuthSessionEmailText model =
    case model.currentEmail of
        Just email ->
            email

        Nothing ->
            Maybe.withDefault "Authenticated session" model.currentSystemEmail


currentAuthSessionRoleText : Model -> String
currentAuthSessionRoleText model =
    case model.currentRole of
        Just role ->
            String.trim role

        Nothing ->
            model.currentSystemRole
                |> Maybe.map String.trim
                |> Maybe.withDefault ""


clearLocalSession : Model -> Model
clearLocalSession model =
    { model
        | authToken = ""
        , systemAuthToken = ""
        , currentEmail = Nothing
        , currentRole = Nothing
        , currentSystemEmail = Nothing
        , currentSystemRole = Nothing
        , authEmail = ""
        , authCode = ""
        , authStage = AuthStageEmail
        , authSubmitting = Nothing
        , sessionRestorePending = False
        , authInlineMessage = Nothing
        , flash = Nothing
        , authToolsOpen = True
    }


saveSessionFromModel : Model -> Cmd Msg
saveSessionFromModel model =
    saveSession
        (Encode.object
            [ ( "authToken", Encode.string (String.trim model.authToken) )
            , ( "systemAuthToken", Encode.string (String.trim model.systemAuthToken) )
            ]
        )


isUnauthorizedError : ApiHttpError -> Bool
isUnauthorizedError httpError =
    case httpError of
        ApiBadResponse payload ->
            payload.statusCode == 401 || payload.errorCode == Just "auth_required"

        _ ->
            False


hasActiveSession : Model -> Bool
hasActiveSession model =
    (model.currentEmail /= Nothing)
        || (model.currentSystemEmail /= Nothing)


isCompactLayout : Model -> Bool
isCompactLayout model =
    model.viewportWidth < 900


closeMobileSidebarForNavigation : Model -> Model
closeMobileSidebarForNavigation model =
    if isCompactLayout model then
        { model | mobileSidebarOpen = False, keepMobileSidebarOpenOnNextRoute = False }

    else
        model


viewPanelTitle : String -> List (Element msg) -> Element msg
viewPanelTitle title details =
    column [ width fill, spacing 6 ]
        (paragraph
            [ width fill
            , Font.bold
            , Font.size 20
            , Font.color (rgb255 34 47 64)
            , htmlAttribute (HtmlAttr.style "min-width" "0")
            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
            ]
            [ text title ]
            :: details
        )


viewPanelHeader : Bool -> String -> List (Element msg) -> List (Element msg) -> Element msg
viewPanelHeader compact title details actions =
    let
        titleBlock =
            if List.isEmpty actions then
                el
                    [ width fill
                    , htmlAttribute (HtmlAttr.style "min-width" "0")
                    , paddingEach { top = 4, right = 0, bottom = 0, left = 0 }
                    ]
                    (viewPanelTitle title details)

            else
                el
                    [ width fill
                    , htmlAttribute (HtmlAttr.style "min-width" "0")
                    ]
                    (viewPanelTitle title details)
    in
    if compact then
        column
            [ width fill, spacing 10 ]
            [ titleBlock
            , if List.isEmpty actions then
                none

              else
                wrappedRow [ width fill, spacing 10 ] actions
            ]

    else
        row [ width fill, spacing 10, htmlAttribute (HtmlAttr.style "min-width" "0") ]
            [ titleBlock
            , if List.isEmpty actions then
                none

              else
                row [ spacing 10 ] actions
            ]


onEnter : msg -> Element.Attribute msg
onEnter message =
    htmlAttribute
        (HtmlEvents.on "keydown"
            (Decode.field "key" Decode.string
                |> Decode.andThen
                    (\key ->
                        if key == "Enter" then
                            Decode.succeed message

                        else
                            Decode.fail "ignore non-enter keys"
                    )
            )
        )


viewFlash : Model -> Element Msg
viewFlash model =
    case model.flash of
        Nothing ->
            none

        Just message ->
            let
                compact =
                    isCompactLayout model

                titleText =
                    if not (hasActiveSession model) then
                        "Something went wrong"

                    else
                        "Service response"
            in
            column
                [ width fill
                , Background.color (rgb255 244 248 255)
                , Border.rounded 10
                , Border.width 1
                , Border.color (rgb255 179 200 236)
                , padding 12
                , spacing 10
                ]
                [ el [ Font.size 11, Font.bold, Font.color (rgb255 70 89 120) ] (text titleText)
                , if compact then
                    column
                        [ width fill
                        , spacing 12
                        ]
                        [ paragraph
                            [ width fill
                            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
                            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
                            ]
                            [ text message ]
                        , cupertinoPrimaryButton (Just ClearFlash) "Close"
                        ]

                  else
                    row
                        [ width fill
                        , spacing 12
                        , htmlAttribute (HtmlAttr.style "align-items" "flex-start")
                        ]
                        [ paragraph
                            [ width fill
                            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
                            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
                            ]
                            [ text message ]
                        , cupertinoPrimaryButton (Just ClearFlash) "Close"
                        ]
                ]


viewDeleteConfirmation : Model -> Element Msg
viewDeleteConfirmation model =
    case model.pendingDelete of
        Nothing ->
            none

        Just pendingDelete ->
            el
                [ width fill
                , height fill
                , Background.color (rgba255 18 24 33 0.36)
                , padding 16
                , htmlAttribute (HtmlAttr.style "backdrop-filter" "blur(16px)")
                , htmlAttribute (HtmlAttr.style "-webkit-backdrop-filter" "blur(16px)")
                ]
                (el
                    ([ centerX
                     , centerY
                     , width (fill |> maximum 480)
                     ]
                        ++ cupertinoPanelAttrs 16 18
                    )
                    (column
                        [ width fill
                        , spacing 16
                        ]
                        [ el [ Font.bold, Font.size 22 ] (text "Confirm deletion")
                        , paragraph
                            [ width fill
                            , Font.size 15
                            , Font.color (rgb255 82 92 108)
                            ]
                            [ text pendingDelete.message ]
                        , if isCompactLayout model then
                            wrappedRow
                                [ width fill
                                , spacing 10
                                ]
                                [ cupertinoNeutralButton (Just CancelDelete) "Cancel"
                                , cupertinoDangerButton (Just ConfirmDelete) "Delete"
                                ]

                          else
                            row
                                [ width fill
                                , spacing 10
                                ]
                                [ el [ width fill ] none
                                , cupertinoNeutralButton (Just CancelDelete) "Cancel"
                                , cupertinoDangerButton (Just ConfirmDelete) "Delete"
                                ]
                        ]
                    )
                )


viewDataPanel : Model -> Element Msg
viewDataPanel model =
    case model.selectedAction of
        Just actionInfo ->
            viewActionPanel model actionInfo

        Nothing ->
            let
                workspace =
                    currentWorkspace model

                compact =
                    isCompactLayout model

                createLabel =
                    case model.selectedEntity of
                        Just entity ->
                            "New " ++ entityDisplayName entity

                        Nothing ->
                            if workspace == AppWorkspace then
                                "Create"

                            else
                                "New"

                mobileHeader =
                    none

                actionsBar =
                    wrappedRow [ width fill, spacing 10 ]
                        [ cupertinoPrimaryButton (Just StartCreate) createLabel
                        , cupertinoNeutralButton (Just ReloadRows) "Refresh"
                        ]

                rowsBlock =
                    el
                        ([ width fill
                         , paddingEach
                            { top =
                                if compact then
                                    0

                                else
                                    7
                            , right = 0
                            , bottom = 0
                            , left =
                                if compact && workspace == AppWorkspace then
                                    0

                                else
                                    8
                            }
                         , htmlAttribute (HtmlAttr.style "min-height" "0")
                         , htmlAttribute (HtmlAttr.style "min-width" "0")
                         ]
                            ++ (if compact then
                                    []

                                else
                                    [ height fill, scrollbarY ]
                               )
                        )
                        (viewRows model)
            in
            if compact then
                column
                    [ width fill
                    , spacing 14
                    , htmlAttribute (HtmlAttr.style "min-width" "0")
                    ]
                    [ mobileHeader
                    , actionsBar
                    , rowsBlock
                    ]

            else
                column
                    ([ width
                        (fillPortion 4)
                     ]
                        ++ cupertinoPanelAttrs 10 16
                        ++ [ paddingEach { top = 10, right = 16, bottom = 4, left = 16 }
                     , htmlAttribute (HtmlAttr.style "min-height" "0")
                     , htmlAttribute (HtmlAttr.style "min-width" "0")
                     ]
                        ++ [ height fill ]
                    )
                    [ actionsBar
                    , rowsBlock
                    ]


viewActionPanel : Model -> ActionInfo -> Element Msg
viewActionPanel model actionInfo =
    case findInputAlias actionInfo.inputAlias model of
        Nothing ->
            column
                ([ height fill ] ++ cupertinoPanelAttrs 12 16)
                [ viewPanelHeader (isCompactLayout model) ("Action: " ++ actionInfo.name) [] []
                , paragraph [ Font.color (rgb255 176 60 46) ] [ text ("Input alias not found: " ++ actionInfo.inputAlias) ]
                ]

        Just aliasInfo ->
            let
                workspace =
                    currentWorkspace model

                fieldInput : InputAliasField -> Element Msg
                fieldInput field =
                    if field.fieldType == "Bool" then
                        formBooleanField
                            (fieldLabel field.name)
                            False
                            (Dict.get field.name model.actionFormValues |> Maybe.withDefault "false")
                            (SetActionField field.name)

                    else
                        Input.text cupertinoTextInputAttrs
                            { onChange = SetActionField field.name
                            , text = Dict.get field.name model.actionFormValues |> Maybe.withDefault ""
                            , placeholder =
                                Just
                                    (Input.placeholder []
                                        (text (placeholderForType field.name field.fieldType))
                                    )
                            , label = Input.labelAbove [ Font.size 12 ] (text (fieldLabel field.name))
                            }
            in
            column
                ([ height fill ]
                    ++ cupertinoPanelAttrs 12 16
                )
                (List.concat
                    [ [ viewPanelHeader (isCompactLayout model)
                            (if workspace == AppWorkspace then
                                actionInfo.name

                             else
                                "Action: " ++ actionInfo.name
                            )
                            (if workspace == AppWorkspace then
                                []

                             else
                                [ el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text ("POST /actions/" ++ actionInfo.name))
                                , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text ("Input: " ++ aliasInfo.name))
                                ]
                            )
                            []
                      ]
                    , List.map fieldInput aliasInfo.fields
                    , [ if isCompactLayout model then
                            wrappedRow [ width fill, spacing 10 ]
                                [ cupertinoPrimaryButton
                                    (Just RunAction)
                                    (if workspace == AppWorkspace then
                                        "Continue"

                                     else
                                        "Run action"
                                    )
                                ]

                        else
                            row [ width fill ]
                                [ el [ width fill ] none
                                , cupertinoPrimaryButton
                                    (Just RunAction)
                                    (if workspace == AppWorkspace then
                                        "Continue"

                                     else
                                        "Run action"
                                    )
                                ]
                      ]
                    , [ case model.actionResult of
                            Nothing ->
                                none

                            Just response ->
                                column
                                    (cupertinoInsetCardAttrs 12 ++ [ width fill, spacing 8 ])
                                    (el [ Font.bold ]
                                        (text
                                            (if workspace == AppWorkspace then
                                                "Result"

                                             else
                                                "Response"
                                            )
                                        )
                                        :: (response
                                                |> Dict.toList
                                                |> List.map
                                                    (\( key, value ) ->
                                                        row [ width fill, spacing 8 ]
                                                            [ el [ Font.bold ] (text key)
                                                            , paragraph [ Font.size 13, Font.color (rgb255 82 92 108) ] [ text (valueToString value) ]
                                                            ]
                                                    )
                                           )
                                    )
                      ]
                    ]
                )


viewRows : Model -> Element Msg
viewRows model =
    case ( model.selectedEntity, model.rows ) of
        ( Nothing, _ ) ->
            paragraph []
                [ text
                    (if currentWorkspace model == AppWorkspace then
                        "Choose something from the menu."

                     else
                        "Choose an entity from the sidebar."
                    )
                ]

        ( Just _, NotAsked ) ->
            paragraph [] [ text "No data loaded yet." ]

        ( Just _, Loading ) ->
            paragraph []
                [ text
                    (if currentWorkspace model == AppWorkspace then
                        "Loading..."

                     else
                        "Loading records..."
                    )
                ]

        ( Just _, Failed message ) ->
            paragraph [ Font.color (rgb255 176 60 46) ] [ text message ]

        ( Just entity, Loaded records ) ->
            if List.isEmpty records then
                paragraph []
                    [ text
                        (if currentWorkspace model == AppWorkspace then
                            "Nothing here yet. Create the first one to get started."

                         else
                            "No records yet."
                        )
                    ]

            else
                let
                    selectedRowId =
                        model.selectedRow
                            |> Maybe.andThen (rowId entity)
                in
                column
                    [ width fill
                    , spacing 8
                    , paddingEach
                        { top = 0
                        , right =
                            if isCompactLayout model then
                                0

                            else
                                18
                        , bottom = 0
                        , left = 0
                        }
                    ]
                    (List.map
                        (\record ->
                            viewRowCard
                                (currentWorkspace model)
                                entity
                                (rowId entity record == selectedRowId && selectedRowId /= Nothing)
                                record
                        )
                        records
                        ++ [ el [ width fill, height (px 4) ] none ]
                    )


viewRowCard : WorkspaceMode -> Entity -> Bool -> Row -> Element Msg
viewRowCard workspace entity isSelected rowValue =
    let
        wrappingTextAttrs =
            [ width fill
            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
            ]

        headingColor =
            if isSelected then
                rgb255 43 76 136

            else
                rgb255 35 50 71

        headingText =
            rowCardTitle workspace entity rowValue

        statusBadges =
            rowPreviewStatusBadges entity rowValue

        summary =
            rowPreviewSummary workspace entity rowValue

        bodyContent =
            List.filterMap identity
                [ Just (paragraph (Font.bold :: Font.color headingColor :: wrappingTextAttrs) [ text headingText ])
                , if List.isEmpty statusBadges then
                    Nothing

                  else
                    Just (wrappedRow [ width fill, spacing 8 ] statusBadges)
                , if summary == "" then
                    Nothing

                  else
                    Just
                        (paragraph
                            (Font.size 13 :: Font.color (rgb255 90 103 120) :: wrappingTextAttrs)
                            [ text summary ]
                        )
                ]

        cardBody =
            row
                [ width fill
                , spacing 12
                ]
                [ column [ width fill, spacing 8 ] bodyContent
                , el
                    [ Font.size 18
                    , Font.color
                        (if isSelected then
                            rgb255 94 135 218

                         else
                            rgb255 132 145 162
                        )
                    , centerY
                    ]
                    (text "›")
                ]
    in
    Input.button
        [ width fill
        , Background.color
            (if isSelected then
                rgb255 237 244 255

             else
                rgb255 250 252 255
            )
        , Border.rounded 12
        , Border.width 1
        , Border.color
            (if isSelected then
                rgb255 227 235 246

             else
                rgb255 226 232 239
            )
        , padding 14
        , htmlAttribute (HtmlAttr.style "cursor" "pointer")
        , cupertinoFocusRing
        , htmlAttribute (HtmlAttr.style "outline" "none")
        , htmlAttribute
            (HtmlAttr.style
                "box-shadow"
                (if isSelected then
                    "0 8px 20px rgba(110, 139, 196, 0.12), inset 0 1px 0 rgba(255,255,255,0.55)"

                 else
                    "none"
                )
            )
        , htmlAttribute (HtmlAttr.style "-webkit-tap-highlight-color" "rgba(0,0,0,0)")
        ]
        { onPress = Just (SelectRow rowValue)
        , label = cardBody
        }


viewInspector : Model -> Element Msg
viewInspector model =
    case model.selectedAction of
        Just actionInfo ->
            if currentWorkspace model == AppWorkspace then
                none

            else
                column
                    ([ width
                        (if isCompactLayout model then
                            fill

                         else
                            fillPortion 1 |> maximum 420
                        )
                     , spacing 14
                     , htmlAttribute (HtmlAttr.style "min-height" "0")
                     , htmlAttribute (HtmlAttr.style "min-width" "0")
                     ]
                        ++ (if isCompactLayout model then
                                []

                            else
                                [ height fill, scrollbarY ]
                           )
                    )
                    [ viewActionInfo actionInfo ]

        Nothing ->
            let
                compact =
                    isCompactLayout model

                inspectorContent =
                    case model.formMode of
                        FormHidden ->
                            case model.selectedRow of
                                Just _ ->
                                    [ viewSelectedRow model ]

                                Nothing ->
                                    [ viewEntitySchema model ]

                        _ ->
                            [ viewFormPanel model ]
            in
            column
                ([ width
                    (if compact then
                        fill

                     else
                        fillPortion 1 |> maximum 420
                    )
                 , spacing 14
                 , htmlAttribute (HtmlAttr.style "min-height" "0")
                 , htmlAttribute (HtmlAttr.style "min-width" "0")
                 ]
                    ++ (if compact then
                            []

                        else
                            [ height fill, scrollbarY ]
                       )
                )
                inspectorContent


viewPerformancePanel : Model -> Element Msg
viewPerformancePanel model =
    let
        panelAttrs =
            cupertinoPanelAttrs 14 16
                ++ (if isCompactLayout model then
                        []

                    else
                        [ height fill
                        , scrollbarY
                        , htmlAttribute (HtmlAttr.style "min-height" "0")
                        ]
                   )
    in
    if not (isAdminProfile model) then
        column
            panelAttrs
            [ el [ Font.bold, Font.size 20 ] (text "Monitoring")
            , paragraph [ Font.size 14, Font.color (rgb255 93 103 120) ]
                [ text "Admin role required to view monitoring information." ]
            ]

    else
        let
            compact =
                isCompactLayout model

            routeRow perfRoute =
                let
                    hasErrors =
                        perfRoute.errors4xx > 0 || perfRoute.errors5xx > 0

                    statusColor =
                        if hasErrors then
                            rgb255 176 60 46

                        else
                            rgb255 34 124 95

                    routeTextAttrs =
                        [ Font.size 13
                        , Font.color (rgb255 41 52 68)
                        ]

                    routeMetaAttrs =
                        [ Font.size 12
                        , Font.color (rgb255 93 103 120)
                        ]

                    routeStatusAttrs =
                        [ Font.size 12
                        , Font.color statusColor
                        ]
                in
                if compact then
                    column
                        (cupertinoInsetCardAttrs 12 ++ [ width fill, spacing 8 ])
                        [ wrappedRow [ width fill, spacing 8 ]
                            [ el (Font.bold :: routeTextAttrs) (text perfRoute.method)
                            , el routeTextAttrs (text perfRoute.route)
                            ]
                        , wrappedRow [ width fill, spacing 8 ]
                            [ el routeMetaAttrs (text ("count: " ++ String.fromInt perfRoute.count))
                            , el routeMetaAttrs (text ("avg: " ++ formatMs perfRoute.avgMs))
                            , el routeStatusAttrs (text ("4xx/5xx: " ++ String.fromInt perfRoute.errors4xx ++ "/" ++ String.fromInt perfRoute.errors5xx))
                            ]
                        ]

                else
                    row
                        (cupertinoInsetCardAttrs 12 ++ [ width fill, spacing 12 ])
                        [ el ([ width (fillPortion 1), Font.bold ] ++ routeTextAttrs) (text perfRoute.method)
                        , el ([ width (fillPortion 3) ] ++ routeTextAttrs) (text perfRoute.route)
                        , el ([ width (fillPortion 1) ] ++ routeMetaAttrs) (text ("count: " ++ String.fromInt perfRoute.count))
                        , el ([ width (fillPortion 1) ] ++ routeMetaAttrs) (text ("avg: " ++ formatMs perfRoute.avgMs))
                        , el ([ width (fillPortion 1) ] ++ routeStatusAttrs) (text ("4xx/5xx: " ++ String.fromInt perfRoute.errors4xx ++ "/" ++ String.fromInt perfRoute.errors5xx))
                        ]

            cards perf =
                wrappedRow [ width fill, spacing 12 ]
                    [ performanceCard "Uptime" (formatSeconds perf.uptimeSeconds)
                    , performanceCard "Memory (heap)" (formatBytes perf.memoryBytes)
                    , performanceCard "SQLite file" (formatBytes perf.sqliteBytes)
                    , performanceCard "Goroutines" (String.fromInt perf.goroutines)
                    , performanceCard "Requests" (String.fromInt perf.http.totalRequests)
                    ]
        in
        column
            panelAttrs
            [ viewPanelHeader (isCompactLayout model)
                "Monitoring"
                []
                [ cupertinoNeutralButton (Just ReloadPerformance) "Refresh" ]
            , viewMonitoringVersion model.adminVersion model.monitoringVersionDetailsOpen
            , el
                [ width fill
                , height (px 1)
                , Background.color (rgb255 226 232 239)
                ]
                none
            , case model.perf of
                NotAsked ->
                    paragraph [] [ text "No monitoring data loaded yet." ]

                Loading ->
                    paragraph [] [ text "Loading monitoring data..." ]

                Failed message ->
                    paragraph [ Font.color (rgb255 176 60 46) ] [ text message ]

                Loaded perf ->
                    column [ width fill, spacing 12 ]
                        [ cards perf
                        , wrappedRow [ width fill, spacing 12 ]
                            [ performanceCard "2xx responses" (String.fromInt perf.http.success2xx)
                            , performanceCard "4xx errors" (String.fromInt perf.http.errors4xx)
                            , performanceCard "5xx errors" (String.fromInt perf.http.errors5xx)
                            ]
                        , column [ width fill, spacing 8 ]
                            (el [ Font.bold, Font.size 18 ] (text "Route metrics")
                                :: (if List.isEmpty perf.http.routes then
                                        [ paragraph [] [ text "No requests captured yet." ] ]

                                    else
                                        List.map routeRow perf.http.routes
                                   )
                            )
                        ]
            ]


viewMonitoringVersion : Remote AdminVersionPayload -> Bool -> Element Msg
viewMonitoringVersion versionRemote detailsOpen =
    case versionRemote of
        NotAsked ->
            none

        Loading ->
            paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ] [ text "Loading version info..." ]

        Failed message ->
            paragraph [ Font.size 13, Font.color (rgb255 176 60 46) ] [ text ("Version info unavailable: " ++ message) ]

        Loaded versionPayload ->
            let
                summaryCards =
                    [ databaseInfoCard "App" versionPayload.app.name
                    , databaseInfoCard "Mar version" versionPayload.mar.version
                    ]
            in
            column
                [ width fill
                , spacing 10
                ]
                [ wrappedRow [ width fill, spacing 12 ] summaryCards
                , Input.button
                    [ Font.size 13
                    , Font.color (rgb255 93 103 120)
                    , alignLeft
                    , paddingEach { top = 2, right = 2, bottom = 2, left = 2 }
                    ]
                    { onPress = Just ToggleMonitoringVersionDetails
                    , label =
                        text
                            (if detailsOpen then
                                "Hide details"

                             else
                                "View details"
                            )
                    }
                , if detailsOpen then
                    column
                        [ width fill
                        , spacing 12
                        ]
                        [ wrappedRow [ width fill, spacing 12 ]
                            [ databaseInfoCard "App build time" versionPayload.app.buildTime
                            , databaseInfoCardWithHint "Manifest hash" versionPayload.app.manifestHash "Changes when the app definition changes."
                            ]
                        , wrappedRow [ width fill, spacing 12 ]
                            [ compactInfoCard "Mar commit" versionPayload.mar.commit
                            , compactInfoCard "Mar build time" versionPayload.mar.buildTime
                            , compactInfoCard "Go version" versionPayload.runtimeInfo.goVersion
                            , compactInfoCard "Platform" versionPayload.runtimeInfo.platform
                            ]
                        ]

                  else
                    none
                ]


viewRequestLogsPanel : Model -> Element Msg
viewRequestLogsPanel model =
    let
        panelAttrs =
            cupertinoPanelAttrs 14 16
                ++ (if isCompactLayout model then
                        []

                    else
                        [ height fill
                        , scrollbarY
                        , htmlAttribute (HtmlAttr.style "min-height" "0")
                        ]
                   )
    in
    if not (isAdminProfile model) then
        column
            panelAttrs
            [ el [ Font.bold, Font.size 20 ] (text "Request logs")
            , paragraph [ Font.size 14, Font.color (rgb255 93 103 120) ]
                [ text "Admin role required to view request logs." ]
            ]

    else
        let
            logsSubtitle =
                case model.requestLogs of
                    Loaded payload ->
                        "Showing "
                            ++ String.fromInt (List.length payload.logs)
                            ++ " entries (buffer size: "
                            ++ String.fromInt payload.buffer
                            ++ ")"

                    _ ->
                        ""
        in
        column
            panelAttrs
            [ viewPanelHeader (isCompactLayout model)
                "Recent request logs"
                (if logsSubtitle == "" then
                    []

                 else
                    [ el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text logsSubtitle) ]
                )
                [ cupertinoNeutralButton (Just ReloadRequestLogs) "Refresh" ]
            , paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                [ text "Sensitive values are masked by the server in this view (tokens, login codes, and emails)." ]
            , viewRequestLogsSection model.requestLogs
            ]


viewRequestLogsSection : Remote RequestLogsPayload -> Element Msg
viewRequestLogsSection requestLogsRemote =
    case requestLogsRemote of
        NotAsked ->
            column [ width fill, spacing 8 ]
                [ paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ] [ text "No request logs loaded yet." ] ]

        Loading ->
            column [ width fill, spacing 8 ]
                [ paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ] [ text "Loading request logs..." ] ]

        Failed message ->
            column [ width fill, spacing 8 ]
                [ paragraph [ Font.size 13, Font.color (rgb255 176 60 46) ] [ text message ] ]

        Loaded payload ->
            column [ width fill, spacing 8 ]
                (if List.isEmpty payload.logs then
                    [ paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                        [ text "No requests captured yet." ]
                    ]

                 else
                    List.map viewRequestLogEntry payload.logs
                )


viewRequestLogEntry : RequestLogEntry -> Element Msg
viewRequestLogEntry entry =
    let
        ( dateText, timeText ) =
            splitLogTimestamp entry.timestamp

        statusColor =
            if entry.status >= 500 then
                rgb255 176 60 46

            else if entry.status >= 400 then
                rgb255 204 102 35

            else
                rgb255 34 124 95

        queryLabel =
            case entry.queryCount of
                1 ->
                    "query"

                _ ->
                    "queries"

        querySummary =
            String.fromInt entry.queryCount ++ " " ++ queryLabel ++ ", " ++ formatMs entry.queryTimeMs
    in
    column
        (cupertinoInsetCardAttrs 12 ++ [ width fill, spacing 8 ])
        (List.concat
            [ [ row [ width fill, spacing 10 ]
                    [ el [ Font.size 12, Font.bold, Font.color (rgb255 70 80 96) ] (text ("Date: " ++ dateText))
                    , el [ Font.size 12, Font.bold, Font.color (rgb255 70 80 96) ] (text ("Time: " ++ timeText))
                    ]
              , wrappedRow [ width fill, spacing 10 ]
                    [ el [ Font.size 13, Font.bold, Font.color (rgb255 44 56 72) ] (text (entry.method ++ " " ++ entry.path))
                    , el [ Font.size 13, Font.color statusColor, Font.bold ] (text (String.fromInt entry.status))
                    , el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text (formatMs entry.durationMs))
                    ]
              , wrappedRow [ width fill, spacing 10 ]
                    [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text ("Route: " ++ entry.route))
                    , el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text querySummary)
                    ]
              ]
            , if String.trim entry.errorMessage /= "" then
                [ paragraph [ Font.size 12, Font.color (rgb255 176 60 46) ] [ text ("Error: " ++ entry.errorMessage) ] ]

              else
                []
            , if List.isEmpty entry.queries then
                []

              else
                [ column [ width fill, spacing 6 ]
                    (el [ Font.size 12, Font.bold, Font.color (rgb255 70 80 96) ] (text "Queries")
                        :: List.map viewRequestLogQuery entry.queries
                    )
                ]
            ]
        )


viewRequestLogQuery : RequestLogQuery -> Element Msg
viewRequestLogQuery query =
    let
        metricsText =
            formatMs query.durationMs ++ " | rows: " ++ String.fromInt query.rowCount
    in
    column
        (cupertinoInsetCardAttrs 8 ++ [ width fill, spacing 4 ])
        (List.concat
            [ case query.reason of
                Just reasonText ->
                    if String.trim reasonText == "" then
                        []

                    else
                        [ el [ Font.size 12, Font.bold, Font.color (rgb255 82 95 127) ] (text reasonText) ]

                Nothing ->
                    []
            , [ paragraph
                    [ Font.size 12
                    , Font.family [ Font.monospace ]
                    , Font.color (rgb255 44 56 72)
                    ]
                    [ text query.sql ]
              , el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text metricsText)
              ]
            , case query.error of
                Just errText ->
                    if String.trim errText == "" then
                        []

                    else
                        [ paragraph [ Font.size 12, Font.color (rgb255 176 60 46) ] [ text ("Error: " ++ errText) ] ]

                Nothing ->
                    []
            ]
        )


viewDatabasePanel : Model -> Element Msg
viewDatabasePanel model =
    if not (isAdminProfile model) then
        column
            (cupertinoPanelAttrs 14 16)
            [ el [ Font.bold, Font.size 20 ] (text "Database")
            , paragraph [ Font.size 14, Font.color (rgb255 93 103 120) ]
                [ text "Admin role required to view database and backup information." ]
            ]

    else
        viewDatabasePanelAdmin model


viewDatabasePanelAdmin : Model -> Element Msg
viewDatabasePanelAdmin model =
    let
        compact =
            isCompactLayout model

        dbPath =
            currentDatabasePath model

        backupDirText =
            case model.backups of
                Loaded payload ->
                    if String.trim payload.backupDir == "" then
                        databaseBackupDir dbPath

                    else
                        payload.backupDir

                _ ->
                    databaseBackupDir dbPath

        sqliteSizeText =
            case model.perf of
                Loaded perf ->
                    formatBytes perf.sqliteBytes

                Loading ->
                    "Loading..."

                Failed _ ->
                    "-"

                NotAsked ->
                    "-"

        lastBackupInfo =
            case model.lastBackup of
                Just backup ->
                    column [ width fill, spacing 6 ]
                        [ el [ Font.bold ] (text "Last backup")
                        , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text backup.path)
                        , el [ Font.size 12, Font.color (rgb255 93 103 120) ]
                            (text ("Removed old backups: " ++ String.fromInt (List.length backup.removed)))
                        ]

                Nothing ->
                    none

        backupsSection =
            case model.backups of
                NotAsked ->
                    paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                        [ text "Backups were not loaded yet." ]

                Loading ->
                    paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                        [ text "Loading backups..." ]

                Failed message ->
                    paragraph [ Font.size 13, Font.color (rgb255 176 60 46) ]
                        [ text message ]

                Loaded payload ->
                    if List.isEmpty payload.backups then
                        paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                            [ text "No backups found yet." ]

                    else
                        column
                            [ width fill
                            , spacing 8
                            ]
                            (List.concat
                                [ if compact then
                                    []

                                  else
                                    [ row
                                        [ width fill
                                        , spacing 12
                                        , paddingEach { top = 6, right = 10, bottom = 6, left = 10 }
                                        , Background.color (rgb255 247 250 254)
                                        , Border.rounded 8
                                        , Border.width 1
                                        , Border.color (rgb255 232 238 246)
                                        ]
                                        [ el [ width (fillPortion 2), Font.bold ] (text "Backup time")
                                        , el [ width (fillPortion 1), Font.bold ] (text "Size")
                                        , el [ width (fillPortion 4), Font.bold ] (text "File")
                                        ]
                                    ]
                                , List.map (backupRow compact) payload.backups
                                ]
                            )
    in
    column
        (cupertinoPanelAttrs 14 16)
        [ viewPanelHeader (isCompactLayout model)
            "Database"
            []
            [ cupertinoNeutralButton (Just ReloadDatabase) "Refresh"
            , cupertinoPrimaryButton (Just TriggerBackup) "Create backup"
            ]
        , wrappedRow [ width fill, spacing 12 ]
            [ performanceCard "SQLite database size" sqliteSizeText
            , databaseInfoCard "File" dbPath
            , databaseInfoCard "Backups directory" backupDirText
            ]
        , lastBackupInfo
        , el [ Font.bold, Font.size 18, Font.color (rgb255 39 51 68) ] (text "Available backups")
        , backupsSection
        ]


currentDatabasePath : Model -> String
currentDatabasePath model =
    case model.schema of
        Loaded schema ->
            schema.database

        _ ->
            "-"


databaseBackupDir : String -> String
databaseBackupDir databasePath =
    let
        cleaned =
            String.trim databasePath

        slashPath =
            String.replace "\\" "/" cleaned

        segments =
            String.split "/" slashPath

        folderSegments =
            if List.length segments <= 1 then
                [ "." ]

            else
                List.take (List.length segments - 1) segments

        folderPath =
            String.join "/" folderSegments
    in
    if cleaned == "" || cleaned == "-" then
        "-"

    else if folderPath == "" then
        "./backups"

    else
        folderPath ++ "/backups"


backupRow : Bool -> BackupFile -> Element Msg
backupRow compact backup =
    if compact then
        column
            ([ width fill
             , spacing 10
             ]
                ++ cupertinoInsetCardAttrs 10
                ++ [ paddingEach { top = 8, right = 10, bottom = 8, left = 10 } ]
            )
            [ backupRowField "Backup time" backup.createdAt
            , backupRowField "Size" (formatBytes backup.sizeBytes)
            , backupRowField "File" (backupDisplayName backup)
            ]

    else
        row
            ([ width fill
             , spacing 12
             ]
                ++ cupertinoInsetCardAttrs 10
                ++ [ paddingEach { top = 8, right = 10, bottom = 8, left = 10 } ]
            )
            [ el [ width (fillPortion 2) ] (text backup.createdAt)
            , el [ width (fillPortion 1), Font.bold ] (text (formatBytes backup.sizeBytes))
            , el [ width (fillPortion 4), Font.size 13, Font.color (rgb255 93 103 120) ] (text (backupDisplayName backup))
            ]


backupRowField : String -> String -> Element Msg
backupRowField label value =
    column
        [ width fill
        , spacing 4
        ]
        [ el [ Font.size 12, Font.color (rgb255 93 103 120), Font.semiBold ] (text label)
        , paragraph
            [ width fill
            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
            , Font.size 14
            , Font.color (rgb255 44 56 72)
            ]
            [ text value ]
        ]


backupDisplayName : BackupFile -> String
backupDisplayName backup =
    if String.trim backup.name /= "" then
        backup.name

    else
        lastPathSegment backup.path


lastPathSegment : String -> String
lastPathSegment rawPath =
    let
        slashPath =
            String.replace "\\" "/" (String.trim rawPath)

        segments =
            String.split "/" slashPath
    in
    List.reverse segments
        |> List.head
        |> Maybe.withDefault rawPath


performanceCard : String -> String -> Element Msg
performanceCard title value =
    column
        (cupertinoInsetCardAttrs 12 ++ [ width fill, spacing 6 ])
        [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text title)
        , el [ Font.size 15, Font.bold ] (text value)
        ]


databaseInfoCard : String -> String -> Element Msg
databaseInfoCard title value =
    databaseInfoCardWithHint title value ""


databaseInfoCardWithHint : String -> String -> String -> Element Msg
databaseInfoCardWithHint title value hint =
    column
        (cupertinoInsetCardAttrs 12 ++ [ width (fill |> minimum 220), spacing 6 ])
        [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text title)
        , paragraph [ Font.size 13, Font.color (rgb255 41 52 68) ] [ text value ]
        , if String.trim hint == "" then
            none

          else
            paragraph [ Font.size 11, Font.color (rgb255 109 121 138) ] [ text hint ]
        ]


compactInfoCard : String -> String -> Element Msg
compactInfoCard title value =
    column
        (cupertinoInsetCardAttrs 10 ++ [ width fill, spacing 4 ])
        [ el [ Font.size 11, Font.color (rgb255 93 103 120) ] (text title)
        , paragraph [ Font.size 12, Font.color (rgb255 41 52 68) ] [ text value ]
        ]


splitLogTimestamp : String -> ( String, String )
splitLogTimestamp rawTimestamp =
    case String.words (String.trim rawTimestamp) of
        datePart :: timePart :: _ ->
            ( formatLogDate datePart, timePart )

        [ datePart ] ->
            ( formatLogDate datePart, "-" )

        _ ->
            ( "-", "-" )


formatLogDate : String -> String
formatLogDate rawDate =
    case String.split "-" (String.trim rawDate) of
        [ year, month, day ] ->
            monthLabel month ++ " " ++ day ++ ", " ++ year

        _ ->
            rawDate


monthLabel : String -> String
monthLabel month =
    case month of
        "01" ->
            "Jan"

        "02" ->
            "Feb"

        "03" ->
            "Mar"

        "04" ->
            "Apr"

        "05" ->
            "May"

        "06" ->
            "Jun"

        "07" ->
            "Jul"

        "08" ->
            "Aug"

        "09" ->
            "Sep"

        "10" ->
            "Oct"

        "11" ->
            "Nov"

        "12" ->
            "Dec"

        _ ->
            month


formatMs : Float -> String
formatMs ms =
    String.fromFloat (roundTo1 ms) ++ " ms"


formatSeconds : Float -> String
formatSeconds seconds =
    let
        totalSeconds =
            max 0 (round seconds)

        days =
            totalSeconds // (24 * 60 * 60)

        hours =
            modBy 24 (totalSeconds // (60 * 60))

        minutes =
            modBy 60 (totalSeconds // 60)

        remainingSeconds =
            modBy 60 totalSeconds

        parts =
            [ if days > 0 then
                Just (String.fromInt days ++ " d")

              else
                Nothing
            , if hours > 0 then
                Just (String.fromInt hours ++ " h")

              else
                Nothing
            , if minutes > 0 then
                Just (String.fromInt minutes ++ " min")

              else
                Nothing
            , if remainingSeconds > 0 || totalSeconds == 0 then
                Just (String.fromInt remainingSeconds ++ " s")

              else
                Nothing
            ]
                |> List.filterMap identity
                |> List.take 3
    in
    String.join " " parts


formatBytes : Float -> String
formatBytes bytes =
    if bytes < 1024 then
        String.fromInt (round bytes) ++ " B"

    else if bytes < 1024 * 1024 then
        String.fromFloat (roundTo1 (bytes / 1024)) ++ " KB"

    else if bytes < 1024 * 1024 * 1024 then
        String.fromFloat (roundTo1 (bytes / (1024 * 1024))) ++ " MB"

    else
        String.fromFloat (roundTo1 (bytes / (1024 * 1024 * 1024))) ++ " GB"


roundTo1 : Float -> Float
roundTo1 value =
    toFloat (round (value * 10)) / 10


viewActionInfo : ActionInfo -> Element Msg
viewActionInfo actionInfo =
    column
        (cupertinoPanelAttrs 10 14)
        [ el [ Font.bold, Font.size 18 ] (text "Action details")
        , wrappedRow [ width fill, spacing 8 ]
            [ badge "ACTION"
            , badge "POST"
            ]
        , wrappedRow [ width fill, spacing 8 ]
            [ el [ Font.bold ] (text "Name")
            , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text actionInfo.name)
            ]
        , wrappedRow [ width fill, spacing 8 ]
            [ el [ Font.bold ] (text "Input")
            , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text actionInfo.inputAlias)
            ]
        , wrappedRow [ width fill, spacing 8 ]
            [ el [ Font.bold ] (text "Steps")
            , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text (String.fromInt actionInfo.steps))
            ]
        ]


appVisibleFields : Entity -> List Field
appVisibleFields entity =
    entity.fields
        |> List.filter (\field -> not field.primary)


displayFieldsForEntity : WorkspaceMode -> Entity -> List Field
displayFieldsForEntity workspace entity =
    if workspace == AppWorkspace then
        let
            visible =
                appVisibleFields entity
        in
        if List.isEmpty visible then
            entity.fields

        else
            visible

    else
        entity.fields


rowCardTitle : WorkspaceMode -> Entity -> Row -> String
rowCardTitle workspace entity rowValue =
    case rowPrimaryFieldValue workspace entity rowValue of
        Just ( _, value ) ->
            value

        Nothing ->
            case rowId entity rowValue of
                Just idValue ->
                    entityDisplayName entity ++ " #" ++ idValue

                Nothing ->
                    entityDisplayName entity


rowPrimaryFieldValue : WorkspaceMode -> Entity -> Row -> Maybe ( Field, String )
rowPrimaryFieldValue workspace entity rowValue =
    let
        preferredNames =
            [ "name", "title", "email", "slug" ]

        visibleFieldValues =
            rowVisibleFieldValues workspace entity rowValue

        firstPreferred =
            preferredNames
                |> List.filterMap (\fieldName -> findFieldValueByName fieldName visibleFieldValues)
                |> List.head
    in
    case firstPreferred of
        Just pair ->
            Just pair

        Nothing ->
            List.head visibleFieldValues


rowPreviewSummary : WorkspaceMode -> Entity -> Row -> String
rowPreviewSummary workspace entity rowValue =
    let
        primaryFieldName =
            rowPrimaryFieldValue workspace entity rowValue
                |> Maybe.map (\( field, _ ) -> field.name)

        metadataPieces =
            displayFieldsForEntity workspace entity
                |> List.filterMap
                    (\field ->
                        if shouldIncludePreviewMetadata entity primaryFieldName field then
                            rowFieldLabel entity field.name rowValue
                                |> Maybe.map (\value -> fieldLabel field.name ++ ": " ++ value)

                        else
                            Nothing
                    )
                |> List.take 2
    in
    if List.isEmpty metadataPieces then
        case rowId entity rowValue of
            Just idValue ->
                if workspace == AdminWorkspace then
                    "ID: " ++ idValue

                else
                    ""

            Nothing ->
                ""

    else
        String.join "  •  " metadataPieces


shouldIncludePreviewMetadata : Entity -> Maybe String -> Field -> Bool
shouldIncludePreviewMetadata entity primaryFieldName field =
    let
        lowerName =
            String.toLower field.name
    in
    field.fieldType
        /= BoolType
        && not field.primary
        && not field.auto
        && primaryFieldName
        /= Just field.name
        && lowerName
        /= String.toLower entity.primaryKey
        && not (List.member lowerName [ "createdat", "updatedat", "deletedat", "created_at", "updated_at", "deleted_at" ])


rowVisibleFieldValues : WorkspaceMode -> Entity -> Row -> List ( Field, String )
rowVisibleFieldValues workspace entity rowValue =
    displayFieldsForEntity workspace entity
        |> List.filterMap
            (\field ->
                rowFieldLabel entity field.name rowValue
                    |> Maybe.map (\value -> ( field, value ))
            )


findFieldValueByName : String -> List ( Field, String ) -> Maybe ( Field, String )
findFieldValueByName fieldName fieldValues =
    fieldValues
        |> List.filter (\( field, _ ) -> field.name == fieldName)
        |> List.head


rowPreviewStatusBadges : Entity -> Row -> List (Element Msg)
rowPreviewStatusBadges entity rowValue =
    entity.fields
        |> List.filterMap (\field -> rowPreviewStatusBadge field rowValue)
        |> List.take 2


rowPreviewStatusBadge : Field -> Row -> Maybe (Element Msg)
rowPreviewStatusBadge field rowValue =
    if field.fieldType /= BoolType then
        Nothing

    else
        rowBooleanValue field.name rowValue
            |> Maybe.map (statusBadgeForBoolean field.name)


rowBooleanValue : String -> Row -> Maybe Bool
rowBooleanValue fieldName rowValue =
    Dict.get fieldName rowValue
        |> Maybe.andThen (\value -> Decode.decodeValue Decode.bool value |> Result.toMaybe)


statusBadgeForBoolean : String -> Bool -> Element Msg
statusBadgeForBoolean fieldName boolValue =
    statusBadge
        (if boolValue then
            rgb255 221 244 229

         else
            rgb255 239 242 247
        )
        (if boolValue then
            rgb255 29 102 66

         else
            rgb255 88 98 113
        )
        (fieldLabel fieldName
            ++ ": "
            ++ (if boolValue then
                    "Yes"

                else
                    "No"
               )
        )


statusBadge : Element.Color -> Element.Color -> String -> Element Msg
statusBadge backgroundColor textColor labelText =
    el
        [ Background.color backgroundColor
        , Font.color textColor
        , Border.rounded 999
        , Font.size 11
        , Border.width 1
        , Border.color (rgba255 255 255 255 0.45)
        , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
        , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.62)")
        ]
        (text labelText)


viewEntitySchema : Model -> Element Msg
viewEntitySchema model =
    if currentWorkspace model == AppWorkspace then
        none

    else
        let
            rowsForEntity =
                case model.selectedEntity of
                    Nothing ->
                        []

                    Just entity ->
                        entity.fields
        in
    column
        (cupertinoPanelAttrs 8 14)
        (el [ Font.bold, Font.size 18 ] (text "Schema")
                :: List.map
                    (\field ->
                        row [ width fill, spacing 8 ]
                            [ el [ Font.bold ] (text (fieldLabel field.name))
                            , el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text (fieldTypeLabel field.fieldType))
                            , if field.primary then
                                badge "primary"

                              else
                                none
                            , if field.auto then
                                badge "auto"

                              else
                                none
                            , if field.optional then
                                badge "optional"

                              else
                                none
                            , case field.defaultValue of
                                Just _ ->
                                    badge "default"

                                Nothing ->
                                    none
                            ]
                    )
                    rowsForEntity
            )


badge : String -> Element Msg
badge labelText =
    el
        [ Background.color (rgb255 240 245 252)
        , Font.color (rgb255 88 101 123)
        , Border.rounded 999
        , Font.size 11
        , Border.width 1
        , Border.color (rgb255 223 231 241)
        , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
        , htmlAttribute (HtmlAttr.style "box-shadow" "inset 0 1px 0 rgba(255,255,255,0.68)")
        ]
        (text labelText)


viewFormPanel : Model -> Element Msg
viewFormPanel model =
    case ( model.selectedEntity, model.formMode ) of
        ( Just entity, FormCreate ) ->
            formCard model entity ("Create new " ++ entityDisplayName entity)

        ( Just entity, FormEdit _ ) ->
            formCard model entity ("Edit " ++ entityDisplayName entity)

        _ ->
            none


formCard : Model -> Entity -> String -> Element Msg
formCard model entity titleText =
    let
        formFields =
            appVisibleFields entity

        fieldInput field =
            case field.fieldType of
                BoolType ->
                    formBooleanField
                        (fieldLabel field.name)
                        field.optional
                        (Dict.get field.name model.formValues
                            |> Maybe.withDefault
                                (if field.optional then
                                    ""

                                 else
                                    "false"
                                )
                        )
                        (SetFormField field.name)

                _ ->
                    Input.text cupertinoTextInputAttrs
                        { onChange = SetFormField field.name
                        , text = Dict.get field.name model.formValues |> Maybe.withDefault ""
                        , placeholder =
                            Just
                                (Input.placeholder []
                                    (text (placeholderForType field.name (fieldTypeLabel field.fieldType)))
                                )
                        , label = Input.labelAbove [ Font.size 12 ] (text (fieldLabel field.name))
                        }
    in
    column
        (cupertinoPanelAttrs 10 14)
        (List.concat
            [ [ el [ Font.bold, Font.size 18 ]
                    (text
                        titleText
                    )
              ]
            , List.map fieldInput formFields
            , [ (if isCompactLayout model then
                    wrappedRow [ width fill, spacing 10 ]

                 else
                    row [ spacing 10 ]
                )
                    [ cupertinoPrimaryButton (Just SubmitForm) "Save"
                    , cupertinoNeutralButton (Just CancelForm) "Cancel"
                    ]
              ]
            ]
        )


viewSelectedRow : Model -> Element Msg
viewSelectedRow model =
    case model.selectedRow of
        Nothing ->
            none

        Just rowValue ->
            case model.selectedEntity of
                Nothing ->
                    none

                Just entity ->
                    let
                        workspace =
                            currentWorkspace model

                        compact =
                            isCompactLayout model

                        visibleRows =
                            displayFieldsForEntity workspace entity
                                |> List.filterMap
                                    (\field ->
                                        Dict.get field.name rowValue
                                            |> Maybe.map (\value -> ( field, value ))
                                    )

                        detailTitle =
                            entityDisplayName entity ++ " details"

                        detailSubtitle =
                            if workspace == AppWorkspace then
                                []

                            else
                                case rowId entity rowValue of
                                    Just idValue ->
                                        [ paragraph
                                            [ width fill
                                            , Font.size 13
                                            , Font.color (rgb255 93 103 120)
                                            , htmlAttribute (HtmlAttr.style "min-width" "0")
                                            , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
                                            , htmlAttribute (HtmlAttr.style "word-break" "break-word")
                                            ]
                                            [ text ("ID " ++ idValue) ]
                                        ]

                                    Nothing ->
                                        []

                        detailActions =
                            List.filterMap identity
                                [ if compact then
                                    Just
                                        (cupertinoNeutralButton (Just CloseSelectedRow) "Back")

                                  else
                                    Nothing
                                , Just
                                    (cupertinoPrimaryButton (Just (StartEdit rowValue)) "Edit")
                                , Just
                                    (cupertinoDangerButton (Just (RequestDeleteRow rowValue)) "Delete")
                                ]
                    in
                    column
                        (cupertinoPanelAttrs 12 14)
                        (viewPanelHeader compact detailTitle detailSubtitle detailActions
                            :: (visibleRows
                                    |> List.map
                                        (\( field, value ) ->
                                            column
                                                [ width fill
                                                , spacing 4
                                                , Background.color (rgb255 248 250 252)
                                                , Border.rounded 10
                                                , padding 12
                                                ]
                                                [ el [ Font.bold, Font.size 12, Font.color (rgb255 84 96 112) ] (text (fieldLabel field.name))
                                                , paragraph
                                                    [ Font.size 14
                                                    , Font.color (rgb255 36 47 61)
                                                    , width fill
                                                    , htmlAttribute (HtmlAttr.style "overflow-wrap" "anywhere")
                                                    , htmlAttribute (HtmlAttr.style "word-break" "break-word")
                                                    ]
                                                    [ text (valueToDisplayString field.fieldType value) ]
                                                ]
                                        )
                               )
                        )


networkErrorMessage : String
networkErrorMessage =
    "We could not connect right now. Please try again in a moment."


httpErrorToString : ApiHttpError -> String
httpErrorToString httpError =
    case httpError of
        ApiBadUrl message ->
            "Bad URL: " ++ message

        ApiTimeout ->
            "Request timeout"

        ApiNetworkError ->
            networkErrorMessage

        ApiBadResponse payload ->
            payload.message

        ApiBadBody message ->
            message


authEmailValidationMessage : String -> Maybe String
authEmailValidationMessage rawEmail =
    let
        email =
            String.trim rawEmail

        invalidMessage =
            "invalid email"

        emailChars =
            String.toList email

        hasControlChars =
            List.any isControlChar emailChars

        hasWhitespace =
            List.any isWhitespaceChar emailChars
    in
    if String.length email > 254 || hasControlChars || hasWhitespace then
        Just invalidMessage

    else
        case String.split "@" email of
            [ localPart, rawDomain ] ->
                let
                    domain =
                        String.toLower rawDomain

                    labels =
                        String.split "." domain
                in
                if String.isEmpty localPart || String.length localPart > 64 then
                    Just invalidMessage

                else if String.isEmpty domain || String.length domain > 253 then
                    Just invalidMessage

                else if String.startsWith "." domain || String.endsWith "." domain || String.contains ".." domain then
                    Just invalidMessage

                else if not (List.all isValidEmailDomainLabel labels) then
                    Just invalidMessage

                else
                    Nothing

            _ ->
                Just invalidMessage


isValidEmailDomainLabel : String -> Bool
isValidEmailDomainLabel label =
    let
        chars =
            String.toList label

        startsOrEndsWithDash =
            case ( List.head chars, List.reverse chars |> List.head ) of
                ( Just firstChar, Just lastChar ) ->
                    firstChar == '-' || lastChar == '-'

                _ ->
                    False
    in
    not (String.isEmpty label)
        && String.length label
        <= 63
        && not startsOrEndsWithDash
        && List.all
            (\char ->
                Char.isLower char || Char.isDigit char || char == '-'
            )
            chars


isControlChar : Char -> Bool
isControlChar char =
    let
        code =
            Char.toCode char
    in
    code < 32 || code == 127


isWhitespaceChar : Char -> Bool
isWhitespaceChar char =
    case Char.toCode char of
        9 ->
            True

        10 ->
            True

        11 ->
            True

        12 ->
            True

        13 ->
            True

        32 ->
            True

        160 ->
            True

        _ ->
            False


authRequestCodeErrorToString : ApiHttpError -> String
authRequestCodeErrorToString httpError =
    case httpError of
        ApiTimeout ->
            "We could not send the code in time. Please try again."

        ApiNetworkError ->
            networkErrorMessage

        ApiBadResponse payload ->
            payload.message

        ApiBadBody message ->
            message

        _ ->
            httpErrorToString httpError


authLoginErrorToString : ApiHttpError -> String
authLoginErrorToString httpError =
    case httpError of
        ApiTimeout ->
            "We could not complete the sign-in in time. Please try again."

        ApiNetworkError ->
            networkErrorMessage

        ApiBadResponse payload ->
            payload.message

        ApiBadBody message ->
            message

        _ ->
            httpErrorToString httpError


shouldReloadCrudAfterLogin : Model -> Bool
shouldReloadCrudAfterLogin model =
    isCrudScreen model && hasAuthorizationError model.rows


isCrudScreen : Model -> Bool
isCrudScreen model =
    let
        hasEntitySelection =
            case model.selectedEntity of
                Just _ ->
                    True

                Nothing ->
                    False

        hasActionSelection =
            case model.selectedAction of
                Just _ ->
                    True

                Nothing ->
                    False
    in
    not model.performanceMode
        && not model.requestLogsMode
        && not model.databaseMode
        && not hasActionSelection
        && hasEntitySelection


hasAuthorizationError : Remote (List Row) -> Bool
hasAuthorizationError rowsRemote =
    case rowsRemote of
        Failed message ->
            let
                lowered =
                    String.toLower message
            in
            String.contains "401" message
                || String.contains "403" message
                || String.contains "authentication required" lowered
                || String.contains "not authorized" lowered
                || String.contains "admin role required" lowered
                || String.contains "forbidden" lowered

        _ ->
            False
