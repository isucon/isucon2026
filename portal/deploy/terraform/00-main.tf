# ISUCON2026 競技ポータル（portal/）のさくらのクラウド構成。
# isucon2026-portal-infra（参加登録ポータル）の terraform を踏襲し、
# develop / production を terraform workspace で切り替える。
#
# 構成: app サーバ 1 台（portal / worker / bot / notifyloop / PostgreSQL / Redis を Docker Swarm で同居）
#       + エンハンスド LB（TLS 終端・Let's Encrypt）+ さくらの DNS（isucon.net ゾーン）

# 予め手動作成したリソースを記載する
variable "predefined" {
  default = {
    develop = {
      # コントロールパネル「公開鍵」に登録済みの SSH 公開鍵の ID（初期ユーザ ubuntu 用）
      # TODO: 運用者の鍵 ID を記載する（isucon2026-portal-infra の 113801576286 を流用するならそれを書く）
      ssh_key_ids = []
      dns_zone    = "isucon.net"
      dns_subhost = "contest-dev"
      app_fqdn    = "contest-dev.isucon.net"
    }
    production = {
      ssh_key_ids = []
      dns_zone    = "isucon.net"
      dns_subhost = "contest"
      app_fqdn    = "contest.isucon.net"
    }
  }
}

# 初期ユーザ ubuntu のパスワード。ansible の init role が admin ユーザを作って ubuntu を無効化するまでの
# 一時的なもので、sudo にのみ使う。リポジトリには置かず TF_VAR_initial_password で渡す
# （ansible 側は同じ値を PORTAL_INITIAL_PASSWORD で受ける）。
variable "initial_password" {
  type      = string
  sensitive = true
}

# 構成するクラスタの設定
variable "config" {
  default = {
    develop = {
      profile      = "iqz41186_isucon"
      default_tags = ["isucon", "sys=2026-contest-portal", "env=develop"]
      dns_ttl      = 60
      # ELB の "/*" を通す接続元。空なら全開放
      proxy_lb_allowed_source_ips = ""
      # SSH(22) を許可する接続元。空なら全開放
      ssh_allowed_source_networks = []
      app = {
        core   = 2
        memory = 4
        disk   = 60
        count  = 1
      }
    }
    production = {
      profile                     = "iqz41186_isucon"
      default_tags                = ["isucon", "sys=2026-contest-portal", "env=production"]
      dns_ttl                     = 60
      proxy_lb_allowed_source_ips = ""
      ssh_allowed_source_networks = []
      app = {
        core   = 4
        memory = 8
        disk   = 100
        count  = 1
      }
    }
  }
}

variable "workspaces" {
  default = {
    develop = {
      stage = "develop"
      zone  = "is1c" # 石狩第 3 ゾーン
    }
    production = {
      stage = "production"
      zone  = "is1c"
    }
  }
}

################################################################################
# input variable から作る local value
################################################################################

locals {
  stage = var.workspaces[terraform.workspace].stage
  zone  = var.workspaces[terraform.workspace].zone
}

locals {
  ssh_key_ids = var.predefined[local.stage].ssh_key_ids
  dns_subhost = var.predefined[local.stage].dns_subhost
  dns_zone    = var.predefined[local.stage].dns_zone
  app_fqdn    = var.predefined[local.stage].app_fqdn

  default_tags     = var.config[local.stage].default_tags
  initial_password = var.initial_password
  dns_ttl          = var.config[local.stage].dns_ttl

  proxy_lb_allowed_source_ips = var.config[local.stage].proxy_lb_allowed_source_ips
  ssh_allowed_source_networks = var.config[local.stage].ssh_allowed_source_networks

  app = var.config[local.stage].app

  # エンハンスド LB とシンプル監視の接続元（さくら公開のアドレス帯）
  elb_source_networks = [
    "153.120.87.224/27",
    "153.120.89.160/27",
    "153.125.175.224/27",
  ]
  simple_monitor_source_networks = ["27.133.139.32/28"]
}

################################################################################
# さくらのクラウド側の現在状態から引いてくる情報
################################################################################

data "sakuracloud_dns" "dns_zone" {
  filter {
    names = [local.dns_zone]
  }
}

data "sakuracloud_archive" "ubuntu" {
  zone = local.zone
  filter {
    tags = ["ubuntu-26.04-latest"]
  }
}

################################################################################
# パケットフィルタ（app サーバ）
################################################################################

resource "sakuracloud_packet_filter" "app_filter" {
  name        = "ISUCON2026 Contest Portal ${terraform.workspace}"
  description = "ISUCON2026 Contest Portal ${terraform.workspace}"

  expression {
    protocol = "icmp"
  }

  expression {
    protocol = "fragment"
  }

  expression {
    protocol    = "udp"
    source_port = "123"
    description = "NTP"
  }

  # SSH: 接続元を絞る設定があればそれだけ、無ければ全開放
  dynamic "expression" {
    for_each = length(local.ssh_allowed_source_networks) > 0 ? local.ssh_allowed_source_networks : [""]
    content {
      protocol         = "tcp"
      destination_port = "22"
      source_network   = expression.value != "" ? expression.value : null
      description      = "SSH"
    }
  }

  expression {
    protocol         = "tcp"
    destination_port = "53"
    description      = "DNS"
  }

  expression {
    protocol         = "udp"
    destination_port = "53"
    description      = "DNS"
  }

  dynamic "expression" {
    for_each = local.elb_source_networks
    content {
      protocol         = "tcp"
      destination_port = "80"
      source_network   = expression.value
      description      = "ELB (HTTP)"
    }
  }

  dynamic "expression" {
    for_each = local.simple_monitor_source_networks
    content {
      protocol         = "tcp"
      destination_port = "80"
      source_network   = expression.value
      description      = "simple monitor"
    }
  }

  expression {
    protocol         = "tcp"
    destination_port = "32768-61000"
    description      = "ephemeral (return traffic)"
  }

  expression {
    protocol         = "udp"
    destination_port = "32768-61000"
    description      = "ephemeral (return traffic)"
  }

  expression {
    protocol    = "ip"
    allow       = false
    description = "Deny ALL"
  }
}
