package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	httpTimeout = 5 * time.Second
	userAgent   = "claude-status/0.1 (+https://status.claude.com)"
)

// Variables so tests can point them at a local server.
var (
	summaryURL   = "https://status.claude.com/api/v2/summary.json"
	incidentsURL = "https://status.claude.com/api/v2/incidents.json"
)

var httpClient = &http.Client{Timeout: httpTimeout}

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cBlue   = "\033[34m"
	cGray   = "\033[90m"
)

type StatusIndicator struct {
	Indicator   string `json:"indicator"`
	Description string `json:"description"`
}

type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type IncidentUpdate struct {
	Body string `json:"body"`
}

type Incident struct {
	Name            string           `json:"name"`
	Status          string           `json:"status"`
	Impact          string           `json:"impact"`
	Shortlink       string           `json:"shortlink"`
	StartedAt       *time.Time       `json:"started_at"`
	ResolvedAt      *time.Time       `json:"resolved_at"`
	IncidentUpdates []IncidentUpdate `json:"incident_updates"`
}

type ScheduledMaintenance struct {
	Name         string     `json:"name"`
	ScheduledFor *time.Time `json:"scheduled_for"`
}

type Summary struct {
	Page struct {
		Name      string    `json:"name"`
		URL       string    `json:"url"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"page"`
	Status                StatusIndicator        `json:"status"`
	Components            []Component            `json:"components"`
	Incidents             []Incident             `json:"incidents"`
	ScheduledMaintenances []ScheduledMaintenance `json:"scheduled_maintenances"`
}

type IncidentsList struct {
	Incidents []Incident `json:"incidents"`
}

const usage = `claude-status - check Anthropic / Claude service status

Usage:
  claude-status [flags]

Flags:
  --json        output the raw JSON summary from status.claude.com
  --no-color    disable ANSI color output (auto-disabled when stdout isn't a TTY)
  -h, --help    show this help

Exit codes:
  0  all systems operational
  1  one or more components degraded, or an active incident
  2  unable to reach the status page, or its response was not recognised
  3  invalid command-line usage
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("claude-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "output the raw JSON summary from status.claude.com")
	noColor := fs.Bool("no-color", false, "disable colored output")
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 3
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument: %s\n", fs.Arg(0))
		fs.Usage()
		return 3
	}

	c := newColorizer(!*noColor && isTerminal(stdout))
	ec := newColorizer(!*noColor && isTerminal(stderr))

	var s *Summary
	var code int
	if *jsonOut {
		body, err := fetchRaw(summaryURL)
		s = new(Summary)
		if err == nil {
			err = json.Unmarshal(body, s)
		}
		if err == nil {
			_, err = stdout.Write(body)
		}
		if err != nil {
			fmt.Fprintln(stderr, ec.red("error: ")+err.Error())
			return 2
		}
		code = exitCode(s)
	} else {
		type summaryResult struct {
			s   *Summary
			err error
		}
		sumCh := make(chan summaryResult, 1)
		recentCh := make(chan *Incident, 1)

		go func() {
			s, err := fetchSummary(summaryURL)
			sumCh <- summaryResult{s, err}
		}()
		go func() {
			recentCh <- fetchRecentResolved(incidentsURL)
		}()

		res := <-sumCh
		if res.err != nil {
			fmt.Fprintln(stderr, ec.red("✗ unable to reach status.claude.com: ")+res.err.Error())
			return 2
		}
		s = res.s

		var recent *Incident
		if len(s.Incidents) == 0 {
			select {
			case recent = <-recentCh:
			case <-time.After(2 * time.Second):
			}
		}
		code = render(stdout, s, recent, c)
	}
	if code == 2 {
		fmt.Fprintf(stderr, "%sunrecognised status indicator %q\n", ec.red("error: "), s.Status.Indicator)
	}
	return code
}

func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	return httpClient.Do(req)
}

func fetchRaw(url string) ([]byte, error) {
	resp, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func fetchSummary(url string) (*Summary, error) {
	body, err := fetchRaw(url)
	if err != nil {
		return nil, err
	}
	var s Summary
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("parse summary: %w", err)
	}
	return &s, nil
}

func fetchRecentResolved(url string) *Incident {
	body, err := fetchRaw(url)
	if err != nil {
		return nil
	}
	var lst IncidentsList
	if err := json.Unmarshal(body, &lst); err != nil {
		return nil
	}
	var newest *Incident
	for i := range lst.Incidents {
		inc := &lst.Incidents[i]
		if inc.ResolvedAt == nil {
			continue
		}
		if newest == nil || inc.ResolvedAt.After(*newest.ResolvedAt) {
			newest = inc
		}
	}
	return newest
}

// exitCode maps a summary to the process exit code: 0 healthy, 1 degraded or
// active incident, 2 when the overall indicator is missing or unrecognised.
func exitCode(s *Summary) int {
	switch s.Status.Indicator {
	case "none", "maintenance":
	case "minor", "major", "critical":
		return 1
	default:
		return 2
	}
	if len(s.Incidents) > 0 {
		return 1
	}
	for _, comp := range s.Components {
		if comp.Status != "operational" && comp.Status != "under_maintenance" {
			return 1
		}
	}
	return 0
}

// clean makes remote text safe to print on one terminal line: control
// characters (ESC, CR, LF, tab) become spaces and whitespace runs collapse.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func render(w io.Writer, s *Summary, recent *Incident, c *colorizer) int {
	var icon string
	switch s.Status.Indicator {
	case "none":
		icon = c.green("✓")
	case "minor":
		icon = c.yellow("●")
	case "major":
		icon = c.red("⚠")
	case "critical":
		icon = c.red("✗")
	case "maintenance":
		icon = c.blue("⚙")
	default:
		icon = "•"
	}
	name := clean(s.Page.Name)
	if name == "" {
		name = "Claude"
	}
	fmt.Fprintf(w, "%s %s - %s\n", icon, c.bold(name+" Status"), c.bold(clean(s.Status.Description)))
	fmt.Fprintln(w, c.dim(fmt.Sprintf("updated %s · %s", humanAgo(s.Page.UpdatedAt), clean(s.Page.URL))))
	fmt.Fprintln(w)

	// shortcut: pads by rune count, not display width, so wide (CJK) names misalign; upgrade if such components appear.
	maxName := 0
	for _, comp := range s.Components {
		if n := utf8.RuneCountInString(clean(comp.Name)); n > maxName {
			maxName = n
		}
	}
	fmt.Fprintln(w, c.bold("Components"))
	for _, comp := range s.Components {
		dot, label := componentBadge(comp.Status, c)
		name := clean(comp.Name)
		pad := strings.Repeat(" ", maxName-utf8.RuneCountInString(name)+2)
		fmt.Fprintf(w, "  %s %s%s%s\n", dot, name, pad, label)
	}
	fmt.Fprintln(w)

	if len(s.Incidents) > 0 {
		fmt.Fprintln(w, c.bold("Active Incidents"))
		for i := range s.Incidents {
			renderIncident(w, &s.Incidents[i], c, false)
		}
		fmt.Fprintln(w)
	}

	if len(s.ScheduledMaintenances) > 0 {
		fmt.Fprintln(w, c.bold("Scheduled Maintenance"))
		for _, m := range s.ScheduledMaintenances {
			when := ""
			if m.ScheduledFor != nil {
				when = " - " + m.ScheduledFor.Local().Format("Jan 2 15:04 MST")
			}
			fmt.Fprintf(w, "  %s %s%s\n", c.blue("⚙"), clean(m.Name), c.dim(when))
		}
		fmt.Fprintln(w)
	}

	if len(s.Incidents) == 0 && recent != nil {
		fmt.Fprintln(w, c.bold("Most Recent Resolved Incident"))
		renderIncident(w, recent, c, true)
	}

	return exitCode(s)
}

func componentBadge(status string, c *colorizer) (dot, label string) {
	switch status {
	case "operational":
		return c.green("●"), c.green("Operational")
	case "degraded_performance":
		return c.yellow("●"), c.yellow("Degraded Performance")
	case "partial_outage":
		return c.yellow("●"), c.yellow("Partial Outage")
	case "major_outage":
		return c.red("●"), c.red("Major Outage")
	case "under_maintenance":
		return c.blue("●"), c.blue("Under Maintenance")
	default:
		return c.dim("●"), c.dim(clean(status))
	}
}

func renderIncident(w io.Writer, inc *Incident, c *colorizer, resolved bool) {
	var marker string
	switch inc.Impact {
	case "critical":
		marker = c.red("✗")
	case "major":
		marker = c.red("⚠")
	case "minor":
		marker = c.yellow("●")
	case "maintenance":
		marker = c.blue("⚙")
	default:
		marker = c.dim("●")
	}

	statusLabel := strings.ToUpper(clean(inc.Status))
	switch inc.Status {
	case "resolved":
		statusLabel = c.green(statusLabel)
	case "monitoring":
		statusLabel = c.blue(statusLabel)
	case "investigating", "identified":
		statusLabel = c.yellow(statusLabel)
	default:
		statusLabel = c.dim(statusLabel)
	}

	fmt.Fprintf(w, "  %s %s  %s\n", marker, c.bold(clean(inc.Name)), statusLabel)

	if resolved && inc.ResolvedAt != nil {
		fmt.Fprintf(w, "    %s\n", c.dim("resolved "+humanAgo(*inc.ResolvedAt)))
	} else if inc.StartedAt != nil {
		fmt.Fprintf(w, "    %s\n", c.dim("started "+humanAgo(*inc.StartedAt)))
	}

	if len(inc.IncidentUpdates) > 0 {
		body := clean(inc.IncidentUpdates[0].Body)
		if r := []rune(body); len(r) > 220 {
			body = string(r[:217]) + "..."
		}
		fmt.Fprintf(w, "    %s %s\n", c.dim("latest:"), body)
	}
	if link := clean(inc.Shortlink); link != "" {
		fmt.Fprintf(w, "    %s\n", c.dim(link))
	}
}

func humanAgo(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return t.Local().Format("Jan 2 15:04 MST")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm ago", int(d.Hours()), int(d.Minutes())%60)
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	default:
		return t.Local().Format("Jan 2 2006")
	}
}

type colorizer struct{ on bool }

func newColorizer(on bool) *colorizer { return &colorizer{on: on} }

func (c *colorizer) wrap(code, s string) string {
	if !c.on {
		return s
	}
	return code + s + cReset
}
func (c *colorizer) red(s string) string    { return c.wrap(cRed, s) }
func (c *colorizer) green(s string) string  { return c.wrap(cGreen, s) }
func (c *colorizer) yellow(s string) string { return c.wrap(cYellow, s) }
func (c *colorizer) blue(s string) string   { return c.wrap(cBlue, s) }
func (c *colorizer) dim(s string) string    { return c.wrap(cGray, s) }
func (c *colorizer) bold(s string) string   { return c.wrap(cBold, s) }

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
