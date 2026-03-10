port module Main exposing (main)

import Mar.Api exposing (ActionInfo, AuthInfo, Entity, Field, FieldType(..), InputAliasField, InputAliasInfo, Row, Schema, SystemAuthInfo, decodeRows, decodeSchema, encodePayload, fieldTypeLabel, rowDecoder, valueToString)
import Browser
import Char
import Dict exposing (Dict)
import Element exposing (Element, alignLeft, centerX, centerY, column, el, fill, fillPortion, height, htmlAttribute, inFront, minimum, none, padding, paddingEach, paragraph, px, rgb255, rgba255, row, scrollbarY, spacing, text, width, wrappedRow)
import Element.Background as Background
import Element.Border as Border
import Element.Font as Font
import Element.Input as Input
import Html.Attributes as HtmlAttr
import Html.Events as HtmlEvents
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import String


type alias Flags =
    { apiBase : String
    , authToken : String
    , systemAuthToken : String
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
    | SystemAuthScope


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


type alias Model =
    { apiBase : String
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
    , flash : Maybe String
    }


type alias PendingDelete =
    { entity : Entity
    , idValue : String
    , message : String
    }


type Msg
    = GotSchema (Result Http.Error Schema)
    | SelectEntity String
    | SelectAction String
    | ReloadRows
    | GotRows (Result Http.Error (List Row))
    | SelectPerformance
    | SelectRequestLogs
    | SelectDatabase
    | ReloadDatabase
    | ReloadPerformance
    | ToggleMonitoringVersionDetails
    | ReloadRequestLogs
    | GotPerformance (Result Http.Error PerfPayload)
    | GotAdminVersion (Result Http.Error AdminVersionPayload)
    | GotRequestLogs (Result Http.Error RequestLogsPayload)
    | GotBackups (Result Http.Error BackupsPayload)
    | TriggerBackup
    | GotBackup (Result Http.Error BackupResponse)
    | SetAuthEmail String
    | SetAuthCode String
    | BackToAuthEmail
    | SwitchWorkspace WorkspaceMode
    | SetActionField String String
    | RequestAuthCode
    | GotRequestAuthCode AuthScope (Result Http.Error RequestCodeResponse)
    | BootstrapFirstAdmin
    | GotBootstrapFirstAdmin AuthScope (Result Http.Error RequestCodeResponse)
    | LoginWithCode
    | GotLoginWithCode AuthScope (Result Http.Error LoginResponse)
    | LoadAuthMe
    | GotAuthMe AuthScope (Result Http.Error AuthMeResponse)
    | LogoutSession
    | GotLogoutSession AuthScope (Result Http.Error ())
    | ToggleAuthTools
    | SelectRow Row
    | StartCreate
    | StartEdit Row
    | CancelForm
    | SetFormField String String
    | SubmitForm
    | GotCreate (Result Http.Error Row)
    | GotUpdate (Result Http.Error Row)
    | RequestDeleteRow Row
    | ConfirmDelete
    | CancelDelete
    | GotDelete (Result Http.Error ())
    | RunAction
    | GotRunAction (Result Http.Error Row)
    | ClearFlash


type alias RequestCodeResponse =
    { message : String
    , devCode : Maybe String
    }


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


main : Program Flags Model Msg
main =
    Browser.document
        { init = init
        , update = update
        , subscriptions = \_ -> Sub.none
        , view = view
        }


init : Flags -> ( Model, Cmd Msg )
init flags =
    let
        initialModel =
            { apiBase = flags.apiBase
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
            , flash = Nothing
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


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        GotSchema result ->
            case result of
                Ok schema ->
                    let
                        keepAuthToolsOpen =
                            model.authToolsOpen || (not (hasActiveSession model))

                        maybeEntity =
                            if keepAuthToolsOpen then
                                model.selectedEntity

                            else
                                preferredInitialEntity schema

                        shouldLoadRows =
                            maybeEntity /= Nothing

                        nextModel =
                            { model
                                | schema = Loaded schema
                                , performanceMode = False
                                , requestLogsMode = False
                                , databaseMode = False
                                , authToolsOpen = keepAuthToolsOpen
                                , selectedEntity = maybeEntity
                                , selectedAction = Nothing
                                , rows =
                                    if shouldLoadRows then
                                        Loading

                                    else
                                        NotAsked
                                , selectedRow = Nothing
                                , formMode = FormHidden
                                , formValues = Dict.empty
                                , actionFormValues = Dict.empty
                                , actionResult = Nothing
                                , requestLogs = NotAsked
                                , backups = NotAsked
                                , adminVersion = NotAsked
                                , authStage =
                                    if hasActiveSession model then
                                        AuthStageSession

                                    else if model.authStage == AuthStageCode || model.firstAdminCodeRequested then
                                        AuthStageCode

                                    else
                                        AuthStageEmail
                                , firstAdminCodeRequested =
                                    model.firstAdminCodeRequested
                                        && String.trim model.authToken == ""
                            }
                    in
                    if shouldLoadRows then
                        ( nextModel, loadRows nextModel )

                    else
                        ( nextModel, Cmd.none )

                Err httpError ->
                    ( { model | schema = Failed (httpErrorToString model httpError), rows = Failed "schema unavailable" }, Cmd.none )

        SelectEntity entityName ->
            let
                nextEntity =
                    findEntity entityName model

                nextModel =
                    { model
                        | performanceMode = False
                        , requestLogsMode = False
                        , databaseMode = False
                        , authToolsOpen = False
                        , selectedEntity = nextEntity
                        , selectedAction = Nothing
                        , rows = Loading
                        , selectedRow = Nothing
                        , formMode = FormHidden
                        , formValues = Dict.empty
                        , actionResult = Nothing
                        , flash = Nothing
                    }
            in
            ( nextModel, loadRows nextModel )

        SelectAction actionName ->
            let
                nextAction =
                    findAction actionName model
            in
            case nextAction of
                Nothing ->
                    ( { model | flash = Just "Action not found" }, Cmd.none )

                Just actionInfo ->
                    ( { model
                        | performanceMode = False
                        , requestLogsMode = False
                        , databaseMode = False
                        , authToolsOpen = False
                        , selectedAction = Just actionInfo
                        , selectedEntity = Nothing
                        , rows = NotAsked
                        , selectedRow = Nothing
                        , formMode = FormHidden
                        , formValues = Dict.empty
                        , actionFormValues = actionFormDefaults model actionInfo
                        , actionResult = Nothing
                        , flash = Nothing
                      }
                    , Cmd.none
                    )

        ReloadRows ->
            let
                nextModel =
                    { model | rows = Loading, flash = Nothing }
            in
            ( nextModel, loadRows nextModel )

        GotRows result ->
            case result of
                Ok rows ->
                    ( { model | rows = Loaded rows }, Cmd.none )

                Err httpError ->
                    ( { model | rows = Failed (httpErrorToString model httpError) }, Cmd.none )

        SelectPerformance ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Admin role required to access monitoring tools." }, Cmd.none )

            else
                let
                    nextModel =
                        { model
                            | performanceMode = True
                            , requestLogsMode = False
                            , databaseMode = False
                            , authToolsOpen = False
                            , selectedEntity = Nothing
                            , selectedAction = Nothing
                            , selectedRow = Nothing
                            , rows = NotAsked
                            , formMode = FormHidden
                            , formValues = Dict.empty
                            , actionResult = Nothing
                            , perf = Loading
                            , adminVersion = Loading
                            , monitoringVersionDetailsOpen = False
                            , flash = Nothing
                        }
                in
                ( nextModel, Cmd.batch [ loadPerformance nextModel, loadAdminVersion nextModel ] )

        SelectRequestLogs ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Admin role required to access request logs." }, Cmd.none )

            else
                let
                    nextModel =
                        { model
                            | performanceMode = False
                            , requestLogsMode = True
                            , databaseMode = False
                            , authToolsOpen = False
                            , selectedEntity = Nothing
                            , selectedAction = Nothing
                            , selectedRow = Nothing
                            , rows = NotAsked
                            , formMode = FormHidden
                            , formValues = Dict.empty
                            , actionResult = Nothing
                            , requestLogs = Loading
                            , flash = Nothing
                        }
                in
                ( nextModel, loadRequestLogs nextModel )

        SelectDatabase ->
            if not (isAdminProfile model) then
                ( { model | flash = Just "Admin role required to access database tools." }, Cmd.none )

            else
                let
                    nextModel =
                        { model
                            | performanceMode = False
                            , requestLogsMode = False
                            , databaseMode = True
                            , authToolsOpen = False
                            , selectedEntity = Nothing
                            , selectedAction = Nothing
                            , selectedRow = Nothing
                            , rows = NotAsked
                            , formMode = FormHidden
                            , formValues = Dict.empty
                            , actionResult = Nothing
                            , perf = Loading
                            , backups = Loading
                            , flash = Nothing
                        }
                in
                ( nextModel, Cmd.batch [ loadPerformance nextModel, loadBackups nextModel ] )

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
                    ( { model | perf = Failed (httpErrorToString model httpError) }, Cmd.none )

        GotAdminVersion result ->
            case result of
                Ok payload ->
                    ( { model | adminVersion = Loaded payload }, Cmd.none )

                Err httpError ->
                    ( { model | adminVersion = Failed (httpErrorToString model httpError) }, Cmd.none )

        GotRequestLogs result ->
            case result of
                Ok payload ->
                    ( { model | requestLogs = Loaded payload }, Cmd.none )

                Err httpError ->
                    ( { model | requestLogs = Failed (httpErrorToString model httpError) }, Cmd.none )

        GotBackups result ->
            case result of
                Ok backups ->
                    ( { model | backups = Loaded backups }, Cmd.none )

                Err httpError ->
                    ( { model | backups = Failed (httpErrorToString model httpError) }, Cmd.none )

        SetAuthEmail email ->
            ( { model | authEmail = email }, Cmd.none )

        SetAuthCode code ->
            ( { model | authCode = code }, Cmd.none )

        BackToAuthEmail ->
            ( { model | authStage = AuthStageEmail, authCode = "", authSubmitting = Nothing, flash = Nothing }, Cmd.none )

        SwitchWorkspace workspace ->
            let
                nextEntity =
                    case model.schema of
                        Loaded schema ->
                            case workspace of
                                AppWorkspace ->
                                    preferredInitialEntity schema

                                AdminWorkspace ->
                                    model.selectedEntity

                        _ ->
                            model.selectedEntity

                shouldLoadRows =
                    workspace == AppWorkspace && nextEntity /= Nothing

                nextModel =
                    { model
                        | workspace = workspace
                        , authToolsOpen = False
                        , performanceMode = False
                        , requestLogsMode = False
                        , databaseMode = False
                        , selectedAction = Nothing
                        , selectedEntity = nextEntity
                        , rows =
                            if shouldLoadRows then
                                Loading

                            else
                                model.rows
                        , selectedRow = Nothing
                        , formMode = FormHidden
                        , formValues = Dict.empty
                        , actionFormValues = Dict.empty
                        , actionResult = Nothing
                        , flash = Nothing
                    }
            in
            if shouldLoadRows then
                ( nextModel, loadRows nextModel )

            else
                ( nextModel, Cmd.none )

        SetActionField fieldName value ->
            ( { model | actionFormValues = Dict.insert fieldName value model.actionFormValues }, Cmd.none )

        RequestAuthCode ->
            if String.trim model.authEmail == "" then
                ( { model | flash = Just "Email is required for request-code" }, Cmd.none )

            else
                let
                    scope =
                        activeAuthScope model
                in
                ( { model | flash = Nothing, authSubmitting = Just AuthSubmitSendingCode }, requestAuthCode scope model )

        GotRequestAuthCode scope result ->
            case result of
                Ok response ->
                    case response.devCode of
                        Just code ->
                            ( { model
                                | authCode = code
                                , authStage = AuthStageCode
                                , authSubmitting = Nothing
                                , flash = Nothing
                              }
                            , Cmd.none
                            )

                        Nothing ->
                            ( { model
                                | authStage = AuthStageCode
                                , authSubmitting = Nothing
                                , flash = Nothing
                              }
                            , Cmd.none
                            )

                Err httpError ->
                    ( { model | authSubmitting = Nothing, flash = Just (authRequestCodeErrorToString model httpError) }, Cmd.none )

        BootstrapFirstAdmin ->
            if String.trim model.authEmail == "" then
                ( { model | flash = Just "Email is required to create the first admin" }, Cmd.none )

            else
                let
                    scope =
                        activeAuthScope model
                in
                ( { model | flash = Nothing, authSubmitting = Just AuthSubmitSendingCode }, bootstrapFirstAdmin scope model )

        GotBootstrapFirstAdmin scope result ->
            case result of
                Ok response ->
                    case response.devCode of
                        Just code ->
                            let
                                nextModel =
                                    { model
                                        | authCode = code
                                        , authStage = AuthStageCode
                                        , authSubmitting = Nothing
                                        , firstAdminCodeRequested = True
                                        , flash = Nothing
                                    }
                            in
                            ( nextModel
                            , loadSchema model.apiBase
                            )

                        Nothing ->
                            ( { model | authStage = AuthStageCode, authSubmitting = Nothing, firstAdminCodeRequested = True, flash = Nothing }, loadSchema model.apiBase )

                Err httpError ->
                    ( { model | authSubmitting = Nothing, flash = Just (authRequestCodeErrorToString model httpError) }, Cmd.none )

        LoginWithCode ->
            if String.trim model.authEmail == "" || String.trim model.authCode == "" then
                ( { model | flash = Just "Email and code are required for login" }, Cmd.none )

            else
                let
                    scope =
                        activeAuthScope model
                in
                ( { model | flash = Nothing, authSubmitting = Just AuthSubmitSigningIn }, loginWithCode scope model )

        GotLoginWithCode scope result ->
            case result of
                Ok response ->
                    case scope of
                        AppAuthScope ->
                            let
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

                        SystemAuthScope ->
                            let
                                nextModel =
                                    { model
                                        | systemAuthToken = ""
                                        , currentSystemRole = response.role
                                        , currentSystemEmail = response.email
                                        , authEmail = ""
                                        , authCode = ""
                                        , authStage = AuthStageSession
                                        , authSubmitting = Nothing
                                        , sessionRestorePending = False
                                        , authToolsOpen = False
                                        , flash = Just "Admin login successful."
                                    }

                                refreshCmd =
                                    if model.performanceMode then
                                        Cmd.batch [ loadPerformance nextModel, loadAdminVersion nextModel ]

                                    else if model.requestLogsMode then
                                        loadRequestLogs nextModel

                                    else if model.databaseMode then
                                        Cmd.batch [ loadPerformance nextModel, loadBackups nextModel ]

                                    else
                                        Cmd.none
                            in
                            ( nextModel, Cmd.batch [ refreshCmd, saveSessionFromModel nextModel ] )

                Err httpError ->
                    ( { model | authSubmitting = Nothing, flash = Just (authLoginErrorToString model httpError) }, Cmd.none )

        LoadAuthMe ->
            let
                scope =
                    activeAuthScope model
            in
            ( { model | flash = Nothing }, loadAuthMe scope model )

        GotAuthMe scope result ->
            case result of
                Ok response ->
                    let
                        roleText =
                            case response.role of
                                Just role ->
                                    " (role: " ++ role ++ ")"

                                Nothing ->
                                    ""
                    in
                    case scope of
                        AppAuthScope ->
                            let
                                preferredEntity =
                                    case model.schema of
                                        Loaded schema ->
                                            preferredInitialEntity schema

                                        _ ->
                                            Nothing

                                shouldLoadRows =
                                    model.selectedEntity == Nothing
                                        && preferredEntity /= Nothing
                                        && currentWorkspace model == AppWorkspace

                                nextModel =
                                    { model
                                        | currentEmail = Just response.email
                                        , currentRole = response.role
                                        , authStage = AuthStageSession
                                        , sessionRestorePending = False
                                        , authToolsOpen = False
                                        , selectedEntity =
                                            if model.selectedEntity == Nothing then
                                                preferredEntity

                                            else
                                                model.selectedEntity
                                        , rows =
                                            if shouldLoadRows then
                                                Loading

                                            else
                                                model.rows
                                        , flash =
                                            if currentWorkspace model == AppWorkspace then
                                                Nothing

                                            else
                                                Just ("Authenticated as " ++ response.email ++ roleText)
                                    }
                            in
                            if shouldLoadRows then
                                ( nextModel, loadRows nextModel )

                            else
                                ( nextModel, Cmd.none )

                        SystemAuthScope ->
                            ( { model
                                | currentSystemEmail = Just response.email
                                , currentSystemRole = response.role
                                , authStage = AuthStageSession
                                , sessionRestorePending = False
                                , authToolsOpen = False
                                , flash =
                                    if currentWorkspace model == AppWorkspace then
                                        Nothing

                                    else
                                        Just ("Admin authenticated as " ++ response.email ++ roleText)
                              }
                            , Cmd.none
                            )

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

                            SystemAuthScope ->
                                let
                                    nextModel =
                                        { model
                                            | systemAuthToken = ""
                                            , currentSystemEmail = Nothing
                                            , currentSystemRole = Nothing
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
                                    Just (httpErrorToString model httpError)
                          }
                        , Cmd.none
                        )

        LogoutSession ->
            let
                scope =
                    activeAuthScope model
            in
            ( { model | flash = Nothing }, logoutSession scope model )

        GotLogoutSession scope result ->
            case result of
                Ok _ ->
                    case scope of
                        AppAuthScope ->
                            let
                                nextModel =
                                    { model | authToken = "", currentEmail = Nothing, currentRole = Nothing, authEmail = "", authCode = "", authStage = AuthStageEmail, authSubmitting = Nothing, sessionRestorePending = False, flash = Just "User session logged out." }
                            in
                            let
                                finalModel =
                                    { nextModel | authToolsOpen = True }
                            in
                            ( finalModel, saveSessionFromModel finalModel )

                        SystemAuthScope ->
                            let
                                nextModel =
                                    { model | systemAuthToken = "", currentSystemEmail = Nothing, currentSystemRole = Nothing, authEmail = "", authCode = "", authStage = AuthStageEmail, authSubmitting = Nothing, sessionRestorePending = False, flash = Just "Admin session logged out." }
                            in
                            let
                                finalModel =
                                    { nextModel | authToolsOpen = True }
                            in
                            ( finalModel, saveSessionFromModel finalModel )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString model httpError) }, Cmd.none )

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
                    in
                    let
                        nextModel =
                            { model | lastBackup = Just response, flash = Just ("Backup created at " ++ response.path ++ "." ++ removedText), backups = Loading }
                    in
                    ( nextModel, Cmd.batch [ loadBackups nextModel, loadPerformance nextModel ] )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString model httpError) }, Cmd.none )

        ToggleAuthTools ->
            let
                nextModel =
                    { model
                        | authToolsOpen = True
                        , authStage =
                            if hasActiveSession model then
                                AuthStageSession

                            else if model.authStage == AuthStageCode || model.firstAdminCodeRequested then
                                AuthStageCode

                            else
                                AuthStageEmail
                        , authSubmitting = Nothing
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
                        , flash = Nothing
                    }
            in
            if model.authToolsOpen then
                ( { nextModel | schema = Loading }, loadSchema model.apiBase )

            else
                ( nextModel, Cmd.none )

        SelectRow rowValue ->
            ( { model | selectedRow = Just rowValue }, Cmd.none )

        StartCreate ->
            let
                defaults =
                    formDefaults model
            in
            ( { model | formMode = FormCreate, formValues = defaults, selectedRow = Nothing, flash = Nothing }, Cmd.none )

        StartEdit rowValue ->
            let
                defaults =
                    formFromRow model rowValue
            in
            ( { model | formMode = FormEdit rowValue, formValues = defaults, selectedRow = Just rowValue, flash = Nothing }, Cmd.none )

        CancelForm ->
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
                    in
                    ( { model | rows = nextRows, formMode = FormHidden, formValues = Dict.empty, flash = Just "Created successfully" }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString model httpError) }, Cmd.none )

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
                    in
                    ( { model | rows = nextRows, selectedRow = Just updatedRow, formMode = FormHidden, formValues = Dict.empty, flash = Just "Updated successfully" }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString model httpError) }, Cmd.none )

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
                            { model | flash = Just "Deleted successfully", selectedRow = Nothing, formMode = FormHidden, formValues = Dict.empty, pendingDelete = Nothing }
                    in
                    ( { nextModel | rows = Loading }, loadRows nextModel )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString model httpError), pendingDelete = Nothing }, Cmd.none )

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
                    ( { model | flash = Just (httpErrorToString model httpError) }, Cmd.none )

        ClearFlash ->
            ( { model | flash = Nothing }, Cmd.none )


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


expectJsonWithApiError : (Result Http.Error a -> msg) -> Decode.Decoder a -> Http.Expect msg
expectJsonWithApiError toMsg decoder =
    Http.expectStringResponse toMsg
        (\response ->
            case response of
                Http.GoodStatus_ _ body ->
                    case Decode.decodeString decoder body of
                        Ok value ->
                            Ok value

                        Err decodeError ->
                            Err (Http.BadBody ("Failed to decode response: " ++ Decode.errorToString decodeError))

                Http.BadStatus_ metadata body ->
                    Err (Http.BadBody (apiErrorMessage metadata.statusCode body))

                Http.BadUrl_ url ->
                    Err (Http.BadUrl url)

                Http.Timeout_ ->
                    Err Http.Timeout

                Http.NetworkError_ ->
                    Err Http.NetworkError
        )


expectUnitWithApiError : (Result Http.Error () -> msg) -> Http.Expect msg
expectUnitWithApiError toMsg =
    Http.expectStringResponse toMsg
        (\response ->
            case response of
                Http.GoodStatus_ _ _ ->
                    Ok ()

                Http.BadStatus_ metadata body ->
                    Err (Http.BadBody (apiErrorMessage metadata.statusCode body))

                Http.BadUrl_ url ->
                    Err (Http.BadUrl url)

                Http.Timeout_ ->
                    Err Http.Timeout

                Http.NetworkError_ ->
                    Err Http.NetworkError
        )


apiErrorMessage : Int -> String -> String
apiErrorMessage statusCode body =
    let
        fallback =
            "HTTP error: " ++ String.fromInt statusCode
    in
    case Decode.decodeString apiErrorDecoder body of
        Ok message ->
            message

        Err _ ->
            fallback


apiErrorDecoder : Decode.Decoder String
apiErrorDecoder =
    Decode.oneOf
        [ Decode.field "error" Decode.string
        , Decode.field "message" Decode.string
        ]


requestCodeDecoder : Decode.Decoder RequestCodeResponse
requestCodeDecoder =
    Decode.map2 RequestCodeResponse
        (Decode.field "message" Decode.string)
        (Decode.oneOf
            [ Decode.field "devCode" (Decode.map Just Decode.string)
            , Decode.succeed Nothing
            ]
        )


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
        |> List.filterMap (\fieldName -> rowFieldLabel fieldName rowValue)
        |> List.head


rowFieldLabel : String -> Row -> Maybe String
rowFieldLabel fieldName rowValue =
    Dict.get fieldName rowValue
        |> Maybe.map valueToString
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
            if char == '_' || char == '-' || char == ' ' || char == '\n' || char == '\t' || char == '\r' then
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
                        , case field.fieldType of
                            BoolType ->
                                if field.optional then
                                    ""

                                else
                                    "false"

                            _ ->
                                ""
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
    if hasActiveSession model then
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


viewAuthGate : Model -> Element Msg
viewAuthGate model =
    let
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
                    , "Enter the 6-digit code we sent to continue."
                    )

                AuthStageSession ->
                    ( "Session ready"
                    , "Your admin session is active."
                    )

        authCardBody =
            case authStage of
                AuthStageEmail ->
                    viewAuthEmailStage model firstAdminMode (if firstAdminMode then "Send access code" else "Continue") (if firstAdminMode then BootstrapFirstAdmin else RequestAuthCode) (model.authSubmitting == Just AuthSubmitSendingCode)

                AuthStageCode ->
                    viewAuthCodeStage model firstAdminMode "Send code again" (if firstAdminMode then BootstrapFirstAdmin else RequestAuthCode) (model.authSubmitting == Just AuthSubmitSigningIn) (model.authSubmitting == Just AuthSubmitSendingCode)

                AuthStageSession ->
                    viewAuthSessionStage model
    in
    el
        [ width fill
        , height fill
        , padding 24
        ]
        (el
            [ centerX
            , centerY
            , width (px 460)
            ]
            (column
                [ width fill
                , spacing 14
                ]
                [ column
                    [ width fill
                    , spacing 18
                    , padding 28
                    , Background.color (rgb255 255 255 255)
                    , Border.rounded 18
                    , Border.width 1
                    , Border.color (rgb255 226 232 239)
                    , htmlAttribute (HtmlAttr.class "auth-stage auth-gate-card")
                    ]
                    [ column
                        [ width fill
                        , spacing 8
                        ]
                        [ el
                            [ Font.size 28
                            , Font.bold
                            , centerX
                            ]
                            (text (currentAppName model))
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
                        , paragraph
                            [ centerX
                            , Font.size 14
                            , Font.color (rgb255 93 103 120)
                            ]
                            [ text stageSubtitle ]
                        ]
                    , authCardBody
                    ]
                , viewFlash model
                ]
            )
        )


viewSidebar : Model -> Element Msg
viewSidebar model =
    let
        workspace =
            currentWorkspace model

        appName =
            currentAppName model

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
                    ([ paragraph [ alignLeft ] [ text title ] ]
                        ++ (case maybeSubtitle of
                                Just subtitle ->
                                    [ paragraph [ alignLeft, Font.size 11, Font.color (rgb255 170 181 196) ] [ text subtitle ] ]

                                Nothing ->
                                    []
                           )
                    )

        workspaceSwitch : List (Element Msg)
        workspaceSwitch =
            if isAdminProfile model then
                [ row [ width fill, spacing 8 ]
                    [ Input.button
                        [ width fill
                        , Border.rounded 10
                        , Background.color
                            (if workspace == AppWorkspace then
                                rgb255 54 94 217

                             else
                                rgb255 24 29 36
                            )
                        , Font.color (rgb255 244 246 248)
                        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                        ]
                        { onPress = Just (SwitchWorkspace AppWorkspace)
                        , label = text "App"
                        }
                    , Input.button
                        [ width fill
                        , Border.rounded 10
                        , Background.color
                            (if workspace == AdminWorkspace then
                                rgb255 54 94 217

                             else
                                rgb255 24 29 36
                            )
                        , Font.color (rgb255 244 246 248)
                        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                        ]
                        { onPress = Just (SwitchWorkspace AdminWorkspace)
                        , label = text "Admin"
                        }
                    ]
                ]

            else
                []

        entityButton entity =
            let
                selected =
                    (not model.authToolsOpen)
                        && (not model.performanceMode)
                        && (not model.requestLogsMode)
                        && (not model.databaseMode)
                        &&
                            (case model.selectedEntity of
                                Just current ->
                                    current.name == entity.name

                                Nothing ->
                                    False
                            )

                backgroundColor =
                    if selected then
                        rgb255 54 94 217

                    else
                        rgb255 24 29 36
            in
            Input.button
                [ width fill
                , Border.rounded 10
                , Background.color backgroundColor
                , Font.color (rgb255 244 246 248)
                , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
                ]
                { onPress = Just (SelectEntity entity.name)
                , label =
                    sidebarItemLabel entity.name (Just entity.resource)
                }

        actionEndpointCard : ActionInfo -> Element Msg
        actionEndpointCard actionInfo =
            let
                selected =
                    (not model.authToolsOpen)
                        && (not model.performanceMode)
                        && (not model.requestLogsMode)
                        && (not model.databaseMode)
                        &&
                            (case model.selectedAction of
                                Just current ->
                                    current.name == actionInfo.name

                                Nothing ->
                                    False
                            )

                backgroundColor =
                    if selected then
                        rgb255 54 94 217

                    else
                        rgb255 24 29 36
            in
            Input.button
                [ width fill
                , Border.rounded 10
                , Background.color backgroundColor
                , Font.color (rgb255 244 246 248)
                , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
                ]
                { onPress = Just (SelectAction actionInfo.name)
                , label =
                    sidebarItemLabel actionInfo.name (Just ("/actions/" ++ actionInfo.name))
                }

        performanceButton : Element Msg
        performanceButton =
            let
                backgroundColor =
                    if model.performanceMode && (not model.authToolsOpen) then
                        rgb255 54 94 217

                    else
                        rgb255 24 29 36
            in
            Input.button
                [ width fill
                , Border.rounded 10
                , Background.color backgroundColor
                , Font.color (rgb255 244 246 248)
                , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
                ]
                { onPress = Just SelectPerformance
                , label =
                    sidebarItemLabel "Monitoring" (Just "/_mar/perf")
                }

        requestLogsButton : Element Msg
        requestLogsButton =
            let
                backgroundColor =
                    if model.requestLogsMode && (not model.authToolsOpen) then
                        rgb255 54 94 217

                    else
                        rgb255 24 29 36
            in
            Input.button
                [ width fill
                , Border.rounded 10
                , Background.color backgroundColor
                , Font.color (rgb255 244 246 248)
                , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
                ]
                { onPress = Just SelectRequestLogs
                , label =
                    sidebarItemLabel "Logs" (Just "/_mar/request-logs")
                }

        databaseButton : Element Msg
        databaseButton =
            let
                backgroundColor =
                    if model.databaseMode && (not model.authToolsOpen) then
                        rgb255 54 94 217

                    else
                        rgb255 24 29 36
            in
            Input.button
                [ width fill
                , Border.rounded 10
                , Background.color backgroundColor
                , Font.color (rgb255 244 246 248)
                , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
                ]
                { onPress = Just SelectDatabase
                , label =
                    sidebarItemLabel "Database" (Just "/_mar/backups")
                }

        authToolsButton : Element Msg
        authToolsButton =
            let
                backgroundColor =
                    if model.authToolsOpen then
                        rgb255 54 94 217

                    else
                        rgb255 24 29 36
            in
            Input.button
                [ width fill
                , Border.rounded 10
                , Background.color backgroundColor
                , Font.color (rgb255 244 246 248)
                , paddingEach { top = 12, right = 12, bottom = 12, left = 12 }
                ]
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
        [ width (px 280)
        , height fill
        , scrollbarY
        , Background.color (rgb255 18 22 28)
        , padding 20
        , spacing 16
        ]
        (List.concat
            [ [ row [ width fill, spacing 8 ]
                    [ el [ Font.size 24, Font.bold, Font.color (rgb255 240 245 250) ] (text appName) ]
              , el [ Font.size 13, Font.color (rgb255 144 158 179) ]
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
                el [ Font.size 11, Font.bold, Font.color (rgb255 118 136 160) ] (text (if workspace == AppWorkspace then "ACCOUNT" else "AUTH"))
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
                    [ paddingEach { top = 10, right = 0, bottom = 0, left = 0 }
                    , Font.size 11
                    , Font.bold
                    , Font.color (rgb255 118 136 160)
                    ]
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
                    [ paddingEach { top = 10, right = 0, bottom = 0, left = 0 }
                    , Font.size 11
                    , Font.bold
                    , Font.color (rgb255 118 136 160)
                    ]
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
                    [ paddingEach { top = 10, right = 0, bottom = 0, left = 0 }
                    , Font.size 11
                    , Font.bold
                    , Font.color (rgb255 118 136 160)
                    ]
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
    column
        [ width fill
        , height fill
        , scrollbarY
        , padding 24
        , spacing 16
        ]
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
                    row [ width fill, height fill, spacing 16 ]
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

            maybeSystemAuth =
                systemAuthInfoFromModel model

            activeScope =
                activeAuthScope model

            authStage =
                currentAuthStage model

            activeBadgeText =
                if workspace == AppWorkspace then
                    []

                else
                    case activeScope of
                        AppAuthScope ->
                            [ badge "POST /auth/request-code"
                            , badge "POST /auth/login"
                            , badge "GET /auth/me"
                            , badge "POST /auth/logout"
                            ]

                        SystemAuthScope ->
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

                        SystemAuthScope ->
                            case maybeSystemAuth of
                                Just systemAuth ->
                                    "Transport: " ++ systemAuth.emailTransport ++ " | Scope: admin"

                                Nothing ->
                                    "Admin authentication is not available."

            needsBootstrap =
                case activeScope of
                    AppAuthScope ->
                        case maybeAppAuth of
                            Just appAuth ->
                                appAuth.needsBootstrap

                            Nothing ->
                                False

                    SystemAuthScope ->
                        case maybeSystemAuth of
                            Just systemAuth ->
                                systemAuth.needsBootstrap

                            Nothing ->
                                False

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

                    SystemAuthScope ->
                        needsBootstrap

            authFlowTitle =
                if workspace == AppWorkspace then
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                "Get started"

                            else
                                "Sign in"

                        AuthStageCode ->
                            "Enter code"

                        AuthStageSession ->
                            "Your account"

                else
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                "First access"

                            else
                                "Login"

                        AuthStageCode ->
                            "Enter code"

                        AuthStageSession ->
                            "Session"

            authFlowSteps =
                if workspace == AppWorkspace then
                    case authStage of
                        AuthStageEmail ->
                            if firstAdminMode then
                                [ "Use your email to receive the first access code." ]

                            else
                                [ "We will send a 6-digit code to your email." ]

                        AuthStageCode ->
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

            tabHint =
                if workspace == AppWorkspace then
                    ""

                else
                    case activeScope of
                        AppAuthScope ->
                            if firstAdminMode then
                                "Complete first admin setup with the same email and login code."

                            else
                                "The request sends a login code and automatically creates the user if it does not exist."

                        SystemAuthScope ->
                            if needsBootstrap then
                                "No admins found. Create the first admin, then login with the code."

                            else
                                "Admin authentication is used only for admin features such as Monitoring and Database backups."

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
                [ width fill
                , spacing 12
                , padding 16
                , Background.color (rgb255 255 255 255)
                , Border.rounded 14
                , Border.width 1
                , Border.color (rgb255 226 232 239)
                ]
                [ viewPanelHeader
                    (if workspace == AppWorkspace then
                        "Account"

                     else
                        "Authorization"
                    )
                    (if String.trim transportText == "" then
                        []

                     else
                        [ el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text transportText) ]
                    )
                    []
                , if List.isEmpty activeBadgeText then
                    none

                  else
                    row [ width fill, spacing 8 ] activeBadgeText
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
                , if String.trim tabHint == "" then
                    none

                  else
                    el
                        [ Font.size 12
                        , Font.color
                            (if needsBootstrap then
                                rgb255 106 84 31

                             else
                                rgb255 93 103 120
                            )
                        ]
                        (text tabHint)
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
            (width fill
                :: if isLoading then
                    []

                   else
                    [ onEnter submitMsg ]
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
                (if isLoading then
                    actionLabel ++ "..."

                 else
                    actionLabel
                )
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
            [ width fill
            , spacing 6
            , Background.color (rgb255 247 249 253)
            , Border.rounded 10
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 10
            ]
            [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text "Code sent to")
            , el [ Font.bold, Font.size 14, Font.color (rgb255 44 56 72) ] (text emailText)
            ]
        , Input.text
            (width fill
                :: if loginLoading || resendLoading then
                    []

                   else
                    [ onEnter LoginWithCode ]
            )
            { onChange = SetAuthCode
            , text = model.authCode
            , placeholder = Just (Input.placeholder [] (text "6-digit code"))
            , label = Input.labelAbove [ Font.size 12 ] (text "Login code")
            }
        , row [ width fill, spacing 10 ]
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
                (if resendLoading then
                    resendLabel ++ "..."

                 else
                    resendLabel
                )
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
                (if loginLoading then
                    "Signing in..."

                 else
                    "Login"
                )
            )
        ]


viewAuthSessionStage : Model -> Element Msg
viewAuthSessionStage model =
    let
        emailText =
            case model.currentEmail of
                Just email ->
                    email

                Nothing ->
                    case model.currentSystemEmail of
                        Just email ->
                            email

                        Nothing ->
                            "Authenticated session"

        roleText =
            case model.currentRole of
                Just role ->
                    String.trim role

                Nothing ->
                    case model.currentSystemRole of
                        Just role ->
                            String.trim role

                        Nothing ->
                            ""
    in
    column
        [ width fill
        , spacing 12
        , htmlAttribute (HtmlAttr.class "auth-stage auth-stage-session")
        ]
        [ column
            [ width fill
            , spacing 6
            , Background.color (rgb255 243 248 245)
            , Border.rounded 10
            , Border.width 1
            , Border.color (rgb255 198 222 209)
            , padding 10
            ]
            [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text "Authenticated as")
            , el [ Font.bold, Font.size 14, Font.color (rgb255 44 56 72) ] (text emailText)
            , if roleText == "" then
                none

              else
                el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text ("Role: " ++ roleText))
            ]
        , row [ width fill, spacing 10 ]
            [ authDangerButton (Just LogoutSession) "Logout" ]
        ]


authActionButton : Element.Color -> Element.Color -> Maybe Msg -> String -> Element Msg
authActionButton backgroundColor textColor onPress labelText =
    Input.button
        [ width fill
        , Background.color backgroundColor
        , Font.color textColor
        , Border.rounded 10
        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
        ]
        { onPress = onPress
        , label = text labelText
        }


authSecondaryButton : Maybe Msg -> String -> Element Msg
authSecondaryButton onPress labelText =
    Input.button
        [ Background.color (rgb255 224 231 241)
        , Font.color (rgb255 55 68 87)
        , Border.rounded 10
        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
        ]
        { onPress = onPress
        , label = text labelText
        }


authDangerButton : Maybe Msg -> String -> Element Msg
authDangerButton onPress labelText =
    Input.button
        [ Background.color (rgb255 248 226 226)
        , Font.color (rgb255 126 43 43)
        , Border.rounded 10
        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
        ]
        { onPress = onPress
        , label = text labelText
        }


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
                            rgb255 84 121 224

                        BooleanFalse ->
                            rgb255 212 219 229

                        BooleanUnset ->
                            rgb255 234 238 244
                    )
                , Border.width 1
                , Border.color
                    (case state of
                        BooleanTrue ->
                            rgb255 70 106 206

                        BooleanFalse ->
                            rgb255 197 205 217

                        BooleanUnset ->
                            rgb255 214 221 231
                    )
                , Border.rounded 999
                , paddingEach { top = 3, right = 3, bottom = 3, left = 3 }
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
                rgb255 233 236 242

             else
                rgb255 246 247 250
            )
        , Font.color
            (if selected then
                rgb255 55 68 87

             else
                rgb255 109 121 138
            )
        , Border.width 1
        , Border.color
            (if selected then
                rgb255 205 212 222

             else
                rgb255 225 230 237
            )
        , Border.rounded 999
        , paddingEach { top = 8, right = 12, bottom = 8, left = 12 }
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


systemAuthInfoFromModel : Model -> Maybe SystemAuthInfo
systemAuthInfoFromModel model =
    case model.schema of
        Loaded schema ->
            schema.systemAuth

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

        maybeSystemAuth =
            systemAuthInfoFromModel model

        needsBootstrap =
            case activeAuthScope model of
                AppAuthScope ->
                    case maybeAppAuth of
                        Just appAuth ->
                            appAuth.needsBootstrap

                        Nothing ->
                            False

                SystemAuthScope ->
                    case maybeSystemAuth of
                        Just systemAuth ->
                            systemAuth.needsBootstrap

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


currentAppName : Model -> String
currentAppName model =
    case model.schema of
        Loaded schema ->
            String.trim schema.appName

        _ ->
            "Mar"


activeAuthScope : Model -> AuthScope
activeAuthScope _ =
    AppAuthScope


authScopeLabel : AuthScope -> String
authScopeLabel scope =
    case scope of
        AppAuthScope ->
            "user"

        SystemAuthScope ->
            "admin"


isAdminProfile : Model -> Bool
isAdminProfile model =
    case model.currentRole of
        Just role ->
            String.toLower (String.trim role) == "admin"

        Nothing ->
            case model.currentSystemRole of
                Just role ->
                    String.toLower (String.trim role) == "admin"

                Nothing ->
                    False


saveSessionFromModel : Model -> Cmd Msg
saveSessionFromModel model =
    saveSession
        (Encode.object
            [ ( "authToken", Encode.string (String.trim model.authToken) )
            , ( "systemAuthToken", Encode.string (String.trim model.systemAuthToken) )
            ]
        )


isUnauthorizedError : Http.Error -> Bool
isUnauthorizedError httpError =
    case httpError of
        Http.BadBody message ->
            String.contains "HTTP error: 401" message

        _ ->
            False


hasActiveSession : Model -> Bool
hasActiveSession model =
    (model.currentEmail /= Nothing)
        || (model.currentSystemEmail /= Nothing)


viewPanelTitle : String -> List (Element msg) -> Element msg
viewPanelTitle title details =
    column [ width fill, spacing 6 ]
        (el [ Font.bold, Font.size 20 ] (text title) :: details)


viewPanelHeader : String -> List (Element msg) -> List (Element msg) -> Element msg
viewPanelHeader title details actions =
    let
        titleBlock =
            if List.isEmpty actions then
                el
                    [ width fill
                    , paddingEach { top = 4, right = 0, bottom = 0, left = 0 }
                    ]
                    (viewPanelTitle title details)

            else
                viewPanelTitle title details
    in
    row [ width fill, spacing 10 ]
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
                , row
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
                    , Input.button
                        [ Background.color (rgb255 217 229 250)
                        , Border.rounded 8
                        , paddingEach { top = 6, right = 10, bottom = 6, left = 10 }
                        ]
                        { onPress = Just ClearFlash
                        , label = text "Close"
                        }
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
                ]
                (el
                    [ centerX
                    , centerY
                    , width (px 480)
                    , Background.color (rgb255 255 255 255)
                    , Border.rounded 14
                    , Border.width 1
                    , Border.color (rgb255 226 232 239)
                    , padding 18
                    ]
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
                        , row
                            [ width fill
                            , spacing 10
                            ]
                            [ el [ width fill ] none
                            , Input.button
                                [ Background.color (rgb255 224 231 241)
                                , Border.rounded 10
                                , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                                ]
                                { onPress = Just CancelDelete
                                , label = text "Cancel"
                                }
                            , Input.button
                                [ Background.color (rgb255 176 60 46)
                                , Font.color (rgb255 252 247 246)
                                , Border.rounded 10
                                , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                                ]
                                { onPress = Just ConfirmDelete
                                , label = text "Delete"
                                }
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

                headerTitle =
                    case model.selectedEntity of
                        Nothing ->
                            if workspace == AppWorkspace then
                                "Choose something to explore"

                            else
                                "No entity selected"

                        Just entity ->
                            if workspace == AppWorkspace then
                                entity.name

                            else
                                entity.name ++ " records"

                createLabel =
                    if workspace == AppWorkspace then
                        "Create"

                    else
                        "New"
            in
            column
                [ width (fillPortion 3)
                , height fill
                , spacing 14
                , Background.color (rgb255 255 255 255)
                , Border.rounded 14
                , Border.width 1
                , Border.color (rgb255 226 232 239)
                , padding 16
                ]
                [ viewPanelHeader
                    headerTitle
                    []
                    [ Input.button
                        [ Background.color (rgb255 34 124 95)
                        , Font.color (rgb255 248 252 250)
                        , Border.rounded 10
                        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                        ]
                        { onPress = Just StartCreate
                        , label = text createLabel
                        }
                    , Input.button
                        [ Background.color (rgb255 224 231 241)
                        , Border.rounded 10
                        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                        ]
                        { onPress = Just ReloadRows
                        , label = text "Refresh"
                        }
                    ]
                , viewRows model
                ]


viewActionPanel : Model -> ActionInfo -> Element Msg
viewActionPanel model actionInfo =
    case findInputAlias actionInfo.inputAlias model of
        Nothing ->
            column
                [ width fill
                , height fill
                , spacing 12
                , Background.color (rgb255 255 255 255)
                , Border.rounded 14
                , Border.width 1
                , Border.color (rgb255 226 232 239)
                , padding 16
                ]
                [ viewPanelHeader ("Action: " ++ actionInfo.name) [] []
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
                        Input.text [ width fill ]
                            { onChange = SetActionField field.name
                            , text = Dict.get field.name model.actionFormValues |> Maybe.withDefault ""
                            , placeholder =
                                Just
                                    (Input.placeholder []
                                        (text
                                            (if workspace == AppWorkspace then
                                                "Enter a value"

                                             else
                                                field.fieldType
                                            )
                                        )
                                    )
                            , label = Input.labelAbove [ Font.size 12 ] (text (fieldLabel field.name))
                            }
            in
            column
                [ width fill
                , height fill
                , spacing 12
                , Background.color (rgb255 255 255 255)
                , Border.rounded 14
                , Border.width 1
                , Border.color (rgb255 226 232 239)
                , padding 16
                ]
                (List.concat
                    [ [ viewPanelHeader
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
                    , [ row [ width fill ]
                            [ el [ width fill ] none
                            , Input.button
                                [ Background.color (rgb255 34 124 95)
                                , Font.color (rgb255 248 252 250)
                                , Border.rounded 10
                                , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                                ]
                                { onPress = Just RunAction
                                , label = text (if workspace == AppWorkspace then "Continue" else "Run action")
                                }
                            ]
                      ]
                    , [ case model.actionResult of
                            Nothing ->
                                none

                            Just response ->
                                column
                                    [ width fill
                                    , spacing 8
                                    , Background.color (rgb255 248 250 252)
                                    , Border.rounded 10
                                    , Border.width 1
                                    , Border.color (rgb255 226 232 239)
                                    , padding 12
                                    ]
                                    (el [ Font.bold ] (text (if workspace == AppWorkspace then "Result" else "Response"))
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
            paragraph [] [ text (if currentWorkspace model == AppWorkspace then "Choose something from the menu." else "Choose an entity from the sidebar.") ]

        ( Just _, NotAsked ) ->
            paragraph [] [ text "No data loaded yet." ]

        ( Just _, Loading ) ->
            paragraph [] [ text (if currentWorkspace model == AppWorkspace then "Loading..." else "Loading records...") ]

        ( Just _, Failed message ) ->
            paragraph [ Font.color (rgb255 176 60 46) ] [ text message ]

        ( Just entity, Loaded records ) ->
            if List.isEmpty records then
                paragraph [] [ text (if currentWorkspace model == AppWorkspace then "Nothing here yet." else "No records yet.") ]

            else
                column [ width fill, spacing 8 ]
                    (List.map (viewRowCard (currentWorkspace model) entity) records)


viewRowCard : WorkspaceMode -> Entity -> Row -> Element Msg
viewRowCard workspace entity rowValue =
    let
        previewFields =
            displayFieldsForEntity workspace entity
                |> List.take 4

        summary =
            previewFields
                |> List.map
                    (\field ->
                        let
                            textValue =
                                Dict.get field.name rowValue
                                    |> Maybe.map valueToString
                                    |> Maybe.withDefault "-"
                        in
                        if workspace == AppWorkspace then
                            textValue

                        else
                            fieldLabel field.name ++ ": " ++ textValue
                    )
                |> String.join "  |  "

        headingText =
            if workspace == AppWorkspace then
                displayLabelForRow workspace entity rowValue

            else
                entity.name ++ " #" ++ (rowId entity rowValue |> Maybe.withDefault "?")
    in
    row
        [ width fill
        , spacing 12
        , Background.color (rgb255 248 250 252)
        , Border.rounded 10
        , padding 12
        ]
        [ column [ width fill, spacing 6 ]
            [ el [ Font.bold ] (text headingText)
            , el [ Font.size 13, Font.color (rgb255 90 103 120) ] (text summary)
            ]
        , Input.button
            [ Background.color (rgb255 222 232 248)
            , Border.rounded 8
            , paddingEach { top = 8, right = 10, bottom = 8, left = 10 }
            ]
            { onPress = Just (SelectRow rowValue)
            , label = text (if workspace == AppWorkspace then "Open" else "View")
            }
        , Input.button
            [ Background.color (rgb255 223 244 238)
            , Border.rounded 8
            , paddingEach { top = 8, right = 10, bottom = 8, left = 10 }
            ]
            { onPress = Just (StartEdit rowValue)
            , label = text "Edit"
            }
        , Input.button
            [ Background.color (rgb255 248 226 226)
            , Border.rounded 8
            , paddingEach { top = 8, right = 10, bottom = 8, left = 10 }
            ]
            { onPress = Just (RequestDeleteRow rowValue)
            , label = text "Delete"
            }
        ]


viewInspector : Model -> Element Msg
viewInspector model =
    case model.selectedAction of
        Just actionInfo ->
            if currentWorkspace model == AppWorkspace then
                none

            else
                column
                    [ width (fillPortion 2)
                    , height fill
                    , spacing 14
                    ]
                    [ viewActionInfo actionInfo ]

        Nothing ->
            column
                [ width (fillPortion 2)
                , height fill
                , spacing 14
                ]
                [ viewEntitySchema model
                , viewFormPanel model
                , viewSelectedRow model
                ]


viewPerformancePanel : Model -> Element Msg
viewPerformancePanel model =
    if not (isAdminProfile model) then
        column
            [ width fill
            , spacing 14
            , Background.color (rgb255 255 255 255)
            , Border.rounded 14
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 16
            ]
            [ el [ Font.bold, Font.size 20 ] (text "Monitoring")
            , paragraph [ Font.size 14, Font.color (rgb255 93 103 120) ]
                [ text "Admin role required to view monitoring information." ]
            ]

    else
        let
            routeRow perfRoute =
                let
                    hasErrors =
                        perfRoute.errors4xx > 0 || perfRoute.errors5xx > 0

                    statusColor =
                        if hasErrors then
                            rgb255 176 60 46

                        else
                            rgb255 34 124 95
                in
                row
                    [ width fill
                    , spacing 12
                    , Background.color (rgb255 248 250 252)
                    , Border.rounded 10
                    , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                    ]
                    [ el [ width (fillPortion 1), Font.bold ] (text perfRoute.method)
                    , el [ width (fillPortion 3) ] (text perfRoute.route)
                    , el [ width (fillPortion 1) ] (text ("count: " ++ String.fromInt perfRoute.count))
                    , el [ width (fillPortion 1) ] (text ("avg: " ++ formatMs perfRoute.avgMs))
                    , el [ width (fillPortion 1), Font.color statusColor ] (text ("4xx/5xx: " ++ String.fromInt perfRoute.errors4xx ++ "/" ++ String.fromInt perfRoute.errors5xx))
                    ]

            cards perf =
                row [ width fill, spacing 12 ]
                    [ performanceCard "Uptime" (formatSeconds perf.uptimeSeconds)
                    , performanceCard "Memory (heap)" (formatBytes perf.memoryBytes)
                    , performanceCard "SQLite file" (formatBytes perf.sqliteBytes)
                    , performanceCard "Goroutines" (String.fromInt perf.goroutines)
                    , performanceCard "Requests" (String.fromInt perf.http.totalRequests)
                    ]
        in
        column
            [ width fill
            , spacing 14
            , Background.color (rgb255 255 255 255)
            , Border.rounded 14
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 16
            ]
            [ viewPanelHeader
                "Monitoring"
                []
                [ Input.button
                    [ Background.color (rgb255 224 231 241)
                    , Border.rounded 10
                    , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                    ]
                    { onPress = Just ReloadPerformance
                    , label = text "Refresh"
                    }
                ]
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
                        , row [ width fill, spacing 12 ]
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
                        [ row [ width fill, spacing 12 ]
                            [ databaseInfoCard "App build time" versionPayload.app.buildTime
                            , databaseInfoCardWithHint "Manifest hash" versionPayload.app.manifestHash "Changes when the app definition changes."
                            ]
                        , row [ width fill, spacing 12 ]
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
    if not (isAdminProfile model) then
        column
            [ width fill
            , spacing 14
            , Background.color (rgb255 255 255 255)
            , Border.rounded 14
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 16
            ]
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
            [ width fill
            , spacing 14
            , Background.color (rgb255 255 255 255)
            , Border.rounded 14
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 16
            ]
            [ viewPanelHeader
                "Recent request logs"
                (if logsSubtitle == "" then
                    []

                 else
                    [ el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text logsSubtitle) ]
                )
                [ Input.button
                    [ Background.color (rgb255 224 231 241)
                    , Border.rounded 10
                    , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                    ]
                    { onPress = Just ReloadRequestLogs
                    , label = text "Refresh"
                    }
                ]
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
        [ width fill
        , spacing 8
        , Background.color (rgb255 248 250 252)
        , Border.rounded 10
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 12
        ]
        (List.concat
            [ [ row [ width fill, spacing 10 ]
                    [ el [ Font.size 12, Font.bold, Font.color (rgb255 70 80 96) ] (text ("Date: " ++ dateText))
                    , el [ Font.size 12, Font.bold, Font.color (rgb255 70 80 96) ] (text ("Time: " ++ timeText))
                    ]
              , row [ width fill, spacing 10 ]
                    [ el [ Font.bold ] (text (entry.method ++ " " ++ entry.path))
                    , el [ Font.color statusColor, Font.bold ] (text (String.fromInt entry.status))
                    , el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text (formatMs entry.durationMs))
                    ]
              , row [ width fill, spacing 10 ]
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
        [ width fill
        , spacing 4
        , Background.color (rgb255 243 247 252)
        , Border.rounded 8
        , padding 8
        ]
        (List.concat
            [ (case query.reason of
                Just reasonText ->
                    if String.trim reasonText == "" then
                        []

                    else
                        [ el [ Font.size 12, Font.bold, Font.color (rgb255 82 95 127) ] (text reasonText) ]

                Nothing ->
                    []
              )
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
            [ width fill
            , spacing 14
            , Background.color (rgb255 255 255 255)
            , Border.rounded 14
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 16
            ]
            [ el [ Font.bold, Font.size 20 ] (text "Database")
            , paragraph [ Font.size 14, Font.color (rgb255 93 103 120) ]
                [ text "Admin role required to view database and backup information." ]
            ]

    else
        viewDatabasePanelAdmin model


viewDatabasePanelAdmin : Model -> Element Msg
viewDatabasePanelAdmin model =
    let
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
                    paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                        [ text "No backup executed in this admin session yet." ]

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
                                [ [ row
                                        [ width fill
                                        , spacing 12
                                        , paddingEach { top = 6, right = 10, bottom = 6, left = 10 }
                                        , Background.color (rgb255 244 247 252)
                                        , Border.rounded 8
                                        ]
                                        [ el [ width (fillPortion 2), Font.bold ] (text "Backup time")
                                        , el [ width (fillPortion 1), Font.bold ] (text "Size")
                                        , el [ width (fillPortion 4), Font.bold ] (text "File")
                                        ]
                                  ]
                                , List.map backupRow payload.backups
                                ]
                            )
    in
    column
        [ width fill
        , spacing 14
        , Background.color (rgb255 255 255 255)
        , Border.rounded 14
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 16
        ]
        [ viewPanelHeader
            "Database"
            []
            [ Input.button
                [ Background.color (rgb255 224 231 241)
                , Border.rounded 10
                , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                ]
                { onPress = Just ReloadDatabase
                , label = text "Refresh"
                }
            , Input.button
                [ Background.color (rgb255 34 124 95)
                , Font.color (rgb255 248 252 250)
                , Border.rounded 10
                , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                ]
                { onPress = Just TriggerBackup
                , label = text "Create backup"
                }
            ]
        , row [ width fill, spacing 12 ]
            [ performanceCard "SQLite database size" sqliteSizeText
            , databaseInfoCard "File" dbPath
            , databaseInfoCard "Backups directory" backupDirText
            ]
        , lastBackupInfo
        , el [ Font.bold, Font.size 18 ] (text "Available backups")
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


backupRow : BackupFile -> Element Msg
backupRow backup =
    row
        [ width fill
        , spacing 12
        , paddingEach { top = 8, right = 10, bottom = 8, left = 10 }
        , Background.color (rgb255 248 250 252)
        , Border.rounded 8
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        ]
        [ el [ width (fillPortion 2) ] (text backup.createdAt)
        , el [ width (fillPortion 1), Font.bold ] (text (formatBytes backup.sizeBytes))
        , el [ width (fillPortion 4), Font.size 13, Font.color (rgb255 93 103 120) ] (text (backupDisplayName backup))
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
        [ width fill
        , spacing 6
        , Background.color (rgb255 248 250 252)
        , Border.rounded 10
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 12
        ]
        [ el [ Font.size 12, Font.color (rgb255 93 103 120) ] (text title)
        , el [ Font.size 20, Font.bold ] (text value)
        ]


databaseInfoCard : String -> String -> Element Msg
databaseInfoCard title value =
    databaseInfoCardWithHint title value ""


databaseInfoCardWithHint : String -> String -> String -> Element Msg
databaseInfoCardWithHint title value hint =
    column
        [ width (fill |> minimum 220)
        , spacing 6
        , Background.color (rgb255 248 250 252)
        , Border.rounded 10
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 12
        ]
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
        [ width fill
        , spacing 4
        , Background.color (rgb255 248 250 252)
        , Border.rounded 10
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 10
        ]
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
    String.fromFloat (roundTo1 seconds) ++ " s"


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
        [ width fill
        , spacing 10
        , Background.color (rgb255 255 255 255)
        , Border.rounded 14
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 14
        ]
        [ el [ Font.bold, Font.size 18 ] (text "Action details")
        , row [ width fill, spacing 8 ]
            [ badge "ACTION"
            , badge "POST"
            ]
        , row [ width fill, spacing 8 ]
            [ el [ Font.bold ] (text "Name")
            , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text actionInfo.name)
            ]
        , row [ width fill, spacing 8 ]
            [ el [ Font.bold ] (text "Input")
            , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text actionInfo.inputAlias)
            ]
        , row [ width fill, spacing 8 ]
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


displayLabelForRow : WorkspaceMode -> Entity -> Row -> String
displayLabelForRow workspace entity rowValue =
    let
        preferredNames =
            [ "name", "title", "email", "slug" ]

        valueForField fieldName =
            Dict.get fieldName rowValue
                |> Maybe.map valueToString
                |> Maybe.map String.trim

        firstPreferred =
            preferredNames
                |> List.filterMap valueForField
                |> List.filter (\value -> value /= "")
                |> List.head

        firstVisible =
            displayFieldsForEntity workspace entity
                |> List.filterMap
                    (\field ->
                        Dict.get field.name rowValue
                            |> Maybe.map valueToString
                            |> Maybe.map String.trim
                    )
                |> List.filter (\value -> value /= "")
                |> List.head
    in
    case firstPreferred of
        Just value ->
            value

        Nothing ->
            case firstVisible of
                Just value ->
                    value

                Nothing ->
                    entity.name


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
            [ width fill
            , spacing 8
            , Background.color (rgb255 255 255 255)
            , Border.rounded 14
            , Border.width 1
            , Border.color (rgb255 226 232 239)
            , padding 14
            ]
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
                            ]
                    )
                    rowsForEntity
            )


badge : String -> Element Msg
badge labelText =
    el
        [ Background.color (rgb255 234 240 250)
        , Border.rounded 999
        , Font.size 11
        , paddingEach { top = 4, right = 8, bottom = 4, left = 8 }
        ]
        (text labelText)


viewFormPanel : Model -> Element Msg
viewFormPanel model =
    case ( model.selectedEntity, model.formMode ) of
        ( Just entity, FormCreate ) ->
            formCard model entity "Create record"

        ( Just entity, FormEdit _ ) ->
            formCard model entity "Edit record"

        _ ->
            none


formCard : Model -> Entity -> String -> Element Msg
formCard model entity titleText =
    let
        workspace =
            currentWorkspace model

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
                    Input.text [ width fill ]
                        { onChange = SetFormField field.name
                        , text = Dict.get field.name model.formValues |> Maybe.withDefault ""
                        , placeholder =
                            Just
                                (Input.placeholder []
                                    (text
                                        (if workspace == AppWorkspace then
                                            "Enter a value"

                                         else
                                            fieldTypeLabel field.fieldType
                                        )
                                    )
                                )
                        , label = Input.labelAbove [ Font.size 12 ] (text (fieldLabel field.name))
                        }
    in
    column
        [ width fill
        , spacing 10
        , Background.color (rgb255 255 255 255)
        , Border.rounded 14
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        , padding 14
        ]
        (List.concat
            [ [ el [ Font.bold, Font.size 18 ] (text (if workspace == AppWorkspace then titleText else titleText)) ]
            , List.map fieldInput formFields
            , [ row [ spacing 10 ]
                    [ Input.button
                        [ Background.color (rgb255 34 124 95)
                        , Font.color (rgb255 247 252 249)
                        , Border.rounded 8
                        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                        ]
                        { onPress = Just SubmitForm
                        , label = text "Save"
                        }
                    , Input.button
                        [ Background.color (rgb255 233 236 242)
                        , Border.rounded 8
                        , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                        ]
                        { onPress = Just CancelForm
                        , label = text "Cancel"
                        }
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

                        visibleFieldNames =
                            displayFieldsForEntity workspace entity
                                |> List.map .name

                        visibleRows =
                            rowValue
                                |> Dict.toList
                                |> List.filter (\( key, _ ) -> List.member key visibleFieldNames)
                    in
                    column
                        [ width fill
                        , spacing 8
                        , Background.color (rgb255 255 255 255)
                        , Border.rounded 14
                        , Border.width 1
                        , Border.color (rgb255 226 232 239)
                        , padding 14
                        ]
                        (el [ Font.bold, Font.size 18 ] (text (if workspace == AppWorkspace then "Details" else "Record detail"))
                            :: (visibleRows
                                    |> List.map
                                        (\( key, value ) ->
                                            row [ width fill, spacing 8 ]
                                                [ el [ Font.bold ] (text (fieldLabel key))
                                                , paragraph [ Font.size 13, Font.color (rgb255 82 92 108) ] [ text (valueToString value) ]
                                                ]
                                        )
                               )
                        )


networkErrorMessage : String
networkErrorMessage =
    "We could not reach the app right now. Please try again in a moment."


httpErrorToString : Model -> Http.Error -> String
httpErrorToString model httpError =
    case httpError of
        Http.BadUrl message ->
            "Bad URL: " ++ message

        Http.Timeout ->
            "Request timeout"

        Http.NetworkError ->
            networkErrorMessage

        Http.BadStatus statusCode ->
            "HTTP error: " ++ String.fromInt statusCode

        Http.BadBody message ->
            message


authRequestCodeErrorToString : Model -> Http.Error -> String
authRequestCodeErrorToString model httpError =
    case httpError of
        Http.Timeout ->
            "We could not send the code in time. Please try again."

        Http.NetworkError ->
            networkErrorMessage

        Http.BadBody message ->
            if String.contains "Too many request-code attempts" message then
                "You requested too many codes. Please wait a minute and try again."

            else
                message

        _ ->
            httpErrorToString model httpError


authLoginErrorToString : Model -> Http.Error -> String
authLoginErrorToString model httpError =
    case httpError of
        Http.Timeout ->
            "We could not complete the sign-in in time. Please try again."

        Http.NetworkError ->
            networkErrorMessage

        Http.BadBody message ->
            if String.contains "Invalid or expired code" message then
                "That code is invalid or expired. Request a new one and try again."

            else
                message

        _ ->
            httpErrorToString model httpError


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
    (not model.performanceMode)
        && (not model.requestLogsMode)
        && (not model.databaseMode)
        && (not hasActionSelection)
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
