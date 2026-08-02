# How a card gets worked on

What happens between dropping a card into a column and getting a comment back:
which agent takes it, where it writes, which branch it commits to, and what
moves the card on. Written for a person using the board; the code is in
`desktop/internal/acp`.

## The three things that decide everything

| | Where it lives | What it answers |
|---|---|---|
| **Column** | column menu → *Agents in this column…* | what happens when a card lands here, who works it, how many at once |
| **Flow (route)** | board menu → *Workflows…* | where the card goes next, and on what event |
| **Registries** | board menu → *Agent repositories…*, *Agents…*, *Deploy targets…* | the machine: which agents exist, which repositories, where to deploy |

The split is worth holding onto: a **column says what is done**, a **route says
where the card goes afterwards**. A board with columns but no route still works
— cards are worked on where they stand and moved by hand. A board with routes
but no columns moves cards around without doing anything.

The registries are per machine, the columns and routes belong to a board. A
board made from the "My Project Tasks" template brings its own columns and
routes; the first time it is opened on a machine with empty registries, the
setup wizard asks for the rest.

## What happens when a card lands in a column

```mermaid
flowchart TD
    A["Card moved into a column"] --> B{"Is this column configured<br/>to do something?"}
    B -- no --> Z["Nothing happens"]
    B -- yes --> C{"Is the card assigned<br/>to a person?"}
    C -- yes --> Z2["Nothing starts.<br/>The card says why and waits for them"]
    C -- no --> D{"Column full?<br/>crew busy or limit reached"}
    D -- yes --> Q["Card waits in the queue.<br/>Starts by itself when a place frees up"]
    D -- no --> E["Pick an agent:<br/>card property → assignee → Agent option<br/>→ the column's crew → the only one registered"]
    E --> F["Find the repository:<br/>repo_path → Repositories option → source column name"]
    F --> G{"worktreeMode"}
    G -- always, the default --> H["Create a git worktree<br/>on a new branch acp/card-title-abcd1234"]
    G -- never --> I["Work in the repository itself.<br/>A second card is refused while one is running"]
    H --> J["Session runs: the agent works,<br/>progress and result land as card comments"]
    I --> J
    J --> K{"Is the card on a route?"}
    K -- no --> L["Card stays where it is.<br/>A person moves it on"]
    K -- yes --> M["The route takes the outcome<br/>and moves the card to the next stage"]
```

Two things about that first step. The trigger is a **change** of the column
property on an existing card — a card created directly in a column starts
nothing. And a card dragged out of a column while its session runs cancels it.

## When a worktree appears, and what becomes of it

With `worktreeMode: "always"` (the default) every card-triggered coding session
gets its own git worktree:

- **created** when the session starts, under `~/Library/Application Support/Focalboard/acp/worktrees`, on a new
  branch named `acp/<card title>-<session id>` — so the branch reads like the
  task and the preview address built from it does too;
- **based on** the card's `branch` property if it has one, otherwise on `HEAD`;
- **kept** when the session finishes successfully — the work is in it;
- **removed** when the session failed or was cancelled *and* the worktree is
  clean: no uncommitted changes and no commits ahead of its base. Anything the
  agent actually wrote survives even a failed session. `keepFailedWorktrees`
  keeps them all.

Three kinds of session never get a worktree, and run in the repository itself: a
**deploy** (it publishes an existing branch), a **test** (it reads the code it
is checking), and a **planning** session (it changes nothing).

## Which branch is followed

The card rarely names its branch, and with worktrees the agent's branch is
invented by us — so anything that watches a repository asks in this order:

```mermaid
flowchart LR
    A["Card property<br/>branch"] --> B["The branch this card's<br/>sessions worked on"]
    B --> C["What the route<br/>already carried"]
    C --> D["The repository's<br/>checked-out branch"]
```

The second one is what makes worktrees and routes work together: without it a
stage waiting for a merge would watch whatever happened to be checked out. The
deploy column resolves its branch the same way, so it publishes what the agent
wrote — the same branch as the **Deploy** button next to it on the card.

## Waiting for the repository

A stage can wait for something that happens outside the board:

| Trigger | Where it comes from | Needs |
|---|---|---|
| `branch.pushed`, `branch.merged` | local git | nothing |
| `pr.opened`, `pr.merged`, `pr.closed`, `review.approved`, `checks.passed`, `checks.failed` | GitHub API | a token in `githubToken` or `GITHUB_TOKEN` for private repositories |

There is nowhere for a webhook to arrive on a laptop, so this is polling —
`vcsPollSeconds`, 60 by default — and **only for the branches a parked card is
actually waiting on**. An idle board makes no requests at all.

## The routes the template ships

### Feature — the long way round

```mermaid
flowchart LR
    P["In Progress<br/>agent"] -->|done| R["In Review"]
    P -->|failed| B["Blocked"]
    R -->|branch merged| D["Deploy<br/>deploy"]
    D -->|done| T["To Test<br/>test"]
    D -->|failed| F["Failed"]
    T -->|passed| OK["Tested"]
    T -->|failed| P
    T -->|could not test| B
```

The loop is the point: a failed check sends the card back to the agent rather
than to a person, and the next session opens a new branch which the route then
follows.

### Hotfix — written and published

```mermaid
flowchart LR
    P["In Progress<br/>agent"] -->|done| D["Deploy<br/>deploy"]
    P -->|failed| B["Blocked"]
    D -->|done| C["Completed"]
    D -->|failed| F["Failed"]
```

### Review only — never deployed from here

```mermaid
flowchart LR
    P["In Progress<br/>agent"] -->|done| R["In Review"]
    P -->|failed| B["Blocked"]
    R -->|branch merged| C["Completed"]
```

A card takes a route by naming it in its **Workflow** field. A card that names
none is still worked on by the columns it passes through — it just does not move
by itself.

## Doing it by hand

Nothing here takes the board away from you:

- **assign a card to yourself** and no agent starts on it — deploy and test still
  run, since that is machine work. Assign an agent, or nobody, to hand it back;
- **drag a card anywhere** at any time: a running session is cancelled, and the
  stage you dropped it on starts as if the route had moved it;
- **open a console** on a card (*Open session*) to talk to an agent directly —
  that is asking for one outright, so the assignee rule does not apply;
- **Deploy** next to the branch publishes it without moving the card.

## When nothing happens

| What you see | Why |
|---|---|
| Card sits, no comment | The column is not configured, or the property that changed is not the one the columns are on |
| "Агент не запускается" | Somebody is assigned to the card |
| "Колонка занята" | The crew is busy or the limit is reached; it starts by itself later |
| "не задан ни repo_path…" | The card matched no repository: check the **Repositories** field against the registry |
| Card never leaves *In Review* | Nobody is watching its branch — see [which branch is followed](#which-branch-is-followed), or the route has no edge for what happened |
| Test stage refuses to start | The agent has no browser MCP server (*Agents…* → MCP servers) |

Everything a session does is written to the card as comments, and the card shows
its route, the stage it is on and what that stage is waiting for.

## The knobs

`~/Library/Application Support/Focalboard/acp/config.json`, all of it editable
by hand:

| | |
|---|---|
| `worktreeMode` | `always` (default) or `never` |
| `maxConcurrent` | how many sessions run at once on this machine (3) |
| `sessionTimeoutMinutes` / `testTimeoutMinutes` | one turn (15) and one browser pass (30) |
| `sessionIdleMinutes` | how long a console session sits between turns (30) |
| `vcsPollSeconds` / `gitRemote` / `githubToken` | watching repositories |
| `autoAllowTools` | what an agent may do without asking. A card-triggered session has nobody to ask, so anything not on the list is refused |
| `artifactsDir` | screenshots and verdicts of test runs |

See also [desktop/README.md](../README.md) for building and running the app.
