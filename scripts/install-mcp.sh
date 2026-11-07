#!/bin/sh
set -eu
exec /usr/bin/python3 -I "$(dirname -- "$0")/../deploy/mcp/manage.py" install "$@"
