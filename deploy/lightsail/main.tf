resource "aws_lightsail_instance" "proxy" {
  name              = var.name
  availability_zone = "${var.aws_region}a"
  blueprint_id      = var.blueprint_id
  bundle_id         = var.bundle_id

  # Changing user_data replaces the instance.
  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    hostname           = var.name
    sites              = var.sites
    tailscale_auth_key = var.tailscale_auth_key
  })

  # Leave the tailnet before the instance goes away, so its name is free for the next one.
  provisioner "local-exec" {
    when       = destroy
    on_failure = continue
    command    = "ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null ubuntu@${self.name} sudo tailscale logout"
  }
}

# Free while attached; keeps the address stable across stop/start and reboots.
resource "aws_lightsail_static_ip" "proxy" {
  name = "${var.name}-ip"
}

resource "aws_lightsail_static_ip_attachment" "proxy" {
  static_ip_name = aws_lightsail_static_ip.proxy.name
  instance_name  = aws_lightsail_instance.proxy.name

  # Both refer to the instance by name, so a replaced instance would otherwise be left without them.
  lifecycle {
    replace_triggered_by = [aws_lightsail_instance.proxy]
  }
}

# Replaces Lightsail's default firewall (which opens 22 and 80 to the world).
resource "aws_lightsail_instance_public_ports" "proxy" {
  instance_name = aws_lightsail_instance.proxy.name

  # Port 80 is needed for the ACME HTTP challenge and the redirect to HTTPS.
  port_info {
    protocol   = "tcp"
    from_port  = 80
    to_port    = 80
    cidrs      = ["0.0.0.0/0"]
    ipv6_cidrs = ["::/0"]
  }

  port_info {
    protocol   = "tcp"
    from_port  = 443
    to_port    = 443
    cidrs      = ["0.0.0.0/0"]
    ipv6_cidrs = ["::/0"]
  }

  dynamic "port_info" {
    for_each = length(var.ssh_allowed_cidrs) > 0 ? [1] : []
    content {
      protocol  = "tcp"
      from_port = 22
      to_port   = 22
      cidrs     = var.ssh_allowed_cidrs
    }
  }

  depends_on = [aws_lightsail_static_ip_attachment.proxy]

  lifecycle {
    replace_triggered_by = [aws_lightsail_instance.proxy]
  }
}

resource "aws_route53_record" "proxy" {
  for_each = var.route53_zone_id != "" ? var.sites : {}

  zone_id = var.route53_zone_id
  name    = each.key
  type    = "A"
  ttl     = 60
  records = [aws_lightsail_static_ip.proxy.ip_address]
}
