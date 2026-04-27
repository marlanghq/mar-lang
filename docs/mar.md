# Mar

Mar is a Scheme-leaning language surface built on s-expressions.

See also [Managed Effects](./managed-effects.md) for the target architecture of Mar application UI as MVU plus runtime-managed effects.

## Goals

- Keep the language small and explicit.
- Preserve strong static structure for entities, migrations, auth, and validation.
- Move screens toward an MVU model inspired by Elm.
- Keep data immutable and expression-oriented.

## Source style

- Only s-expressions.
- `kebab-case` in source.
- Primitive types are lowercase.
- Comments use `;`.

## Top-level forms

- `define-app`
- `define`
- `define-record`
- `define-type`

Typical backend definitions still use named values:

```lisp
(define-entity purchase
    (fields
      ((amount-paid decimal)
       (purchase-date date))))
```

Records are closed shapes used by screens and helpers:

```lisp
(define-record orders-screen-model
  (orders (list order))
  (loading bool)
  (error (maybe string)))
```

Tagged union types model exclusive alternatives:

```lisp
(define-type orders-state
  (loading)
  (loaded (orders (list order)))
  (failed (message string)))
```

## Core expression forms

- `if`
- `cond`
- `match`
- `let`
- `let*`
- `lambda`
- `begin`
- `error`

Collection functions:

- `cons`
- `first`
- `rest`
- `empty?`
- `map`
- `filter`
- `fold-left`
- `fold-right`

Optional values:

- `(just value)`
- `(nothing)`

Result values:

- `(ok value)`
- `(err message)`

Data access:

- `(get record field)`
- `(assoc record (field value) ...)`

## Primitive types

- `string`
- `bool`
- `int`
- `decimal`
- `date`
- `datetime`
- `cursor`
- `(unit)`
- `(maybe t)`
- `(list t)`
- `(result e t)`

`decimal` is the numeric type for non-integer values in source.

## Entities

Entity clauses:

- `fields`
- `belongs-to`
- `unique`
- `defaults`
- `validate`
- `authorize`

Validation and authorization now receive normal expressions:

```lisp
(define-entity purchase
    (fields
      ((amount-paid decimal)))
    (validate
      (if (> amount-paid 0)
          true
          (error "amount paid must be greater than zero")))
    (authorize
      (((read create)
         (authenticated? current-user)))))
```

When multiple actions share the same authorization expression, `authorize` may group
them in a single list:

```lisp
(authorize
  (((read update delete)
     (or (same-user? current-user user)
         (has-role? current-user "admin")))
   (create (authenticated? current-user))))
```

`current-user` is either `(authenticated id email role)` or `(anonymous)`.
Use helpers such as `authenticated?`, `same-user?`, and `has-role?` for common
checks; use `match` when code needs to directly read the authenticated user's
id, email, or role.

## Screens

A screen may be static:

```lisp
(define-screen about
  (view
    (section
      (title "About")
      (text "Pet Food Log"))))
```

Or dynamic with MVU clauses:

```lisp
(define-screen (orders-by-status wanted-status)
  (msg
    (loaded result)
    back)

  (init
     ((orders-screen-model
       (orders ())
       (loading true)
       (error (nothing)))
     ((command (load-orders wanted-status) loaded failed))))

  (update msg model
    (match msg
      ((loaded result)
       (match result
         ((ok orders)
          ((assoc model
             (orders orders)
             (loading false)
             (error (nothing)))
           ()))
         ((err message)
          ((assoc model
             (loading false)
             (error (just message)))
           ()))))
      (back
       (model
        ((back))))))

  (view model
    (section
      (title "Orders")
      (text "Orders screen"))))
```

Screen rules:

- `view` is always required.
- `msg`, `init`, and `update` are optional.
- If `update` exists, then `msg` and `init` are also required.

## Effects

Effects:

- `command`
- `go`
- `back`

`command` follows this shape:

```lisp
(command (load-orders wanted-status) loaded failed)
```

## Data operations

Regular expressions in Mar can also describe queries and mutations:

```lisp
(from post
  (where (same-user? current-user author))
  (order-by created-at desc)
  (limit 20))
```

```lisp
(create post
  ((body "hello world")))
```

```lisp
(update post post-id
  ((body "edited")))
```

```lisp
(delete post post-id)
```

## UI nodes

The initial UI nodes are:

- `section`
- `text`
- `button`
- `field`
- `list`
- `row`

`button` follows this shape:

```lisp
(button "Like" (like-clicked post.id))
```

Example:

```lisp
(section
  (title "Orders")
  (list
    (map
      (lambda (order)
        (row
          (title (get order title))
          (subtitle (get order status))))
      orders))
  (button "Back" back))
```
