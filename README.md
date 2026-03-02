# Belm (Go)

Belm é uma linguagem inspirada em Elm para backend, agora reimplementada em **Go** com foco em legibilidade e manutenção.

## Objetivo

- Sintaxe simples e declarativa (`entity`, `rule`, `authorize`, `auth`)
- CRUD REST automático
- SQLite como banco
- Login por código enviado por email
- Autorização por regras
- Migrações automáticas seguras

## Arquitetura (Go)

- [cmd/belmc/main.go](/Users/marcio/dev/github/belm/cmd/belmc/main.go): CLI do compilador/runtime
- [internal/parser/parser.go](/Users/marcio/dev/github/belm/internal/parser/parser.go): parser da linguagem `.belm`
- [internal/expr/parser.go](/Users/marcio/dev/github/belm/internal/expr/parser.go): parser de expressões (`rule`/`authorize`)
- [internal/runtime](/Users/marcio/dev/github/belm/internal/runtime): servidor HTTP, auth/authz e migrações
- [internal/sqlitecli/sqlitecli.go](/Users/marcio/dev/github/belm/internal/sqlitecli/sqlitecli.go): acesso SQLite via binário `sqlite3` (sem dependências externas)

## Comandos

Compilar `.belm` para manifesto JSON:

```bash
go run ./cmd/belmc compile examples/store.belm build/store.manifest.json
```

Ao compilar, o Belm também gera um cliente Elm no mesmo diretório do manifesto.
Exemplo de saída:

- `build/store.manifest.json`
- `build/StoreApiClient.elm`

Rodar direto do `.belm`:

```bash
go run ./cmd/belmc serve examples/store.belm
```

Rodar a partir de manifesto compilado:

```bash
go run ./cmd/belmc serve-manifest build/store.manifest.json
```

## Cliente Elm gerado automaticamente

O módulo gerado (`<AppName>Client.elm`) inclui:

- `schema` (metadados das entidades)
- `rowDecoder`
- funções CRUD por entidade:
  - `list<Entity>`
  - `get<Entity>`
  - `create<Entity>`
  - `update<Entity>`
  - `delete<Entity>`
- quando auth está habilitado:
  - `requestCode`
  - `login`
  - `logout`
  - `me`

Exemplo de uso em Elm:

```elm
import StoreApiClient as Api

type Msg
    = GotCustomers (Result Http.Error (List Api.Row))

load : Cmd Msg
load =
    Api.listCustomer
        { baseUrl = "http://localhost:4100", token = "" }
        GotCustomers
```

## Painel Admin (Elm + elm-ui)

Foi adicionado um painel gráfico em Elm:

- código: [admin/src/Main.elm](/Users/marcio/dev/github/belm/admin/src/Main.elm)
- docs: [admin/README.md](/Users/marcio/dev/github/belm/admin/README.md)

Ele consome `GET /_belm/schema` para descobrir entidades e permite listar/criar/editar/deletar registros.

## Sintaxe da linguagem

Exemplo mínimo:

```belm
app TodoApi
port 4000
database "./todo.db"

entity Todo {
  id: Int primary auto
  title: String
  done: Bool
  rule "Title must have at least 3 chars" when len(title) >= 3
}
```

### Statements

- `app <Name>`
- `port <number>`
- `database "<sqlite_path>"`
- `auth { ... }`
- `entity <Name> { ... }`

### Fields

`<fieldName>: <Type> [primary] [auto] [optional]`

Tipos:

- `Int`
- `String`
- `Bool`
- `Float`

Atributos:

- `primary`: chave primária
- `auto`: autoincrement (normalmente usado com `Int primary`)
- `optional`: campo nullable

Se não houver primary key, Belm adiciona automaticamente:

`id: Int primary auto`

## Regras de negócio (`rule`)

Dentro de `entity`:

```belm
rule "Customer must be 18 or older" when age >= 18
```

Operadores:

- `and`, `or`, `not`
- `==`, `!=`, `>`, `>=`, `<`, `<=`
- `+`, `-`, `*`, `/`

Funções:

- `contains(text, part)`
- `startsWith(text, prefix)`
- `endsWith(text, suffix)`
- `len(value)`
- `matches(text, regex)`

Literais:

- `true`, `false`, `null`

Se uma regra falha, retorna HTTP `422` com `error` e `details`.

## Autenticação (`auth`)

Fluxo nativo de login por código:

1. `POST /auth/request-code`
2. envio do código por email
3. `POST /auth/login` (email + code) retorna bearer token
4. `POST /auth/logout` revoga sessão

Configuração:

```belm
auth {
  user_entity Customer
  email_field email
  role_field role
  code_ttl_minutes 10
  session_ttl_hours 24
  email_transport console
  email_from "no-reply@store.local"
  email_subject "Your StoreApi login code"
  dev_expose_code true
}
```

`email_transport`:

- `console`: imprime código no log
- `sendmail`: usa binário local (`sendmail_path`)

## Autorização (`authorize`)

Por operação CRUD:

```belm
authorize list when isRole("admin")
authorize get when auth_authenticated and (id == auth_user_id or isRole("admin"))
authorize create when true
authorize update when auth_authenticated and (id == auth_user_id or isRole("admin"))
authorize delete when isRole("admin")
```

Contexto disponível em expressões de autorização:

- `auth_authenticated`
- `auth_email`
- `auth_user_id`
- `auth_role`
- campos da entidade (`id`, `customerId`, etc.)

Função extra:

- `isRole("admin")`

## Endpoints gerados

Para cada entidade `X`:

- `GET /xs`
- `GET /xs/:id`
- `POST /xs`
- `PUT /xs/:id`
- `PATCH /xs/:id`
- `DELETE /xs/:id`

Sempre:

- `GET /health`
- `GET /_belm/schema`

Com auth habilitado:

- `POST /auth/request-code`
- `POST /auth/login`
- `POST /auth/logout`
- `GET /auth/me`

## Migrações

As migrações rodam automaticamente no startup.

Automático:

- cria tabelas faltantes
- adiciona colunas novas opcionais
- cria/migra tabelas internas de auth
- registra operações em `belm_schema_migrations`

Bloqueado (migração manual):

- troca de tipo de coluna
- mudança de primary key
- mudança de nulabilidade
- adição de campo obrigatório em tabela existente
- adição de coluna primary/auto em tabela existente

Quando bloqueia, o servidor falha no startup com mensagem explícita.

## Exemplo completo

Use [examples/store.belm](/Users/marcio/dev/github/belm/examples/store.belm), que já inclui:

- regras de negócio (`age >= 18`, validação email etc.)
- auth por código
- autorização por papel/ownership
- entidades `Customer`, `Product`, `Order`, `OrderItem`
