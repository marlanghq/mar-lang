# Roll Call

Attendance & roster for teachers — a small fullstack Mar app.

A teacher signs in, keeps a roster of classes and the students in each,
takes daily attendance (present / late / absent), and reads back
per-student attendance rates and present-streaks plus a per-class daily
summary. Every row is scoped to the signed-in teacher; the server never
returns another teacher's data.

## Run it

```bash
mar dev examples/roll-call
```

Then open http://localhost:3037. Sign in with any email — in dev mode the
one-time code is printed to the server log (no SMTP needed). For
production, set the mailer under `mail` in `mar.json`.

## Screens

- **Sign in / Verify** — email + one-time code (framework `Auth`).
- **Classes** — your classes with student counts; add via a bottom
  sheet, drag to reorder, swipe/confirm to delete.
- **Class detail** — a live "today" summary, a link into attendance, and
  the class roster (reorder + delete).
- **Take attendance** — pick a day, then tap present / late / absent per
  student. Taps are optimistic and upsert in the background.
- **Student detail** — attendance rate (an exact `Decimal` percentage),
  current streak, a present/late/absent breakdown, and a recent day log.

## What it shows off

- **Auth + per-user isolation.** Email one-time-code sign-in; every query
  filters by `teacherId`, so tenants never see each other's rows.
- **Four related entities** — classes, students, attendance marks (unique
  per student per day, so "mark today" is an upsert) — with cascading
  deletes and a server-computed roster count.
- **Typed REST.** `Service.declare GET/POST/PATCH/DELETE "/api/..."`, with
  the request/response types shared and checked on both ends. API routes
  live under `/api` so they never collide with the page routes.
- **`Decimal`.** Attendance rates are exact base-10 percentages, rounded
  once at the edge with an explicit mode — no float drift.
- **A shared vocabulary.** The `Status` union (Present / Late / Absent)
  and the day-normalization live in `Shared.mar` and are used by both the
  frontend and the backend, so the two halves can't disagree.
- **The UI kit.** Navigation stack + typed routes, bottom sheets, confirm
  dialogs, drag-to-reorder + swipe-to-delete lists, a date picker, and
  optimistic updates.

## Layout

```
Shared.mar            types, Status vocabulary, shared date math, Service contracts
Backend/Users.mar     users entity (backs Auth)
Backend/Classes.mar   classes entity + services + the roster-count projection
Backend/Students.mar  students entity + services + cascades
Backend/Attendance.mar attendance marks entity + upsert + cascades
Backend/Account.mar   account deletion (cascades everything)
Frontend/Routes.mar   the whole URL surface, as typed paths
Frontend/*.mar        the six pages
Main.mar              Auth config + the services and pages lists
```
