variable "aws_profile" {
  description = "AWS CLI profile to create the instance with."
  type        = string
  default     = "cax-dev"
}

variable "aws_region" {
  description = "Lightsail region."
  type        = string
  default     = "ap-southeast-1"
}

variable "name" {
  description = "Name for the Lightsail resources and the Tailscale hostname."
  type        = string
  default     = "downloads-proxy"
}

variable "bundle_id" {
  description = "Lightsail plan. nano_3_0 is the cheapest with a public IPv4 address ($5/month)."
  type        = string
  default     = "nano_3_0"
}

variable "blueprint_id" {
  description = "Lightsail OS image."
  type        = string
  default     = "ubuntu_24_04"
}

variable "sites" {
  description = <<-EOT
    Public hostnames served by the proxy, keyed by hostname. Caddy gets a Let's Encrypt certificate for each.
    upstream is a tailnet address: host:port for plain HTTP, or an https:// URL (e.g. a Tailscale Serve endpoint).
    strip_prefix is the BASE_PATH an app is configured with; the proxy strips it before forwarding, like a Funnel mount does.
  EOT
  type = map(object({
    upstream     = string
    strip_prefix = optional(string, "")
  }))
}

variable "tailscale_auth_key" {
  description = "Tailscale auth key the instance joins the tailnet with. Use a reusable, ephemeral key so the instance can be destroyed and recreated at will."
  type        = string
  sensitive   = true
}

variable "ssh_allowed_cidrs" {
  description = "CIDRs allowed to reach port 22. Empty keeps it closed; use Tailscale SSH instead."
  type        = list(string)
  default     = []
}

variable "route53_zone_id" {
  description = "Optional Route 53 hosted zone ID. When set, an A record for each site is managed here; otherwise point DNS at the static_ip output yourself."
  type        = string
  default     = ""
}
