#!/usr/bin/env ruby
# frozen_string_literal: true

# enola Rubydex reference provider.
#
# Indexes the repository given as ARGV[0] with Rubydex, the shared Ruby
# analysis engine, resolves it, and emits JSONL facts on stdout in enola's fact
# schema for the three things Rubydex settles that enola's own Ruby extractor
# and the Prism provider do not:
#
#   a constant reference resolved through Ruby's nesting and inheritance
#   rules                                   -> "rubydex-ref: <referrer> -> <Decl>",
#                                              depends_on, resolution_level "resolved"
#   a method call whose receiver resolves to a constant OTHER than the
#   lexical enclosing class                 -> "rubydex-call: <caller> -> <Recv#m>",
#                                              calls, resolution_level "constant-receiver"
#   a class's linearised ancestor chain,
#   mixins in method-resolution order       -> "rubydex-ancestor: <Class> -> <Ancestor>",
#                                              implements, resolution_level "resolved"
#
# Only facts LOCATED in the workspace are emitted, so gem and standard-library
# declarations never enter the graph as members; they may appear as the far
# end of an edge, which is what a rule about a base class a gem declares
# needs. Built-in ancestors (Object, Kernel, BasicObject) are omitted because
# every class reaches them. Enclosing-class receivers are omitted because the
# extractor and the Prism provider already say those; what Rubydex adds is the
# cross-class receiver.
#
# Fact names carry the rubydex- prefix so they cannot collide with the
# identities the extractor or any other provider emits. Nothing is guessed: a
# qualified read is one dependency on its leaf, carrying the file that defines
# it, with the segments before it as its path; a reference that resolves to
# nothing, or to a constant alias, is a fact with no relation and the cause
# named in resolution_cause, counted in the census beside Rubydex's own
# diagnostics, which are its refusals to guess (a dynamic mixin argument, a
# parse warning).
#
# Determinism: facts are built in document order, output lines are sorted
# before printing, and nothing time- or environment-dependent is emitted.
# --version prints a fixed semver on stdout and exits, which is how the seam
# learns what build it is talking to.

require "json"

PROVIDER_VERSION = "0.1.0"

if ARGV.include?("--version")
  puts PROVIDER_VERSION
  exit 0
end

root = ARGV[0]
abort "usage: enola_rubydex_provider.rb <repo-path>" unless root && File.directory?(root)

begin
  require "rubydex"
rescue LoadError
  abort "the rubydex gem is not loadable by this ruby; install it or configure the provider under bundle exec"
end

BUILT_IN_URI = "rubydex:built-in"

class RubydexFacts
  def initialize(root)
    @root = File.expand_path(root)
    @workspace = "file://#{@root}/"
    @facts = []
    @unresolved = 0
    @aliased_references = 0
    @undefined_targets = 0
    @enclosing_receivers = 0
    @untyped_receivers = 0
    @aliased_declarations = 0
  end

  def collect
    unless File.exist?(File.join(@root, "Gemfile"))
      census(0, 0, "no Gemfile: not a workspace Rubydex can index")
      return @facts
    end
    graph = Dir.chdir(@root) do
      built = Rubydex::Graph.new
      built.index_workspace
      built.resolve
      built
    end
    documents = graph.documents.select { |doc| in_workspace?(doc.uri) }
    documents.each do |doc|
      doc.definitions.each { |definition| emit_ancestry(definition) }
      doc.method_references.each { |reference| emit_call(reference) }
    end
    emit_references(graph.constant_references)
    diagnostics = graph.diagnostics.count { |diagnostic| in_workspace?(diagnostic.location.uri) }
    census(documents.size, diagnostics)
    @facts
  end

  private

  def in_workspace?(uri)
    uri.to_s.start_with?(@workspace)
  end

  def relative(uri)
    uri.to_s.delete_prefix(@workspace)
  end

  # The engine reports one reference per segment of a qualified path, so
  # `Foo::VERSION` arrives as `Foo` and as `VERSION`, adjacent on one line with
  # the separator between them. The leaf is the dependency and the segments
  # before it are its path: a read of `Foo::VERSION` depends on VERSION and only
  # names Foo on the way, and a prefix emitted as a dependency of its own landed
  # on whichever reopening of Foo a consumer took first.
  def emit_references(references)
    kept = references.select { |reference| in_workspace?(reference.location.uri) }
    by_line = kept.group_by { |reference| [reference.location.uri.to_s, reference.location.start_line] }
    inner = {}
    by_line.each_value do |line|
      line.each do |prefix|
        line.each do |following|
          inner[prefix] = true if !prefix.equal?(following) && prefix.location.end_column + "::".length == following.location.start_column
        end
      end
    end
    kept.each do |reference|
      next if inner[reference]

      emit_reference(reference, path_prefixes(by_line, reference))
    end
  end

  def path_prefixes(by_line, leaf)
    line = by_line[[leaf.location.uri.to_s, leaf.location.start_line]]
    prefixes = []
    current = leaf
    loop do
      previous = line.find { |candidate| candidate.location.end_column + "::".length == current.location.start_column }
      break unless previous

      prefixes.unshift(written_name(previous))
      current = previous
    end
    prefixes
  end

  def written_name(reference)
    return plain_name(reference.declaration.name) if reference.is_a?(Rubydex::ResolvedConstantReference) && reference.declaration

    reference.name
  end

  def emit_reference(reference, prefixes)
    referrer = enclosing_name(reference.document, reference.location) || relative(reference.location.uri)
    written = (prefixes + [written_name(reference)]).join("::")
    unless reference.is_a?(Rubydex::ResolvedConstantReference) && reference.declaration
      @unresolved += 1
      @facts << miss_fact("rubydex-ref: #{referrer} -> #{written}", reference.location, "unresolved", prefixes)
      return
    end
    target = reference.declaration
    return if singleton?(target.name)

    if target.is_a?(Rubydex::ConstantAlias)
      @aliased_references += 1
      @facts << miss_fact("rubydex-ref: #{referrer} -> #{written}", reference.location, "alias", prefixes)
      return
    end
    props = { "declared_in_workspace" => declared_in_workspace?(target) }
    props["path_prefixes"] = prefixes unless prefixes.empty?
    files, located = defining_files(target)
    if !located
      @undefined_targets += 1
      props["resolution_cause"] = "no_definition"
    elsif files.size == 1
      props["target_file"] = files.first
    end
    @facts << fact("rubydex-ref: #{referrer} -> #{plain_name(target.name)}", reference.location, "resolved",
      { "kind" => "depends_on", "target" => plain_name(target.name) }, props)
  end

  # The workspace files a declaration is defined in, and whether the engine
  # reports any location at all; a name defined in several files carries none,
  # because picking one would be the bug restated.
  def defining_files(declaration)
    located = false
    files = declaration.definitions.filter_map do |definition|
      located = true
      relative(definition.location.uri) if in_workspace?(definition.location.uri)
    end
    [files.uniq.sort, located]
  end

  def miss_fact(name, location, cause, prefixes)
    props = { "resolution_level" => "name-only", "resolution_cause" => cause }
    props["path_prefixes"] = prefixes unless prefixes.empty?
    {
      "kind" => "dependency",
      "name" => name,
      "file" => relative(location.uri),
      "line" => location.to_display.start_line,
      "props" => props,
      "relations" => []
    }
  end

  def emit_call(reference)
    receiver = reference.receiver
    if receiver.nil?
      @untyped_receivers += 1
      return
    end
    receiver_name = plain_name(receiver.name)
    enclosing = enclosing_name(reference.document, reference.location)
    if enclosing && same_lexical_owner?(enclosing, receiver_name)
      @enclosing_receivers += 1
      return
    end
    separator = singleton?(receiver.name) ? "." : "#"
    callee = "#{receiver_name}#{separator}#{reference.name}"
    caller = enclosing || relative(reference.location.uri)
    @facts << fact("rubydex-call: #{caller} -> #{callee}", reference.location, "constant-receiver",
      { "kind" => "calls", "target" => callee })
  end

  def emit_ancestry(definition)
    return unless definition.is_a?(Rubydex::ClassDefinition) || definition.is_a?(Rubydex::ModuleDefinition)

    declaration = definition.declaration
    return if declaration.nil?

    unless declaration.respond_to?(:ancestors)
      @aliased_declarations += 1
      return
    end

    chain = declaration.ancestors.to_a.take_while { |ancestor| !built_in?(ancestor) }
    chain.each_with_index do |ancestor, distance|
      next if distance.zero? || ancestor.name == declaration.name

      @facts << fact("rubydex-ancestor: #{declaration.name} -> #{ancestor.name}", definition.location, "resolved",
        { "kind" => "implements", "target" => ancestor.name },
        "ancestor_distance" => distance, "declared_in_workspace" => declared_in_workspace?(ancestor))
    end
  end

  def fact(name, location, level, relation, extra_props = {})
    {
      "kind" => "dependency",
      "name" => name,
      "file" => relative(location.uri),
      "line" => location.to_display.start_line,
      "props" => { "resolution_level" => level }.merge(extra_props),
      "relations" => [relation]
    }
  end

  def enclosing_name(document, location)
    candidates = document.definitions.select do |definition|
      next false unless definition.declaration
      next false unless definition.is_a?(Rubydex::ClassDefinition) || definition.is_a?(Rubydex::ModuleDefinition) || definition.is_a?(Rubydex::MethodDefinition)

      span = definition.location
      span.start_line <= location.start_line && span.end_line >= location.start_line
    end
    innermost = candidates.max_by { |definition| [definition.location.start_line, definition.location.start_column] }
    name = innermost&.declaration&.name
    name && plain_name(name)
  end

  def same_lexical_owner?(enclosing, receiver_name)
    owner = enclosing.sub(/[#.][^#.]*\z/, "")
    owner == receiver_name
  end

  def singleton?(name)
    name.end_with?(">")
  end

  def plain_name(name)
    name.sub(/::<[^>]+>\z/, "").delete_suffix("()")
  end

  def built_in?(declaration)
    declaration.definitions.any? { |definition| definition.location.uri.to_s == BUILT_IN_URI }
  end

  def declared_in_workspace?(declaration)
    declaration.definitions.any? { |definition| in_workspace?(definition.location.uri) }
  end

  def census(files_seen, diagnostics, refusal = nil)
    causes = []
    causes << { "cause" => refusal, "count" => 1 } if refusal
    causes << { "cause" => "unresolved constant reference", "count" => @unresolved } if @unresolved.positive?
    causes << { "cause" => "reference resolves to a constant alias", "count" => @aliased_references } if @aliased_references.positive?
    causes << { "cause" => "resolved declaration has no definition location", "count" => @undefined_targets } if @undefined_targets.positive?
    causes << { "cause" => "receiver is the lexical enclosing class", "count" => @enclosing_receivers } if @enclosing_receivers.positive?
    causes << { "cause" => "receiver resolves to no constant", "count" => @untyped_receivers } if @untyped_receivers.positive?
    causes << { "cause" => "declaration is a constant alias, not a class", "count" => @aliased_declarations } if @aliased_declarations.positive?
    causes << { "cause" => "rubydex diagnostic", "count" => diagnostics } if diagnostics.positive?
    skipped = causes.sum { |cause| cause["count"] }
    warn "enola-provider-census: " + JSON.generate(
      "files_seen" => files_seen,
      "declarations_parsed" => @facts.size,
      "constructs_skipped" => skipped,
      "skip_causes" => causes
    )
  end
end

puts RubydexFacts.new(root).collect.map { |fact| JSON.generate(fact) }.sort
