module Types
  class QueryType < Types::BaseObject
    field :page_views, [Types::PageViewType], null: false
    field :company, Types::CompanyType
  end
end
