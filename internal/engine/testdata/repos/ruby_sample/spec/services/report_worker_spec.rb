# frozen_string_literal: true

# Item 3 (v18): a spec file references production code. The extractor's test-ref
# pass emits a single KindTestRef fact carrying only the outbound RelCalls edges
# (Reporting::ReportWorker.build / .new / #summarize), so those production
# symbols are seen as exercised and never mis-reported as dead. The spec emits
# NO symbol facts of its own.
RSpec.describe Reporting::ReportWorker do
  it "builds a report by type" do
    expect(Reporting::ReportWorker.build(:daily)).to be_present
  end

  it "summarizes users" do
    worker = Reporting::ReportWorker.new
    worker.summarize([])
  end
end
