#!/bin/sh
set -eu
routes.sh
exec docker-entrypoint.sh "$@"
