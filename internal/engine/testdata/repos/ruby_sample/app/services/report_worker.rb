# frozen_string_literal: true

# v36: `Reporting` is a NAMESPACE module — its body only defines a nested class,
# no instance methods — so it is `abstract:false` (a Rails namespace, not an
# abstraction). Its counterpart is the `Trackable` Concern (abstract:true).
module Reporting
  class ReportWorker < BaseWorker
    include Trackable

    # v18/v19: a custom class-body DSL macro (bare-receiver class-body call) is
    # recorded as a use (`track_metrics`) on the class fact, so the base method
    # backing the macro is not mis-reported as dead.
    track_metrics :runs

    # v25: `delegate :a, :b, to: X` folds the delegated method names
    # (`formatted_name`, `subtitle`) as calls on the class fact.
    delegate :formatted_name, :subtitle, to: :presenter

    STOP_WORDS = %w[the a an].freeze

    # Item 1: a SCALING `.each` loop over a runtime collection adds loop_depth,
    # while the constant-bounded loops below must NOT.
    def summarize(users)
      users.each do |user|            # scaling loop -> loop_depth 1
        notify(user)                  # in-loop call (calls_in_loop)
        user.recent_posts             # v31: association read on the block-local var
      end

      6.times { |i| bucket(i) }       # v34: integer.times is constant-bounded -> no depth
      STOP_WORDS.each { |w| index(w) } # v34: ALL-CAPS constant .each -> bounded, no depth
      %w[csv pdf].map.each { |fmt| render_format(fmt) } # v35: bounded literal behind a chain
    end

    # Item 2 (recursion precision): `super` climbs the ancestor chain.
    #   v22: records a call edge to the same-named ancestor method (`display_label`).
    #   v32: must NOT be flagged `recursive_self` (super terminates, not recursion).
    def display_label
      decorate(__method__)
      super
    end

    # v31: find_in_batches YIELDS a batch (array); the inner `.each` over that
    # batch is the real per-element loop -> loop_depth 1, not 2.
    def reindex(model)
      model.find_in_batches do |batch|
        batch.each { |record| persist(record) }
      end
    end

    # Item 7 (v21): an interpolated symbol `:"report_#{type}"` used with
    # public_send records the static prefix "report_" so same-prefix methods
    # (report_daily / report_weekly) are treated as dynamically dispatched (used),
    # not dead. v30: the prefix is committed because this scope also calls send/
    # public_send (dispatcher-proximity).
    def self.build(type)
      method_name = :"report_#{type}"
      public_send(method_name, type)
    end

    def report_daily(type); end
    def report_weekly(type); end

    # v24: a call in a DEFAULT PARAMETER value (`self.class.default_label`) is
    # walked for call edges. v33: `self.class.foo` is a sibling class-method
    # dispatch (different method), so it is NOT `recursive_self`.
    def label(text = self.class.default_label)
      text
    end

    def self.default_label
      "report"
    end

    private

    def notify(user); end
    def bucket(index); end
    def index(word); end
    def render_format(format); end
    def decorate(method_name); end
    def persist(record); end
  end
end
