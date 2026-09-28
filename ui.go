package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	red    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	green  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	gray   = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	accent = lipgloss.AdaptiveColor{Light: "#8250DF", Dark: "#A371F7"}

	pendingStyle   = lipgloss.NewStyle().Foreground(red).Bold(true)
	approvedStyle  = lipgloss.NewStyle().Foreground(green)
	commentedStyle = lipgloss.NewStyle().Foreground(gray)
	faintStyle     = lipgloss.NewStyle().Foreground(gray).Faint(true)
	repoStyle      = lipgloss.NewStyle().Foreground(gray)
	titleStyle     = lipgloss.NewStyle().Bold(true)
	selectedBar    = lipgloss.NewStyle().Foreground(accent).Render("▌ ")
	errorStyle     = lipgloss.NewStyle().Foreground(red)
	successStyle   = lipgloss.NewStyle().Foreground(green)
	statusStyle    = lipgloss.NewStyle().Padding(0, 2)
)

// ---- list item & delegate ----

type prItem struct{ PR }

func (i prItem) FilterValue() string { return fmt.Sprintf("%s#%d %s", i.Repo, i.Number, i.Title) }

type prDelegate struct{}

func (prDelegate) Height() int                         { return 2 }
func (prDelegate) Spacing() int                        { return 1 }
func (prDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (prDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pr := item.(prItem)
	prefix := "  "
	title := pr.Title
	tStyle := titleStyle
	if index == m.Index() {
		prefix = selectedBar
		tStyle = tStyle.Foreground(accent)
	}

	line1 := repoStyle.Render(fmt.Sprintf("%s#%d", pr.Repo, pr.Number)) + "  " + tStyle.Render(title)
	if pr.IsDraft {
		line1 += faintStyle.Render("  [draft]")
	}

	var reviewers []string
	for _, r := range pr.Reviewers {
		reviewers = append(reviewers, renderReviewer(r))
	}
	line2 := strings.Join(reviewers, "  ")
	if line2 == "" {
		line2 = faintStyle.Render("no reviewers")
	}
	if n := len(pr.Pings); n > 0 {
		summary := fmt.Sprintf("↻ pinged %d×, last %s", n, relTime(pr.Pings[n-1].At))
		line2 += "    " + faintStyle.Render(summary)
	}

	width := m.Width() - 2
	fmt.Fprintf(w, "%s%s\n%s%s", prefix, ansi.Truncate(line1, width, "…"), prefix, ansi.Truncate(line2, width, "…"))
}

func renderReviewer(r Reviewer) string {
	name := "@" + r.Login
	switch r.State {
	case Approved:
		return approvedStyle.Render("✓ " + name)
	case Commented:
		return commentedStyle.Render("• " + name)
	case ChangesRequested:
		return commentedStyle.Render("✗ " + name)
	default:
		return pendingStyle.Render("● " + name)
	}
}

// ---- messages ----

type prsLoadedMsg struct {
	prs []PR
	err error
}
type removedMsg struct{ err error }
type addedMsg struct{ err error }
type commentedMsg struct{ err error }
type countdownMsg struct{}
type autoRefreshMsg struct{}
type openedMsg struct {
	pr  PR
	err error
}

type step int

const (
	idle step = iota
	confirming
	removing
	waiting
	adding
	commenting
)

type options struct {
	dryRun  bool
	comment bool
	wait    time.Duration
	refresh time.Duration
	repos   []string
}

// ---- model ----

type model struct {
	list    list.Model
	spinner spinner.Model
	width   int
	height  int

	options

	loading   bool
	loadErr   error
	updatedAt time.Time

	step      step
	target    PR
	pending   []Reviewer
	remaining time.Duration
	quitAfter bool

	showHistory bool

	status string
}

var (
	pingKey    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ping reviewers"))
	refreshKey = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	openKey    = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open"))
	historyKey = key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "ping history"))
)

func newModel(opts options) model {
	l := list.New(nil, prDelegate{}, 0, 0)
	l.Title = "My open PRs"
	if len(opts.repos) > 0 {
		l.Title += " in " + strings.Join(opts.repos, ", ")
	}
	if opts.dryRun {
		l.Title += " (dry-run)"
	}
	l.Styles.Title = lipgloss.NewStyle().Background(accent).Foreground(lipgloss.Color("#FFFFFF")).Bold(true).Padding(0, 1)
	l.SetStatusBarItemName("PR", "PRs")
	l.AdditionalShortHelpKeys = func() []key.Binding { return []key.Binding{pingKey, historyKey, refreshKey, openKey} }
	l.AdditionalFullHelpKeys = l.AdditionalShortHelpKeys

	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(accent)))

	return model{list: l, spinner: sp, options: opts, loading: true}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadPRs(), m.spinner.Tick, m.scheduleRefresh())
}

// startLoad refreshes the PR list in the background; the UI stays usable.
func (m model) startLoad() (model, tea.Cmd) {
	if m.loading {
		return m, nil
	}
	m.loading = true
	return m, tea.Batch(m.loadPRs(), m.spinner.Tick)
}

func (m model) scheduleRefresh() tea.Cmd {
	if m.refresh <= 0 {
		return nil
	}
	return tea.Tick(m.refresh, func(time.Time) tea.Msg { return autoRefreshMsg{} })
}

func (m model) loadPRs() tea.Cmd {
	repos := m.repos
	return func() tea.Msg {
		prs, err := fetchPRs(repos)
		return prsLoadedMsg{prs, err}
	}
}

func (m model) busy() bool { return m.step != idle }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, msg.Height-2)
		return m, nil

	case spinner.TickMsg:
		if !m.busy() && !m.loading {
			return m, nil // stop the tick chain
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case prsLoadedMsg:
		m.loading = false
		m.loadErr = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.updatedAt = time.Now()
		items := make([]list.Item, len(msg.prs))
		for i, pr := range msg.prs {
			items[i] = prItem{pr}
		}
		return m, m.list.SetItems(items)

	case openedMsg:
		if msg.err != nil {
			m.status = errorStyle.Render("Failed to open browser: " + msg.err.Error())
		} else {
			m.status = faintStyle.Render(fmt.Sprintf("Opened %s#%d in browser", msg.pr.Repo, msg.pr.Number))
		}
		return m, nil

	case autoRefreshMsg:
		next := m.scheduleRefresh()
		if m.busy() {
			return m, next // don't refresh in the middle of a ping
		}
		m, cmd := m.startLoad()
		return m, tea.Batch(cmd, next)

	case removedMsg:
		if msg.err != nil {
			m.step = idle
			m.quitAfter = false
			m.status = errorStyle.Render("Failed to remove reviewers: " + msg.err.Error())
			return m, nil
		}
		if m.quitAfter {
			return m.startAdding()
		}
		m.step = waiting
		m.remaining = m.wait
		m.status = ""
		return m, countdown()

	case countdownMsg:
		if m.step != waiting {
			return m, nil
		}
		m.remaining -= time.Second
		if m.remaining > 0 {
			return m, countdown()
		}
		return m.startAdding()

	case addedMsg:
		m.step = idle
		if msg.err != nil {
			m.status = errorStyle.Render(fmt.Sprintf("Failed to re-request review (%s were removed!): %s", reviewerNames(m.pending), msg.err))
			m.quitAfter = false // stay open so the user sees the error
			return m, nil
		}
		if m.quitAfter {
			return m, tea.Quit
		}
		if m.comment {
			m.step = commenting
			pr, pending, dry := m.target, m.pending, m.dryRun
			return m, func() tea.Msg { return commentedMsg{postPingComment(pr, pending, dry)} }
		}
		m.status = successStyle.Render(fmt.Sprintf("✓ Pinged %s on %s#%d", reviewerNames(m.pending), m.target.Repo, m.target.Number))
		return m.startLoad()

	case commentedMsg:
		m.step = idle
		if msg.err != nil {
			m.status = errorStyle.Render(fmt.Sprintf("Re-requested %s but failed to comment: %s", reviewerNames(m.pending), msg.err))
		} else {
			m.status = successStyle.Render(fmt.Sprintf("✓ Pinged %s on %s#%d (re-requested + commented)", reviewerNames(m.pending), m.target.Repo, m.target.Number))
		}
		return m.startLoad()

	case tea.KeyMsg:
		if m.showHistory {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc", "h", "q", "enter":
				m.showHistory = false
			}
			return m, nil
		}
		if m.step == confirming {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "y", "Y":
				return m.startPing()
			default:
				m.step = idle
				m.status = faintStyle.Render("Cancelled.")
			}
			return m, nil
		}
		if m.busy() {
			if msg.String() == "ctrl+c" {
				switch m.step {
				case waiting:
					// Don't leave reviewers removed: re-add them right away, then quit.
					m.quitAfter = true
					return m.startAdding()
				default:
					m.quitAfter = true
				}
			}
			return m, nil
		}
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch {
		case key.Matches(msg, pingKey):
			return m.askConfirm()
		case key.Matches(msg, historyKey):
			if m.list.SelectedItem() != nil {
				m.showHistory = true
			}
			return m, nil
		case key.Matches(msg, refreshKey):
			return m.startLoad()
		case key.Matches(msg, openKey):
			if it, ok := m.list.SelectedItem().(prItem); ok {
				return m, openBrowser(it.PR)
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) askConfirm() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return m, nil
	}
	pending := it.Pingable()
	if len(pending) == 0 {
		m.status = faintStyle.Render("No pending reviewers to ping on this PR (teams are never pinged).")
		return m, nil
	}
	m.target, m.pending = it.PR, pending
	m.step = confirming
	m.status = ""
	return m, nil
}

func (m model) startPing() (tea.Model, tea.Cmd) {
	m.step = removing
	pr, pending, dry := m.target, m.pending, m.dryRun
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		return removedMsg{setReviewRequests(http.MethodDelete, pr, pending, dry)}
	})
}

func (m model) startAdding() (tea.Model, tea.Cmd) {
	m.step = adding
	pr, pending, dry := m.target, m.pending, m.dryRun
	return m, func() tea.Msg {
		return addedMsg{setReviewRequests(http.MethodPost, pr, pending, dry)}
	}
}

func countdown() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return countdownMsg{} })
}

func openBrowser(pr PR) tea.Cmd {
	return func() tea.Msg {
		return openedMsg{pr, openURL(pr.URL)}
	}
}

func (m model) View() string {
	var status string
	names := reviewerNames(m.pending)
	if m.showHistory {
		return m.historyView()
	}
	switch m.step {
	case confirming:
		how := "re-request"
		if m.comment {
			how += " + comment"
		}
		status = fmt.Sprintf("Ping %s on %s#%d (%s)?  %s  %s",
			pendingStyle.Render(names), m.target.Repo, m.target.Number, how,
			titleStyle.Render("y")+faintStyle.Render(" confirm"),
			faintStyle.Render("any other key cancels"))
	case removing:
		status = m.spinner.View() + " Removing " + pendingStyle.Render(names) + "…"
	case waiting:
		secs := int((m.remaining + time.Second - 1) / time.Second)
		status = m.spinner.View() + fmt.Sprintf(" Waiting %ds before re-requesting %s…", secs, pendingStyle.Render(names))
	case adding:
		status = m.spinner.View() + " Re-requesting review from " + pendingStyle.Render(names) + "…"
	case commenting:
		status = m.spinner.View() + " Commenting " + faintStyle.Render("\"ping "+names+"\"") + "…"
	default:
		var parts []string
		switch {
		case m.loading && m.updatedAt.IsZero():
			parts = append(parts, m.spinner.View()+" Loading PRs…")
		case m.loading:
			parts = append(parts, m.spinner.View()+" Refreshing…")
		case m.loadErr != nil:
			parts = append(parts, errorStyle.Render("Error: "+m.loadErr.Error()))
		case !m.updatedAt.IsZero():
			parts = append(parts, faintStyle.Render("updated "+m.updatedAt.Format("15:04:05")))
		}
		if m.status != "" {
			parts = append(parts, m.status)
		}
		status = strings.Join(parts, faintStyle.Render("  ·  "))
	}
	return m.list.View() + "\n" + statusStyle.Render(ansi.Truncate(status, max(m.width-4, 0), "…"))
}

func (m model) historyView() string {
	it, ok := m.list.SelectedItem().(prItem)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(repoStyle.Render(fmt.Sprintf("%s#%d", it.Repo, it.Number)) + "  " + titleStyle.Render(it.Title) + "\n\n")
	if len(it.Pings) == 0 {
		b.WriteString(faintStyle.Render("No pings yet.") + "\n")
	}
	for i := len(it.Pings) - 1; i >= 0; i-- {
		p := it.Pings[i]
		names := make([]string, len(p.Reviewers))
		for j, r := range p.Reviewers {
			names[j] = "@" + r
		}
		when := p.At.Local().Format("Mon 02 Jan 15:04")
		b.WriteString(fmt.Sprintf("%s  %s  %s\n", titleStyle.Render(when), faintStyle.Render(fmt.Sprintf("%-8s", relTime(p.At))), strings.Join(names, " ")))
	}
	b.WriteString("\n" + faintStyle.Render("esc close"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(1, 2).
		Width(min(m.width-4, 100)).
		Render(strings.TrimRight(b.String(), "\n"))
	header := m.list.Styles.Title.Render("Ping history")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, header+"\n\n"+box)
}

func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
