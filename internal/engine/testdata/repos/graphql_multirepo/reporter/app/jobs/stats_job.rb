class StatsJob
  def stats_query
    "query {
      pageViews {
        total
      }
    }"
  end

  def company_doc
    <<~GQL
      query {
        company {
          name
        }
      }
    GQL
  end
end
