package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/enola-labs/enola/pkg/bootstrap"
	"github.com/enola-labs/enola/pkg/dashboard"
)

// Dashboard restores the latest snapshot and serves its read-only dashboard
// without also starting an MCP stdio server. It remains in the foreground so
// Ctrl-C has the unsurprising effect of stopping the listener.
func (r *Runner) Dashboard(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	open := fs.Bool("open", false, "open the dashboard in the default browser")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s dashboard [--open] [repo_path|config_path]\n\n", r.name())
		fmt.Fprintln(os.Stderr, "Serve the latest architecture snapshot as a read-only local dashboard.")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		r.checkFatal("dashboard: %v", err)
	}
	if fs.NArg() > 1 {
		r.checkFatal("dashboard accepts at most one repository or config path")
	}
	arg := ""
	if fs.NArg() == 1 {
		arg = fs.Arg(0)
	}

	t := r.resolveTarget(arg)
	if restored := bootstrap.AutoLoadSnapshot(t.engine, t.engine.Config()); restored == nil {
		r.checkFatal("no snapshot found for %s\n\nGenerate one first:\n\n    %s --generate %s",
			t.configNote, r.name(), arg)
	}

	port := dashboard.ResolveStablePort(t.engine.Config().Dashboard.Port)
	dash, err := dashboard.Start(t.engine, dashboard.Options{StablePort: port})
	if err != nil {
		r.checkFatal("starting dashboard: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Dashboard: %s\n", dash.URL())
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
