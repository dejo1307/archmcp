variable "region" {
  type = string
}

resource "aws_vpc" "core" {
  cidr_block = "10.0.0.0/16"
}

resource "aws_instance" "web" {
  subnet_id  = aws_vpc.core.id
  region     = var.region
  depends_on = [aws_vpc.core]
}

output "web_ip" {
  value = aws_instance.web.public_ip
}

module "dns" {
  source = "./modules/dns"
  zone   = var.region
}
