package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/dashboard"
	"github.com/enola-labs/enola/pkg/status"
)

// Dashboard restores the latest snapshot and serves its read-only dashboard
// without also starting an MCP stdio server. It remains in the foreground so
// Ctrl-C has the unsurprising effect of stopping the listener.
func (r *Runner) Dashboard(ctx context.Context, args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			r.dashboardStatus()
			return
		case "stop":
			r.stopDashboards()
			return
		}
	}
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	open := fs.Bool("open", false, "open the dashboard in the default browser")
	foreground := fs.Bool("foreground", false, "stay attached to this terminal until Ctrl-C")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s dashboard [--open] [--foreground] [repo_path|config_path]\n", r.name())
		fmt.Fprintf(os.Stderr, "       %s dashboard <status|stop>\n\n", r.name())
		fmt.Fprintln(os.Stderr, "Explore an existing architecture snapshot in a read-only local web dashboard.")
		fmt.Fprintln(os.Stderr, "Nothing is regenerated automatically; run `"+r.name()+" --generate` first when no snapshot exists.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  --open         launch the dashboard in the default browser")
		fmt.Fprintln(os.Stderr, "  --foreground   stay attached to this terminal; stop with Ctrl-C")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Lifecycle:")
		fmt.Fprintln(os.Stderr, "  dashboard status   list every dashboard, including dashboards hosted by MCP servers")
		fmt.Fprintln(os.Stderr, "  dashboard stop     stop standalone dashboards (MCP servers are never stopped)")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "The server binds to 127.0.0.1 and prints a complete http:// URL.")
		fmt.Fprintln(os.Stderr, "If Safari blocks local HTTP in HTTPS-Only mode, allow this local address or use another browser.")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		r.dashboardFatal("dashboard: %v", err)
	}
	if fs.NArg() > 1 {
		r.dashboardFatal("dashboard accepts at most one repository or config path")
	}
	arg := ""
	if fs.NArg() == 1 {
		arg = fs.Arg(0)
		if _, err := os.Stat(arg); err != nil {
			r.dashboardFatal("cannot use target %q: %v", arg, err)
		}
	}

	// Validate in the parent before detaching. Otherwise the child has no stderr,
	// and a useful "generate first" error degrades into "did not become ready".
	t := r.resolveTarget(arg)
	if restored := bootstrap.AutoLoadSnapshot(t.engine, t.engine.Config()); restored == nil {
		r.missingDashboardSnapshot(t.configNote, arg)
	}
	snapshotDir := t.engine.OutputDir(t.repoPaths[0])
	if !*foreground {
		r.startDashboardDetached(*open, arg, snapshotDir)
		return
	}

	// A dashboard-only process is still an active Enola session. Register it just
	// like the MCP startup path does so Activity does not claim zero sessions while
	// the process serving that very page is running. No tool callback is installed:
	// this process serves no MCP calls, so its session count truthfully stays zero.
	tracker := status.NewTracker(t.repoPaths[0])
	tracker.SetStartTime(time.Now())
	wd, _ := os.Getwd()
	tracker.SetIdentity(status.Identity{
		Binary:     r.name() + " dashboard",
		Version:    r.buildVersion(),
		ConfigPath: t.cfgPath,
		WorkDir:    wd,
	})
	tracker.SetGraphFunc(bootstrap.GraphStateFunc(t.engine))
	defer tracker.Close()

	port := dashboard.ResolveStablePort(t.engine.Config().Dashboard.Port)
	// The binary's own options — title, overlay panels, and the InsightLabels
	// admission list that decides which explainers' findings the page will show at
	// all. Tracker and StablePort are this command's to set, and are stamped over
	// whatever the callback returned: they describe THIS process, not the binary.
	opts := r.dashboardOptions(t.engine)
	opts.Tracker, opts.StablePort = tracker, port
	dash, err := dashboard.Start(t.engine, opts)
	if err != nil {
		r.dashboardFatal("starting dashboard: %v", err)
	}
	tracker.SetDashboardPort(dash.Port())
	tracker.PersistStartup()
	fmt.Fprintln(os.Stderr, "Dashboard running in foreground")
	fmt.Fprintf(os.Stderr, "Snapshot: %s\n", snapshotDir)
	fmt.Fprintf(os.Stderr, "Open: %s\n", dash.URL())
	if port > 0 {
		fmt.Fprintf(os.Stderr, "Bookmarkable URL: http://127.0.0.1:%d\n", port)
	}
	fmt.Fprintln(os.Stderr, "Press Ctrl-C to stop.")
	if *open {
		if err := openBrowser(dash.URL()); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
		}
	}
	<-ctx.Done()
}

func (r *Runner) startDashboardDetached(open bool, target, snapshotDir string) {
	if !detachable {
		r.dashboardFatal("background dashboards are not supported on this platform; use --foreground")
	}
	exe, err := os.Executable()
	if err != nil {
		r.dashboardFatal("locating executable: %v", err)
	}
	args := []string{"dashboard", "--foreground"}
	if target != "" {
		args = append(args, target)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	if err := cmd.Start(); err != nil {
		r.dashboardFatal("starting background dashboard: %v", err)
	}

	var inst status.Instance
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, candidate := range dashboardInstances(status.LiveInstances()) {
			if candidate.PID == cmd.Process.Pid && candidate.URL() != "" {
				inst = candidate
				break
			}
		}
		if inst.PID != 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if inst.PID == 0 {
		r.dashboardFatal("background dashboard did not become ready")
	}
	fmt.Fprintln(os.Stderr, "Dashboard started in background")
	fmt.Fprintf(os.Stderr, "Snapshot: %s\n", snapshotDir)
	fmt.Fprintf(os.Stderr, "Open: %s\n", inst.URL())
	fmt.Fprintf(os.Stderr, "Stop: %s dashboard stop\n", r.name())
	if open {
		if err := openBrowser(inst.URL()); err != nil {
			fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
		}
	}
}

func (r *Runner) missingDashboardSnapshot(subject, target string) {
	generate := r.name() + " --generate"
	open := r.name() + " dashboard --open"
	if target != "" {
		generate += fmt.Sprintf(" %q", target)
		open += fmt.Sprintf(" %q", target)
	}
	r.dashboardFatal("no snapshot found for %s\n\nCreate one:\n  %s\n\nThen open it:\n  %s", subject, generate, open)
}

func (r *Runner) dashboardFatal(format string, args ...any) {
	r.cmdFatal("dashboard", format, args...)
}

func dashboardInstances(instances []status.Instance) []status.Instance {
	out := make([]status.Instance, 0, len(instances))
	for _, inst := range instances {
		if strings.HasSuffix(inst.Binary, " dashboard") {
			out = append(out, inst)
		}
	}
	return out
}

func (r *Runner) dashboardStatus() {
	instances := dashboardServingInstances(status.LiveInstances())
	if len(instances) == 0 {
		fmt.Fprintln(os.Stderr, "No dashboards are running.")
		return
	}
	for _, inst := range instances {
		fmt.Fprintf(os.Stderr, "%s · PID %d · %s · %s\n", dashboardKind(inst), inst.PID, inst.RepoLabels(), inst.URL())
	}
}

func dashboardServingInstances(instances []status.Instance) []status.Instance {
	out := make([]status.Instance, 0, len(instances))
	for _, inst := range instances {
		if inst.URL() != "" {
			out = append(out, inst)
		}
	}
	return out
}

func dashboardKind(inst status.Instance) string {
	if strings.HasSuffix(inst.Binary, " dashboard") {
		return "standalone"
	}
	return "MCP server"
}

func (r *Runner) stopDashboards() {
	instances := dashboardInstances(status.LiveInstances())
	if len(instances) == 0 {
		fmt.Fprintln(os.Stderr, "No standalone dashboards are running.")
		return
	}
	stopped := 0
	for _, inst := range instances {
		if err := stopDashboardProcess(inst.PID); err != nil {
			fmt.Fprintf(os.Stderr, "Could not stop dashboard PID %d: %v\n", inst.PID, err)
			continue
		}
		stopped++
	}
	fmt.Fprintf(os.Stderr, "Stopped %d standalone dashboard(s).\n", stopped)
}

func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start()
}
