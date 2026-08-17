#!/usr/bin/env ruby
# frozen_string_literal: true

require "json"

PROVIDER_VERSION = "0.1.0"

if ARGV.include?("--version")
  puts PROVIDER_VERSION
  exit 0
end

root = ARGV[0]
abort "usage: enola_rbs_provider.rb <repo-path>" unless root && File.directory?(root)

SKIP_SEGMENTS = %w[vendor node_modules tmp].freeze
RESOLUTION_LEVEL = "declared"
CENSUS_PREFIX = "enola-provider-census: "

module RbsProvider
  class Census
    def initialize
      @files_seen = 0
      @declarations_parsed = 0
      @skip_causes = Hash.new(0)
    end

    def saw_file
      @files_seen += 1
    end

    def parsed_declaration
      @declarations_parsed += 1
    end

    def retract_declarations(count)
      @declarations_parsed -= count
    end

    def skip(cause)
      @skip_causes[cause] += 1
    end

    def emit
      causes = @skip_causes.sort_by { |cause, count| [-count, cause] }
      warn CENSUS_PREFIX + JSON.generate(
        "files_seen" => @files_seen,
        "declarations_parsed" => @declarations_parsed,
        "constructs_skipped" => @skip_causes.values.sum,
        "skip_causes" => causes.map { |cause, count| { "cause" => cause, "count" => count } }
      )
    end
  end

  module Balance
    def self.scan(text, counts = { paren: 0, bracket: 0, brace: 0 })
      quote = nil
      text.each_char do |char|
        if quote
          quote = nil if char == quote
          next
        end
        case char
        when '"', "'" then quote = char
        when "(" then counts[:paren] += 1
        when ")" then counts[:paren] -= 1
        when "[" then counts[:bracket] += 1
        when "]" then counts[:bracket] -= 1
        when "{" then counts[:brace] += 1
        when "}" then counts[:brace] -= 1
        end
      end
      counts
    end

    def self.balanced?(counts)
      counts.values.all?(&:zero?)
    end

    def self.split_top_level(text, separator)
      parts = []
      current = +""
      depth = 0
      quote = nil
      index = 0
      while index < text.length
        char = text[index]
        if quote
          quote = nil if char == quote
          current << char
        elsif ['"', "'"].include?(char)
          quote = char
          current << char
        elsif ["(", "[", "{"].include?(char)
          depth += 1
          current << char
        elsif [")", "]", "}"].include?(char)
          depth -= 1
          current << char
        elsif depth.zero? && text[index, separator.length] == separator
          parts << current
          current = +""
          index += separator.length
          next
        else
          current << char
        end
        index += 1
      end
      parts << current
      parts
    end

    def self.matching_paren(text, open_index)
      depth = 0
      (open_index...text.length).each do |position|
        case text[position]
        when "(" then depth += 1
        when ")"
          depth -= 1
          return position if depth.zero?
        end
      end
      nil
    end
  end

  module ContractFact
    def self.build(receiver:, method:, singleton:, line:, props:)
      separator = singleton ? "." : "#"
      identity = "#{receiver}#{separator}#{method}"
      {
        "kind" => "symbol",
        "name" => "rbs-signature: #{identity}",
        "line" => line,
        "props" => props.merge(
          "receiver" => receiver,
          "method" => method,
          "singleton" => singleton
        ),
        "relations" => [{ "kind" => "has_method", "target" => identity }]
      }
    end

    def self.split_arrow(overload)
      pieces = Balance.split_top_level(overload, "->")
      return [nil, nil] if pieces.length < 2

      return_type = pieces.last.strip
      head = pieces[0..-2].join("->").strip
      open_paren = head.index("(")
      params = nil
      if open_paren
        close_paren = Balance.matching_paren(head, open_paren)
        if close_paren
          inner = head[(open_paren + 1)...close_paren].strip
          params = inner.empty? ? [] : Balance.split_top_level(inner, ",").map { |entry| entry.strip.squeeze(" ") }
        end
      end
      [params, return_type]
    end
  end

  class RbsFileParser
    DECL_RE = /\A\s*(class|module|interface)\s+(_?[A-Z]\w*(?:::_?[A-Z]\w*)*)\s*(?:\[([^\]]*)\])?\s*(.*)\z/
    METHOD_RE = /\A\s*def\s+(self\??\.)?([^:\s]+):\s*(.*)\z/
    SKIP_LINE_CAUSES = [
      [/\A\s*(attr_reader|attr_writer|attr_accessor)\b/, "rbs-attribute"],
      [/\A\s*(include|extend|prepend)\b/, "rbs-mixin"],
      [/\A\s*alias\b/, "rbs-alias"],
      [/\A\s*type\b/, "rbs-type-alias"],
      [/\A\s*(public|private)\s*\z/, "rbs-visibility"],
      [/\A\s*use\b/, "rbs-use-directive"],
      [/\A\s*%a\{/, "rbs-annotation"],
      [/\A\s*(self\.)?@\w+|\A\s*@@\w+/, "rbs-variable"],
      [/\A\s*\$\w+\s*:/, "rbs-global"],
      [/\A\s*[A-Z]\w*(?:::[A-Z]\w*)*\s*:/, "rbs-constant"]
    ].freeze

    def initialize(file, source, census)
      @file = file
      @source = source
      @census = census
      @scopes = []
      @facts = []
    end

    def parse
      lines = @source.lines.map(&:chomp)
      index = 0
      while index < lines.length
        line = strip_comment(lines[index])
        number = index + 1
        index += 1
        next if line.strip.empty?

        if (match = line.match(DECL_RE))
          abandoned = handle_decl(match, number)
          return abandon_file("rbs-decl-unparsed") if abandoned
        elsif (match = line.match(METHOD_RE))
          index = handle_method(match, number, lines, index)
        elsif line.strip == "end"
          return abandon_file("rbs-file-unbalanced") if @scopes.empty?

          @scopes.pop
        elsif (cause = skip_cause(line))
          index = consume_balanced(line, lines, index)
          @census.skip(cause)
        else
          @census.skip("rbs-unrecognized")
        end
      end
      return abandon_file("rbs-file-unbalanced") unless @scopes.empty?

      @facts
    end

    private

    def abandon_file(cause)
      @census.retract_declarations(@facts.length)
      @census.skip(cause)
      []
    end

    def handle_decl(match, number)
      keyword, name, type_params, rest = match.captures
      rest = rest.strip
      superclass = nil
      self_types = nil
      if keyword == "class" && rest.start_with?("<")
        superclass = rest.delete_prefix("<").strip
      elsif keyword == "module" && rest.start_with?(":")
        self_types = rest.delete_prefix(":").strip
      elsif !rest.empty?
        return true
      end
      @scopes.push(name)
      props = {
        "resolution_level" => RESOLUTION_LEVEL,
        "declared_in" => @file,
        "syntax" => "rbs",
        "decl_kind" => keyword
      }
      props["type_params"] = type_params.split(",").map(&:strip) if type_params
      props["superclass"] = superclass if superclass
      props["self_types"] = self_types if self_types
      emit("kind" => "symbol", "name" => "rbs-decl: #{@scopes.join("::")}", "line" => number, "props" => props)
      false
    end

    def handle_method(match, number, lines, index)
      selfdot, method, signature = match.captures
      if selfdot == "self?."
        @census.skip("rbs-def-self-query")
        return index
      end
      if @scopes.empty?
        @census.skip("rbs-def-outside-scope")
        return index
      end
      counts = Balance.scan(signature)
      until Balance.balanced?(counts) && !continues?(lines, index)
        if index >= lines.length
          @census.skip("rbs-method-unparsed")
          return index
        end
        continuation = strip_comment(lines[index])
        index += 1
        signature = "#{signature} #{continuation.strip}"
        counts = Balance.scan(continuation, counts)
      end
      overloads = Balance.split_top_level(signature, "|").map { |overload| overload.strip.squeeze(" ") }.reject(&:empty?)
      if overloads.empty? || overloads.any? { |overload| Balance.split_top_level(overload, "->").length < 2 }
        @census.skip("rbs-method-unparsed")
        return index
      end
      props = {
        "resolution_level" => RESOLUTION_LEVEL,
        "declared_in" => @file,
        "syntax" => "rbs",
        "signature" => overloads.join(" | ")
      }
      if overloads.length == 1
        params, return_type = ContractFact.split_arrow(overloads.first)
        props["params"] = params if params
        props["return_type"] = return_type if return_type
      else
        props["overload_count"] = overloads.length
      end
      emit(ContractFact.build(
        receiver: @scopes.join("::"),
        method: method,
        singleton: selfdot == "self.",
        line: number,
        props: props
      ))
      index
    end

    def continues?(lines, index)
      return false if index >= lines.length

      strip_comment(lines[index]).strip.start_with?("|")
    end

    def consume_balanced(line, lines, index)
      counts = Balance.scan(line)
      while !Balance.balanced?(counts) && index < lines.length
        counts = Balance.scan(strip_comment(lines[index]), counts)
        index += 1
      end
      index
    end

    def skip_cause(line)
      SKIP_LINE_CAUSES.each do |pattern, cause|
        return cause if line.match?(pattern)
      end
      nil
    end

    def strip_comment(line)
      quote = nil
      line.each_char.with_index do |char, position|
        if quote
          quote = nil if char == quote
        elsif ['"', "'"].include?(char)
          quote = char
        elsif char == "#"
          return line[0...position]
        end
      end
      line
    end

    def emit(fact)
      @facts << fact
      @census.parsed_declaration
    end
  end

  class SorbetFileParser
    SCOPE_RE = /\A(\s*)(class|module)\s+([A-Z]\w*(?:::[A-Z]\w*)*)(.*)\z/
    SINGLETON_SCOPE_RE = /\A(\s*)class\s*<<\s*self\s*\z/
    DEF_RE = /\A(\s*)def\s+(self\.)?([a-zA-Z_]\w*[?!=]?|\[\]=?|[+\-*\/%<>=!~^&|]+)/
    SIG_RE = /\A(\s*)sig\b[^{]*(\{|do)(.*)\z/
    END_RE = /\A(\s*)end\s*\z/
    HEREDOC_RE = /<<[-~]?(["']?)([A-Z_]\w*)\1/
    TYPE_MEMBER_RE = /=\s*type_(member|template)\b/
    KNOWN_LINKS = %w[abstract override overridable final implementation checked on_failure bind type_parameters params returns void].freeze

    def initialize(file, source, census, syntax)
      @file = file
      @source = source
      @census = census
      @syntax = syntax
      @scopes = []
      @facts = []
      @pending_sig = nil
    end

    def parse
      lines = @source.lines.map(&:chomp)
      index = 0
      while index < lines.length
        line = lines[index]
        number = index + 1
        index += 1
        next if line.strip.start_with?("#")

        if (heredoc = line.match(HEREDOC_RE))
          index = skip_heredoc(lines, index, heredoc[2])
        elsif (match = line.match(SINGLETON_SCOPE_RE))
          @scopes.push(indent: match[1], name: nil, singleton: true)
        elsif (match = line.match(SCOPE_RE))
          handle_scope(match, number)
        elsif (match = line.match(SIG_RE))
          index = handle_sig(match, number, lines, index)
        elsif (match = line.match(DEF_RE))
          handle_def(match, number)
        elsif (match = line.match(END_RE))
          @scopes.pop if !@scopes.empty? && @scopes.last[:indent] == match[1]
        elsif @syntax == "sorbet-rbi" && line.match?(TYPE_MEMBER_RE)
          @census.skip("rbi-type-member")
        end
      end
      @census.skip("sig-unconsumed") if @pending_sig
      @facts
    end

    private

    def handle_scope(match, number)
      indent, keyword, name, rest = match.captures
      return if rest.match?(/;\s*end\s*\z/)

      drop_pending_sig
      @scopes.push(indent: indent, name: name, singleton: false)
      return unless @syntax == "sorbet-rbi"

      superclass = rest.strip.delete_prefix("<").strip if rest.strip.start_with?("<")
      props = {
        "resolution_level" => RESOLUTION_LEVEL,
        "declared_in" => @file,
        "syntax" => @syntax,
        "decl_kind" => keyword
      }
      props["superclass"] = superclass if superclass && !superclass.empty?
      emit("kind" => "symbol", "name" => "rbs-decl: #{qualified_name}", "line" => number, "props" => props)
    end

    def handle_sig(match, number, lines, index)
      indent, opener, remainder = match.captures
      drop_pending_sig
      if opener == "{"
        body = +remainder
        counts = Balance.scan("{#{remainder}")
        while counts[:brace].positive? && index < lines.length
          body << "\n" << lines[index]
          counts = Balance.scan(lines[index], counts)
          index += 1
        end
        if counts[:brace].positive?
          @census.skip("sig-unparsed")
          return index
        end
        body = body[0...body.rindex("}")]
      else
        body = +""
        closed = false
        while index < lines.length
          if lines[index].match?(/\A#{Regexp.escape(indent)}end\s*\z/)
            index += 1
            closed = true
            break
          end
          body << "\n" << lines[index]
          index += 1
        end
        unless closed
          @census.skip("sig-unparsed")
          return index
        end
      end
      @pending_sig = parse_sig_body(body.strip, number)
      index
    end

    def parse_sig_body(body, number)
      links = Balance.split_top_level(body, ".").map(&:strip).reject(&:empty?)
      contract = { params: nil, return_type: nil, type_params: nil, line: number }
      links.each do |link|
        name = link[/\A\w+/]
        unless KNOWN_LINKS.include?(name)
          @census.skip("sig-link-#{name || "unrecognized"}")
          return nil
        end
        argument = link[/\A\w+\s*\((.*)\)\s*\z/m, 1]
        case name
        when "params"
          entries = Balance.split_top_level(argument.to_s, ",").map { |entry| entry.strip.squeeze(" ") }.reject(&:empty?)
          if entries.empty?
            @census.skip("sig-params-empty")
            return nil
          end
          contract[:params] = entries
        when "returns"
          contract[:return_type] = argument.to_s.strip.squeeze(" ")
        when "void"
          contract[:return_type] = "void"
        when "type_parameters"
          contract[:type_params] = Balance.split_top_level(argument.to_s, ",").map { |entry| entry.strip.delete_prefix(":") }
        end
      end
      if contract[:return_type].nil?
        @census.skip("sig-without-return")
        return nil
      end
      contract
    end

    def handle_def(match, number)
      _, selfdot, method = match.captures
      unless @pending_sig
        @census.skip("rbi-def-without-sig") if @syntax == "sorbet-rbi"
        return
      end
      sig = @pending_sig
      @pending_sig = nil
      receiver = qualified_name
      if receiver.empty?
        @census.skip("sig-outside-scope")
        return
      end
      params = sig[:params] || []
      props = {
        "resolution_level" => RESOLUTION_LEVEL,
        "declared_in" => @file,
        "syntax" => @syntax,
        "signature" => "(#{params.join(", ")}) -> #{sig[:return_type]}",
        "params" => params,
        "return_type" => sig[:return_type]
      }
      props["type_params"] = sig[:type_params] if sig[:type_params]
      emit(ContractFact.build(
        receiver: receiver,
        method: method,
        singleton: selfdot == "self." || @scopes.any? { |scope| scope[:singleton] },
        line: number,
        props: props
      ))
    end

    def drop_pending_sig
      return unless @pending_sig

      @census.skip("sig-unconsumed")
      @pending_sig = nil
    end

    def qualified_name
      @scopes.map { |scope| scope[:name] }.compact.join("::")
    end

    def skip_heredoc(lines, index, terminator)
      while index < lines.length
        return index + 1 if lines[index].strip == terminator

        index += 1
      end
      index
    end

    def emit(fact)
      @facts << fact
      @census.parsed_declaration
    end
  end

  def self.candidate_files(root, pattern)
    Dir.glob(pattern, base: root).reject do |rel|
      rel.split("/").any? { |segment| SKIP_SEGMENTS.include?(segment) }
    end.sort
  end

  def self.run(root)
    census = Census.new
    lines = []
    collect = lambda do |rel, parser|
      parser.parse.each do |fact|
        fact["file"] = rel
        lines << JSON.generate(fact)
      end
    end

    candidate_files(root, File.join("**", "*.rbs")).each do |rel|
      source = read_or_skip(root, rel, census)
      next unless source

      census.saw_file
      collect.call(rel, RbsFileParser.new(rel, source, census))
    end
    candidate_files(root, File.join("**", "*.rbi")).each do |rel|
      source = read_or_skip(root, rel, census)
      next unless source

      census.saw_file
      collect.call(rel, SorbetFileParser.new(rel, source, census, "sorbet-rbi"))
    end
    candidate_files(root, File.join("**", "*.rb")).each do |rel|
      source = read_or_skip(root, rel, census)
      next unless source

      census.saw_file
      next unless source.match?(/^\s*sig\b/)

      collect.call(rel, SorbetFileParser.new(rel, source, census, "sorbet-sig"))
    end

    puts lines.sort
    census.emit
  end

  def self.read_or_skip(root, rel, census)
    File.read(File.join(root, rel))
  rescue StandardError
    census.skip("file-unreadable")
    nil
  end
end

RbsProvider.run(root)
