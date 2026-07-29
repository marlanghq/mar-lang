package scaffold

import "fmt"

// minimumFiles returns the file set for `mar init` when the operator
// picks the minimum kind. The smallest possible mar app: one Main.mar
// with a single empty page, wired through App.fullstack with no
// services. Useful as a blank slate when the user wants to design
// the structure themselves rather than start from one of the
// opinionated layouts.
func minimumFiles(name string) map[string]string {
	files := sharedFiles(name)
	files["mar.json"] = fmt.Sprintf(`{
  "name": "%s"
}
`, name)
	files["Main.mar"] = `module Main exposing (main)


-- A blank starter: one empty page wired through App.fullstack.
-- Nothing renders, no model state, no services. Build from here.


import UI exposing (text)


type alias Model = ()


type Msg
    = NoOp


init : (Model, Cmd Msg)
init = ((), Cmd.none)


update : Msg -> Model -> (Model, Cmd Msg)
update _ _ = ((), Cmd.none)


view : Model -> View Msg
view _ =
    text [] ""


page : Page
page =
    Page.create
        { path = "/"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.fullstack
        { services = []
        , pages    = [ page ]
        }
`
	return files
}
