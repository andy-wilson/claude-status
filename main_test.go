package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const summaryFixture = `{
	"page": {
		"name": "Claude",
		"url": "https://status.claude.com",
		"updated_at": "2026-07-05T21:38:56.928Z"
	},
	"status": {"indicator": "none", "description": "All Systems Operational"},
	"components": [
		{"name": "claude.ai", "status": "operational"},
		{"name": "Claude API (api.anthropic.com)", "status": "operational"}
	],
	"incidents": [],
	"scheduled_maintenances": []
}`

func noColor() *colorizer { return newColorizer(false) }

func renderOut(s *Summary, recent *Incident) (string, int) {
	var b bytes.Buffer
	exit := render(&b, s, recent, noColor())
	return b.String(), exit
}

func serveJSON(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchSummary(t *testing.T) {
	srv := serveJSON(t, http.StatusOK, summaryFixture)
	s, err := fetchSummary(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Page.Name != "Claude" {
		t.Errorf("page name = %q, want Claude", s.Page.Name)
	}
	if s.Status.Indicator != "none" {
		t.Errorf("indicator = %q, want none", s.Status.Indicator)
	}
	if len(s.Components) != 2 {
		t.Errorf("components = %d, want 2", len(s.Components))
	}
}

func TestFetchSummaryHTTPError(t *testing.T) {
	srv := serveJSON(t, http.StatusInternalServerError, "boom")
	if _, err := fetchSummary(srv.URL); err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
}

func TestFetchSummaryBadJSON(t *testing.T) {
	srv := serveJSON(t, http.StatusOK, "not json")
	if _, err := fetchSummary(srv.URL); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestFetchSummaryUnreachable(t *testing.T) {
	if _, err := fetchSummary("http://127.0.0.1:1"); err == nil {
		t.Fatal("expected error on unreachable server, got nil")
	}
}

func TestFetchRecentResolved(t *testing.T) {
	srv := serveJSON(t, http.StatusOK, `{"incidents": [
		{"name": "older", "status": "resolved", "resolved_at": "2026-06-01T00:00:00Z"},
		{"name": "unresolved", "status": "investigating", "resolved_at": null},
		{"name": "newest", "status": "resolved", "resolved_at": "2026-07-01T00:00:00Z"}
	]}`)
	inc := fetchRecentResolved(srv.URL)
	if inc == nil {
		t.Fatal("expected an incident, got nil")
	}
	if inc.Name != "newest" {
		t.Errorf("picked %q, want newest", inc.Name)
	}
}

func TestFetchRecentResolvedFailuresReturnNil(t *testing.T) {
	if inc := fetchRecentResolved("http://127.0.0.1:1"); inc != nil {
		t.Errorf("unreachable server: got %v, want nil", inc)
	}
	srv := serveJSON(t, http.StatusOK, "not json")
	if inc := fetchRecentResolved(srv.URL); inc != nil {
		t.Errorf("bad JSON: got %v, want nil", inc)
	}
	empty := serveJSON(t, http.StatusOK, `{"incidents": []}`)
	if inc := fetchRecentResolved(empty.URL); inc != nil {
		t.Errorf("no incidents: got %v, want nil", inc)
	}
}

func TestRenderAllOperational(t *testing.T) {
	s := &Summary{
		Status: StatusIndicator{Indicator: "none", Description: "All Systems Operational"},
		Components: []Component{
			{Name: "claude.ai", Status: "operational"},
		},
	}
	s.Page.Name = "Claude"
	out, exit := renderOut(s, nil)
	if exit != 0 {
		t.Errorf("exit = %d, want 0", exit)
	}
	if !strings.Contains(out, "All Systems Operational") {
		t.Errorf("output missing status description:\n%s", out)
	}
	if !strings.Contains(out, "claude.ai") {
		t.Errorf("output missing component name:\n%s", out)
	}
}

func TestRenderDegradedComponent(t *testing.T) {
	s := &Summary{
		Status: StatusIndicator{Indicator: "none", Description: "All Systems Operational"},
		Components: []Component{
			{Name: "claude.ai", Status: "degraded_performance"},
		},
	}
	_, exit := renderOut(s, nil)
	if exit != 1 {
		t.Errorf("exit = %d, want 1 for degraded component", exit)
	}
}

func TestRenderIndicatorExitCodes(t *testing.T) {
	for indicator, want := range map[string]int{
		"none": 0, "minor": 1, "major": 1, "critical": 1, "maintenance": 0, "unknown": 2, "": 2,
	} {
		s := &Summary{Status: StatusIndicator{Indicator: indicator}}
		_, exit := renderOut(s, nil)
		if exit != want {
			t.Errorf("indicator %q: exit = %d, want %d", indicator, exit, want)
		}
	}
}

func TestRenderActiveIncident(t *testing.T) {
	started := time.Now().Add(-30 * time.Minute)
	s := &Summary{
		Status: StatusIndicator{Indicator: "minor", Description: "Partial Outage"},
		Incidents: []Incident{{
			Name:      "Elevated errors",
			Status:    "investigating",
			Impact:    "minor",
			Shortlink: "https://stspg.io/abc",
			StartedAt: &started,
			IncidentUpdates: []IncidentUpdate{
				{Body: "We are looking into it."},
			},
		}},
	}
	out, exit := renderOut(s, nil)
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	for _, want := range []string{"Active Incidents", "Elevated errors", "INVESTIGATING", "We are looking into it.", "https://stspg.io/abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderScheduledMaintenance(t *testing.T) {
	when := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	s := &Summary{
		Status: StatusIndicator{Indicator: "none"},
		ScheduledMaintenances: []ScheduledMaintenance{
			{Name: "DB upgrade", ScheduledFor: &when},
		},
	}
	out, exit := renderOut(s, nil)
	if exit != 0 {
		t.Errorf("exit = %d, want 0", exit)
	}
	if !strings.Contains(out, "Scheduled Maintenance") || !strings.Contains(out, "DB upgrade") {
		t.Errorf("output missing maintenance section:\n%s", out)
	}
}

func TestRenderRecentResolvedShownOnlyWithoutActiveIncidents(t *testing.T) {
	resolved := time.Now().Add(-24 * time.Hour)
	recent := &Incident{Name: "Old incident", Status: "resolved", ResolvedAt: &resolved}

	quiet := &Summary{Status: StatusIndicator{Indicator: "none"}}
	out, _ := renderOut(quiet, recent)
	if !strings.Contains(out, "Most Recent Resolved Incident") || !strings.Contains(out, "Old incident") {
		t.Errorf("output missing recent resolved incident:\n%s", out)
	}

	busy := &Summary{Status: StatusIndicator{Indicator: "minor"}, Incidents: []Incident{{Name: "Live incident"}}}
	out, _ = renderOut(busy, recent)
	if strings.Contains(out, "Most Recent Resolved Incident") || strings.Contains(out, "Old incident") {
		t.Errorf("resolved incident shown alongside active incident:\n%s", out)
	}
}

func TestRenderIncidentTruncatesLongBody(t *testing.T) {
	inc := &Incident{
		Name:   "Big incident",
		Status: "resolved",
		IncidentUpdates: []IncidentUpdate{
			{Body: strings.Repeat("x", 300)},
		},
	}
	var b bytes.Buffer
	renderIncident(&b, inc, noColor(), false)
	out := b.String()
	if !strings.Contains(out, strings.Repeat("x", 217)+"...") {
		t.Errorf("long body not truncated:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("x", 218)) {
		t.Errorf("body longer than truncation limit:\n%s", out)
	}
}

func TestComponentBadge(t *testing.T) {
	c := noColor()
	for status, wantLabel := range map[string]string{
		"operational":          "Operational",
		"degraded_performance": "Degraded Performance",
		"partial_outage":       "Partial Outage",
		"major_outage":         "Major Outage",
		"under_maintenance":    "Under Maintenance",
		"something_new":        "something_new",
	} {
		_, label := componentBadge(status, c)
		if label != wantLabel {
			t.Errorf("componentBadge(%q) label = %q, want %q", status, label, wantLabel)
		}
	}
}

func TestHumanAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, "unknown"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3*time.Hour - 15*time.Minute), "3h15m ago"},
		{now.Add(-72 * time.Hour), "3d ago"},
	}
	for _, tt := range tests {
		if got := humanAgo(tt.t); got != tt.want {
			t.Errorf("humanAgo(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}

	old := now.Add(-90 * 24 * time.Hour)
	if got := humanAgo(old); got != old.Local().Format("Jan 2 2006") {
		t.Errorf("humanAgo(90d ago) = %q, want date format", got)
	}
	future := now.Add(time.Hour)
	if got := humanAgo(future); got != future.Local().Format("Jan 2 15:04 MST") {
		t.Errorf("humanAgo(future) = %q, want timestamp format", got)
	}
}

func TestColorizer(t *testing.T) {
	on := newColorizer(true)
	if got := on.red("x"); got != cRed+"x"+cReset {
		t.Errorf("colorizer on: got %q", got)
	}
	off := newColorizer(false)
	if got := off.red("x"); got != "x" {
		t.Errorf("colorizer off: got %q, want plain", got)
	}
}

func TestRenderIncidentTruncatesOnRuneBoundary(t *testing.T) {
	inc := &Incident{IncidentUpdates: []IncidentUpdate{{Body: strings.Repeat("é", 300)}}}
	var b bytes.Buffer
	renderIncident(&b, inc, noColor(), false)
	out := b.String()
	if !utf8.ValidString(out) {
		t.Errorf("truncated output is not valid UTF-8:\n%q", out)
	}
	if !strings.Contains(out, " "+strings.Repeat("é", 217)+"...") {
		t.Errorf("body not truncated to 217 characters:\n%s", out)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"line one\r\nline two": "line one line two",
		"tab\there":            "tab here",
		"\x1b[31mred\x1b[0m":   "[31mred [0m",
		"  plain text  ":       "plain text",
		"unicode – stays é ✓":  "unicode – stays é ✓",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderStripsControlCharacters(t *testing.T) {
	s := &Summary{
		Status:     StatusIndicator{Indicator: "minor", Description: "bad\x1b[2Jdesc"},
		Components: []Component{{Name: "comp\rname", Status: "operational"}},
		Incidents: []Incident{{
			Name:            "inc\x1bname",
			IncidentUpdates: []IncidentUpdate{{Body: "first\r\nsecond"}},
		}},
	}
	out, _ := renderOut(s, nil)
	if strings.ContainsAny(out, "\r\x1b") {
		t.Errorf("control characters reached output:\n%q", out)
	}
	if !strings.Contains(out, "first second") {
		t.Errorf("CRLF not collapsed to a space:\n%q", out)
	}
}

func TestRenderPadsByCharacterNotByte(t *testing.T) {
	s := &Summary{
		Status: StatusIndicator{Indicator: "none"},
		Components: []Component{
			{Name: "aé", Status: "operational"},
			{Name: "abc", Status: "operational"},
		},
	}
	out, _ := renderOut(s, nil)
	for _, want := range []string{"● aé   Operational", "● abc  Operational"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestExitCodeUnknownIndicatorWins(t *testing.T) {
	s := &Summary{Incidents: []Incident{{Name: "x"}}}
	if got := exitCode(s); got != 2 {
		t.Errorf("exitCode = %d, want 2 for missing indicator with active incident", got)
	}
}

// runWith points the tool at local servers and runs it with args.
func runWith(t *testing.T, summaryStatus int, summaryBody string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	oldS, oldI := summaryURL, incidentsURL
	t.Cleanup(func() { summaryURL, incidentsURL = oldS, oldI })
	summaryURL = serveJSON(t, summaryStatus, summaryBody).URL
	incidentsURL = serveJSON(t, http.StatusOK, `{"incidents": []}`).URL
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestRun(t *testing.T) {
	const major = `{"status": {"indicator": "major", "description": "Major Outage"}}`
	tests := []struct {
		name       string
		status     int
		body       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"healthy", 200, summaryFixture, nil, 0, "All Systems Operational", ""},
		{"degraded", 200, major, nil, 1, "Major Outage", ""},
		{"wrong shape", 200, `{}`, nil, 2, "", "unrecognised status indicator"},
		{"http error", 500, "boom", nil, 2, "", "HTTP 500"},
		{"json healthy", 200, summaryFixture, []string{"--json"}, 0, summaryFixture, ""},
		{"json degraded", 200, major, []string{"--json"}, 1, major, ""},
		{"json wrong shape", 200, `{}`, []string{"--json"}, 2, "", "unrecognised status indicator"},
		{"json not json", 200, "<html>", []string{"--json"}, 2, "", "error: "},
		{"json http error", 500, "boom", []string{"--json"}, 2, "", "HTTP 500"},
		{"bad flag", 200, summaryFixture, []string{"--nocolor"}, 3, "", "Usage:"},
		{"positional arg", 200, summaryFixture, []string{"json"}, 3, "", "unexpected argument: json"},
		{"help", 200, summaryFixture, []string{"-h"}, 0, "", "Usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := runWith(t, tt.status, tt.body, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
			if tt.name == "json not json" && stdout != "" {
				t.Errorf("non-JSON body was written to stdout: %q", stdout)
			}
		})
	}
}
