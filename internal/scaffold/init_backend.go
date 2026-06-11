package scaffold

import "fmt"

// backendFiles returns the file set for `mar init` when the operator
// picks the backend-only kind. Single Main.mar exposing two RPC
// services (listEntries + addEntry) over an Entity, mounted via
// App.backend. No frontend — useful for API services consumed by an
// iOS app, an external SPA, or shell tooling via plain HTTP POST.
func backendFiles(name string) map[string]string {
	files := sharedFiles(name)
	files["mar.json"] = fmt.Sprintf(`{
  "name": "%s"
}
`, name)
	files["Main.mar"] = `module Main exposing (main)


-- A backend-only mar app: one Entity, two Services, no UI. App.backend
-- mounts every Service.implement at its auto-derived URL. From the
-- shell:
--
--     curl http://localhost:3000/services/Main.listEntries -d '{}'
--     curl http://localhost:3000/services/Main.addEntry    -d '{"name":"hello"}'
--
-- The same contracts can be called by a Mar frontend (App.frontend),
-- an iOS app, or anything that speaks JSON over HTTP.


type alias Entry =
    { id   : Int
    , name : String
    }


type alias NewEntry =
    { name : String
    }


-- Database schema. Schema migrations are derived from this Entity
-- definition and auto-applied at startup.
entries : Entity Entry
entries =
    Entity.define
        { name = "entries"
        , columns =
            { id   = Entity.serial
            , name = Entity.text Entity.notNull
            }
        , uniques = []
        }


-- RPC contracts. The binding name + module become the URL path
-- (` + "`/services/Main.listEntries`" + ` etc.). Service.declare is the
-- contract; Service.implement attaches the handler below.
listEntries : Service () (List Entry)
listEntries = Service.declare


addEntry : Service NewEntry Entry
addEntry = Service.declare


-- Service handlers.
listEntriesImpl : () -> Effect (List Entry)
listEntriesImpl _ =
    Repo.all entries


addEntryImpl : NewEntry -> Effect Entry
addEntryImpl input =
    Repo.create entries input


services =
    [ Service.implement listEntries listEntriesImpl
    , Service.implement addEntry   addEntryImpl
    ]


main : Effect ()
main =
    App.backend
        { routes   = []
        , services = services
        }
`
	return files
}
