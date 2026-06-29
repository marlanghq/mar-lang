# mar-website

The public site for the Mar language, written in Mar.

The site itself is built in Mar. It's the dogfood test: if the
framework can't render its own site, it can't render yours either.

## Pages

```
/              Frontend/Home.mar         Landing — hero + nav to the rest
/why           Frontend/Why.mar          Why Mar exists, design pillars, trade-offs
/get-started   Frontend/GetStarted.mar   Install, scaffold, run, deploy
/examples      Frontend/Examples.mar     Reference projects, ordered by complexity
/credits       Frontend/Credits.mar      Author, license, inspirations, source
```

URL surface lives in `Frontend/Routes.mar` as typed `Path {}`
bindings. Renaming a route flips every `navigationLink` call site
into a compile-time error.

## Run locally

```sh
mar dev
```

Hot-reload server on `http://localhost:3000`. Edit any `.mar` file
and the page rebuilds.

## Deploy

The `deploy.fly` block in `mar.json` points at the `mar-lang-website`
Fly app. After the first push, the site lives at
`https://mar-lang-website.fly.dev` (or whatever custom domain you
point at it — `mar-lang.dev` in production).

```sh
mar fly deploy
```

Single command. First deploy creates the Fly app; subsequent
deploys just push a new container.

## Why frontend-only

The site has no auth, no database, no services to call. Everything
is static at build time — the `program.json` AST gets embedded
into `index.html`, and the deploy pushes the static bundle to a
container. No SQLite, no volume, no admin panel needed.
