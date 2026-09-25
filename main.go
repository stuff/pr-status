package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "simulate the re-request without changing reviewers")
	wait := flag.Duration("wait", 10*time.Second, "delay between removing and re-requesting reviewers")
	refresh := flag.Duration("refresh", 5*time.Minute, "auto-refresh interval (0 to disable)")

	var repos []string
	addRepos := func(v string) error {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if strings.Count(r, "/") != 1 || strings.HasPrefix(r, "/") || strings.HasSuffix(r, "/") {
				return fmt.Errorf("expected OWNER/REPO, got %q", r)
			}
			repos = append(repos, r)
		}
		return nil
	}
	const repoUsage = "only show PRs from `OWNER/REPO` (repeatable or comma-separated)"
	flag.Func("repo", repoUsage, addRepos)
	flag.Func("R", "shorthand for --repo", addRepos)
	flag.Parse()

	if _, err := tea.NewProgram(newModel(*dryRun, *wait, *refresh, repos), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
