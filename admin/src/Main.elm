module Main exposing (main)

import Belm.Api exposing (Entity, Field, Row, Schema, decodeRows, decodeSchema, encodePayload, fieldTypeLabel, rowDecoder, valueToString)
import Browser
import Dict exposing (Dict)
import Element exposing (Element, alignLeft, centerY, column, el, fill, fillPortion, height, none, padding, paddingEach, paragraph, px, rgb255, row, spacing, text, width)
import Element.Background as Background
import Element.Border as Border
import Element.Font as Font
import Element.Input as Input
import Http
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
    , authToken : String
    , schema : Remote Schema
    , selectedEntity : Maybe Entity
    , rows : Remote (List Row)
    , selectedRow : Maybe Row
    , formMode : FormMode
    , formValues : Dict String String
    , flash : Maybe String
    }


type Msg
    = ReloadSchema
    | GotSchema (Result Http.Error Schema)
    | SelectEntity String
    | ReloadRows
    | GotRows (Result Http.Error (List Row))
    | SetApiBase String
    | SetToken String
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
    | ClearFlash


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
      , authToken = ""
      , schema = Loading
      , selectedEntity = Nothing
      , rows = NotAsked
      , selectedRow = Nothing
      , formMode = FormHidden
      , formValues = Dict.empty
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
                                , selectedEntity = maybeEntity
                                , rows = Loading
                                , selectedRow = Nothing
                                , formMode = FormHidden
                                , formValues = Dict.empty
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
                        , rows = Loading
                        , selectedRow = Nothing
                        , formMode = FormHidden
                        , formValues = Dict.empty
                        , flash = Nothing
                    }
            in
            ( nextModel, loadRows nextModel )

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

        ClearFlash ->
            ( { model | flash = Nothing }, Cmd.none )


loadSchema : String -> Cmd Msg
loadSchema apiBase =
    Http.get
        { url = apiBase ++ "/_belm/schema"
        , expect = Http.expectJson GotSchema decodeSchema
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
                , expect = Http.expectJson GotRows decodeRows
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
        , expect = Http.expectJson GotCreate rowDecoder
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
        , expect = Http.expectJson GotUpdate rowDecoder
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
        , expect = Http.expectWhatever GotDelete
        , timeout = Nothing
        , tracker = Nothing
        }


authHeaders : Model -> List Http.Header
authHeaders model =
    if String.trim model.authToken == "" then
        []

    else
        [ Http.header "Authorization" ("Bearer " ++ String.trim model.authToken) ]


findEntity : String -> Model -> Maybe Entity
findEntity entityName model =
    case model.schema of
        Loaded schema ->
            List.filter (\entity -> entity.name == entityName) schema.entities
                |> List.head

        _ ->
            Nothing


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
        entities =
            case model.schema of
                Loaded schema ->
                    schema.entities

                _ ->
                    []

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
         ]
            ++ List.map entityButton entities
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
        , viewFlash model
        , row [ width fill, height fill, spacing 16 ]
            [ viewDataPanel model
            , viewInspector model
            ]
        ]


viewTopBar : Model -> Element Msg
viewTopBar model =
    row
        [ width fill
        , spacing 12
        , padding 16
        , Background.color (rgb255 255 255 255)
        , Border.rounded 14
        , Border.width 1
        , Border.color (rgb255 226 232 239)
        ]
        [ Input.text [ width (fillPortion 3) ]
            { onChange = SetApiBase
            , text = model.apiBase
            , placeholder = Just (Input.placeholder [] (text "API base URL"))
            , label = Input.labelAbove [ Font.size 12 ] (text "API")
            }
        , Input.text [ width (fillPortion 2) ]
            { onChange = SetToken
            , text = model.authToken
            , placeholder = Just (Input.placeholder [] (text "Bearer token"))
            , label = Input.labelAbove [ Font.size 12 ] (text "Auth token")
            }
        , Input.button
            [ centerY
            , Background.color (rgb255 54 94 217)
            , Font.color (rgb255 245 248 252)
            , Border.rounded 10
            , paddingEach { top = 12, right = 16, bottom = 12, left = 16 }
            ]
            { onPress = Just ReloadSchema
            , label = text "Reload schema"
            }
        ]


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
    column
        [ width (fillPortion 2)
        , height fill
        , spacing 14
        ]
        [ viewEntitySchema model
        , viewFormPanel model
        , viewSelectedRow model
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
