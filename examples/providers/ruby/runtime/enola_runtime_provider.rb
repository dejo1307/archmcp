#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"

PROVIDER_VERSION = "0.1.0"

if ARGV.include?("--version")
  puts PROVIDER_VERSION
  exit 0
end

root = ARGV[0]
abort "usage: enola_runtime_provider.rb <repo-path>" unless root && File.directory?(root)

CAPTURE_DIR = ".enola-runtime"
RESOLUTION_LEVEL = "runtime-observed"
BOOT_SOURCE = "enola runtime"
QUERY_SOURCE = "activesupport-notifications"

module RuntimeProvider
  class CaptureError < StandardError; end

  class << self
    def facts_for(capture_path, capture)
      case capture["source"]
      when BOOT_SOURCE then boot_facts(capture_path, capture)
      when QUERY_SOURCE then query_facts(capture)
      else
        raise CaptureError, "unrecognized capture source #{capture["source"].inspect}"
      end
    end

    def boot_facts(capture_path, capture)
      unreachable = capture["unreachable"]
      if unreachable.is_a?(Array) && !unreachable.empty?
        first = unreachable.first
        raise CaptureError,
          "capture reports #{unreachable.size} unreachable subject(s), first: #{first["subject"]} (#{first["error"]}) — an incomplete boot must not become partial truth"
      end

      Array(capture["facts"]).map do |observed|
        case observed["kind"]
        when "route" then boot_route_fact(capture_path, observed)
        when "association" then boot_association_fact(capture_path, observed)
        when "storage" then boot_storage_fact(capture_path, observed)
        else
          raise CaptureError, "unrecognized boot fact kind #{observed["kind"].inspect}"
        end
      end
    end

    def boot_route_fact(capture_path, observed)
      verb = observed["verb"].to_s
      path = observed["path"].to_s
      raise CaptureError, "route observation without verb and path: #{observed.inspect}" if verb.empty? || path.empty?

      handler = observed["handler"]
      name = "runtime-route: #{verb} #{path}"
      name += " -> #{handler}" if handler

      props = boot_props.merge("method" => verb, "path" => path)
      props["endpoint_kind"] = observed["endpoint_kind"] if observed["endpoint_kind"]
      props["handler"] = handler if handler
      { "kind" => "route", "name" => name, "file" => capture_path, "props" => props }
    end

    def boot_association_fact(capture_path, observed)
      model = observed["model"].to_s
      association = observed["association"].to_s
      if model.empty? || association.empty?
        raise CaptureError, "association observation without model and association: #{observed.inspect}"
      end

      props = boot_props.merge("model" => model, "association" => association)
      props["macro"] = observed["macro"] if observed["macro"]
      props["target"] = observed["target"] if observed["target"]
      props["through"] = observed["through"] if observed["through"]
      props["polymorphic"] = true if observed["polymorphic"]
      { "kind" => "association", "name" => "runtime-association: #{model}##{association}", "file" => capture_path, "props" => props }
    end

    def boot_storage_fact(capture_path, observed)
      model = observed["model"].to_s
      table = observed["name"].to_s
      raise CaptureError, "storage observation without model and table: #{observed.inspect}" if model.empty? || table.empty?

      props = boot_props.merge("model" => model, "table" => table)
      { "kind" => "storage", "name" => "runtime-storage: #{model} -> #{table}", "file" => capture_path, "props" => props }
    end

    def boot_props
      { "resolution_level" => RESOLUTION_LEVEL, "observed_via" => "rails-boot" }
    end

    def query_facts(capture)
      Array(capture["counts"]).map do |row|
        frame = row["frame"].to_s
        queries = row["queries"]
        file, label = frame.split(":", 2)
        unless file && label && !file.empty? && !label.empty? && queries.is_a?(Integer) && queries >= 0
          raise CaptureError, "unrecognized query-count row: #{row.inspect}"
        end

        {
          "kind" => "dependency",
          "name" => "runtime-queries: #{frame}",
          "file" => file,
          "props" => {
            "resolution_level" => RESOLUTION_LEVEL,
            "observed_via" => "query-counter",
            "frame_label" => label,
            "queries" => queries
          }
        }
      end
    end
  end
end

lines = []
captures = Dir.glob(File.join(CAPTURE_DIR, "*.json"), base: root).sort
captures.each do |rel|
  capture = begin
    JSON.parse(File.read(File.join(root, rel)))
  rescue JSON::ParserError, SystemCallError => error
    abort "runtime capture #{rel}: #{error.message}"
  end

  begin
    RuntimeProvider.facts_for(rel, capture).each { |fact| lines << JSON.generate(fact) }
  rescue RuntimeProvider::CaptureError => error
    abort "runtime capture #{rel}: #{error.message}"
  end
end

puts lines.sort.uniq
