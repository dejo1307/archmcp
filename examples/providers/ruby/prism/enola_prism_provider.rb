#!/usr/bin/env ruby
# frozen_string_literal: true

# enola Prism reference provider.
#
# Parses every **/*.rb under the path given as ARGV[0] with Prism (the Ruby
# parser that ships in the standard library from Ruby 3.3) and emits JSONL
# call facts on stdout in enola's fact schema — one fact per method call,
# typed by what the receiver resolves to LEXICALLY. No runtime, no gems, no
# guessing:
#
#   receiver is a constant                  -> callee "Const#method",     resolution_level "constant-receiver"
#   self or no receiver, inside a def
#   nested in a nameable class/module       -> callee "Enclosing#method", resolution_level "lexical-self"
#   anything else                           -> callee "method",           resolution_level "name-only"
#
# Fact naming is designed to ADD typed call edges without colliding with the
# identities enola's own Ruby extractor emits — the extractor already owns the
# symbol facts, and the seam skips any provider fact sharing a name+kind
# identity with an extractor fact. Every fact here is therefore kind
# "dependency" with the distinctive name "prism-call: <caller> -> <callee>",
# carrying one calls relation targeting the callee. The seam stamps
# provider/provider_version onto accepted facts; this script declares only
# resolution_level, which the seam requires on every provider fact.
#
# Determinism: files are enumerated in sorted order, output lines are sorted
# before printing, and nothing time- or environment-dependent is emitted.
# vendor/, node_modules/ and tmp/ subtrees are skipped at any depth; a file
# Prism cannot parse cleanly is skipped whole — fail closed, never guess.
# --version prints a fixed semver on stdout and exits, which is how the seam
# learns what build it is talking to.

require "json"
require "prism"

PROVIDER_VERSION = "0.1.0"

if ARGV.include?("--version")
  puts PROVIDER_VERSION
  exit 0
end

root = ARGV[0]
abort "usage: enola_prism_provider.rb <repo-path>" unless root && File.directory?(root)

SKIP_SEGMENTS = %w[vendor node_modules tmp].freeze

class CallCollector < Prism::Visitor
  attr_reader :facts

  def initialize(file)
    @file = file
    @scopes = []
    @methods = []
    @facts = []
    super()
  end

  def visit_class_node(node)
    with_scope(constant_name(node.constant_path)) { super }
  end

  def visit_module_node(node)
    with_scope(constant_name(node.constant_path)) { super }
  end

  # An eigenclass body (class << self) pushes an unnameable scope: calls inside
  # it degrade to name-only rather than being attributed to a name this script
  # cannot lexically prove.
  def visit_singleton_class_node(node)
    with_scope(nil) { super }
  end

  def visit_def_node(node)
    @methods.push(node.name.to_s)
    super
    @methods.pop
  end

  def visit_call_node(node)
    record(node)
    super
  end

  private

  def with_scope(name)
    @scopes.push(name)
    yield
    @scopes.pop
  end

  def enclosing
    return nil if @scopes.empty? || @scopes.any?(&:nil?)

    @scopes.join("::")
  end

  def caller_name
    enc = enclosing
    return @file unless enc
    return enc if @methods.empty?

    "#{enc}##{@methods.last}"
  end

  def constant_name(node)
    case node
    when Prism::ConstantReadNode
      node.name.to_s
    when Prism::ConstantPathNode
      begin
        node.full_name
      rescue StandardError
        nil
      end
    end
  end

  def record(node)
    method = node.name.to_s
    receiver = node.receiver
    callee, level =
      if receiver && (const = constant_name(receiver))
        ["#{const}##{method}", "constant-receiver"]
      elsif (receiver.nil? || receiver.is_a?(Prism::SelfNode)) && !@methods.empty? && (enc = enclosing)
        ["#{enc}##{method}", "lexical-self"]
      else
        [method, "name-only"]
      end
    @facts << {
      "kind" => "dependency",
      "name" => "prism-call: #{caller_name} -> #{callee}",
      "file" => @file,
      "line" => node.location.start_line,
      "props" => { "resolution_level" => level },
      "relations" => [{ "kind" => "calls", "target" => callee }]
    }
  end
end

lines = []
files = Dir.glob(File.join("**", "*.rb"), base: root).reject do |rel|
  rel.split("/").any? { |segment| SKIP_SEGMENTS.include?(segment) }
end.sort

files.each do |rel|
  source = begin
    File.read(File.join(root, rel))
  rescue StandardError
    next
  end
  result = Prism.parse(source)
  next unless result.success?

  collector = CallCollector.new(rel)
  result.value.accept(collector)
  collector.facts.each { |fact| lines << JSON.generate(fact) }
end

puts lines.sort
