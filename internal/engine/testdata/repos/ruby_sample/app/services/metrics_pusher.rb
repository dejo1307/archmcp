class MetricsPusher
  def push(attributes)
    connection.post(build_url("/pageview"), attributes)
  end

  def labels
    connection.get(t("labels.metrics"))
  end

  private

  def build_url(path)
    "#{ENV["METRICS_HOST_URL"]}#{path}"
  end

  def connection
    Faraday.new
  end
end
