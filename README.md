# gtasks

A small command-line client for [Google Tasks](https://tasks.google.com).
Works on Linux, macOS, Windows and WSL.

```
$ gtasks ls
My Tasks
   1  [ ] Renew passport  (overdue 2 days)
   2  [ ] Groceries  (due today, +notes)
   3  [ ]   Milk
   4  [ ]   Eggs
   5  [ ] Book dentist  (due Mon)

$ gtasks done 3 4
Completed: Milk
Completed: Eggs
```

## Install

```sh
go install github.com/xnmp/gtasks-cli/cmd/gtasks@latest
```

or build from a checkout: `go build -o gtasks ./cmd/gtasks` (add
`CGO_ENABLED=0` for a fully static binary). Requires Go 1.26+.

## Setup (one time)

Google requires every API client to have its own OAuth client ID, so you
create one in your own Google Cloud project. It's free.

1. Open the [Google Cloud Console](https://console.cloud.google.com/) and
   create (or pick) a project.
2. **APIs & Services → Library**: enable the **Google Tasks API**.
3. **APIs & Services → OAuth consent screen**: choose *External*, fill in the
   app name and your email, and add yourself under **Test users**.
4. **APIs & Services → Credentials → Create credentials → OAuth client ID**,
   application type **Desktop app**. Download the JSON.
5. Save it as the credentials file (run `gtasks auth status` to see the path):
   - Linux / WSL: `~/.config/gtasks/credentials.json`
   - macOS: `~/Library/Application Support/gtasks/credentials.json`
   - Windows: `%AppData%\gtasks\credentials.json`
6. Run `gtasks auth login` and approve access in the browser.

On a remote or headless machine use `gtasks auth login --no-browser`: open the
printed URL anywhere, approve, then paste the `http://127.0.0.1:…` URL the
browser was redirected to (the page itself will fail to load; that's expected).
Pasting also works in a normal interactive login if the browser can't reach
the local callback (e.g. some WSL network setups).

> While the consent screen is in *Testing* status, Google expires refresh
> tokens after 7 days. Publish the app (no verification is needed for
> personal use) to keep logins valid.

## Usage

| Command | Description |
| --- | --- |
| `gtasks ls [-a]` | List pending tasks (`-a` includes completed) |
| `gtasks add TITLE [-d DUE] [-n NOTES] [-p PARENT]` | Add a task (optionally as a subtask) |
| `gtasks done TASK...` | Mark tasks completed |
| `gtasks undo TASK...` | Reopen completed tasks |
| `gtasks edit TASK [-t TITLE] [-n NOTES] [-d DUE \| --no-due]` | Change a task |
| `gtasks show TASK` | Show all details of a task |
| `gtasks rm TASK... [-y]` | Delete tasks (confirms if subtasks or Docs/Chat assignments go too) |
| `gtasks clear` | Hide completed tasks |
| `gtasks lists` | Show task lists |
| `gtasks lists add TITLE` / `rename LIST TITLE` / `rm LIST [-y]` | Manage lists |
| `gtasks auth login [--no-browser]` / `logout` / `status` | Authentication |
| `gtasks completion bash\|zsh\|fish\|powershell` | Shell completions |

Global flags: `-l, --list LIST` selects a list (exact title, unique title
prefix, or ID; defaults to `$GTASKS_LIST`, else your default list) and
`--json` prints machine-readable output.

**Referring to tasks.** `TASK` is the number the last `gtasks ls` showed for
it (pending tasks only), the task ID or a unique prefix of it (4+ characters,
see `--json` or `show`), or the exact title. Numbers are remembered per list
until the next `ls`, so tasks added or reordered in the meantime (here or on
your phone) never change what `#3` means; if that task was deleted elsewhere
you get an error instead of hitting a different task. When titles collide,
`done` picks the pending one and `undo` the completed one. `ls --json` does
not renumber (scripts should use IDs), so a status-bar widget polling it never
changes what your numbers mean.

`lists rm` and `lists rename` require the exact list title or ID.

**Due dates.** `YYYY-MM-DD`, `today`, `tomorrow`, `yesterday`, `+3d`, `+2w`,
or a weekday (`fri`, `monday`) meaning its next occurrence after today.
Google Tasks stores dates only, not times.

## Configuration

| Variable | Effect |
| --- | --- |
| `GTASKS_LIST` | Default list for all commands |
| `GTASKS_CONFIG_DIR` | Directory holding `credentials.json` and `token.json` |
| `GTASKS_CREDENTIALS` | Path to the OAuth client JSON (overrides the above) |
| `GTASKS_CACHE_DIR` | Where the `ls` numbering is remembered (default: OS cache dir) |

The token is stored in the config directory (mode 0600 on Unix; on Windows it
relies on your profile's `%AppData%` permissions). `gtasks auth logout`
revokes it at Google and deletes it. If the saved login expires, commands say
so; run `gtasks auth login` again.

## Development

```sh
go test ./...
```

Layout: `internal/domain` (pure model: dates, ordering, reference
resolution), `internal/app` (use cases against `Store` and `NumberStore`
ports), `internal/google` (Tasks API adapter), `internal/auth` (OAuth loopback
+ PKCE, token storage), `internal/state` (remembered numbering),
`internal/render` (output), `internal/cli` (commands).
