package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
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
	var exit int
	out := captureStdout(t, func() { exit = render(s, nil, noColor()) })
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
	var exit int
	captureStdout(t, func() { exit = render(s, nil, noColor()) })
	if exit != 1 {
		t.Errorf("exit = %d, want 1 for degraded component", exit)
	}
}

func TestRenderIndicatorExitCodes(t *testing.T) {
	for indicator, want := range map[string]int{
		"none": 0, "minor": 1, "major": 1, "critical": 1, "maintenance": 0, "unknown": 0,
	} {
		s := &Summary{Status: StatusIndicator{Indicator: indicator}}
		var exit int
		captureStdout(t, func() { exit = render(s, nil, noColor()) })
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
				{Status: "investigating", Body: "We are looking into it."},
			},
		}},
	}
	var exit int
	out := captureStdout(t, func() { exit = render(s, nil, noColor()) })
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
			{Name: "DB upgrade", Status: "scheduled", Impact: "maintenance", ScheduledFor: &when},
		},
	}
	var exit int
	out := captureStdout(t, func() { exit = render(s, nil, noColor()) })
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
	out := captureStdout(t, func() { render(quiet, recent, noColor()) })
	if !strings.Contains(out, "Most Recent Resolved Incident") || !strings.Contains(out, "Old incident") {
		t.Errorf("output missing recent resolved incident:\n%s", out)
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
	out := captureStdout(t, func() { renderIncident(inc, noColor(), false) })
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
