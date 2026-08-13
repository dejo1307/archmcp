package rubyextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestRailsComponent(t *testing.T) {
	for _, tc := range []struct {
		name, qual, super string
		includes          []string
		file, want        string
	}{
		// The superclass is what Rails actually dispatches on, so it wins.
		{name: "job by superclass", super: "ApplicationJob", file: "app/lib/thing.rb", want: "job"},
		{name: "job by ActiveJob base", super: "ActiveJob::Base", file: "x.rb", want: "job"},
		{name: "mailer", super: "ApplicationMailer", file: "x.rb", want: "mailer"},
		{name: "channel", super: "ApplicationCable::Channel", file: "x.rb", want: "channel"},
		{name: "model", super: "ApplicationRecord", file: "x.rb", want: "model"},
		{name: "controller", super: "ApplicationController", file: "x.rb", want: "controller"},
		{name: "controller by name suffix", super: "Admin::BaseController", file: "x.rb", want: "controller"},
		{name: "view component", super: "ViewComponent::Base", file: "x.rb", want: "component"},
		// A Sidekiq worker INCLUDES rather than inherits.
		{name: "sidekiq worker", includes: []string{"Sidekiq::Worker"}, file: "x.rb", want: "job"},
		{name: "sidekiq job", includes: []string{"Sidekiq::Job"}, file: "x.rb", want: "job"},
		// Path fallback for the conventions the framework does not enforce.
		{name: "path fallback jobs", file: "app/jobs/cleanup_job.rb", want: "job"},
		{name: "path fallback workers", file: "app/workers/sync_worker.rb", want: "job"},
		{name: "path fallback policies", file: "app/policies/post_policy.rb", want: "policy"},
		// Concerns are mixins, not instances of the directory they live under.
		{name: "model concern", file: "app/models/concerns/trashable.rb", want: "concern"},
		{name: "controller concern", file: "app/controllers/concerns/authenticable.rb", want: "concern"},
		// A Pundit policy outside app/policies is recognizable only by name.
		{name: "policy by name", qual: "Admin::UserPolicy", file: "lib/admin/user_policy.rb", want: "policy"},
		// Most classes are nothing in particular.
		{name: "plain service", qual: "Billing::ChargeService", file: "app/services/billing/charge_service.rb", want: ""},
		{name: "plain lib class", qual: "Util", file: "lib/util.rb", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := railsComponent(tc.qual, tc.super, tc.includes, tc.file); got != tc.want {
				t.Errorf("railsComponent(%q,%q,%v,%q) = %q, want %q",
					tc.qual, tc.super, tc.includes, tc.file, got, tc.want)
			}
		})
	}
}

// TestRailsModuleRole_MigrationsAreTooling is the case with teeth: a migration is a
// one-shot script nothing references by design, so classifying db/migrate as production
// code makes every migration in a large Rails app a dead-code candidate.
func TestRailsModuleRole_MigrationsAreTooling(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"db/migrate", facts.ModuleRoleTooling},
		{"db/post_migrate", ""},
		{"lib/tasks", facts.ModuleRoleTooling},
		{"app/models", facts.ModuleRoleProduction},
		{"app/controllers/admin", facts.ModuleRoleProduction},
		{"config", facts.ModuleRoleProduction},
		// A genuine app directory that merely contains the word stays production — the
		// `migrate` rule only fires directly below `db/`.
		{"app/services/migrate", facts.ModuleRoleProduction},
		{"lib/util", ""},
	} {
		if got := railsModuleRole(tc.dir); got != tc.want {
			t.Errorf("railsModuleRole(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}
