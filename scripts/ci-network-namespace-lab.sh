#!/usr/bin/env bash
set -Eeuo pipefail

OUTPUT_DIR="${1:-build/deep-test/network}"
mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

for command in ip nft dnsmasq busybox dig iperf3 nmap nc jq python3 ping timeout; do
	command -v "$command" >/dev/null 2>&1 || {
		echo "Missing required command: $command" >&2
		exit 1
	}
done

if [ "$(id -u)" -ne 0 ]; then
	echo "Network namespace lab must run as root" >&2
	exit 1
fi

suffix="$$"
router_ns="mr-router-$suffix"
wan_ns="mr-wan-$suffix"
lan_ns="mr-lan-$suffix"
router_wan="mrwr$suffix"
wan_peer="mrww$suffix"
router_lan="mrlr$suffix"
lan_peer="mrlw$suffix"

cleanup() {
	set +e
	ip netns delete "$lan_ns" 2>/dev/null
	ip netns delete "$wan_ns" 2>/dev/null
	ip netns delete "$router_ns" 2>/dev/null
}
trap cleanup EXIT INT TERM
cleanup

ip netns add "$router_ns"
ip netns add "$wan_ns"
ip netns add "$lan_ns"

ip link add "$router_wan" type veth peer name "$wan_peer"
ip link add "$router_lan" type veth peer name "$lan_peer"
ip link set "$router_wan" netns "$router_ns"
ip link set "$router_lan" netns "$router_ns"
ip link set "$wan_peer" netns "$wan_ns"
ip link set "$lan_peer" netns "$lan_ns"

ip -n "$router_ns" link set "$router_wan" name wan0
ip -n "$router_ns" link set "$router_lan" name lan0
ip -n "$wan_ns" link set "$wan_peer" name wan0
ip -n "$lan_ns" link set "$lan_peer" name lan0

for ns in "$router_ns" "$wan_ns" "$lan_ns"; do
	ip -n "$ns" link set lo up
done
ip -n "$router_ns" link set wan0 up
ip -n "$router_ns" link set lan0 up
ip -n "$wan_ns" link set wan0 up
ip -n "$lan_ns" link set lan0 up

ip -n "$router_ns" address add 198.18.0.2/24 dev wan0
ip -n "$router_ns" address add 192.0.2.1/24 dev lan0
ip -n "$wan_ns" address add 198.18.0.1/24 dev wan0
ip -n "$wan_ns" route add 192.0.2.0/24 via 198.18.0.2
ip netns exec "$router_ns" sysctl -q -w net.ipv4.ip_forward=1
ip netns exec "$router_ns" sysctl -q -w net.ipv4.conf.all.rp_filter=1
ip netns exec "$router_ns" sysctl -q -w net.ipv4.conf.default.rp_filter=1

ip netns exec "$router_ns" nft -f - <<'NFT'
table inet minimalrouter_test {
	chain input {
		type filter hook input priority filter; policy drop;
		iifname "lo" accept
		ct state invalid drop
		ct state established,related accept
		iifname "lan0" udp dport { 53, 67 } accept
		iifname "lan0" tcp dport 53 accept
		iifname "lan0" ip protocol icmp icmp type echo-request accept
	}

	chain forward {
		type filter hook forward priority filter; policy drop;
		ct state invalid drop
		ct state established,related accept
		iifname "lan0" oifname "wan0" accept
	}
}

table ip minimalrouter_test_nat {
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		oifname "wan0" masquerade
	}
}
NFT

cat >"$OUTPUT_DIR/udhcpc-script.sh" <<'UDHCPC'
#!/bin/sh
set -eu
case "$1" in
	bound|renew)
		ip address flush dev "$interface"
		ip address add "$ip/24" dev "$interface"
		if [ -n "${router:-}" ]; then
			set -- $router
			ip route replace default via "$1" dev "$interface"
		fi
		;;
esac
UDHCPC
chmod 700 "$OUTPUT_DIR/udhcpc-script.sh"

ip netns exec "$router_ns" dnsmasq \
	--keep-in-foreground \
	--conf-file=/dev/null \
	--user=root \
	--interface=lan0 \
	--bind-interfaces \
	--listen-address=192.0.2.1 \
	--dhcp-authoritative \
	--dhcp-range=192.0.2.100,192.0.2.150,255.255.255.0,1h \
	--dhcp-option=3,192.0.2.1 \
	--dhcp-option=6,192.0.2.1 \
	--address=/router.test/192.0.2.1 \
	--no-resolv \
	--dhcp-leasefile="$OUTPUT_DIR/dnsmasq.leases" \
	>"$OUTPUT_DIR/dnsmasq.log" 2>&1 &
dnsmasq_pid=$!
sleep 1
kill -0 "$dnsmasq_pid"

ip netns exec "$lan_ns" busybox udhcpc \
	-i lan0 \
	-s "$OUTPUT_DIR/udhcpc-script.sh" \
	-n -q -t 5 -T 1 \
	>"$OUTPUT_DIR/udhcpc.log" 2>&1

lan_address="$(ip -n "$lan_ns" -4 -o address show dev lan0 scope global | awk '{print $4}')"
case "$lan_address" in
	192.0.2.*/*) ;;
	*)
		echo "DHCP did not assign a LAN address: $lan_address" >&2
		exit 1
		;;
esac

dns_answer="$(ip netns exec "$lan_ns" dig +time=2 +tries=1 +short @192.0.2.1 router.test A | tail -n1)"
if [ "$dns_answer" != "192.0.2.1" ]; then
	echo "DNS test returned $dns_answer instead of 192.0.2.1" >&2
	exit 1
fi

ip netns exec "$wan_ns" iperf3 -s -D
sleep 1
ip netns exec "$lan_ns" nc -z -w 2 198.18.0.1 5201

ip netns exec "$lan_ns" ping -q -c 20 -i 0.05 -W 1 198.18.0.1 | tee "$OUTPUT_DIR/ping.txt"
packet_loss="$(awk -F', ' '/packet loss/ {gsub(/% packet loss/, "", $3); print $3}' "$OUTPUT_DIR/ping.txt")"
latency_avg_ms="$(awk -F'/' '/^(rtt|round-trip)/ {print $5}' "$OUTPUT_DIR/ping.txt")"
if [ -z "$packet_loss" ] || [ "$packet_loss" != "0" ]; then
	echo "Unexpected packet loss: ${packet_loss:-unknown}%" >&2
	exit 1
fi

ip netns exec "$lan_ns" iperf3 -c 198.18.0.1 -P 16 -t 5 -J >"$OUTPUT_DIR/tcp.json"
tcp_bps="$(jq -r '.end.sum_received.bits_per_second // 0' "$OUTPUT_DIR/tcp.json")"
jq -e '(.end.sum_received.bits_per_second // 0) > 10000000' "$OUTPUT_DIR/tcp.json" >/dev/null

ip netns exec "$lan_ns" iperf3 -c 198.18.0.1 -u -b 200M -t 5 -J >"$OUTPUT_DIR/udp.json"
udp_loss="$(jq -r '.end.sum.lost_percent // .end.sum_received.lost_percent // 100' "$OUTPUT_DIR/udp.json")"
jq -e '(.end.sum.lost_percent // .end.sum_received.lost_percent // 100) < 5' "$OUTPUT_DIR/udp.json" >/dev/null

ip netns exec "$lan_ns" iperf3 -c 198.18.0.1 -P 64 -t 3 -J >"$OUTPUT_DIR/tcp-64-streams.json"
jq -e '(.end.sum_received.bits_per_second // 0) > 10000000' "$OUTPUT_DIR/tcp-64-streams.json" >/dev/null

ip netns exec "$lan_ns" python3 -m http.server 9090 --bind "${lan_address%/*}" >"$OUTPUT_DIR/lan-http.log" 2>&1 &
http_pid=$!
sleep 1
kill -0 "$http_pid"
if ip netns exec "$wan_ns" timeout 4 nc -z -w 2 "${lan_address%/*}" 9090; then
	echo "Unsolicited WAN-to-LAN connection was accepted" >&2
	exit 1
fi

ip netns exec "$wan_ns" nmap \
	-Pn -n \
	--max-retries 1 \
	--host-timeout 30s \
	-p 22,53,80,443,8080,8443,51820 \
	-oG "$OUTPUT_DIR/wan-scan.gnmap" \
	198.18.0.2 >"$OUTPUT_DIR/wan-scan.txt"
if grep -q '/open/' "$OUTPUT_DIR/wan-scan.gnmap"; then
	echo "Unexpected port exposed on WAN:" >&2
	cat "$OUTPUT_DIR/wan-scan.gnmap" >&2
	exit 1
fi

ip netns exec "$router_ns" nft list ruleset >"$OUTPUT_DIR/nft-ruleset.txt"
ip -n "$router_ns" -s link >"$OUTPUT_DIR/router-links.txt"
ip -n "$router_ns" route show table all >"$OUTPUT_DIR/router-routes.txt"

jq -n \
	--arg lan_address "$lan_address" \
	--arg dns_answer "$dns_answer" \
	--arg latency_avg_ms "$latency_avg_ms" \
	--arg packet_loss_percent "$packet_loss" \
	--argjson tcp_bits_per_second "$tcp_bps" \
	--argjson udp_loss_percent "$udp_loss" \
	'{
		lan_address: $lan_address,
		dns_answer: $dns_answer,
		latency_avg_ms: ($latency_avg_ms | tonumber),
		packet_loss_percent: ($packet_loss_percent | tonumber),
		tcp_bits_per_second: $tcp_bits_per_second,
		udp_loss_percent: $udp_loss_percent,
		parallel_tcp_streams: 64,
		wan_open_ports: 0
	}' >"$OUTPUT_DIR/summary.json"

cat "$OUTPUT_DIR/summary.json"
