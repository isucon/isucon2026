################################################################################
# app サーバ（portal / worker / bot / notifyloop / PostgreSQL / Redis を同居）
################################################################################

resource "sakuracloud_disk" "app_disk" {
  zone              = local.zone
  count             = local.app.count
  name              = "app-${format("%03d", count.index + 1)}.${local.dns_subhost}.${local.dns_zone}"
  source_archive_id = data.sakuracloud_archive.ubuntu.id
  size              = local.app.disk
  plan              = "ssd"
  tags              = local.default_tags

  timeouts {
    create = "30m"
  }

  # source_archive_id が変更になった場合も VM 再作成は行わない
  lifecycle {
    ignore_changes = [
      source_archive_id,
    ]
  }
}

resource "sakuracloud_server" "app_server" {
  zone   = local.zone
  count  = local.app.count
  name   = "app-${format("%03d", count.index + 1)}.${local.dns_subhost}.${local.dns_zone}"
  disks  = [element(sakuracloud_disk.app_disk.*.id, count.index)]
  core   = local.app.core
  memory = local.app.memory
  # class=app は ansible の dynamic inventory がグループ名として使う
  tags = concat(["class=app", "__with_sacloud_inventory", local.zone], local.default_tags)

  network_interface {
    upstream         = "shared"
    packet_filter_id = sakuracloud_packet_filter.app_filter.id
  }

  timeouts {
    create = "30m"
  }

  # ssh_key_ids 等が変更になった場合も VM 再作成は行わない
  lifecycle {
    ignore_changes = [
      disk_edit_parameter,
    ]
  }

  disk_edit_parameter {
    hostname        = "app-${format("%03d", count.index + 1)}"
    password        = local.initial_password
    disable_pw_auth = true
    ssh_key_ids     = local.ssh_key_ids
  }
}

# サーバ本体の DNS レコード（ansible / CI が SSH する先）
resource "sakuracloud_dns_record" "app_server_record" {
  count  = local.app.count
  dns_id = data.sakuracloud_dns.dns_zone.id
  name   = "app-${format("%03d", count.index + 1)}.${local.dns_subhost}"
  type   = "A"
  value  = element(sakuracloud_server.app_server.*.ip_address, count.index)
  ttl    = local.dns_ttl
}

################################################################################
# エンハンスド LB（TLS 終端 + Let's Encrypt）
################################################################################

resource "sakuracloud_proxylb" "app_proxylb" {
  name           = "${terraform.workspace}-2026-contest-portal"
  plan           = 100
  vip_failover   = true
  sticky_session = false
  gzip           = false
  timeout        = 10
  region         = "is1"

  description = "proxylb for ISUCON2026 contest portal app-xxx servers"
  tags        = concat(["app=2026-contest-portal"], local.default_tags)

  # portal は /site/sha でリビジョンを返す（認証なし）
  health_check {
    protocol    = "http"
    delay_loop  = 10
    host_header = local.app_fqdn
    path        = "/site/sha"
  }

  bind_port {
    proxy_mode        = "http"
    port              = 80
    redirect_to_https = true
  }

  bind_port {
    proxy_mode    = "https"
    port          = 443
    support_http2 = true
  }

  dynamic "server" {
    for_each = sakuracloud_server.app_server
    content {
      ip_address = server.value.ip_address
      port       = 80
      group      = "app"
    }
  }

  # ベンチマーカー・envcheck・選手が叩く API は接続元制限の対象外にしない（全体を一括で制御する）
  rule {
    action     = "forward"
    path       = "/*"
    group      = "app"
    source_ips = local.proxy_lb_allowed_source_ips
  }

  rule {
    action             = "fixed"
    fixed_content_type = "text/plain"
    fixed_message_body = "service is under maintenance"
    fixed_status_code  = "403"
    path               = "/*"
  }
}

resource "sakuracloud_dns_record" "app_proxylb_record" {
  dns_id = data.sakuracloud_dns.dns_zone.id
  name   = local.dns_subhost
  type   = "ALIAS"
  value  = "${sakuracloud_proxylb.app_proxylb.fqdn}."
}

resource "sakuracloud_proxylb_acme" "app_proxylb_acme" {
  proxylb_id        = sakuracloud_proxylb.app_proxylb.id
  accept_tos        = true
  common_name       = local.app_fqdn
  subject_alt_names = []
  update_delay_sec  = 40
}

################################################################################
# 出力
################################################################################

output "app_fqdn" {
  value = local.app_fqdn
}

output "app_servers" {
  value = {
    for s in sakuracloud_server.app_server : s.name => s.ip_address
  }
}

output "proxylb_fqdn" {
  value = sakuracloud_proxylb.app_proxylb.fqdn
}
