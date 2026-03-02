# Belm Admin (Elm + elm-ui)

This is a lightweight admin panel similar to PocketBase for inspecting Belm entities and records.

## Build

```bash
cd /Users/marcio/dev/github/belm/admin
elm make src/Main.elm --output=dist/app.js
```

## Run

1. Start Belm API (example):

```bash
cd /Users/marcio/dev/github/belm
GOCACHE=/tmp/belm-gocache go run ./cmd/belmc serve examples/store.belm
```

2. Serve the admin static files:

```bash
cd /Users/marcio/dev/github/belm/admin
python3 -m http.server 8080
```

3. Open:

- <http://localhost:8080/index.html?api=http://localhost:4100>

## Features

- Reads schema from `GET /_belm/schema`
- Sidebar with entities
- List and inspect rows
- Create, edit and delete rows
- Optional bearer token input for protected endpoints
