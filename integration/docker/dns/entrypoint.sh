#!/bin/sh
set -eu

mkdir -p /var/cache/bind/primary /var/run/named
chown bind:bind /var/cache/bind /var/cache/bind/primary /var/run/named
chmod 750 /var/cache/bind/primary
for seed in /config/zones/db.*; do
    target="/var/cache/bind/primary/$(basename "$seed")"
    # Preserve dynamic records and journals across container recreation.
    if [ ! -f "$target" ]; then
        cp "$seed" "$target"
    fi
done
chown -R bind:bind /var/cache/bind/primary
named-checkconf -z /etc/bind/named.conf
exec named -u bind -c /etc/bind/named.conf -g
