#!/bin/sh
set -e

systemd-sysusers /usr/lib/sysusers.d/dutagent.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/dutagent.conf

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
fi

