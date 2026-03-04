# Belm Admin

This is a lightweight admin panel for inspecting Belm entities and records.

## Build

```bash
cd /Users/marcio/dev/github/belm/admin
elm make src/Main.elm --output=dist/app.js
```

## Run

1. Start Belm API (example):

```bash
cd /Users/marcio/dev/github/belm
./belm compile examples/store.belm
./build/store/store serve
```

2. Serve the admin static files:

```bash
cd /Users/marcio/dev/github/belm/admin
python3 -m http.server 8080
```

3. Open:

- <http://localhost:8080/index.html?api=http://localhost:4100>

## One-command mode

You can run backend + admin and open the browser automatically:

```bash
cd /Users/marcio/dev/github/belm
./belm compile examples/store.belm
./build/store/store serve
```

The embedded admin URL is:

- <http://localhost:4100/_belm/admin>

## Features

- Reads schema from `GET /_belm/schema`
- Sidebar with entities
- List and inspect rows
- Create, edit and delete rows
- Authentication area with `Auth token` for protected endpoints
