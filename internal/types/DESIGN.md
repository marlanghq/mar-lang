# HM Type System for mar-lang — Phase 0 Design

Resultado da Fase 0 do plano em `~/.claude/plans/async-beaming-sketch.md`. Documenta as decisões concretas que orientam a implementação.

## Restrição absoluta

**Zero mudança de sintaxe `.mar` visível ao usuário.** Todos os programas em `examples/*.mar` continuam compilando idênticos. Toda mudança é interna ao compilador. Se algum caso da implementação forçar nova sintaxe, parar e perguntar antes.

## Escopo: abordagem B (HM total) em 5 frentes

Decidido depois de descobrir que mar-lang tem múltiplos contextos de tipagem (frontend/backend checkers + usage_analysis) e que [docs/managed-effects.md](../../docs/managed-effects.md) já declara a intenção de Elm-Architecture pura.

| # | Frente | Resumo |
|---|--------|--------|
| F1 | HM clássico para expressões puras | Foco deste design doc. Cobre `define`, `validate`, `authorize`, `where`, `init`, `update`, action step values, function bodies. |
| F2 | Queries/actions como serviços tipados | Assinaturas no `TypeEnv` global. Extensão direta de F1. |
| F3 | Effects como `Cmd Msg` | `command`/`go`/`back` deixam de ser forma especial; viram valores. Depende de F4. |
| F4 | Mensagens de screen como sum types nominais | Cada screen tem `TUnion` própria. |
| F5 | View como `View Msg` | View DSL passa a ser expressão; cada item kind vira construtor de `View Msg`. Maior frente. |

**Ordem**: F1 → F2 → F4 → F3 → F5. Este documento foca em **F1**; frentes posteriores ganham seu próprio design doc quando chegarem.

## Estado atual (baseline)

### Como o checker atual funciona

Implementado em [`internal/parser/frontend_typecheck.go`](../parser/frontend_typecheck.go) (~2360 linhas).

**Representação de tipos** (`frontendType`, linhas 13–40):
- Tagged struct com `Kind` enum (`bool`, `int`, `decimal`, `string`, `list`, `record`, `union`, `any`, `unknown`, `never`, `empty-list`, …).
- Sem variáveis de tipo. Polimorfismo é simulado por re-inferência por call-site.
- `any` e `unknown` são escapatórias silenciosas — quando o checker desiste, devolve um desses.

**Algoritmo de inferência**:
- `inferExprType` (linha 467) é um switch grande sobre construtores da AST sexp. Devolve `frontendType` direto, sem substitutions.
- Cada `(define (foo args) body)` registrado em `c.functions[name]`.
- Toda chamada `(foo arg1 arg2)` re-roda `inferUserFunctionReturn` (linha 1502) com os tipos concretos dos argumentos. Resultado é cacheado por chave `nome|tipo1|tipo2|...`.
- **Recursão é detectada e rejeitada** (linha 1524–1526): se a chave já está no stack, retorna `"return type could not be inferred because it is recursive"`.

**Funções não são first-class hoje**:
- Lambdas só aparecem como argumentos imediatos a `map`/`filter`/`fold_left`/`fold_right`.
- `inferFunctionValueCall` (linha 1784) aceita só nome de função ou literal `(lambda ...)` — variável contendo função, retorno de função, etc., não são suportados.
- Builtins polimórficos (`map`, `filter`, `fold_*`, `cons`, `first`, `rest`, `empty?`) têm handlers ad-hoc no checker.

**Constraints reverso**:
- `functionParameterConstraints` (linha 1544) tenta inferir constraints sintáticas dos parâmetros olhando como são usados (ex: se `x` aparece em `(+ x 1)`, infere `x: int`). Heurístico, parcial.

### Conversões dinâmicas que sobrevivem em runtime

Em [`internal/expr/ast.go`](../expr/ast.go) e [`internal/expr/values.go`](../expr/values.go) (~30+ call sites):
- `RequireBool`, `RequireString` — type assertions com erro.
- `ToDecimal`, `ToList`, `ToInt64` — conversões que podem falhar.

Cada uma é um ponto onde o runtime ainda checa o que o checker deveria ter garantido. Após HM bem feito, a maioria some (vira garantia estática).

## Decisões para a Fase 1

### Representação interna de tipos

Novo pacote `internal/types/`. AST de tipos:

```go
type Type interface{ isType() }

type TVar struct {
    ID   int      // identificador único, atribuído por contador global
    Ref  *Type    // se != nil, esta variável foi unificada (union-find/ranks ficam pra otimizar depois)
}

type TCon struct {
    Name string  // "bool", "int", "decimal", "string", "date", "datetime", "cursor", "unit"
    Args []Type  // para construtores n-ários: TCon{"list", [α]}, TCon{"maybe", [α]}, TCon{"->", [α, β]}
}

type TRecord struct {
    Name   string
    Fields map[string]Type
    Order  []string
}

type TUnion struct {
    Name     string                 // nome do tipo nominal, se houver
    Variants map[string][]Type      // tag → tipos dos campos
    Order    map[string][]string    // tag → nomes dos campos (preserva ordem da declaração)
}

type TForall struct {
    Vars []int   // IDs das TVars quantificadas
    Body Type
}
```

**Decisões**:
- Funções modeladas como `TCon{"->", [arg, ret]}` para uniformidade — não como construtor especial. Múltiplos argumentos viram currying interno: `f(x,y)` é `TCon{"->", [x_type, TCon{"->", [y_type, ret_type]}]}`. Pode mudar para tupla se ficar incômodo na unificação; decidir na implementação.
- `TVar.Ref` para path compression simples (estilo OCaml/Roc). Sem rank balancing por enquanto — otimização futura se profile mostrar gargalo.
- `TForall` só aparece em entradas do `TypeEnv` (let-bound) e em assinaturas de builtins; nunca aparece dentro de outro tipo (rank-1 polymorphism, padrão HM).
- `TRecord` e `TUnion` ficam separados de `TCon` porque têm estrutura interna rica (campos/variantes). Unificação tem casos especiais pra eles.
- `Any` e `Unknown` **não existem** no novo sistema. Toda incerteza vira `TVar`.

### Substituição

```go
type Subst map[int]Type  // mapeia TVar.ID → Type

func (s Subst) Apply(t Type) Type
func (s Subst) Compose(other Subst) Subst
```

Aplicação substitui recursivamente. Composição: `s1.Compose(s2) = {x → s1(s2(x))} ∪ s1` para vars não em s2.

### Unificação

```go
func Unify(a, b Type) (Subst, error)
```

Algoritmo padrão:
1. Resolve vars (`TVar` com `Ref` segue até a raiz).
2. Casos:
   - `TVar(α)` vs qualquer `t`: occurs check, se passar bind `α → t`.
   - Mesmo `TCon` (mesmo nome, mesma aridade): unifica argumentos pairwise.
   - `TRecord` vs `TRecord`: mesmo nome (nominal) ou mesmo conjunto de campos (estrutural — decidir abaixo).
   - `TUnion` vs `TUnion`: idem.
   - Caso contrário: erro de unificação.

**Decisão sobre records/unions**: começar **nominal** (mesmo nome = mesmo tipo) para preservar semântica atual. Row polymorphism (records flexíveis) fica pra fase futura.

### Generalização e instanciação

```go
func Generalize(env TypeEnv, t Type) Type            // devolve TForall
func Instantiate(t Type) Type                        // se t é TForall, troca vars por frescas
```

- `Generalize`: encontra vars livres em `t` que **não** estão livres em nenhum tipo do `env`. Quantifica essas.
- `Instantiate`: cria mapa `oldID → freshTVar` para cada quantificada, aplica.

**Quando generalizar**:
- `let`/`let*` bindings: sim (let-polymorphism clássico).
- Top-level `(define ...)`: sim (forma o env global).
- Lambda parameters: **não** (parâmetros de lambda permanecem monomórficos no escopo — HM padrão).

### Recursão

Para um grupo de definições mutuamente recursivas (top-level + `define` group):
1. Atribui `TVar` fresca pra cada nome no env.
2. Inferir o corpo de cada uma assumindo o env preliminar.
3. Unificar o tipo inferido com a `TVar` correspondente.
4. Generalizar todos no fim.

Para `let rec` simples (caso de uma só definição): mesmo processo com grupo de tamanho 1.

**Limitação aceita**: recursão polimórfica (Mycroft) **não é suportada** sem anotação. Se aparecer caso real, perguntar ao usuário antes de adicionar sintaxe (acordado).

### TypeEnv inicial (builtins)

Builtins viram entradas no env com tipos `TForall`. Deixa de ter handlers ad-hoc no checker:

```
true, false       : bool
unit              : unit
nothing           : ∀α. maybe α
just              : ∀α. α → maybe α
ok                : ∀ε α. α → result ε α
err               : ∀ε α. ε → result ε α

cons              : ∀α. α → list α → list α
first             : ∀α. list α → maybe α
rest              : ∀α. list α → list α
empty?            : ∀α. list α → bool
length            : ∀α. list α → int       // também aceita string — ver nota
map               : ∀α β. (α → β) → list α → list β
filter            : ∀α. (α → bool) → list α → list α
fold_left         : ∀α β. (β → α → β) → β → list α → β
fold_right        : ∀α β. (α → β → β) → list α → β → β

not               : bool → bool
=, !=             : ∀α. α → α → bool
>, >=, <, <=      : numeric overloading — ver nota
+, -, *, /        : numeric overloading — ver nota
and, or           : bool → bool → bool

contains, starts-with, ends-with : string → string → bool
matches           : string-literal → string → bool

number->string    : numeric → string
date->string      : date → string
datetime->string  : datetime → string
string_append     : string → string → string  // variádico hoje, virar binário ou usar fold

authenticated?, anonymous? : current-user → bool
same_user?        : current-user → int → bool
has_role?         : current-user → string → bool

current_user      : current-user
```

**Notas de tradução**:
- **`length`** aceita string ou lista hoje. Em HM puro precisa de overloading (type classes) ou duas funções (`string-length`, `list-length`). Decidir: por enquanto, manter como caso especial no checker (não generalizar via env), até type classes existirem ou separar nomes.
- **Aritmética e comparação** (`+ - * / > >= < <=`): hoje aceitam `int` e `decimal`, com promoção. Mesmo problema. Mesma solução: caso especial no checker por enquanto.
- **`string_append` variádico**: HM clássico não tem aridade variável. Modelar como binário e exigir `fold_left string_append "" lista` ou aceitar açúcar sintático no checker.
- **`current-user`** é tipo nominal `union` (`authenticated id email role | anonymous`). Vira `TUnion` no env.

Isso é trabalho real mas isolado — três casos especiais bem delimitados. Não inviabiliza a abordagem geral.

### Funções como first-class

Decisão aprovada. Lambda passa a produzir valor de tipo `TCon{"->", [...]}`, capturável por variável, retornável de função, passável a função user-defined. Sem mudança de sintaxe.

### Pattern matching

`match` continua funcionando como hoje. O checker novo:
1. Infere tipo do subject.
2. Para cada cláusula, infere tipos dos vars do pattern do tipo do subject.
3. Infere tipo do body com vars no env.
4. Unifica todos os tipos de body — resultado é o tipo do `match`.

Exhaustividade ainda **não** é checada (item separado, fora de escopo desta fase).

### Errors / `error` / `RaisedError`

`(error "mensagem")` tem tipo `never` (bottom). Em HM, `never` unifica com qualquer coisa — ou seja, é `TVar` fresca a cada uso. Isso permite `(if cond x (error "..."))` ter o tipo de `x`.

## Mapeamento dos call sites de `Require*`/`To*`

Catalogados em `internal/expr/ast.go` (33 ocorrências) e `values.go` (definições). Após HM:

| Call site | Status pós-HM |
|-----------|---------------|
| `RequireBool` em `Unary not`, `If condition`, `Cond test`, `and`/`or` short-circuit | ✅ Removível — checker garante bool |
| `RequireString` em `RegexMatch.Text`, `contains`/`starts-with`/`ends-with`, `matches`, `has_role?` | ✅ Removível |
| `ToDecimal` em `Unary -`, `Binary + - * /`, `Binary > >= < <=` | ✅ Removível (com caveat de overload numérico) |
| `ToList` em builtins de lista (`first`, `rest`, `cons`, `empty?`, `map`, `filter`, `fold_*`) | ✅ Removível |
| Conversões em `runtime/dbutil.go` (parsing de input HTTP) | ❌ Manter — borda externa, não é avaliação |
| Conversões em `runtime/queries.go` (parâmetros de query) | ❌ Manter — borda externa |

**Estimativa**: ~25 dos ~33 call sites somem; ~8 ficam (todos na borda HTTP, fora do avaliador puro).

## Integração com o resto do compilador

Pontos de toque na Fase 2:

1. **Substituir** `frontendTypeChecker` em `internal/parser/frontend_typecheck.go`. Decisão: criar `internal/types/checker.go` novo, deixar o antigo até a paridade, depois remover.
2. **`internal/parser/parser.go:185`** chama o checker indiretamente. Verificar se a interface fica igual.
3. **`internal/runtime/compile.go:18`** chama `expr.Parse`. Não deve precisar mudar — checker roda antes.
4. **`internal/expr/ast.go`** e **`values.go`** — remover `Require*`/`To*` dos call sites internos após o checker garantir.
5. **`internal/lsp/`** — type info usada em hover/completion. Adaptar para o novo formato.

## Estratégia de migração

Para evitar big-bang:
1. Implementar `internal/types/` completo com testes próprios primeiro (isolado).
2. Adicionar feature flag (env var ou config) para escolher checker antigo vs novo.
3. Rodar ambos em paralelo nos exemplos `examples/*.mar` e comparar resultados — primeira validação de paridade.
4. Quando paridade for atingida, remover o antigo.

## Próximos passos (Fase 1)

Em ordem:
1. Criar `internal/types/types.go` com a AST de tipos + pretty-printer.
2. Criar `internal/types/subst.go` (Subst + Apply + Compose).
3. Criar `internal/types/unify.go` (Unify + occurs check).
4. Criar `internal/types/scheme.go` (Generalize + Instantiate + TypeEnv).
5. Criar `internal/types/builtins.go` com o env inicial.
6. Criar `internal/types/infer.go` com `Infer(node sexp.Node, env TypeEnv) (Type, Subst, error)`.
7. Testes unitários para cada um, casos clássicos de literatura HM.

Estimativa: 1 sessão Opus 4.7 High para 1–4, mais 1 sessão para 5–7.
