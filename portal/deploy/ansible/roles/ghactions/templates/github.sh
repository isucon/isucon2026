#!/bin/bash
set -euo pipefail
# GitHub Actions（portal-deploy.yml）から `sudo.ws /opt/bin/github.sh <service>` で呼ばれる

service=$1

case "${service}" in
  # 同一タグの再 push でも digest を解決してローリングアップデートする
  "app" )
    /usr/bin/docker service update --with-registry-auth --force --detach=false --image {{ app.image }} {{ app.stack }}_portal
    /usr/bin/docker service update --with-registry-auth --force --detach=false --image {{ app.image }} {{ app.stack }}_worker
    /usr/bin/docker service update --with-registry-auth --force --detach=false --image {{ app.image }} {{ app.stack }}_notifyloop
    if /usr/bin/docker service inspect {{ app.stack }}_bot > /dev/null 2>&1; then
      /usr/bin/docker service update --with-registry-auth --force --detach=false --image {{ app.image }} {{ app.stack }}_bot
    fi
    ;;
  * )
    echo "invalid argument"
    exit 1
esac
