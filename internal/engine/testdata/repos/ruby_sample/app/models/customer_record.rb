class CustomerRecord < Sequel::Model(:customers)
  def display_name
    name.upcase
  end
end
