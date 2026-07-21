// Package dashboard serves a read-only localhost HTTP dashboard alongside the
// enola MCP server. It binds a free ephemeral port on 127.0.0.1 and renders —
// refreshing every 30 seconds — the same activity data as --status plus the
// contents of the current-snapshot and graph-wide receipt.json files, so a user
// can visually inspect what the snapshot captured.
//
// The dashboard is strictly a viewer: every request only reads through existing
// concurrency-safe accessors (the engine's published snapshot, the status usage
// files) and never mutates server state. All logging goes to stderr; stdout is
// reserved for the MCP stdio protocol.
//
// It is a public package so a wrapper binary can add panels of its own without
// forking the page: see Options for the template-overlay and insight-label
// extension points.
package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/facts"
	"github.com/enola-labs/enola/pkg/status"
)

// refreshSeconds is the client-side auto-refresh interval, stated on the page.
const refreshSeconds = 30

//go:embed page.html.tmpl
var pageTemplate string

var baseTmpl = template.Must(template.New("dashboard").Parse(pageTemplate))

// OverlayBlocks names the template blocks an Options.Overlay may redefine, in
// page order. They are the stable half of the overlay contract — renaming one
// silently drops a wrapper's panel — so a wrapper can assert its fragment
// defines only these, and TestOverlayBlocksExistInPage keeps the page honest.
func OverlayBlocks() []string {
	return []string{"extra-styles", "extra-cards", "extra-modals", "extra-scripts"}
}

// Options configures a dashboard, and is the whole extension surface a wrapper
// binary has. A zero Options is the plain engine dashboard.
type Options struct {
	// Overlay is a template fragment that redefines any of the page's extension
	// blocks: "extra-styles" (inside <style>), "extra-cards" (end of the summary
	// card grid), "extra-modals" (after the last modal) and "extra-scripts"
	// (inside the trailing <script>). Each is empty by default.
	//
	// A fragment renders against the same page model as the rest of the template,
	// so it reaches its own data through {{.Extra}} and may reuse the page's CSS
	// classes — .card, .modal, .modal-panel, .count-link and .insight-summary are
	// the stable ones.
	Overlay string

	// Extra computes the data the overlay blocks render, once per request from
	// the live fact store, and is exposed to the template as {{.Extra}}. Leaving
	// it nil — or returning nil, e.g. for an unlicensed feature — renders the
	// blocks with no data, which a fragment guarded by {{if .Extra}} skips.
	Extra func(*facts.Store) any

	// InsightLabels adds explainer-id → display-label entries to the page's own
	// map. That map is also the admission list (see insightDetails), so a wrapper
	// that registers extra explainers MUST list them here or their insights are
	// filtered out of its own dashboard.
	InsightLabels map[string]string

	// Title names the product in the page title, heading and footer. Defaults to
	// defaultTitle.
	Title string
}

// defaultTitle is the product name shown when Options.Title is empty.
const defaultTitle = "enola"

// engineView is the slice of the engine the dashboard depends on. Narrowing to
// an interface keeps the handler testable without constructing a full engine.
// *bootstrap.Engine satisfies it.
//
//   - GetArtifact fetches the live in-memory receipt ("receipt.json").
//   - ActiveRepo + OutputDir locate the last-written receipt on disk, used as a
//     fallback: AutoLoadSnapshot restores a snapshot's facts with an near-empty
//     Meta, so after a server restart the in-memory receipt is blank while a full
//     one persists at <repo>/.enola/receipt.json.
type engineView interface {
	GetArtifact(name string) ([]byte, error)
	ActiveRepo() string
	OutputDir(repoPath string) string
	// Store exposes the live fact store, from which the dashboard enumerates the
	// service and cross-repo-edge lists behind the graph-receipt counters (the
	// receipt itself stores only the counts). Reads are concurrency-safe.
	Store() *facts.Store
}

// Server is a running dashboard HTTP server bound to a loopback port.
type Server struct {
	port   int
	eng    engineView
	opts   Options
	tmpl   *template.Template
	labels map[string]string // insight-source allowlist + display labels
	title  string            // product name in the page title/heading/footer
}

// Start binds a free ephemeral port on 127.0.0.1, serves the dashboard from a
// background goroutine, and returns immediately. A serve error after startup is
// logged to stderr and never propagated — the MCP server must keep running.
//
// An invalid Options.Overlay is reported here rather than at render time, so a
// wrapper's template mistake fails loudly at startup instead of silently
// dropping its panels from every page.
func Start(eng *bootstrap.Engine, opts Options) (*Server, error) {
	tmpl, err := buildTemplate(opts.Overlay)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("binding dashboard port: %w", err)
	}
	s := &Server{
		port:   ln.Addr().(*net.TCPAddr).Port,
		eng:    eng,
		opts:   opts,
		tmpl:   tmpl,
		labels: mergedLabels(opts.InsightLabels),
		title:  titleOr(opts.Title),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)

	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("dashboard: serve stopped: %v", err)
		}
	}()

	return s, nil
}

// Port returns the loopback port the dashboard is listening on.
func (s *Server) Port() int { return s.port }

// URL returns the dashboard's localhost URL.
func (s *Server) URL() string { return fmt.Sprintf("http://127.0.0.1:%d", s.port) }

// handleIndex gathers live data on each request (so the periodic reload shows
// fresh numbers) and renders the page. Only the root path is served.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := s.buildPage()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, data); err != nil {
		log.Printf("dashboard: render failed: %v", err)
	}
}

// titleOr returns the configured product name, or the default when unset.
func titleOr(title string) string {
	if title == "" {
		return defaultTitle
	}
	return title
}

// buildTemplate returns the page template for one server, with an optional
// overlay fragment applied over it. Parsing a fragment onto a clone redefines
// whichever extension blocks it declares and leaves the rest of the page
// untouched.
//
// Every server gets its own clone, even without an overlay: html/template
// refuses to Clone a template that has already executed, so serving a page
// straight from baseTmpl would make the NEXT server's Clone fail. Keeping
// baseTmpl un-executed is what lets a wrapper start a dashboard after a plain
// one in the same process.
func buildTemplate(overlay string) (*template.Template, error) {
	t, err := baseTmpl.Clone()
	if err != nil {
		return nil, fmt.Errorf("cloning dashboard template: %w", err)
	}
	if overlay == "" {
		return t, nil
	}
	if t, err = t.Parse(overlay); err != nil {
		return nil, fmt.Errorf("parsing dashboard overlay: %w", err)
	}
	return t, nil
}

// toolRow is one row of the tool-usage table (session vs lifetime total).
type toolRow struct {
	Name    string
	Session int
	Total   int
}

// valueRow is one row of the value-estimate table (pre-formatted for display).
type valueRow struct {
	Label       string
	Calls       int
	TimeSaved   string
	TokensSaved string
}

// pageData is the full template model.
type pageData struct {
	RefreshSeconds int
	Title          string

	Running       bool
	PID           int
	Port          int
	Uptime        string
	StartedAt     string
	TrackingSince string
	ReposTracked  int

	Tools      []toolRow
	Values     []valueRow
	ValueTotal valueRow

	HasReceipt  bool
	Receipt     *facts.Receipt
	ReceiptNote string

	HasGraph  bool
	Graph     *facts.GraphReceipt
	GraphNote string

	// Services and CrossRepoEdges are enumerated from the live fact store (not the
	// receipt, which holds only counts) to back the clickable graph-receipt cards.
	Services       []serviceRow
	CrossRepoEdges []edgeRow
	// EdgeDiagram is the node-link layout of CrossRepoEdges for the diagram view of
	// the edges modal; nil when there are no edges (the modal shows the table only).
	EdgeDiagram *diagramView

	// Insights (grouped by explainer) back the clickable Insights card; the
	// structural/candidate split is shown in the modal header.
	Insights          []insightGroup
	InsightStructural int
	InsightCandidate  int
	InsightTotal      int // structural + candidate; initial "shown" count for the modal filter

	// Extraction-quality proof data: per-service coverage and the unmatched-route
	// list (from the live store), plus the capped skip/parse-error samples (from the
	// receipt) that back the clickable Extraction-quality cards.
	Coverage         []coverageRow
	UnresolvedRoutes []routeRow
	SkippedSample    []string
	ParseErrors      []facts.ParseError

	// Extra is whatever Options.Extra returned for this request — the data the
	// overlay blocks render. Nil in a plain engine dashboard, and nil whenever a
	// wrapper declines to supply it (e.g. an unlicensed feature), which is what a
	// fragment guarded by {{if .Extra}} keys off.
	Extra any
}

// buildPage collects the status, current-snapshot receipt and graph-wide receipt
// into the template model. Every source degrades gracefully to a note on error.
func (s *Server) buildPage() pageData {
	data := pageData{
		RefreshSeconds: refreshSeconds,
		Title:          s.title,
		Port:           s.port,
	}

	ss := status.ServerSnapshot()
	if ss.Found {
		data.Running = ss.Alive
		data.PID = ss.PID
		data.ReposTracked = ss.Repos
		if !ss.StartTime.IsZero() {
			data.StartedAt = ss.StartTime.Format("2006-01-02 15:04:05")
			if ss.Alive {
				data.Uptime = formatDuration(time.Since(ss.StartTime))
			}
		}
		if !ss.TrackingSince.IsZero() {
			data.TrackingSince = ss.TrackingSince.Format("2006-01-02 15:04:05")
		}
		data.Tools = toolRows(ss.GrandTotal, ss.Session)
		data.Values, data.ValueTotal = valueRows(ss.GrandTotal)
	}

	// Current-snapshot receipt: prefer the live in-memory receipt, falling back
	// to the last-written one on disk (see currentReceipt).
	if rv := s.currentReceipt(); rv != nil {
		data.HasReceipt = true
		data.Receipt = rv
		// Capped skip/parse-error samples back the clickable Extraction-quality cards.
		data.SkippedSample = rv.Quality.SkippedSample
		data.ParseErrors = rv.Quality.ParseErrorSample
	} else {
		data.ReceiptNote = "No snapshot loaded yet — run generate_snapshot to populate this."
	}

	// Graph-wide receipt: ~/.enola/receipt.json on disk.
	if gv, err := readGraphReceipt(); err != nil {
		data.GraphNote = "No graph receipt yet — it is written to ~/.enola/receipt.json when a snapshot is generated."
	} else {
		data.HasGraph = true
		data.Graph = gv
	}

	// Service and cross-repo-edge lists from the live store, backing the clickable
	// counters. Empty (store not loaded) → the cards render as plain numbers.
	data.Services, data.CrossRepoEdges = graphDetails(s.eng.Store())
	data.EdgeDiagram = buildEdgeDiagram(data.Services, data.CrossRepoEdges)

	// Insight list (grouped by explainer) backing the clickable Insights counter.
	// Empty → the counter renders as a plain number.
	data.Insights, data.InsightStructural, data.InsightCandidate = insightDetails(s.currentInsights(), s.labels)
	data.InsightTotal = data.InsightStructural + data.InsightCandidate

	// Cross-repo coverage + unmatched routes from the live store, backing the
	// clickable Extraction-quality coverage cards.
	data.Coverage, data.UnresolvedRoutes = coverageDetails(s.eng.Store())

	// Whatever a wrapper's overlay blocks render, recomputed per request from the
	// same live store as everything above.
	if s.opts.Extra != nil {
		data.Extra = s.opts.Extra(s.eng.Store())
	}

	return data
}

// currentReceipt returns the receipt to display for the current snapshot, or nil
// if none is available. It prefers the live in-memory receipt (fresh after a
// generate_snapshot), but falls back to the last-written receipt on disk when the
// in-memory one is missing or blank — the common case after a server restart,
// where AutoLoadSnapshot restores facts without full receipt metadata.
func (s *Server) currentReceipt() *facts.Receipt {
	if b, err := s.eng.GetArtifact("receipt.json"); err == nil {
		var rv facts.Receipt
		if err := json.Unmarshal(b, &rv); err == nil && rv.SnapshotID != "" {
			return &rv
		}
	}

	repo := s.eng.ActiveRepo()
	if repo == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.eng.OutputDir(repo), "receipt.json"))
	if err != nil {
		return nil
	}
	var rv facts.Receipt
	if err := json.Unmarshal(b, &rv); err != nil {
		return nil
	}
	return &rv
}

// currentInsights returns the insight list for the current snapshot, or nil. Like
// currentReceipt it prefers the live in-memory artifact (fresh after generate) but
// falls back to the last-written insights.json on disk — AutoLoadSnapshot restores
// facts without the snapshot's insights, so after a server restart the in-memory
// list is empty while a full one persists at <repo>/.enola/insights.json.
func (s *Server) currentInsights() []facts.Insight {
	if b, err := s.eng.GetArtifact("insights.json"); err == nil {
		var ins []facts.Insight
		if err := json.Unmarshal(b, &ins); err == nil && len(ins) > 0 {
			return ins
		}
	}

	repo := s.eng.ActiveRepo()
	if repo == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.eng.OutputDir(repo), "insights.json"))
	if err != nil {
		return nil
	}
	var ins []facts.Insight
	if err := json.Unmarshal(b, &ins); err != nil {
		return nil
	}
	return ins
}

// readGraphReceipt reads and parses the graph-wide receipt at ~/.enola/receipt.json.
func readGraphReceipt() (*facts.GraphReceipt, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".enola", "receipt.json"))
	if err != nil {
		return nil, err
	}
	var gv facts.GraphReceipt
	if err := json.Unmarshal(b, &gv); err != nil {
		return nil, err
	}
	return &gv, nil
}

// toolRows builds the sorted union of tool-usage rows (session and lifetime).
func toolRows(total, session map[string]int) []toolRow {
	set := make(map[string]struct{}, len(total)+len(session))
	for k := range total {
		set[k] = struct{}{}
	}
	for k := range session {
		set[k] = struct{}{}
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)

	rows := make([]toolRow, 0, len(names))
	for _, n := range names {
		rows = append(rows, toolRow{Name: n, Session: session[n], Total: total[n]})
	}
	return rows
}

// valueRows builds the per-tool value-estimate rows plus the total row, reusing
// the shared status value model so the numbers match --status exactly.
func valueRows(total map[string]int) ([]valueRow, valueRow) {
	rep := status.ComputeValue(total)
	rows := make([]valueRow, 0, len(rep.Tools))
	for _, tv := range rep.Tools {
		rows = append(rows, valueRow{
			Label:       tv.Tool,
			Calls:       tv.Calls,
			TimeSaved:   formatDuration(tv.TimeSaved),
			TokensSaved: humanCount(tv.TokensSaved),
		})
	}
	totalRow := valueRow{
		Label:       "TOTAL",
		Calls:       rep.TotalCalls,
		TimeSaved:   formatDuration(rep.TotalTimeSaved),
		TokensSaved: humanCount(rep.TotalTokensSaved),
	}
	return rows, totalRow
}
