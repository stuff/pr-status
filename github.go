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
	Slug   string // team slug (teams only)
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
}

// Pending returns the reviewers who have been requested but haven't reviewed yet.
func (p PR) Pending() []Reviewer {
	var out []Reviewer
	for _, r := range p.Reviewers {
		if r.State == Pending {
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
              ... on Team { slug combinedSlug }
            }
          }
        }
        latestReviews(first: 50) {
          nodes { author { login } state }
        }
      }
    }
  }
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
						Slug         string
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
		}
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
				pr.Reviewers = append(pr.Reviewers, Reviewer{Login: rv.CombinedSlug, Slug: rv.Slug, IsTeam: true, State: Pending})
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

		prs = append(prs, pr)
	}
	return prs, nil
}

func searchQuery(repos []string) string {
	q := "is:pr is:open author:@me archived:false sort:updated-desc"
	for _, r := range repos {
		q += " repo:" + r // multiple repo: qualifiers are OR'ed
	}
	return q
}

// setReviewRequests removes (method DELETE) or adds (method POST) review
// requests for the given reviewers on a PR.
func setReviewRequests(method string, pr PR, reviewers []Reviewer, dryRun bool) error {
	if dryRun {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	body := struct {
		Reviewers     []string `json:"reviewers"`
		TeamReviewers []string `json:"team_reviewers"`
	}{Reviewers: []string{}, TeamReviewers: []string{}}
	for _, r := range reviewers {
		if r.IsTeam {
			body.TeamReviewers = append(body.TeamReviewers, r.Slug)
		} else {
			body.Reviewers = append(body.Reviewers, r.Login)
		}
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
