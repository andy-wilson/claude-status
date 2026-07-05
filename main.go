package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	summaryURL   = "https://status.claude.com/api/v2/summary.json"
	incidentsURL = "https://status.claude.com/api/v2/incidents.json"
	httpTimeout  = 5 * time.Second
	userAgent    = "claude-status/0.1 (+https://status.claude.com)"
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
	Status    string    `json:"status"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
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
	Status       string     `json:"status"`
	Impact       string     `json:"impact"`
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

func main() {
	jsonOut := flag.Bool("json", false, "output the raw JSON summary from status.claude.com")
	noColor := flag.Bool("no-color", false, "disable colored output")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `claude-status — check Anthropic / Claude service status

Usage:
  claude-status [flags]

Flags:
  --json        output the raw JSON summary from status.claude.com
  --no-color    disable ANSI color output (auto-disabled when stdout isn't a TTY)
  -h, --help    show this help

Exit codes:
  0  all systems operational
  1  one or more components degraded, or an active incident
  2  unable to reach the status page
`)
	}
	flag.Parse()

	c := newColorizer(!*noColor && isTerminal(os.Stdout))

	if *jsonOut {
		body, err := fetchRaw(summaryURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, c.red("error: ")+err.Error())
			os.Exit(2)
		}
		os.Stdout.Write(body)
		return
	}

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
		fmt.Fprintln(os.Stderr, c.red("✗ unable to reach status.claude.com: ")+res.err.Error())
		os.Exit(2)
	}

	var recent *Incident
	if len(res.s.Incidents) == 0 {
		select {
		case recent = <-recentCh:
		case <-time.After(2 * time.Second):
		}
	}
	os.Exit(render(res.s, recent, c))
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

func render(s *Summary, recent *Incident, c *colorizer) int {
	exit := 0

	var icon, name string
	switch s.Status.Indicator {
	case "none":
		icon = c.green("✓")
	case "minor":
		icon = c.yellow("●")
		exit = 1
	case "major":
		icon = c.red("⚠")
		exit = 1
	case "critical":
		icon = c.red("✗")
		exit = 1
	case "maintenance":
		icon = c.blue("⚙")
	default:
		icon = "•"
	}
	name = s.Page.Name
	if name == "" {
		name = "Claude"
	}
	fmt.Printf("%s %s — %s\n", icon, c.bold(name+" Status"), c.bold(s.Status.Description))
	fmt.Println(c.dim(fmt.Sprintf("updated %s · %s", humanAgo(s.Page.UpdatedAt), s.Page.URL)))
	fmt.Println()

	maxName := 0
	for _, comp := range s.Components {
		if len(comp.Name) > maxName {
			maxName = len(comp.Name)
		}
	}
	fmt.Println(c.bold("Components"))
	for _, comp := range s.Components {
		dot, label := componentBadge(comp.Status, c)
		pad := strings.Repeat(" ", maxName-len(comp.Name)+2)
		fmt.Printf("  %s %s%s%s\n", dot, comp.Name, pad, label)
		if comp.Status != "operational" && comp.Status != "under_maintenance" {
			exit = 1
		}
	}
	fmt.Println()

	if len(s.Incidents) > 0 {
		exit = 1
		fmt.Println(c.bold("Active Incidents"))
		for i := range s.Incidents {
			renderIncident(&s.Incidents[i], c, false)
		}
		fmt.Println()
	}

	if len(s.ScheduledMaintenances) > 0 {
		fmt.Println(c.bold("Scheduled Maintenance"))
		for _, m := range s.ScheduledMaintenances {
			when := ""
			if m.ScheduledFor != nil {
				when = " — " + m.ScheduledFor.Local().Format("Jan 2 15:04 MST")
			}
			fmt.Printf("  %s %s%s\n", c.blue("⚙"), m.Name, c.dim(when))
		}
		fmt.Println()
	}

	if len(s.Incidents) == 0 && recent != nil {
		fmt.Println(c.bold("Most Recent Resolved Incident"))
		renderIncident(recent, c, true)
	}

	return exit
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
		return c.dim("●"), c.dim(status)
	}
}

func renderIncident(inc *Incident, c *colorizer, resolved bool) {
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

	statusLabel := strings.ToUpper(inc.Status)
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

	fmt.Printf("  %s %s  %s\n", marker, c.bold(inc.Name), statusLabel)

	if resolved && inc.ResolvedAt != nil {
		fmt.Printf("    %s\n", c.dim("resolved "+humanAgo(*inc.ResolvedAt)))
	} else if inc.StartedAt != nil {
		fmt.Printf("    %s\n", c.dim("started "+humanAgo(*inc.StartedAt)))
	}

	if len(inc.IncidentUpdates) > 0 {
		latest := inc.IncidentUpdates[0]
		body := strings.TrimSpace(strings.ReplaceAll(latest.Body, "\n", " "))
		if len(body) > 220 {
			body = body[:217] + "..."
		}
		fmt.Printf("    %s %s\n", c.dim("latest:"), body)
	}
	if inc.Shortlink != "" {
		fmt.Printf("    %s\n", c.dim(inc.Shortlink))
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

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
