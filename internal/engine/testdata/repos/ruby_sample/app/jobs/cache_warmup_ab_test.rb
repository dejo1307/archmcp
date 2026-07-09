# frozen_string_literal: true

# v97: a PRODUCTION class whose basename ends in the token `test`. Ruby's test
# globs are directory-scoped (`**/test/**/*_test.rb`), so this file — under
# app/jobs, with no `test`/`spec` directory segment — is indexed as source.
#
# Before v97 the glob was the bare suffix `**/*_test.rb`, which both ignored this
# file and routed it to reference-only test-ref extraction: the class never became
# a symbol fact and vanished from the graph. Naming a job after the A/B test it
# implements is ordinary; so are `*_load_test.rb`, `*_smoke_test.rb`.
class CacheWarmupABTest < BaseWorker
  def perform(event_name, args = {})
    notify(event_name, args)
  end

  private

  def notify(event_name, args); end
end
