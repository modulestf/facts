check "health" {
  data "http" "this" {
    url = "https://example.com"
  }

  assert {
    condition     = data.http.this.status_code == 200
    error_message = "unhealthy"
  }
}
