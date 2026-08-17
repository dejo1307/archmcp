#!/usr/bin/env ruby
# frozen_string_literal: true

# enola Zeitwerk reference provider.
#
# Replicates Zeitwerk's autoloading convention WITHOUT booting the application:
# every .rb file under an autoload root maps to the constant Zeitwerk would
# expect it to define, derived purely from the path. No runtime, no gems, no
# guessing — the mapping is the convention itself:
#
#   app/models/user.rb                    -> User
#   app/models/billing/invoice_item.rb    -> Billing::InvoiceItem
#   app/controllers/users_controller.rb   -> UsersController
#   app/models/concerns/archivable.rb     -> Archivable   (concerns collapse)
#
# Autoload roots are the child directories of app/ (excluding assets/,
# javascript/ and views/, which Rails never autoloads), with a first-level
# concerns/ directory collapsed the way Rails collapses app/*/concerns. lib/
# joins the roots only when a Zeitwerk marker exists — config/application.rb
# mentioning autoload_lib, or the Gemfile declaring the zeitwerk gem — and then
# its assets/, tasks/ and generators/ subtrees are left out, matching Rails'
# own autoload_lib ignore defaults.
#
# Camelization is Zeitwerk's acronym-free default: each underscore-separated
# part is capitalized ("api_client" -> "ApiClient", never "APIClient"),
# because without booting the app no inflector overrides can be known. The
# acronym edge is left fail-closed: a path segment outside the plain
# lowercase_snake shape (a dash, an uppercase letter, a leading digit) means
# the constant cannot be derived confidently, so the file emits NOTHING and is
# counted in a one-line stderr summary the seam ignores on success.
#
# Facts are kind "dependency" named "zeitwerk-map: <Constant> -> <path>", each
# carrying one depends_on relation targeting the constant — depends_on because
# it is in the seam's closed relation vocabulary — with the autoload semantic
# declared in props (mapping: autoload) and resolution_level
# "convention-derived": the honest level, a convention applied, not code
# observed defining the constant.
#
# Determinism: roots and files are enumerated in sorted order, output lines
# are sorted before printing, and nothing time- or environment-dependent is
# emitted. vendor/, node_modules/ and tmp/ subtrees are skipped at any depth.
# --version prints a fixed semver on stdout and exits, which is how the seam
# learns what build it is talking to.

require "json"

PROVIDER_VERSION = "0.1.0"

if ARGV.include?("--version")
  puts PROVIDER_VERSION
  exit 0
end

root = ARGV[0]
abort "usage: enola_zeitwerk_provider.rb <repo-path>" unless root && File.directory?(root)

SKIP_SEGMENTS = %w[vendor node_modules tmp].freeze
NON_AUTOLOAD_APP_DIRS = %w[assets javascript views].freeze
LIB_IGNORED_SUBTREES = %w[assets tasks generators].freeze
PLAIN_SEGMENT_RE = /\A[a-z][a-z0-9]*(?:_[a-z0-9]+)*\z/

def camelize(segment)
  segment.split("_").map(&:capitalize).join
end

def zeitwerk_marker?(root)
  application_rb = File.join(root, "config", "application.rb")
  begin
    return true if File.file?(application_rb) && File.read(application_rb).include?("autoload_lib")
  rescue StandardError
    nil
  end
  gemfile = File.join(root, "Gemfile")
  begin
    return true if File.file?(gemfile) && File.read(gemfile).match?(/^\s*gem\s+["']zeitwerk["']/)
  rescue StandardError
    nil
  end
  false
end

roots = []
app = File.join(root, "app")
if File.directory?(app)
  Dir.children(app).sort.each do |child|
    next if NON_AUTOLOAD_APP_DIRS.include?(child)
    next unless File.directory?(File.join(app, child))

    roots << File.join("app", child)
  end
end
roots << "lib" if File.directory?(File.join(root, "lib")) && zeitwerk_marker?(root)

lines = []
skipped = 0

roots.each do |rel_root|
  files = Dir.glob(File.join("**", "*.rb"), base: File.join(root, rel_root)).sort
  files.each do |rel|
    segments = rel.split("/")
    next if segments.any? { |segment| SKIP_SEGMENTS.include?(segment) }
    next if rel_root == "lib" && LIB_IGNORED_SUBTREES.include?(segments.first)

    segments[-1] = segments[-1].delete_suffix(".rb")
    segments.shift if rel_root.start_with?("app/") && segments.first == "concerns" && segments.length > 1
    unless !segments.empty? && segments.all? { |segment| PLAIN_SEGMENT_RE.match?(segment) }
      skipped += 1
      next
    end

    constant = segments.map { |segment| camelize(segment) }.join("::")
    path = File.join(rel_root, rel)
    lines << JSON.generate(
      "kind" => "dependency",
      "name" => "zeitwerk-map: #{constant} -> #{path}",
      "file" => path,
      "props" => { "resolution_level" => "convention-derived", "mapping" => "autoload" },
      "relations" => [{ "kind" => "depends_on", "target" => constant }]
    )
  end
end

warn "zeitwerk-map: skipped #{skipped} file(s) whose constant could not be derived confidently" if skipped.positive?
puts lines.sort
