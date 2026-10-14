#!/bin/sh
set -eu

daemon="$1"
shift
case "$daemon" in
    kea-dhcp4|kea-dhcp6|kea-dhcp-ddns) ;;
    *) echo "Unknown Kea daemon: $daemon" >&2; exit 1 ;;
esac

resolve() {
    attempts=0
    while :; do
        address="$(getent ahostsv4 "$1" | awk 'NR == 1 { print $1 }')"
        if [ -n "$address" ]; then
            printf '%s\n' "$address"
            return
        fi
        attempts=$((attempts + 1))
        if [ "$attempts" -ge 30 ]; then
            echo "Cannot resolve Docker service: $1" >&2
            return 1
        fi
        sleep 1
    done
}

self_address="$(resolve "$(hostname)")"
dns_address=""
ddns_address=""
case "$daemon" in
    kea-dhcp-ddns) dns_address="$(resolve dns)" ;;
    *) ddns_address="$(resolve ddns)" ;;
esac

mkdir -p /run/kea /var/lib/kea
runtime="/run/kea/$daemon.conf"
sed -e "s/__SELF_ADDRESS__/$self_address/g" \
    -e "s/__DNS_ADDRESS__/$dns_address/g" \
    -e "s/__DDNS_ADDRESS__/$ddns_address/g" \
    "/config/$daemon.conf" > "$runtime"

# Kea 2.6 uses one UNIX control socket; the agent shares the socket volume.
if [ "${KEA_INTEGRATION_MODE:-direct}" = "control-agent" ]; then
    sed '/"control-sockets": \[/,/\],/c\
    "control-socket": { "socket-type": "unix", "socket-name": "/run/kea/'"$daemon"'.sock" },
' "$runtime" > "$runtime.legacy"
    mv "$runtime.legacy" "$runtime"
    case "${1:-}" in
        -t|-T) exec "$daemon" "$1" "$runtime" ;;
        *) exec "$daemon" -c "$runtime" "$@" ;;
    esac
fi

# Validation uses the rendered configuration and the same runtime policy.
case "${1:-}" in
    -t|-T) exec "$daemon" -X "$1" "$runtime" ;;
    *) exec "$daemon" -X -c "$runtime" "$@" ;;
esac
