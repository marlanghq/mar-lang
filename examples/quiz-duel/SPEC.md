# QUIZ DUEL — spec (v1)

Asynchronous 1v1 trivia duels. You pick a rival and a theme, answer 10 timed
multiple-choice questions, and the challenge waits for your rival to accept and
play the same 10 questions. Best score wins; fastest total time breaks ties.
Head-to-head and overall records accumulate. In the spirit of QuizClash /
"Perguntados", built entirely in Mar.

This is the **fullstack multiplayer showcase** for Mar: it exercises the auth
flow (email + code), protected REST services, entities/Repo, and the App UI —
the pillars none of the canvas games touch. Mobile-first web; iOS later.

**Status: SPEC ONLY — awaiting sign-off before implementation.**
Open questions at the end of this document.

---

## 1. UI approach: App UI, not canvas (v1)

The quiz screen renders arbitrary-length question text. Canvas has no text
measurement, so word-wrapping arbitrary strings in rects is fragile — while the
App UI wraps `paragraph` text natively, gives real tappable buttons, lists,
forms and navigation. Everything this game needs is UI-shaped: lobby, inbox,
pickers, results. So v1 is 100% App UI.

Canvas remains a candidate for a v2 flourish only (an animated "VS" result
screen or a timer ring), listed in open questions. The 8-bit personality comes
through in copy and layout instead: blocky section titles, a text-glyph
countdown bar (`##########-----`), big scores.

## 2. Game rules

- A duel is 10 questions, both players answer the **same 10 questions in the
  same order** (the challenger picked the theme; that is their edge, as in
  every duel-quiz game).
- Each question: 4 options, exactly 1 correct, **15 seconds** to answer
  (`questionLimit = 15`, a tunable constant).
- 1 point per correct answer. Timeout or no answer = wrong (0 points), and the
  full 15s is charged.
- Winner = higher score. Tie → **lower total time** (sum of per-question
  elapsed seconds). Still tied → **draw**.
- Records: each profile stores overall `wins / losses / draws`. Head-to-head
  (your W-L-D against one specific rival) is computed from finished duels.

## 3. Duel lifecycle

```
[Novo desafio] --create--> DRAFT (challenger answering; invisible to opponent)
DRAFT --challenger finishes Q10--> PENDING (appears in opponent's inbox)
PENDING --opponent declines--> DECLINED (terminal; challenger sees it; no score effect)
PENDING --opponent accepts--> ACCEPTED (opponent answering)
ACCEPTED --opponent finishes Q10--> DONE (result computed, records updated)
```

Who sees what:

| status   | challenger sees                          | opponent sees                        |
|----------|------------------------------------------|--------------------------------------|
| draft    | "continue" card (resumable quiz)          | nothing                              |
| pending  | "waiting for <nick>"                      | inbox card: nick + theme + accept/decline |
| accepted | "<nick> is playing…"                      | the quiz (resumable)                 |
| declined | "declined" card                           | history entry                        |
| done     | result card (NEW badge until opened)      | result immediately on finishing Q10  |

Key ordering rule: the challenger plays FIRST, before the opponent even knows
the duel exists. The opponent's inbox card and the duel detail **never reveal
the challenger's score before the opponent finishes** — otherwise the opponent
would know exactly the score/time to beat.

Both players can start a rematch from the result screen: a NEW duel, same
theme, fresh questions, and whoever tapped "rematch" is the new challenger.

## 4. Question bank (static content, no DB)

Questions are **static Mar data**, not entities — a `Backend/Bank.mar` module
with a top-level list of records:

```
{ id : Int, theme : String, text : String
, a : String, b : String, c : String, d : String
, correct : Int  -- 0..3
}
```

- Target: **6 themes x 20 questions** (120 total, authored at build time):
  Football, Video Games, World Geography, Science, Movies, Music.
- Per duel, the create service samples **10 distinct questions** of the chosen
  theme with a hand-rolled LCG (the same Int-only pattern the canvas games
  use), seeded from `Time.now` seconds + the new duel id. The 10 ids are
  stored on the duel as a CSV string (entities have no list fields).
- The serve payload **never includes `correct`**. Correctness is judged
  server-side; the client learns it only from the answer response.

Static content means: no seeding problem, GET services stay read-only, and the
bank is versioned with the code.

## 5. Server-authoritative timing

The frontend cannot read the clock (`Time.now` is a backend Task); the client
countdown is cosmetic. All timing that matters is stamped by the server:

- `serve` (POST): creates/returns the current question's Answer row, stamping
  `servedAt = Time.now`. **Idempotent**: re-serving an already-served,
  unanswered question returns the same question WITHOUT resetting `servedAt` —
  refreshing the page cannot restart the timer.
- `answer` (POST): `elapsed = Time.toSeconds (Time.diff servedAt now)`, clamped
  to `questionLimit`. If `elapsed >= questionLimit`, the answer is recorded as
  a timeout (chosen ignored, wrong, 15s charged). Otherwise correctness is
  `chosen == bank.correct`. Response carries `correct`, `correctIndex`,
  `elapsed`, and `nextIndex` (or `done`).
- The client runs `Time.every 1000` for the visible countdown and auto-submits
  `chosen = -1` when it hits 0; if the client dies mid-question, the server
  clamp still charges 15s on the next interaction. Stalling BETWEEN questions
  is free by design (time only runs while a question is served).
- Network latency is included in elapsed time. Known limitation, same for both
  players; acceptable for a friendly duel game.
- Granularity is **whole seconds** (Duration only exposes `Time.toSeconds`).
  Tiebreak therefore compares totals in seconds (0–150). See open question 1.

## 6. Data model (entities)

```
profiles
  id        serial
  userId    text  notNull unique     -- Auth user id
  nick      text  notNull unique     -- 3..16 chars, shown everywhere
  wins      int   notNull            -- overall, denormalized
  losses    int   notNull
  draws     int   notNull

challenges
  id             serial
  challengerId   text notNull        -- profiles.userId
  opponentId     text notNull
  theme          text notNull
  qids           text notNull        -- "17,3,88,..." 10 bank ids
  status         enum draft|pending|accepted|declined|done
  chScore        int  notNull        -- filled as answers land
  chTime         int  notNull        -- total seconds
  opScore        int  notNull
  opTime         int  notNull
  result         enum none|challenger|opponent|draw
  challengerSeen bool notNull        -- NEW-result badge for the challenger
  createdAt      timestamp
  finishedAt     timestamp

answers
  id          serial
  challengeId int  notNull
  role        enum challenger|opponent
  qIndex      int  notNull           -- 0..9
  qid         int  notNull           -- bank id
  servedAt    timestamp
  answeredAt  timestamp
  chosen      int  notNull           -- 0..3, -1 = timeout/none
  correct     bool notNull
  elapsed     int  notNull           -- seconds, clamped to limit
```

Progress/resume is derived: answered rows for (challenge, role) → next index.
Overall W/L/D live on the profile and are incremented once, atomically with
the `done` transition. Head-to-head is computed on read by scanning finished
duels between the pair (fine at example scale; no counter drift possible).

## 7. Services (REST)

All services are `Service.declare` verb+path, implemented behind
`Auth.protect` (the handler receives the calling `user`). Authorization is
enforced server-side on every call — the client is never trusted.

| # | verb + path                          | req                    | resp / outcomes                                        | who may call |
|---|--------------------------------------|------------------------|--------------------------------------------------------|--------------|
| 1 | GET  /me                             | –                      | `HasProfile profile \| NoProfile`                       | any signed-in |
| 2 | POST /profiles                       | `{ nick }`             | `Created profile \| NickTaken \| InvalidNick`           | any signed-in, once |
| 3 | GET  /players                        | `{ search }`           | list of `{ nick, wins, losses, draws }` minus self      | profile owner |
| 4 | GET  /themes                         | –                      | list of `{ name, questionCount }` (from the bank)       | profile owner |
| 5 | POST /challenges                     | `{ opponentNick, theme }` | `Started { id } \| OpponentNotFound \| BadTheme \| SelfChallenge` | profile owner |
| 6 | POST /challenges/{id}/serve          | –                      | `Question { index, text, a,b,c,d, remainingHint } \| Finished \| NotYours` | current answerer |
| 7 | POST /challenges/{id}/answer         | `{ chosen }`           | `Feedback { correct, correctIndex, elapsed, done } \| NotYours` | current answerer |
| 8 | POST /challenges/{id}/accept         | –                      | `Accepted \| NotYours \| WrongState`                    | opponent, pending |
| 9 | POST /challenges/{id}/decline        | –                      | `Declined \| NotYours \| WrongState`                    | opponent, pending |
| 10| GET  /challenges                     | –                      | `{ inbox, waiting, drafts, results }` card lists        | participant  |
| 11| GET  /challenges/{id}                | –                      | detail (shape depends on status + viewer, see below)    | participant  |
| 12| POST /challenges/{id}/seen           | –                      | `Ok` (clears the NEW badge)                             | challenger   |
| 13| GET  /rivals                         | –                      | list of `{ nick, myWins, theirWins, draws }`            | profile owner |

"Current answerer" = challenger while `draft`, opponent while `accepted`.
Serve/answer in any other state (or by the other player) → `NotYours` /
`WrongState`; double answers to the same index are rejected (idempotent flow).

Detail (#11) shaping:
- while not `done`, the opponent never receives `chScore` / `chTime`;
- after `done`, both receive the full comparison: per-question breakdown
  (both sides' correct/elapsed + the right answers) and the head-to-head line.

## 8. Pages (mobile-first App UI)

- **SignIn / VerifyCode** — the hello-auth email+code flow, unchanged.
- **Nickname** (gate) — after first sign-in, `GET /me` says `NoProfile` →
  forced nickname form (unique, 3–16 chars). Then Home.
- **Home** — three sections + actions:
  - *Your turn*: inbox cards (accept / decline) and resumable drafts;
  - *Waiting*: duels the rival hasn't finished ("waiting for Ana…");
  - *Results*: finished duels, most recent first, NEW badge while
    `challengerSeen = False`.
  - Buttons: **New duel**, **Rivals**. Pull-to-refresh substitute: the page
    refetches on entry and polls `GET /challenges` with `Time.every` every
    ~15s while visible (there is no push channel; see limitation note).
- **New duel** — searchable player list (from `GET /players`) → theme picker
  (from `GET /themes`) → creates the duel and jumps straight into the quiz.
- **Quiz** (shared by both roles) — header "Question 3/10" + countdown "12s"
  + a text-glyph bar; the question as a `paragraph`; 4 full-width option
  buttons. On answer (or timeout): options lock, feedback line shows
  "Correct!" or "Wrong — answer: X", ~1.2s beat (tick-driven), then the next
  question is served. After Q10: challenger sees "Challenge sent to Ana!",
  opponent goes straight to the result screen.
- **Result / duel detail** — WIN / LOSE / DRAW headline, `7 x 5` score, total
  times, per-question breakdown table, head-to-head line ("You 3 x 1 Ana"),
  and **Rematch** (same theme, fresh questions, you become the challenger).
  Opening this as the challenger fires `POST .../seen`.
- **Rivals** — every player you have finished at least one duel with, with
  head-to-head W-L-D and a "duel again" shortcut; your overall record on top.

## 9. Edge cases and rules

- **Abandon mid-quiz**: the duel stays `draft`/`accepted` and is resumable
  from Home (served-but-unanswered question keeps its original `servedAt`, so
  walking away burns that question's clock — at most 15s lost, by design).
- **Refresh mid-question**: idempotent serve returns the same question with
  the original stamp; the visible countdown re-syncs from `remainingHint`.
- **Double submit / race**: answering an already-answered index is rejected;
  the client then re-serves to converge.
- **Self-challenge**: rejected server-side (`SelfChallenge`).
- **Concurrent duels**: any number of duels may be open between the same pair
  (v1 does not restrict).
- **Decline**: terminal, visible to the challenger, no effect on records.
- **Expiry**: none in v1 — pending duels wait forever (open question 6).
- **Users without a profile**: not listed, cannot be challenged.

## 10. Non-goals (v1)

- No realtime push (WebSockets/SSE don't exist in Mar) — polling only.
- No global leaderboard page (only overall record + rivals). Trivial to add.
- No avatars, no friend graph, no chat, no question-authoring UI, no i18n
  layer, no moderation. No canvas effects (see open question 9). No iOS pass.

## 11. Milestones

- **M0** — scaffold fullstack app (hello-auth as template): auth config,
  mar.json (port 3024), Nickname gate, empty Home. Workspace launch.json entry.
- **M1** — bank module (start with 3 themes x 12 while authoring the rest) +
  create duel + the full challenger quiz loop with server timing (serve /
  answer / resume / timeout).
- **M2** — inbox + accept/decline + opponent flow + result computation +
  profile W/L/D updates + waiting/results sections with polling.
- **M3** — duel detail with per-question breakdown, NEW-badge + seen, rivals
  page + head-to-head, rematch, full 6x20 bank, empty states, copy pass.
- **M4 (stretch)** — canvas "VS" result flourish, global leaderboard,
  pending-duel expiry.

Each milestone ends with `mar check` + a live browser pass (two sessions side
by side — two browsers signed in as different users — to exercise a full duel).

## 12. Dev notes

- Folder `examples/quiz-duel`, fullstack topology (server + SQLite db
  provisioned by `mar dev`), pinned **port 3024** in mar.json; add a
  `quiz-duel` entry to the workspace `.claude/launch.json`.
- Auth in dev prints the code to the console (no SMTP needed), as in
  hello-auth. Testing a duel locally = one normal window + one private window.
- Zero language changes is the goal; if a real gap appears mid-build (e.g.
  sub-second time, see below) it gets raised before any workaround.

---

## Open questions (need your call before M0)

1. **Tiebreak granularity** — Mar's `Duration` only exposes whole seconds
   (`Time.toSeconds`; there is no `toMillis`). v1 therefore breaks ties on
   total seconds (0–150), and identical totals are a draw. OK — or do you want
   a tiny stdlib addition (`Time.toMillis`) for millisecond tiebreaks? Unlike
   the canvas games, this example may drive language work if you want it to.
2. **Question language** — repo convention is English; the bank in English?
   Or pt-BR content (audience) with English UI? Or both-in-pt-BR?
3. **Themes + size** — Football, Video Games, World Geography, Science,
   Movies, Music; 20 questions each, authored by me. Adjust list/size?
4. **Timer** — 15s per question good?
5. **Decline** — stays consequence-free (no record impact)?
6. **Expiry** — pending duels never expire in v1. Want auto-expiry (e.g. 7
   days → cancelled) instead?
7. **Concurrent duels** — unlimited open duels per pair, OK?
8. **Polling** — Home refetches every ~15s while open. Fine?
9. **Canvas** — v1 ships without canvas (reasoning in §1). Want the M4 canvas
   "VS"/timer flourish kept on the roadmap, or drop canvas entirely?
10. **Rematch semantics** — same theme, fresh questions, rematcher becomes
    challenger. Or should rematch swap to the OTHER player's theme choice?
11. **Nickname rules** — 3–16 chars, letters/digits/underscore, unique
    case-insensitive. OK?
12. **Name** — "Quiz Duel" (folder `quiz-duel`). Keep?
