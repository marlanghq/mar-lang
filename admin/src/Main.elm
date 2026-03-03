module Main exposing (main)

import Belm.Api exposing (ActionInfo, AuthInfo, Entity, Field, InputAliasField, InputAliasInfo, Row, Schema, decodeRows, decodeSchema, encodePayload, fieldTypeLabel, rowDecoder, valueToString)
import Browser
import Dict exposing (Dict)
import Element exposing (Element, alignLeft, centerY, column, el, fill, fillPortion, height, none, padding, paddingEach, paragraph, px, rgb255, row, spacing, text, width)
import Element.Background as Background
import Element.Border as Border
import Element.Font as Font
import Element.Input as Input
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import String


type alias Flags =
    { apiBase : String }


type Remote a
    = NotAsked
    | Loading
    | Loaded a
    | Failed String


type FormMode
    = FormHidden
    | FormCreate
    | FormEdit Row


type alias Model =
    { apiBase : String
    , advancedMode : Bool
    , authToken : String
    , authEmail : String
    , authCode : String
    , authToolsOpen : Bool
    , schema : Remote Schema
    , selectedEntity : Maybe Entity
    , selectedAction : Maybe ActionInfo
    , rows : Remote (List Row)
    , selectedRow : Maybe Row
    , formMode : FormMode
    , formValues : Dict String String
    , actionFormValues : Dict String String
    , actionResult : Maybe Row
    , flash : Maybe String
    }


type Msg
    = ReloadSchema
    | GotSchema (Result Http.Error Schema)
    | SelectEntity String
    | SelectAction String
    | ReloadRows
    | GotRows (Result Http.Error (List Row))
    | SetApiBase String
    | SetToken String
    | SetAuthEmail String
    | SetAuthCode String
    | SetActionField String String
    | RequestAuthCode
    | GotRequestAuthCode (Result Http.Error RequestCodeResponse)
    | LoginWithCode
    | GotLoginWithCode (Result Http.Error LoginResponse)
    | LoadAuthMe
    | GotAuthMe (Result Http.Error AuthMeResponse)
    | LogoutSession
    | GotLogoutSession (Result Http.Error ())
    | ToggleAdvanced
    | ToggleAuthTools
    | SelectRow Row
    | StartCreate
    | StartEdit Row
    | CancelForm
    | SetFormField String String
    | SubmitForm
    | GotCreate (Result Http.Error Row)
    | GotUpdate (Result Http.Error Row)
    | DeleteRow Row
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
    }


type alias AuthMeResponse =
    { email : String
    , role : Maybe String
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
    ( { apiBase = flags.apiBase
      , advancedMode = False
      , authToken = ""
      , authEmail = ""
      , authCode = ""
      , authToolsOpen = False
      , schema = Loading
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
    , loadSchema flags.apiBase
    )


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        ReloadSchema ->
            ( { model | schema = Loading, flash = Nothing }, loadSchema model.apiBase )

        GotSchema result ->
            case result of
                Ok schema ->
                    let
                        maybeEntity =
                            List.head schema.entities

                        nextModel =
                            { model
                                | schema = Loaded schema
                                , authToolsOpen = False
                                , selectedEntity = maybeEntity
                                , selectedAction = Nothing
                                , rows = Loading
                                , selectedRow = Nothing
                                , formMode = FormHidden
                                , formValues = Dict.empty
                                , actionFormValues = Dict.empty
                                , actionResult = Nothing
                            }
                    in
                    ( nextModel, loadRows nextModel )

                Err httpError ->
                    ( { model | schema = Failed (httpErrorToString httpError), rows = Failed "schema unavailable" }, Cmd.none )

        SelectEntity entityName ->
            let
                nextEntity =
                    findEntity entityName model

                nextModel =
                    { model
                        | selectedEntity = nextEntity
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
                        | selectedAction = Just actionInfo
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
                    ( { model | rows = Failed (httpErrorToString httpError) }, Cmd.none )

        SetApiBase value ->
            ( { model | apiBase = value }, Cmd.none )

        SetToken token ->
            ( { model | authToken = token }, Cmd.none )

        SetAuthEmail email ->
            ( { model | authEmail = email }, Cmd.none )

        SetAuthCode code ->
            ( { model | authCode = code }, Cmd.none )

        SetActionField fieldName value ->
            ( { model | actionFormValues = Dict.insert fieldName value model.actionFormValues }, Cmd.none )

        RequestAuthCode ->
            if String.trim model.authEmail == "" then
                ( { model | flash = Just "Email is required for request-code" }, Cmd.none )

            else
                ( { model | flash = Nothing }, requestAuthCode model )

        GotRequestAuthCode result ->
            case result of
                Ok response ->
                    case response.devCode of
                        Just code ->
                            ( { model | authCode = code, flash = Just ("Code generated. devCode: " ++ code) }, Cmd.none )

                        Nothing ->
                            ( { model
                                | flash =
                                    Just
                                        (response.message
                                            ++ " No development code was returned. This can happen when the app keeps generic auth responses in this environment."
                                        )
                              }
                            , Cmd.none
                            )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        LoginWithCode ->
            if String.trim model.authEmail == "" || String.trim model.authCode == "" then
                ( { model | flash = Just "Email and code are required for login" }, Cmd.none )

            else
                ( { model | flash = Nothing }, loginWithCode model )

        GotLoginWithCode result ->
            case result of
                Ok response ->
                    ( { model | authToken = response.token, flash = Just "Login successful. Bearer token filled automatically." }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        LoadAuthMe ->
            if String.trim model.authToken == "" then
                ( { model | flash = Just "Provide a bearer token first" }, Cmd.none )

            else
                ( { model | flash = Nothing }, loadAuthMe model )

        GotAuthMe result ->
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
                    ( { model | flash = Just ("Authenticated as " ++ response.email ++ roleText) }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        LogoutSession ->
            if String.trim model.authToken == "" then
                ( { model | flash = Just "Provide a bearer token first" }, Cmd.none )

            else
                ( { model | flash = Nothing }, logoutSession model )

        GotLogoutSession result ->
            case result of
                Ok _ ->
                    ( { model | authToken = "", flash = Just "Logged out. Token cleared." }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        ToggleAdvanced ->
            ( { model | advancedMode = not model.advancedMode }, Cmd.none )

        ToggleAuthTools ->
            ( { model | authToolsOpen = not model.authToolsOpen }, Cmd.none )

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
                    in
                    ( { model | rows = nextRows, selectedRow = Just updatedRow, formMode = FormHidden, formValues = Dict.empty, flash = Just "Updated successfully" }, Cmd.none )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

        DeleteRow rowValue ->
            case model.selectedEntity of
                Nothing ->
                    ( { model | flash = Just "Select an entity first" }, Cmd.none )

                Just entity ->
                    case rowId entity rowValue of
                        Nothing ->
                            ( { model | flash = Just "Could not find row id" }, Cmd.none )

                        Just idValue ->
                            ( model, deleteRowRequest model entity idValue )

        GotDelete result ->
            case result of
                Ok _ ->
                    let
                        nextModel =
                            { model | flash = Just "Deleted successfully", selectedRow = Nothing, formMode = FormHidden, formValues = Dict.empty }
                    in
                    ( { nextModel | rows = Loading }, loadRows nextModel )

                Err httpError ->
                    ( { model | flash = Just (httpErrorToString httpError) }, Cmd.none )

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


loadSchema : String -> Cmd Msg
loadSchema apiBase =
    Http.get
        { url = apiBase ++ "/_belm/schema"
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
                , headers = authHeaders model
                , url = model.apiBase ++ entity.resource
                , body = Http.emptyBody
                , expect = expectJsonWithApiError GotRows decodeRows
                , timeout = Nothing
                , tracker = Nothing
                }


createRow : Model -> Entity -> Encode.Value -> Cmd Msg
createRow model entity payload =
    Http.request
        { method = "POST"
        , headers = authHeaders model
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
        , headers = authHeaders model
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
        , headers = authHeaders model
        , url = model.apiBase ++ entity.resource ++ "/" ++ idValue
        , body = Http.emptyBody
        , expect = expectUnitWithApiError GotDelete
        , timeout = Nothing
        , tracker = Nothing
        }


authHeaders : Model -> List Http.Header
authHeaders model =
    if String.trim model.authToken == "" then
        []

    else
        [ Http.header "Authorization" ("Bearer " ++ String.trim model.authToken) ]


requestAuthCode : Model -> Cmd Msg
requestAuthCode model =
    Http.request
        { method = "POST"
        , headers = []
        , url = model.apiBase ++ "/auth/request-code"
        , body =
            Http.jsonBody
                (Encode.object
                    [ ( "email", Encode.string (String.trim model.authEmail) )
                    ]
                )
        , expect = expectJsonWithApiError GotRequestAuthCode requestCodeDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loginWithCode : Model -> Cmd Msg
loginWithCode model =
    Http.request
        { method = "POST"
        , headers = []
        , url = model.apiBase ++ "/auth/login"
        , body =
            Http.jsonBody
                (Encode.object
                    [ ( "email", Encode.string (String.trim model.authEmail) )
                    , ( "code", Encode.string (String.trim model.authCode) )
                    ]
                )
        , expect = expectJsonWithApiError GotLoginWithCode loginResponseDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


loadAuthMe : Model -> Cmd Msg
loadAuthMe model =
    Http.request
        { method = "GET"
        , headers = authHeaders model
        , url = model.apiBase ++ "/auth/me"
        , body = Http.emptyBody
        , expect = expectJsonWithApiError GotAuthMe authMeResponseDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


logoutSession : Model -> Cmd Msg
logoutSession model =
    Http.request
        { method = "POST"
        , headers = authHeaders model
        , url = model.apiBase ++ "/auth/logout"
        , body = Http.emptyBody
        , expect = expectUnitWithApiError GotLogoutSession
        , timeout = Nothing
        , tracker = Nothing
        }


runAction : Model -> ActionInfo -> Encode.Value -> Cmd Msg
runAction model actionInfo payload =
    Http.request
        { method = "POST"
        , headers = authHeaders model
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
    Decode.map LoginResponse (Decode.field "token" Decode.string)


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


actionFormDefaults : Model -> ActionInfo -> Dict String String
actionFormDefaults model actionInfo =
    case findInputAlias actionInfo.inputAlias model of
        Nothing ->
            Dict.empty

        Just aliasInfo ->
            aliasInfo.fields
                |> List.map (\field -> ( field.name, "" ))
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
                Err ("Field " ++ field.name ++ " is required")

            else
                case field.fieldType of
                    "String" ->
                        Ok (( field.name, Encode.string rawValue ) :: items)

                    "Int" ->
                        case String.toInt rawValue of
                            Just value ->
                                Ok (( field.name, Encode.int value ) :: items)

                            Nothing ->
                                Err ("Field " ++ field.name ++ " expects Int")

                    "Float" ->
                        case String.toFloat rawValue of
                            Just value ->
                                Ok (( field.name, Encode.float value ) :: items)

                            Nothing ->
                                Err ("Field " ++ field.name ++ " expects Float")

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
                            Err ("Field " ++ field.name ++ " expects Bool (true/false)")

                    _ ->
                        Err ("Unsupported input type " ++ field.fieldType ++ " for field " ++ field.name)


rowId : Entity -> Row -> Maybe String
rowId entity rowValue =
    Dict.get entity.primaryKey rowValue
        |> Maybe.map valueToString


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
                |> List.map (\field -> ( field.name, "" ))
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
    { title = "Belm Admin"
    , body =
        [ Element.layout
            [ Background.color (rgb255 244 245 247)
            , Font.family
                [ Font.typeface "Space Grotesk"
                , Font.typeface "IBM Plex Sans"
                , Font.sansSerif
                ]
            , Font.color (rgb255 29 36 44)
            ]
            (viewLayout model)
        ]
    }


viewLayout : Model -> Element Msg
viewLayout model =
    row [ width fill, height fill ]
        [ viewSidebar model
        , viewContent model
        ]


viewSidebar : Model -> Element Msg
viewSidebar model =
    let
        ( entities, actions ) =
            case model.schema of
                Loaded schema ->
                    ( schema.entities, schema.actions )

                _ ->
                    ( [], [] )

        entityButton entity =
            let
                selected =
                    case model.selectedEntity of
                        Just current ->
                            current.name == entity.name

                        Nothing ->
                            False

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
                    row [ width fill ]
                        [ paragraph [ alignLeft ] [ text entity.name ]
                        , el [ Font.size 12, Font.color (rgb255 170 181 196) ] (text entity.resource)
                        ]
                }

        actionEndpointCard : ActionInfo -> Element Msg
        actionEndpointCard actionInfo =
            let
                selected =
                    case model.selectedAction of
                        Just current ->
                            current.name == actionInfo.name

                        Nothing ->
                            False

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
                    row [ width fill ]
                        [ paragraph [ alignLeft ] [ text actionInfo.name ]
                        , el [ Font.size 12, Font.color (rgb255 170 181 196) ] (text "action")
                        ]
                }
    in
    column
        [ width (px 280)
        , height fill
        , Background.color (rgb255 18 22 28)
        , padding 20
        , spacing 16
        ]
        ([ el [ Font.size 24, Font.bold, Font.color (rgb255 240 245 250) ] (text "Belm Admin")
         , el [ Font.size 13, Font.color (rgb255 144 158 179) ]
            (text
                (case model.schema of
                    Loaded schema ->
                        schema.appName

                    _ ->
                        "loading schema..."
                )
            )
         , el [ Font.size 11, Font.bold, Font.color (rgb255 118 136 160) ] (text "CRUD")
         ]
            ++ List.map entityButton entities
            ++ (if List.isEmpty actions then
                    []

                else
                    [ el
                        [ paddingEach { top = 10, right = 0, bottom = 0, left = 0 }
                        , Font.size 11
                        , Font.bold
                        , Font.color (rgb255 118 136 160)
                        ]
                        (text "ACTIONS")
                    ]
                        ++ List.map actionEndpointCard actions
               )
        )


viewContent : Model -> Element Msg
viewContent model =
    column
        [ width fill
        , height fill
        , padding 24
        , spacing 16
        ]
        [ viewTopBar model
        , viewAuthToolsPanel model
        , viewFlash model
        , case model.selectedAction of
            Just _ ->
                viewDataPanel model

            Nothing ->
                row [ width fill, height fill, spacing 16 ]
                    [ viewDataPanel model
                    , viewInspector model
                    ]
        ]


viewTopBar : Model -> Element Msg
viewTopBar model =
    let
        tokenInput attrs =
            Input.text attrs
                { onChange = SetToken
                , text = model.authToken
                , placeholder = Just (Input.placeholder [] (text "Bearer token"))
                , label = Input.labelAbove [ Font.size 12 ] (text "Auth token")
                }

        apiInput =
            Input.text [ width (fillPortion 3) ]
                { onChange = SetApiBase
                , text = model.apiBase
                , placeholder = Just (Input.placeholder [] (text "API base URL"))
                , label = Input.labelAbove [ Font.size 12 ] (text "API")
                }

        reloadSchemaButton =
            Input.button
                [ Element.alignBottom
                , Background.color (rgb255 54 94 217)
                , Font.color (rgb255 245 248 252)
                , Border.rounded 10
                , paddingEach { top = 12, right = 16, bottom = 12, left = 16 }
                ]
                { onPress = Just ReloadSchema
                , label = text "Reload schema"
                }

        advancedButton =
            Input.button
                [ Element.alignBottom
                , Background.color
                    (if model.advancedMode then
                        rgb255 76 111 224

                     else
                        rgb255 224 231 241
                    )
                , Font.color
                    (if model.advancedMode then
                        rgb255 245 248 252

                     else
                        rgb255 41 52 68
                    )
                , Border.rounded 10
                , paddingEach { top = 12, right = 16, bottom = 12, left = 16 }
                ]
                { onPress = Just ToggleAdvanced
                , label =
                    if model.advancedMode then
                        text "Hide advanced"

                    else
                        text "Advanced"
                }

        authToolsButtons =
            case authInfoFromModel model of
                Just _ ->
                    [ Input.button
                        [ Element.alignBottom
                        , Background.color (rgb255 224 231 241)
                        , Border.rounded 10
                        , paddingEach { top = 12, right = 16, bottom = 12, left = 16 }
                        ]
                        { onPress = Just ToggleAuthTools
                        , label =
                            if model.authToolsOpen then
                                text "Hide auth tools"

                            else
                                text "Auth tools"
                        }
                    ]

                Nothing ->
                    []

        mainControls =
            if model.advancedMode then
                [ apiInput
                , tokenInput [ width (fillPortion 2) ]
                , reloadSchemaButton
                ]

            else
                [ tokenInput [ width fill ] ]
    in
    row
        [ width fill
        , spacing 12
        , padding 16
        , Background.color (rgb255 255 255 255)
        , Border.rounded 14
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        ]
        (mainControls ++ [ advancedButton ] ++ authToolsButtons)


viewAuthToolsPanel : Model -> Element Msg
viewAuthToolsPanel model =
    if not model.authToolsOpen then
        none

    else
        case authInfoFromModel model of
            Just authInfo ->
                column
                    [ width fill
                    , spacing 10
                    , padding 16
                    , Background.color (rgb255 255 255 255)
                    , Border.rounded 14
                    , Border.width 1
                    , Border.color (rgb255 226 232 239)
                    ]
                    [ row [ width fill, spacing 12, centerY ]
                        [ el [ Font.bold, Font.size 18 ] (text "Authentication")
                        , el [ Font.size 12, Font.color (rgb255 93 103 120) ]
                            (text ("Transport: " ++ authInfo.emailTransport ++ " | User entity: " ++ authInfo.userEntity))
                        ]
                    , row [ width fill, spacing 8 ]
                        [ badge "POST /auth/request-code"
                        , badge "POST /auth/login"
                        , badge "GET /auth/me"
                        , badge "POST /auth/logout"
                        ]
                    , row [ width fill, spacing 10 ]
                        [ Input.text [ width (fillPortion 3) ]
                            { onChange = SetAuthEmail
                            , text = model.authEmail
                            , placeholder = Just (Input.placeholder [] (text "user@email.com"))
                            , label = Input.labelAbove [ Font.size 12 ] (text "Email")
                            }
                        , Input.text [ width (fillPortion 2) ]
                            { onChange = SetAuthCode
                            , text = model.authCode
                            , placeholder = Just (Input.placeholder [] (text "6-digit code"))
                            , label = Input.labelAbove [ Font.size 12 ] (text "Code")
                            }
                        , Input.button
                            [ Element.alignBottom
                            , Background.color (rgb255 84 121 224)
                            , Font.color (rgb255 246 248 252)
                            , Border.rounded 10
                            , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                            ]
                            { onPress = Just RequestAuthCode
                            , label = text "Request code"
                            }
                        , Input.button
                            [ Element.alignBottom
                            , Background.color (rgb255 34 124 95)
                            , Font.color (rgb255 246 251 248)
                            , Border.rounded 10
                            , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                            ]
                            { onPress = Just LoginWithCode
                            , label = text "Login"
                            }
                        , Input.button
                            [ Element.alignBottom
                            , Background.color (rgb255 224 231 241)
                            , Border.rounded 10
                            , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                            ]
                            { onPress = Just LoadAuthMe
                            , label = text "Me"
                            }
                        , Input.button
                            [ Element.alignBottom
                            , Background.color (rgb255 248 226 226)
                            , Border.rounded 10
                            , paddingEach { top = 10, right = 12, bottom = 10, left = 12 }
                            ]
                            { onPress = Just LogoutSession
                            , label = text "Logout"
                            }
                        ]
                    ]

            Nothing ->
                none


authInfoFromModel : Model -> Maybe AuthInfo
authInfoFromModel model =
    case model.schema of
        Loaded schema ->
            schema.auth

        _ ->
            Nothing


viewFlash : Model -> Element Msg
viewFlash model =
    case model.flash of
        Nothing ->
            none

        Just message ->
            row
                [ width fill
                , Background.color (rgb255 255 245 230)
                , Border.rounded 10
                , Border.width 1
                , Border.color (rgb255 250 200 120)
                , padding 12
                , spacing 12
                ]
                [ el [ width fill ] (text message)
                , Input.button
                    [ Background.color (rgb255 251 185 79)
                    , Border.rounded 8
                    , paddingEach { top = 6, right = 10, bottom = 6, left = 10 }
                    ]
                    { onPress = Just ClearFlash
                    , label = text "Dismiss"
                    }
                ]


viewDataPanel : Model -> Element Msg
viewDataPanel model =
    case model.selectedAction of
        Just actionInfo ->
            viewActionPanel model actionInfo

        Nothing ->
            let
                header =
                    case model.selectedEntity of
                        Nothing ->
                            text "No entity selected"

                        Just entity ->
                            text (entity.name ++ " records")
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
                [ row [ width fill, spacing 10 ]
                    [ el [ Font.size 20, Font.bold, width fill ] header
                    , Input.button
                        [ Background.color (rgb255 34 124 95)
                        , Font.color (rgb255 248 252 250)
                        , Border.rounded 10
                        , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                        ]
                        { onPress = Just StartCreate
                        , label = text "New"
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
                [ el [ Font.size 20, Font.bold ] (text ("Action: " ++ actionInfo.name))
                , paragraph [ Font.color (rgb255 176 60 46) ] [ text ("Input alias not found: " ++ actionInfo.inputAlias) ]
                ]

        Just aliasInfo ->
            let
                fieldInput : InputAliasField -> Element Msg
                fieldInput field =
                    Input.text [ width fill ]
                        { onChange = SetActionField field.name
                        , text = Dict.get field.name model.actionFormValues |> Maybe.withDefault ""
                        , placeholder = Just (Input.placeholder [] (text field.fieldType))
                        , label = Input.labelAbove [ Font.size 12 ] (text field.name)
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
                ([ row [ width fill, spacing 10, centerY ]
                    [ el [ Font.size 20, Font.bold, width fill ] (text ("Action: " ++ actionInfo.name)) ]
                 , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text ("POST /actions/" ++ actionInfo.name))
                 , el [ Font.size 13, Font.color (rgb255 93 103 120) ] (text ("Input: " ++ aliasInfo.name))
                 ]
                    ++ List.map fieldInput aliasInfo.fields
                    ++ [ row [ width fill ]
                            [ el [ width fill ] none
                            , Input.button
                                [ Background.color (rgb255 34 124 95)
                                , Font.color (rgb255 248 252 250)
                                , Border.rounded 10
                                , paddingEach { top = 10, right = 14, bottom = 10, left = 14 }
                                ]
                                { onPress = Just RunAction
                                , label = text "Run action"
                                }
                            ]
                       ]
                    ++ [ case model.actionResult of
                            Nothing ->
                                paragraph [ Font.size 13, Font.color (rgb255 93 103 120) ]
                                    [ text "Fill the fields and run the action." ]

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
                                    ([ el [ Font.bold ] (text "Response") ]
                                        ++ (response
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
                )


viewRows : Model -> Element Msg
viewRows model =
    case ( model.selectedEntity, model.rows ) of
        ( Nothing, _ ) ->
            paragraph [] [ text "Choose an entity from the sidebar." ]

        ( Just _, NotAsked ) ->
            paragraph [] [ text "No data loaded yet." ]

        ( Just _, Loading ) ->
            paragraph [] [ text "Loading records..." ]

        ( Just _, Failed message ) ->
            paragraph [ Font.color (rgb255 176 60 46) ] [ text message ]

        ( Just entity, Loaded records ) ->
            if List.isEmpty records then
                paragraph [] [ text "No records yet." ]

            else
                column [ width fill, spacing 8 ]
                    (List.map (viewRowCard entity) records)


viewRowCard : Entity -> Row -> Element Msg
viewRowCard entity rowValue =
    let
        previewFields =
            List.take 4 entity.fields

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
                        field.name ++ ": " ++ textValue
                    )
                |> String.join "  |  "

        idText =
            rowId entity rowValue |> Maybe.withDefault "?"
    in
    row
        [ width fill
        , spacing 12
        , Background.color (rgb255 248 250 252)
        , Border.rounded 10
        , padding 12
        ]
        [ column [ width fill, spacing 6 ]
            [ el [ Font.bold ] (text (entity.name ++ " #" ++ idText))
            , el [ Font.size 13, Font.color (rgb255 90 103 120) ] (text summary)
            ]
        , Input.button
            [ Background.color (rgb255 222 232 248)
            , Border.rounded 8
            , paddingEach { top = 8, right = 10, bottom = 8, left = 10 }
            ]
            { onPress = Just (SelectRow rowValue)
            , label = text "View"
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
            { onPress = Just (DeleteRow rowValue)
            , label = text "Delete"
            }
        ]


viewInspector : Model -> Element Msg
viewInspector model =
    case model.selectedAction of
        Just actionInfo ->
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


viewEntitySchema : Model -> Element Msg
viewEntitySchema model =
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
        ([ el [ Font.bold, Font.size 18 ] (text "Schema") ]
            ++ List.map
                (\field ->
                    row [ width fill, spacing 8 ]
                        [ el [ Font.bold ] (text field.name)
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
        formFields =
            entity.fields |> List.filter (\field -> not field.primary)

        fieldInput field =
            Input.text [ width fill ]
                { onChange = SetFormField field.name
                , text = Dict.get field.name model.formValues |> Maybe.withDefault ""
                , placeholder = Just (Input.placeholder [] (text (fieldTypeLabel field.fieldType)))
                , label = Input.labelAbove [ Font.size 12 ] (text field.name)
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
        ([ el [ Font.bold, Font.size 18 ] (text titleText) ]
            ++ List.map fieldInput formFields
            ++ [ row [ spacing 10 ]
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
        )


viewSelectedRow : Model -> Element Msg
viewSelectedRow model =
    case model.selectedRow of
        Nothing ->
            none

        Just rowValue ->
            column
                [ width fill
                , spacing 8
                , Background.color (rgb255 255 255 255)
                , Border.rounded 14
                , Border.width 1
                , Border.color (rgb255 226 232 239)
                , padding 14
                ]
                ([ el [ Font.bold, Font.size 18 ] (text "Record detail") ]
                    ++ (rowValue
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


httpErrorToString : Http.Error -> String
httpErrorToString httpError =
    case httpError of
        Http.BadUrl message ->
            "Bad URL: " ++ message

        Http.Timeout ->
            "Request timeout"

        Http.NetworkError ->
            "Network error"

        Http.BadStatus statusCode ->
            "HTTP error: " ++ String.fromInt statusCode

        Http.BadBody message ->
            message
