output "static_ip" {
  description = "Public IPv4 address. Point an A record for each site here unless route53_zone_id is set."
  value       = aws_lightsail_static_ip.proxy.ip_address
}

output "urls" {
  description = "Public URLs."
  value       = [for host, site in var.sites : "https://${host}${trimsuffix(site.strip_prefix, "/")}/"]
}

output "ssh" {
  description = "Shell access over the tailnet."
  value       = "ssh ubuntu@${var.name}"
}
