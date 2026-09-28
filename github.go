package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

type ReviewState int

const (
	Pending ReviewState = iota
	Approved
	Commented
	ChangesRequested
)

type Reviewer struct {
	Login  string // user login, or "org/slug" for teams
	IsTeam bool
	State  ReviewState
}

type PR struct {
	Repo      string
	Number    int
	Title     string
	URL       string
	IsDraft   bool
	Reviewers []Reviewer
	Pings     []Ping // oldest first
}

// Ping is one re-request of reviewers: a review request removed then added
// back shortly after, both by me.
type Ping struct {
	At        time.Time
	Reviewers []string
}

// Max delay between the removal and the re-request for them to count as a ping.
const pingMaxGap = 2 * time.Minute

// Pingable returns the users who have been requested but haven't reviewed yet.
// Teams are left out: re-requesting a team with auto-assignment enabled could
// assign it to a different member.
func (p PR) Pingable() []Reviewer {
	var out []Reviewer
	for _, r := range p.Reviewers {
		if r.State == Pending && !r.IsTeam {
			out = append(out, r)
		}
	}
	return out
}

const prsQuery = `
query($q: String!) {
  viewer { login }
  search(query: $q, type: ISSUE, first: 50) {
    nodes {
      ... on PullRequest {
        number
        title
        url
        isDraft
        repository { nameWithOwner }
        reviewRequests(first: 50) {
          nodes {
            requestedReviewer {
              __typename
              ... on User { login }
              ... on Team { combinedSlug }
            }
          }
        }
        latestReviews(first: 50) {
          nodes { author { login } state }
        }
        timelineItems(last: 100, itemTypes: [REVIEW_REQUESTED_EVENT, REVIEW_REQUEST_REMOVED_EVENT]) {
          nodes {
            __typename
            ... on ReviewRequestedEvent { createdAt actor { login } requestedReviewer { ...reviewerName } }
            ... on ReviewRequestRemovedEvent { createdAt actor { login } requestedReviewer { ...reviewerName } }
          }
        }
      }
    }
  }
}

fragment reviewerName on RequestedReviewer {
  ... on User { login }
  ... on Team { combinedSlug }
}`

type prsResponse struct {
	Viewer struct{ Login string }
	Search struct {
		Nodes []struct {
			Number         int
			Title          string
			URL            string
			IsDraft        bool
			Repository     struct{ NameWithOwner string }
			ReviewRequests struct {
				Nodes []struct {
					RequestedReviewer struct {
						Typename     string `json:"__typename"`
						Login        string
						CombinedSlug string
					}
				}
			}
			LatestReviews struct {
				Nodes []struct {
					Author struct{ Login string }
					State  string
				}
			}
			TimelineItems struct {
				Nodes []timelineEvent
			}
		}
	}
}

type timelineEvent struct {
	Typename          string `json:"__typename"`
	CreatedAt         time.Time
	Actor             struct{ Login string }
	RequestedReviewer struct {
		Login        string
		CombinedSlug string
	}
}

// fetchPRs returns my open PRs, optionally limited to the given repos ("owner/name").
func fetchPRs(repos []string) ([]PR, error) {
	client, err := api.DefaultGraphQLClient()
	if err != nil {
		return nil, err
	}
	var resp prsResponse
	if err := client.Do(prsQuery, map[string]interface{}{"q": searchQuery(repos)}, &resp); err != nil {
		return nil, err
	}

	var prs []PR
	for _, n := range resp.Search.Nodes {
		if n.Number == 0 {
			continue // not a PullRequest node
		}
		pr := PR{
			Repo:    n.Repository.NameWithOwner,
			Number:  n.Number,
			Title:   n.Title,
			URL:     n.URL,
			IsDraft: n.IsDraft,
		}

		reviewed := map[string]ReviewState{}
		for _, r := range n.LatestReviews.Nodes {
			if r.Author.Login == "" || r.Author.Login == resp.Viewer.Login {
				continue
			}
			switch r.State {
			case "APPROVED":
				reviewed[r.Author.Login] = Approved
			case "CHANGES_REQUESTED":
				reviewed[r.Author.Login] = ChangesRequested
			case "COMMENTED", "DISMISSED":
				reviewed[r.Author.Login] = Commented
			}
		}

		seen := map[string]bool{}
		for _, rr := range n.ReviewRequests.Nodes {
			rv := rr.RequestedReviewer
			switch rv.Typename {
			case "User":
				state, ok := reviewed[rv.Login]
				if !ok {
					state = Pending
				}
				pr.Reviewers = append(pr.Reviewers, Reviewer{Login: rv.Login, State: state})
				seen[rv.Login] = true
			case "Team":
				pr.Reviewers = append(pr.Reviewers, Reviewer{Login: rv.CombinedSlug, IsTeam: true, State: Pending})
			}
		}
		// People who reviewed without (or no longer) being requested.
		var extra []string
		for login := range reviewed {
			if !seen[login] {
				extra = append(extra, login)
			}
		}
		sort.Strings(extra)
		for _, login := range extra {
			pr.Reviewers = append(pr.Reviewers, Reviewer{Login: login, State: reviewed[login]})
		}

		pr.Pings = findPings(n.TimelineItems.Nodes, resp.Viewer.Login)
		prs = append(prs, pr)
	}
	return prs, nil
}

// findPings pairs my "review request removed" events with my following
// "review requested" event for the same reviewer, and groups them by time.
func findPings(events []timelineEvent, me string) []Ping {
	removedAt := map[string]time.Time{}
	var pings []Ping
	for _, e := range events {
		name := e.RequestedReviewer.Login
		if name == "" {
			name = e.RequestedReviewer.CombinedSlug
		}
		if name == "" || e.Actor.Login != me {
			continue
		}
		switch e.Typename {
		case "ReviewRequestRemovedEvent":
			removedAt[name] = e.CreatedAt
		case "ReviewRequestedEvent":
			removed, ok := removedAt[name]
			delete(removedAt, name)
			if !ok || e.CreatedAt.Sub(removed) > pingMaxGap {
				continue
			}
			if last := len(pings) - 1; last >= 0 && e.CreatedAt.Sub(pings[last].At) < pingMaxGap {
				pings[last].Reviewers = append(pings[last].Reviewers, name)
			} else {
				pings = append(pings, Ping{At: e.CreatedAt, Reviewers: []string{name}})
			}
		}
	}
	return pings
}

func searchQuery(repos []string) string {
	q := "is:pr is:open author:@me archived:false sort:updated-desc"
	for _, r := range repos {
		q += " repo:" + r // multiple repo: qualifiers are OR'ed
	}
	return q
}

// postPingComment comments "ping @a @b" on the PR, since a re-request alone
// doesn't reach every notification channel (e.g. Slack).
func postPingComment(pr PR, reviewers []Reviewer, dryRun bool) error {
	if dryRun {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	payload, err := json.Marshal(map[string]string{"body": "ping " + reviewerNames(reviewers)})
	if err != nil {
		return err
	}
	client, err := api.DefaultRESTClient()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("repos/%s/issues/%d/comments", pr.Repo, pr.Number)
	return client.Post(path, bytes.NewReader(payload), nil)
}

// setReviewRequests removes (method DELETE) or adds (method POST) review
// requests for the given users on a PR.
func setReviewRequests(method string, pr PR, reviewers []Reviewer, dryRun bool) error {
	if dryRun {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	body := struct {
		Reviewers []string `json:"reviewers"`
	}{}
	for _, r := range reviewers {
		body.Reviewers = append(body.Reviewers, r.Login)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	client, err := api.DefaultRESTClient()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers", pr.Repo, pr.Number)
	return client.Do(method, path, bytes.NewReader(payload), nil)
}

func reviewerNames(rs []Reviewer) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = "@" + r.Login
	}
	return strings.Join(names, " ")
}
