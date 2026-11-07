#!/bin/sh
set -eu
if [ "$#" -ne 0 ]; then
    echo 'usage: uninstall-mcp.sh' >&2
    exit 64
fi
exec /usr/bin/python3 -I "$(dirname -- "$0")/../deploy/mcp/manage.py" uninstall
