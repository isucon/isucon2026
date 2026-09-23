#!/bin/bash
# ansible-vault のパスワードを返す。
# - ANSIBLE_VAULT_PASSWORD があればそれを使う（CI や一時的な作業用）
# - 無ければ 1Password CLI（op）から読む。項目のパスは PORTAL_VAULT_OP_PATH で指定する
#   （例: op://<vault>/<item>/password。isucon2026-portal-infra と同じ項目を使ってよい）
set -euo pipefail

if [[ -n "${ANSIBLE_VAULT_PASSWORD:-}" ]]; then
  printf '%s' "${ANSIBLE_VAULT_PASSWORD}"
  exit 0
fi

if [[ -z "${PORTAL_VAULT_OP_PATH:-}" ]]; then
  echo "set ANSIBLE_VAULT_PASSWORD or PORTAL_VAULT_OP_PATH (op:// reference)" >&2
  exit 1
fi

op --account "${PORTAL_VAULT_OP_ACCOUNT:-ccsakura.1password.com}" read "${PORTAL_VAULT_OP_PATH}"
