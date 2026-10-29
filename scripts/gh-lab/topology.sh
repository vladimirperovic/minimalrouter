#!/bin/bash
# Builds or removes the lab network. Usage: topology.sh up|down
set -euo pipefail
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

down() {
    if [ -f "$LAB_RUN/provider-dns.pid" ]; then
        # The fixture lives only inside this lab's provider namespace.
        isp kill "$(cat "$LAB_RUN/provider-dns.pid")" 2>/dev/null || true
        rm -f "$LAB_RUN/provider-dns.pid"
    fi
    sudo pkill -f 'pppoe-server -F -I isp0' 2>/dev/null || true
    sudo pkill -f 'http.server 8080' 2>/dev/null || true
    sudo pkill -x dhclient 2>/dev/null || true
    for dev in tap-wan tap-lan; do sudo ip link del "$dev" 2>/dev/null || true; done
    for ns in isp client; do sudo ip netns del "$ns" 2>/dev/null || true; done
    for dev in br-wan br-lan isp-br isp-up cl-br; do sudo ip link del "$dev" 2>/dev/null || true; done
    sudo nft delete table ip lab 2>/dev/null || true
    sudo rm -rf /etc/netns/client
}

up() {
    down
    sudo modprobe -a tun bridge veth ppp_generic pppox pppoe
    sudo sysctl -qw net.ipv4.ip_forward=1
    # Docker on the runner sets a DROP policy on forwarded packets.
    sudo iptables -I FORWARD -j ACCEPT 2>/dev/null || true

    # WAN side: a bridge the router's WAN tap and the ISP namespace share.
    sudo ip link add br-wan type bridge
    sudo ip link set br-wan up
    sudo ip tuntap add dev tap-wan mode tap user "$(id -un)"
    sudo ip link set tap-wan master br-wan up

    # LAN side: the host is the management station on 192.168.1.50.
    sudo ip link add br-lan type bridge
    sudo ip addr add "$HOST_LAN_IP/24" dev br-lan
    sudo ip link set br-lan up
    sudo ip tuntap add dev tap-lan mode tap user "$(id -un)"
    sudo ip link set tap-lan master br-lan up

    # The "provider": a namespace with the PPPoE server and an uplink to the
    # runner's real network (NAT), reachable from the router only through PPP.
    sudo ip netns add isp
    sudo ip link add isp-br type veth peer name isp0
    sudo ip link set isp-br master br-wan up
    sudo ip link set isp0 netns isp
    isp ip link set lo up
    isp ip link set isp0 up
    sudo ip link add isp-up type veth peer name isp-dn
    sudo ip link set isp-dn netns isp
    sudo ip addr add 172.31.0.1/30 dev isp-up
    sudo ip link set isp-up up
    isp ip addr add 172.31.0.2/30 dev isp-dn
    isp ip link set isp-dn up
    isp ip route add default via 172.31.0.1
    isp sysctl -qw net.ipv4.ip_forward=1
    isp ip addr add "$INET_IP/32" dev lo
    # A deterministic upstream for the DNS flood only. TTL=0 and unique names
    # exercise forwarding without saturating or rate-limiting public resolvers.
    isp dnsmasq --conf-file=/dev/null --no-hosts --no-resolv --bind-interfaces \
        --listen-address="$INET_IP" --pid-file="$LAB_RUN/provider-dns.pid" --server=1.1.1.1 \
        --address="/flood.lab.invalid/$INET_IP" --local=/flood.lab.invalid/ --local-ttl=0 \
        --log-facility="$LOGS/provider-dns.log"
    sudo nft -f - <<'EOF'
table ip lab {
  chain post {
    type nat hook postrouting priority 100;
    ip saddr 172.31.0.0/30 oifname != "isp-up" masquerade
  }
}
EOF
    isp nft -f - <<'EOF'
table ip lab {
  chain post {
    type nat hook postrouting priority 100;
    ip saddr 10.250.0.0/24 oifname "isp-dn" masquerade
  }
}
EOF

    # A client behind the router, in its own namespace so the host's DNS and
    # routes stay untouched.
    sudo ip netns add client
    sudo mkdir -p /etc/netns/client
    : | sudo tee /etc/netns/client/resolv.conf >/dev/null
    sudo ip link add cl-br type veth peer name cl0
    sudo ip link set cl-br master br-lan up
    sudo ip link set cl0 netns client
    client ip link set lo up
    client ip link set cl0 up

    # PPPoE server: PAP or CHAP, one session per router, 10.250.0.1 <-> .50.
    sudo mkdir -p "$LAB_RUN/www"
    echo LAB_ISP_MARKER | sudo tee "$LAB_RUN/www/marker.txt" >/dev/null
    printf '"%s" * "%s" *\n' "$PPPOE_USER" "$PPPOE_PASS" | sudo tee /etc/ppp/pap-secrets /etc/ppp/chap-secrets >/dev/null
    sudo tee /etc/ppp/pppoe-lab.options >/dev/null <<EOF
auth
name isp-lab
lcp-echo-interval 10
lcp-echo-failure 3
ms-dns 1.1.1.1
noipdefault
nodefaultroute
EOF
    sudo ip netns exec isp bash -c "nohup pppoe-server -F -I isp0 -k -L $ISP_PPP_IP -R $ROUTER_PPP_IP -N 8 -S isp-lab -C isp-lab -O /etc/ppp/pppoe-lab.options >'$LOGS/pppoe-server.log' 2>&1 &"
    sudo ip netns exec isp bash -c "cd '$LAB_RUN/www' && nohup python3 -m http.server 8080 --bind 0.0.0.0 >'$LOGS/isp-http.log' 2>&1 &"
    sleep 2
    pgrep -f 'pppoe-server -F -I isp0' >/dev/null || { cat "$LOGS/pppoe-server.log" >&2; echo 'ERROR: pppoe-server did not start' >&2; exit 1; }
    log "topology is up"
}

case "${1:-}" in
    up) up ;;
    down) down ;;
    *) echo 'usage: topology.sh up|down' >&2; exit 2 ;;
esac
