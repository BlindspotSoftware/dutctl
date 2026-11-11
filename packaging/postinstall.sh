#!/bin/sh
set -e

systemd-sysusers /usr/lib/sysusers.d/dutagent.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/dutagent.conf

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
fi

# Apply dutagent-usb-ports.rules to the hubs that are already attached.
if command -v udevadm >/dev/null 2>&1; then
	udevadm control --reload || true
	udevadm trigger --subsystem-match=usb || true
fi

