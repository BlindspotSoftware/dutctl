#!/bin/sh
set -e

systemd-sysusers /usr/lib/sysusers.d/dutagent.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/dutagent.conf

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    # On upgrade, dpkg calls postinst with "configure <old-version>".
    if [ "$1" = "configure" ] && [ -n "$2" ]; then
        systemctl try-restart dutagent.service || true
    fi
fi
