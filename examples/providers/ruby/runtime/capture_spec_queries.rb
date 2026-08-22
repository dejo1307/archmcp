# frozen_string_literal: true

# Produces the query-counter capture the runtime provider reads, from a
# Rails spec suite: require this file from spec_helper (or pass it with
# `-r`) and run the suite; every example becomes a frame keyed by the file
# and the example's description, with the number of SQL statements Active
# Record issued while it ran. The capture lands in .enola-runtime/ under the
# repository root, which is where enola_runtime_provider.rb looks.
#
# Nothing here runs at snapshot time: the capture is an operator's run, and
# the snapshot reads it through the seam like any other observation.

require "json"
require "fileutils"

module EnolaQueryCapture
  CAPTURE_DIR = ".enola-runtime"
  CAPTURE_FILE = "queries.json"

  class << self
    def install(root: Dir.pwd)
      counts = Hash.new(0)
      current = nil
      subscriber = ActiveSupport::Notifications.subscribe("sql.active_record") do |*, payload|
        next if current.nil?
        next if %w[SCHEMA TRANSACTION].include?(payload[:name])

        counts[current] += 1
      end

      RSpec.configure do |config|
        config.around(:each) do |example|
          file = example.metadata[:file_path].sub(%r{\A\./}, "")
          current = "#{file}:#{example.full_description}"
          example.run
          current = nil
        end
        config.after(:suite) do
          ActiveSupport::Notifications.unsubscribe(subscriber)
          EnolaQueryCapture.write(root, counts)
        end
      end
    end

    def write(root, counts)
      dir = File.join(root, CAPTURE_DIR)
      FileUtils.mkdir_p(dir)
      rows = counts.sort.map { |frame, queries| { "frame" => frame, "queries" => queries } }
      File.write(File.join(dir, CAPTURE_FILE), JSON.pretty_generate("source" => "activesupport-notifications", "counts" => rows))
      warn "enola runtime capture: #{rows.size} frame(s) written to #{File.join(CAPTURE_DIR, CAPTURE_FILE)}"
    end
  end
end

EnolaQueryCapture.install if defined?(RSpec) && defined?(ActiveSupport::Notifications)
