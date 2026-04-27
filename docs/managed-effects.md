# Managed Effects

This document describes the intended direction for Mar application UI.

The goal is to make it accurate to say:

Mar uses a managed side-effect system.

More precisely:

- backend reads and writes are explicit services
- frontend screens follow a Model-View-Update architecture
- `view` stays pure
- side effects are explicit values returned by `init` and `update`
- only the runtime is allowed to interpret and execute those effects

## Core idea

Mar should separate backend and frontend clearly.

Backend:

- `entity`
- `define-query`
- `action`

Frontend:

- `define-screen`
- `msg`
- `init`
- `update`
- `view`

This makes the boundary easier to explain:

- backend defines REST-facing capabilities
- frontend emits messages and requests effects
- runtime executes effects and feeds results back into `update`

## Managed effects

Mar should treat effects as data, not as arbitrary code execution.

That means:

- `view` cannot perform effects
- helper functions should remain pure
- `init` may return initial effects
- `update` may return effects for the runtime to execute
- effects belong to a closed, runtime-managed set

The minimum effect family is:

- `command`
- `go`
- `back`

Example:

```lisp
(init
  (((unit))
   ((command (load-profile) profile-loaded profile-failed))))
```

```lisp
(update msg model
  (match msg
    ((retry-clicked)
     (model
       ((command (load-profile) profile-loaded profile-failed))))
    ((close-clicked)
     (model
       ((back))))))
```

This is the key claim:

- application code describes effects
- application code does not execute effects directly

## Backend services

Backend access should be explicit.

Queries:

- read-only
- no side effects
- safe to reason about as data access services

Actions:

- may write to the database
- may fail
- represent controlled mutations

Frontend code should call backend capabilities through `command`, not by embedding ad hoc database behavior in the view.

Example:

```lisp
(define-query (timeline-page maybe-cursor)
  (from post)
  (where (or (same-user? current-user author)
             (followed-by-current-user author)))
  (order-by created-at desc)
  (limit 20)
  (after maybe-cursor))

(define-action follow-user
  (input ((user-id int)))
  (create follow ((followed user-id))))
```

```lisp
(update msg model
  (match msg
    ((follow-clicked user-id)
     ((assoc model
        (submitting true))
      ((command (follow-user user-id) follow-finished follow-failed))))
    ((follow-finished result)
     (match result
       ((ok _)
        ((assoc model
           (submitting false))
         ()))
       ((err message)
        ((assoc model
           (submitting false)
           (error (just message)))
         ()))))))
```

## No UI CRUD sugar

For application UI, Mar should move away from special screen items such as:

- `create`
- `edit`
- `delete`

These forms are convenient, but they hide too much:

- the label shown to the user
- the message emitted by the interaction
- the state transition
- the effect being requested
- the success and failure paths

That works against a simple MVU story.

Instead, application UI should be built from explicit nodes such as:

- `button`
- `field`
- `list`
- `row`
- input nodes like `text-input`, `textarea`, `toggle`, `select`

Then the flow becomes uniform:

- user interacts with the view
- the view emits a `msg`
- `update` returns a new model plus effects
- the runtime executes those effects
- the runtime sends a new `msg` with the result

This makes "create post", "follow", "like", "save profile", and "load more" all the same kind of thing.

## Admin vs app UI

Generated CRUD may still make sense for admin tooling.

That does not need to define the model for end-user applications.

Recommended direction:

- keep generated CRUD for admin surfaces
- make user-facing app screens explicit MVU programs
- avoid user-facing UI sugar that bypasses the message/effect cycle

This keeps Mar fullstack while preserving a clean language story.

## Shape of a screen

A screen should be understood as a pure state machine plus a pure view.

```lisp
(define-screen timeline
  (msg
    refresh-clicked
    (timeline-loaded result)
    (composer-opened)
    (post-clicked post-id))

  (init
    ((timeline-model
       (items ())
       (loading true)
       (error (nothing)))
     ((command (timeline-page (nothing)) timeline-loaded timeline-failed))))

  (update msg model
    (match msg
      (refresh-clicked
       ((assoc model
          (loading true)
          (error (nothing)))
        ((command (timeline-page (nothing)) timeline-loaded timeline-failed))))

      ((timeline-loaded result)
       (match result
         ((ok page)
          ((assoc model
             (items (get page items))
             (loading false)
             (error (nothing)))
           ()))
         ((err message)
          ((assoc model
             (loading false)
             (error (just message)))
           ()))))

      (composer-opened
       (model
         ((go compose-post))))

      ((post-clicked post-id)
       (model
         ((go post-detail post-id))))))

  (view model
    (section
      (title "Timeline")
      (button "New post" composer-opened))))
```

Important properties:

- `msg` is explicit
- `model` shape is explicit
- `update` is exhaustive
- effects are explicit in the return value
- `view` is pure and only emits messages

## Creating and editing without sugar

Create and edit flows should be explicit screens.

Instead of:

```lisp
(create post ((field body)))
```

The direction should be:

```lisp
(define-screen compose-post
  (msg
    (body-changed text)
    submit-clicked
    (submit-finished result)
    cancel-clicked)

  (init
    ((compose-post-model
       (body "")
       (submitting false)
       (error (nothing)))
     ()))

  (update msg model
    (match msg
      ((body-changed text)
       ((assoc model
          (body text))
        ()))

      (submit-clicked
       ((assoc model
          (submitting true)
          (error (nothing)))
        ((command (publish-post (get model body)) submit-finished submit-failed))))

      ((submit-finished result)
       (match result
         ((ok _)
          (model
            ((back))))
         ((err message)
          ((assoc model
             (submitting false)
             (error (just message)))
           ()))))

      (cancel-clicked
       (model
         ((back))))))

  (view model
    (section
      (title "New post")
      (textarea "What's happening?" (get model body) body-changed)
      (button "Post" submit-clicked)
      (button "Cancel" cancel-clicked))))
```

The important point is not the exact input widget syntax.

The important point is the architecture:

- no hidden create behavior in the view layer
- no hidden label generation
- no hidden success/failure handling
- everything passes through the same MVU pipeline

## Language summary

The target language story becomes:

- Mar backend defines entities, queries, and actions
- Mar frontend is an MVU program
- all end-user interactions happen through explicit messages
- all side effects are managed by the runtime
- purity is the default, effects are explicit

That is the model we should optimize for when revising the language.
