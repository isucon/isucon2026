terraform {
  required_version = ">= 1.5"

  required_providers {
    sakuracloud = {
      source  = "sacloud/sakuracloud"
      version = "2.31.2"
    }
  }
}

# usacloud のプロファイル（`usacloud config create --name iqz41186_isucon`）で認証する。
# isucon2026-portal-infra と同じ ISUCON プロジェクトの API キー（権限: 作成・削除）。
provider "sakuracloud" {
  profile = var.config[local.stage].profile
  zone    = local.zone
}
