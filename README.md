# pr-status

A terminal UI that lists your open GitHub pull requests with the review status of each reviewer, and lets you **re-ping reviewers who haven't reviewed yet** with a single key press.

```
 My open PRs

  3 PRs

▌ acme/api#128  feat: add rate limiting middleware
▌ ● @alice  ✓ @bob  • @carol    ↻ pinged 2×, last 3h ago

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

Team review requests are shown as `@org/team` and are always considered pending, but teams are **never pinged**: re-requesting a team with auto-assignment enabled could assign the review to a different member.

## Pinging reviewers

Selecting a PR and pressing <kbd>enter</kbd> asks for confirmation (<kbd>y</kbd> to confirm, any other key cancels), then:

1. removes the review request for every **pending** (red) reviewer, except teams,
2. waits 10 seconds (`--wait`),
3. requests their review again,
4. posts a `ping @alice @bob` comment on the PR (disable with `--no-comment`).

The re-request sends a fresh "review requested" notification on GitHub, but integrations don't always relay it (in our Slack setup, a re-request alone sent nothing), so the comment's @mentions make sure people actually get pinged. Reviewers who already approved or commented are left untouched.

If you press <kbd>ctrl+c</kbd> during the wait, the reviewers are re-requested immediately before quitting (without the comment), so nobody is left removed.

### Ping history

PRs you already pinged show `↻ pinged N×, last …` next to their reviewers. Press <kbd>h</kbd> to see every ping on the selected PR, with its date and reviewers.

The history is read from the PR's GitHub timeline, not stored locally: a ping is a review request **you** removed and then re-requested for the same reviewer within 2 minutes. It therefore also includes pings made from another machine or by hand in the GitHub UI, but not `--dry-run` runs. Only the last 100 review-request events of each PR are scanned.

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
| `--wait DURATION` | `10s` | Delay between removing and re-requesting reviewers. |
| `--refresh DURATION` | `5m` | Auto-refresh interval, `0` to disable. Skipped while a ping is in progress. |
| `--no-comment` | off | Only re-request reviews, don't post the `ping @…` comment. |
| `--dry-run` | off | Run the whole flow without calling the GitHub write APIs (no re-request, no comment). |

### Keys

| Key | Action |
| --- | ------ |
| <kbd>↑</kbd> / <kbd>↓</kbd> (or <kbd>k</kbd> / <kbd>j</kbd>) | Move the selection |
| <kbd>enter</kbd> | Ping pending reviewers of the selected PR (asks for confirmation) |
| <kbd>h</kbd> | Show the ping history of the selected PR |
| <kbd>r</kbd> | Refresh now |
| <kbd>o</kbd> | Open the PR in your browser |
| <kbd>/</kbd> | Filter PRs by repo, number or title |
| <kbd>?</kbd> | Show all key bindings |
| <kbd>q</kbd> | Quit |

## Browser

<kbd>o</kbd> uses the same browser as `gh --web`: `GH_BROWSER`, then `gh config get browser`, then `BROWSER`. If none of them is set and you are running under WSL, the PR opens in your **Windows default browser** (via `wslview`, or `rundll32.exe` if wslu isn't installed) instead of a Linux browser running through WSLg.

## API usage

Listing PRs is a single GraphQL query costing 2 points, out of the 5,000 points per hour GitHub allows. The default 5-minute auto-refresh therefore uses about 24 points per hour. A ping makes up to three REST calls: `DELETE` then `POST` on `/repos/{owner}/{repo}/pulls/{number}/requested_reviewers`, then `POST /repos/{owner}/{repo}/issues/{number}/comments`.

Only the 50 most recently updated open PRs are listed.

## Project layout

| File | Contents |
| ---- | -------- |
| `main.go` | Flag parsing and program startup |
| `github.go` | GraphQL query, reviewer status mapping, remove/re-request calls |
| `ui.go` | Bubble Tea model, list rendering and styles |
| `browser.go` | Opening PRs in the browser (WSL-aware) |

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), [Lip Gloss](https://github.com/charmbracelet/lipgloss) and [go-gh](https://github.com/cli/go-gh).
