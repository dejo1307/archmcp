# frozen_string_literal: true

# Presence of config/application.rb flips the extractor into Rails mode
# (detectRailsProject), which enables route parsing and template indexing.
require "rails/all"

module RubySample
  class Application < Rails::Application
    config.load_defaults 7.1
  end
end
