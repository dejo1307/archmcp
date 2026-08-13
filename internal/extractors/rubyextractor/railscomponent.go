package rubyextractor

import (
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// A Rails codebase is not a flat bag of classes. `app/jobs`, `app/mailers`,
// `app/channels`, `app/policies` and `app/serializers` are each a distinct kind of
// thing with a distinct lifecycle, and none of them was distinguishable in the graph:
// every one came out as a class in a directory, which is why a background job invoked
// only through `perform_later` and a mailer invoked only from a view both read as
// ordinary unreferenced classes.
//
// The component is derived from the SUPERCLASS or an included module first, and from
// the directory only as a fallback. That order matters: `app/services` is a convention
// with no framework meaning and a class can live anywhere, while `< ApplicationJob` is
// what Rails actually dispatches on.

// railsComponent classifies a class by its superclass, the modules it includes, and its
// path. Returns "" when the class is nothing in particular, which is most of them.
func railsComponent(qualifiedName, superclass string, includes []string, relFile string) string {
	super := normalizeConstant(superclass)
	// Strip a `Grape::API::Instance`-style suffix chain down to the meaningful base.
	switch {
	case super == "ApplicationJob" || super == "ActiveJob::Base" || strings.HasSuffix(super, "::ApplicationJob"):
		return "job"
	case super == "ApplicationMailer" || super == "ActionMailer::Base" || strings.HasSuffix(super, "::ApplicationMailer"):
		return "mailer"
	case super == "ApplicationCable::Channel" || super == "ActionCable::Channel::Base":
		return "channel"
	case super == "ApplicationCable::Connection" || super == "ActionCable::Connection::Base":
		return "channel"
	case super == "ApplicationRecord" || super == "ActiveRecord::Base":
		return "model"
	case super == "ApplicationController" || super == "ActionController::Base" ||
		super == "ActionController::API" || strings.HasSuffix(super, "Controller"):
		return "controller"
	case super == "ViewComponent::Base" || super == "ApplicationComponent":
		return "component"
	}
	for _, inc := range includes {
		switch normalizeConstant(inc) {
		case "Sidekiq::Worker", "Sidekiq::Job":
			return "job"
		case "ActiveModel::Serializer":
			return "serializer"
		}
	}
	// Directory fallback for the conventions the framework does not enforce.
	if c := railsComponentForPath(relFile); c != "" {
		return c
	}
	// Pundit policies are plain classes named <Model>Policy in app/policies; the name is
	// the only signal when the directory is non-standard.
	if strings.HasSuffix(qualifiedName, "Policy") {
		return "policy"
	}
	return ""
}

// railsComponentForPath maps an `app/<dir>/` segment to a component name.
func railsComponentForPath(relFile string) string {
	parts := strings.Split(filepath.ToSlash(relFile), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != "app" {
			continue
		}
		// `app/models/concerns` holds mixins, not models — and the same for every other
		// component directory. Classifying them as their parent would report a repo's
		// shared behavior as whatever it happens to be mixed into.
		for _, seg := range parts[i+1:] {
			if seg == "concerns" {
				return "concern"
			}
		}
		switch parts[i+1] {
		case "jobs", "workers":
			return "job"
		case "mailers":
			return "mailer"
		case "channels":
			return "channel"
		case "policies":
			return "policy"
		case "serializers":
			return "serializer"
		case "controllers":
			return "controller"
		case "models":
			return "model"
		case "components":
			return "component"
		case "helpers":
			return "helper"
		}
	}
	return ""
}

// railsModuleRole classifies a Rails directory more precisely than the language-agnostic
// facts.ModuleRoleForPath can.
//
// `db/migrate` is the case that matters: a migration is a one-shot script that nothing
// references by design, so leaving it classified as production code makes every
// migration a dead-code candidate — and a large Rails app has thousands. `lib/tasks`
// holds rake tasks, which are tooling for the same reason.
//
// Returns "" to defer to facts.ModuleRoleForPath.
func railsModuleRole(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	for i := range parts {
		switch parts[i] {
		case "migrate", "seeds":
			// Only under db/, so a genuine app/services/migrate stays production.
			if i > 0 && parts[i-1] == "db" {
				return facts.ModuleRoleTooling
			}
		case "tasks":
			if i > 0 && parts[i-1] == "lib" {
				return facts.ModuleRoleTooling
			}
		}
	}
	if len(parts) > 0 && (parts[0] == "app" || parts[0] == "config") {
		return facts.ModuleRoleProduction
	}
	return ""
}
