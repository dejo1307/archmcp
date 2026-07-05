# frozen_string_literal: true

# v36: an ActiveSupport::Concern that defines INSTANCE methods is a mixin, so it
# is flagged `abstract:true` for package-metrics abstractness (it is never
# instantiated on its own). Contrast with the `Reporting` namespace module in
# app/services/report_worker.rb, which only wraps a class and is `abstract:false`.
module Trackable
  extend ActiveSupport::Concern

  included do
    # A DSL call inside the Concern's `included` block.
    track_metrics :touched_at
  end

  # An instance method -> makes the module a mixin -> abstract:true (v36).
  def track!
    touch(:tracked_at)
  end
end
