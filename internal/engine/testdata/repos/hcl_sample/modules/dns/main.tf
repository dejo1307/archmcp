variable "zone" {
  type = string
}

output "fqdn" {
  value = var.zone
}
