# pr-status

A terminal UI that lists your open GitHub pull requests with the review status of each reviewer, and lets you **re-ping reviewers who haven't reviewed yet** with a single key press.

```
 My open PRs

  3 PRs

▌ acme/api#128  feat: add rate limiting middleware
▌ ● @alice  ✓ @bob  • @carol

  acme/web#342  fix: broken redirect after login
  ● @acme/frontend  ✗ @dave

  acme/api#120  chore: bump dependencies
  no reviewers

  updated 12:12:30  ·  ✓ Pinged @alice on acme/api#128
```

## Reviewer colors

| Marker | Color | Meaning |
| ------ | ----- | ------- |
| `●` | red | review requested, no review yet |
| `✓` | green | approved |
| `•` | gray | commented |
| `✗` | gray | changes requested |

Team review requests are shown as `@org/team` and are always considered pending.

## Pinging reviewers

Selecting a PR and pressing <kbd>enter</kbd>:

1. removes the review request for every **pending** (red) reviewer,
2. waits 5 seconds (`--wait`),
3. requests their review again.

GitHub then sends them a fresh "review requested" notification. Reviewers who already approved or commented are left untouched.

If you press <kbd>ctrl+c</kbd> during the wait, the reviewers are re-requested immediately before quitting, so nobody is left removed.

## Requirements

- [GitHub CLI](https://cli.github.com/) (`gh`), logged in with `gh auth login`. `pr-status` reuses its credentials, so no token setup is needed.
- Go 1.26+ to build.

## Install

```bash
go install .
```

This builds `pr-status` into `$(go env GOPATH)/bin`. Make sure that directory is on your `PATH`.

## Usage

```bash
pr-status                                  # all your open PRs
pr-status -R acme/api                      # only one repo
pr-status -R acme/api -R acme/web          # several repos (or -R acme/api,acme/web)
pr-status --dry-run                        # try it without touching reviewers
```

### Options

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `-R`, `--repo OWNER/REPO` | all repos | Only show PRs from these repos. Repeatable or comma-separated. |
| `--wait DURATION` | `5s` | Delay between removing and re-requesting reviewers. |
| `--refresh DURATION` | `5m` | Auto-refresh interval, `0` to disable. Skipped while a ping is in progress. |
| `--dry-run` | off | Run the whole flow without calling the GitHub write APIs. |

### Keys

| Key | Action |
| --- | ------ |
| <kbd>↑</kbd> / <kbd>↓</kbd> (or <kbd>k</kbd> / <kbd>j</kbd>) | Move the selection |
| <kbd>enter</kbd> | Ping pending reviewers of the selected PR |
| <kbd>r</kbd> | Refresh now |
| <kbd>o</kbd> | Open the PR in your browser |
| <kbd>/</kbd> | Filter PRs by repo or title |
| <kbd>?</kbd> | Show all key bindings |
| <kbd>q</kbd> | Quit |

## API usage

Listing PRs is a single GraphQL query costing 1 point, out of the 5,000 points per hour GitHub allows. The default 5-minute auto-refresh therefore uses about 12 points per hour. A ping makes two REST calls: `DELETE` then `POST` on `/repos/{owner}/{repo}/pulls/{number}/requested_reviewers`.

Only the 50 most recently updated open PRs are listed.

## Project layout

| File | Contents |
| ---- | -------- |
| `main.go` | Flag parsing and program startup |
| `github.go` | GraphQL query, reviewer status mapping, remove/re-request calls |
| `ui.go` | Bubble Tea model, list rendering and styles |

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), [Lip Gloss](https://github.com/charmbracelet/lipgloss) and [go-gh](https://github.com/cli/go-gh).
