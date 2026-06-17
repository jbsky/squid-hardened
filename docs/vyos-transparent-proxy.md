# Transparent HTTPS Interception on VyOS with Podman Containers

> **TL;DR** — Traditional host-level DNAT breaks `SO_ORIGINAL_DST` across network
> namespaces. The solution: use Policy-Based Routing (PBR) to deliver packets
> intact into the container's netns, then apply `nft redirect` *inside* that
> namespace. This guide covers the full setup on VyOS 1.5+ with `squid-hardened`.

---

## Table of Contents

1. [The Problem: SO_ORIGINAL_DST Across Namespaces](#the-problem)
2. [Architecture](#architecture)
3. [Prerequisites](#prerequisites)
4. [Step 1: Container Network & Definition](#step-1-container-network--definition)
5. [Step 2: Policy-Based Routing](#step-2-policy-based-routing)
6. [Step 3: Firewall Zones & Chains](#step-3-firewall-zones--chains)
7. [Step 4: nft REDIRECT Injection](#step-4-nft-redirect-injection)
8. [Step 5: Squid Configuration](#step-5-squid-configuration)
9. [Step 6: Validation & Testing](#step-6-validation--testing)
10. [Gotchas](#gotchas)
11. [Debugging Toolkit](#debugging-toolkit)
12. [Appendix: DNAT vs PBR Comparison](#appendix-dnat-vs-pbr-comparison)

---

<a id="the-problem"></a>
## 1. The Problem: SO_ORIGINAL_DST Across Namespaces

Squid's `intercept` mode uses `getsockopt(SO_ORIGINAL_DST)` to recover the
client's intended destination after a REDIRECT/DNAT. This syscall queries the
**local** conntrack table of the network namespace where the socket lives.

```
Host-level DNAT approach (BROKEN for containers):

  Client ──► VyOS DNAT (host netns) ──► Container port
                 │                            │
                 │ conntrack entry HERE        │ SO_ORIGINAL_DST looks HERE
                 │ (host netns)               │ (container netns)
                 └── MISMATCH ────────────────┘
```

The conntrack entry is created in the **host** netns (where DNAT happens), but
Squid calls `SO_ORIGINAL_DST` in the **container** netns -- it gets nothing.

### The Solution: PBR + In-Container REDIRECT

Instead of DNATing at the host level, we:

1. **Route** the original packet (destination intact) into the container's netns
   using Policy-Based Routing
2. **REDIRECT** the destination port inside the container's netns using nftables

Now both the conntrack entry and `SO_ORIGINAL_DST` live in the same namespace:

```
PBR + REDIRECT approach (WORKING):

  Client ──► VyOS PBR (route to container IP) ──► Container netns
                                                       │
                                                  nft REDIRECT
                                                  (conntrack HERE)
                                                       │
                                                  SO_ORIGINAL_DST
                                                  (reads HERE)
                                                       │
                                                  ✓ MATCH
```

---

<a id="architecture"></a>
## 2. Architecture

### Packet Flow (HTTPS intercept example)

```
                        VyOS Router
┌─────────┐       ┌──────────────────────────────────────────┐       ┌──────────┐
│  Client  │       │                                          │       │ Internet │
│<C_SUBNET>│──────►│ eth0.X ──► PBR mark ──► table <T>       │       │          │
│  :443    │       │              │                           │       │          │
└─────────┘       │              ▼                           │       │          │
     ▲             │   route to <PROXY_IP> via <BRIDGE>       │       │          │
     │             │              │                           │       │          │
     │             │              ▼                           │       │          │
     │             │   ┌─────────────────────────┐            │       │          │
     │             │   │  Container netns (squid) │            │       │          │
     │             │   │                         │            │       │          │
     │             │   │  nft: dport 443 ──►:3131│            │       │          │
     │             │   │  nft: dport 80  ──►:3129│            │       │          │
     │             │   │                         │            │       │          │
     │             │   │  Squid intercept:       │            │       │          │
     │             │   │    :3129 (HTTP)         │──upstream─►│──────►│          │
     │             │   │    :3131 (HTTPS bump)   │            │       │          │
     │             │   │                         │            │       │          │
     │             │   └────────────┬────────────┘            │       │          │
     │             │                │ response                │       │          │
     │             │                ▼                          │       │          │
     │             │   reverse NAT (conntrack)                │       │          │
     │             │   src=<ORIG_DST>:443 → client            │       │          │
     │             │                │                          │       │          │
     │             │                ▼                          │       │          │
     │             │   <BRIDGE> ──► VyOS forward ──► eth0.X   │       │          │
     └─────────────┼────────────────────────────────────────  │       │          │
                   └──────────────────────────────────────────┘       └──────────┘
```

### Return Path Detail

When Squid sends the response back to the client, the packet inside the
container netns has:
- src = `<PROXY_IP>:3131`  (squid's intercept port)
- dst = `<CLIENT_IP>:<CLIENT_PORT>`

The nft conntrack in the container netns performs **reverse NAT**:
- src becomes `<ORIGINAL_DST>:443` (the real server IP the client intended)
- dst stays `<CLIENT_IP>:<CLIENT_PORT>`

This spoofed packet exits the container onto the bridge, enters VyOS for
forwarding back to the client VLAN. From VyOS's host conntrack perspective, this
is the **reply** to the original flow (client → server) that was PBR-routed
through the bridge -- so it's marked as `ESTABLISHED`.

---

<a id="prerequisites"></a>
## 3. Prerequisites

| Component | Requirement |
|-----------|-------------|
| VyOS | 1.5 Circinus / rolling (nftables, podman 4.x+) |
| Image | `jbsky/squid-hardened` with intercept ports configured |
| Network | Podman network (bridge) for the proxy containers |
| CA | SSL Bump CA generated and deployed (for HTTPS intercept) |
| Client | Traffic from client subnet must reach VyOS as gateway |

### Port allocation

| Port | Mode | Description |
|------|------|-------------|
| 3128 | Explicit | Standard forward proxy (no interception) |
| 3129 | Intercept HTTP | Transparent HTTP (via REDIRECT from port 80) |
| 3130 | Explicit + SSL Bump | Forward proxy with HTTPS decryption |
| 3131 | Intercept HTTPS | Transparent HTTPS with ssl-bump (via REDIRECT from 443) |

---

<a id="step-1-container-network--definition"></a>
## 4. Step 1: Container Network & Definition

### Create the Podman network

```vyos
configure

set container network <NETWORK_NAME> prefix <PROXY_NETWORK>

# Example:
# set container network webproxy prefix 172.20.0.0/24
```

### Define the Squid container

```vyos
set container name squid image 'docker.io/jbsky/squid-hardened:<VERSION>'
set container name squid network <NETWORK_NAME> address <PROXY_IP>
set container name squid cap-add 'net-admin'

# Volumes
set container name squid volume squid-conf source '/config/containers/squid'
set container name squid volume squid-conf destination '/etc/squid'
set container name squid volume squid-ssldb source '/config/containers/squid_ssldb'
set container name squid volume squid-ssldb destination '/var/lib/ssl_db'

# Memory limit (adjust to your needs)
set container name squid memory '512'

commit
```

> **Note**: `cap-add net-admin` is required for Squid's `intercept` mode
> (`SO_ORIGINAL_DST`) and optionally for self-injecting nft REDIRECT rules.

---

<a id="step-2-policy-based-routing"></a>
## 5. Step 2: Policy-Based Routing

PBR marks packets from the client subnet and routes them to the container IP
via the Podman bridge, preserving the original destination.

### Firewall mark (mangle)

```vyos
configure

# Mark traffic from client subnet destined for HTTP/HTTPS
set firewall ipv4 name PBR-intercept rule 10 action 'accept'
set firewall ipv4 name PBR-intercept rule 10 source address '<CLIENT_SUBNET>'
set firewall ipv4 name PBR-intercept rule 10 protocol 'tcp'
set firewall ipv4 name PBR-intercept rule 10 destination port '80,443'
set firewall ipv4 name PBR-intercept rule 10 set mark '<PBR_MARK>'
set firewall ipv4 name PBR-intercept rule 10 description 'Mark IoT HTTP/S for intercept'

# Exclude traffic destined for the proxy itself (avoid loops)
set firewall ipv4 name PBR-intercept rule 5 action 'return'
set firewall ipv4 name PBR-intercept rule 5 destination address '<PROXY_NETWORK>'
set firewall ipv4 name PBR-intercept rule 5 description 'Exclude proxy network from PBR'

commit
```

### Apply to client interface (prerouting)

```vyos
set firewall ipv4 prerouting raw rule 10 action 'jump'
set firewall ipv4 prerouting raw rule 10 jump-target 'PBR-intercept'
set firewall ipv4 prerouting raw rule 10 inbound-interface name '<CLIENT_IFACE>'

commit
```

### Route table

```vyos
set protocols static table <PBR_TABLE> route 0.0.0.0/0 next-hop <PROXY_IP>

# Policy route: marked packets use this table
set policy route PBR-to-proxy rule 10 set table '<PBR_TABLE>'
set policy route PBR-to-proxy rule 10 match mark '<PBR_MARK>'

# Attach to client interface
set interfaces ethernet <CLIENT_IFACE> policy route 'PBR-to-proxy'

commit
```

> **How it works**: Marked packets get a routing decision override -- instead of
> the default route (WAN), they're sent to `<PROXY_IP>` as next-hop. Since
> `<PROXY_IP>` is on the Podman bridge, VyOS sends an L2 frame addressed to the
> container's veth MAC. The packet enters the container's netns with the
> **original destination IP intact**.

---

<a id="step-3-firewall-zones--chains"></a>
## 6. Step 3: Firewall Zones & Chains

### Zone definitions

```vyos
configure

# Client zone (e.g., IoT VLAN)
set firewall zone <CLIENT_ZONE> interface '<CLIENT_IFACE>'
set firewall zone <CLIENT_ZONE> default-action 'drop'
set firewall zone <CLIENT_ZONE> default-log

# Proxy zone (Podman bridge)
set firewall zone <PROXY_ZONE> interface '<PROXY_BRIDGE>'
set firewall zone <PROXY_ZONE> default-action 'drop'
set firewall zone <PROXY_ZONE> default-log

commit
```

### Port groups

```vyos
# Proxy ports (explicit mode responses)
set firewall group port-group P_proxy port '3128-3131'

# HTTP/HTTPS ports (transparent intercept responses)
set firewall group port-group all-http port '80'
set firewall group port-group all-http port '443'

commit
```

### Chain: Client-to-Proxy (inbound to squid)

```vyos
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 action 'accept'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 protocol 'tcp'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 destination group port-group 'all-http'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 description 'Allow HTTP/S to proxy (PBR intercept)'

set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 action 'accept'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 protocol 'tcp'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 destination group port-group 'P_proxy'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 description 'Allow explicit proxy ports'

# Attach to zone
set firewall zone <PROXY_ZONE> from <CLIENT_ZONE> firewall name '<CLIENT>-to-<PROXY>'

commit
```

### Chain: Proxy-to-Client (return traffic from squid)

> **CRITICAL**: This chain needs TWO rules. Transparent intercept responses have
> `source port 80/443` (spoofed original server), NOT `source port 3128-3131`.

```vyos
# Rule 1: Explicit proxy responses (sport = proxy ports)
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 action 'accept'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 state 'established'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 state 'related'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 protocol 'tcp'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 source group port-group 'P_proxy'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 description 'Explicit proxy return'

# Rule 2: Transparent intercept responses (sport = 80/443, SPOOFED source IP)
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 action 'accept'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 state 'established'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 state 'related'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 protocol 'tcp'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 source group port-group 'all-http'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 description 'Intercept transparent return (splice/bump)'

# Attach to zone
set firewall zone <CLIENT_ZONE> from <PROXY_ZONE> firewall name '<PROXY>-to-<CLIENT>'

commit
```

### Chain: Proxy-to-WAN (squid upstream)

Squid needs outbound internet access to fetch content from origin servers:

```vyos
set firewall ipv4 name <PROXY>-to-WAN rule 10 action 'accept'
set firewall ipv4 name <PROXY>-to-WAN rule 10 state 'established'
set firewall ipv4 name <PROXY>-to-WAN rule 10 state 'related'
set firewall ipv4 name <PROXY>-to-WAN rule 10 description 'Squid upstream return'

set firewall ipv4 name <PROXY>-to-WAN rule 20 action 'accept'
set firewall ipv4 name <PROXY>-to-WAN rule 20 source address '<PROXY_IP>'
set firewall ipv4 name <PROXY>-to-WAN rule 20 protocol 'tcp'
set firewall ipv4 name <PROXY>-to-WAN rule 20 description 'Squid upstream requests'

commit
```

---

<a id="step-4-nft-redirect-injection"></a>
## 7. Step 4: nft REDIRECT Injection

The REDIRECT rules must be applied **inside** the container's network namespace
after the container starts. Two approaches:

### Option A: Systemd oneshot service (recommended for VyOS)

Create `/config/etc/services/squid-redirect.service`:

```ini
[Unit]
Description=Inject nftables REDIRECT into squid container netns
After=vyos-container-squid.service
BindsTo=vyos-container-squid.service
PartOf=vyos-container-squid.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=/bin/sleep 3
ExecStart=/bin/bash -c '\
  PID=$(podman inspect squid --format "{{.State.Pid}}") && \
  nsenter -t $PID -n nft flush ruleset && \
  nsenter -t $PID -n nft add table ip nat && \
  nsenter -t $PID -n nft add chain ip nat prerouting \
    "{ type nat hook prerouting priority dstnat; policy accept; }" && \
  nsenter -t $PID -n nft add rule ip nat prerouting \
    tcp dport 80 redirect to :3129 && \
  nsenter -t $PID -n nft add rule ip nat prerouting \
    tcp dport 443 redirect to :3131'
ExecStop=/bin/bash -c '\
  PID=$(podman inspect squid --format "{{.State.Pid}}" 2>/dev/null) && \
  nsenter -t $PID -n nft flush ruleset 2>/dev/null || true'

[Install]
WantedBy=multi-user.target
WantedBy=vyos-container-squid.service
```

Enable it:
```bash
sudo ln -sf /config/etc/services/squid-redirect.service \
  /etc/systemd/system/squid-redirect.service
sudo systemctl daemon-reload
sudo systemctl enable --now squid-redirect.service
```

> **Important**: The `WantedBy=vyos-container-squid.service` ensures the service
> starts whenever the container starts (including after config commits that
> recreate the container). See [Gotcha #3](#gotcha-3) for why `PartOf=` alone is
> insufficient.

### Option B: Self-inject via Go init (advanced)

If the container has `CAP_NET_ADMIN`, the Go init binary can inject the REDIRECT
rules at startup using the `github.com/google/nftables` library. This eliminates
the external systemd service entirely. See the `squid-hardened` source for
implementation details.

---

<a id="step-5-squid-configuration"></a>
## 8. Step 5: Squid Configuration

### Relevant squid.conf directives

```squid
# Explicit proxy (no interception)
http_port 3128 name=explicit

# Transparent HTTP intercept (receives REDIRECT from port 80)
http_port 3129 intercept name=intercept_http

# Explicit SSL Bump (opt-in clients with CA installed)
http_port 3130 name=explicit_bump \
  ssl-bump \
  cert=/etc/squid/ssl_cert/bump.pem \
  generate-host-certificates=on \
  dynamic_cert_mem_cache_size=16MB \
  tls-dh=/etc/squid/ssl_cert/dhparam.pem \
  options=NO_SSLv3,NO_TLSv1,NO_TLSv1_1

# Transparent HTTPS intercept (receives REDIRECT from port 443)
https_port 3131 intercept name=intercept_https \
  ssl-bump \
  cert=/etc/squid/ssl_cert/bump.pem \
  generate-host-certificates=off \
  dynamic_cert_mem_cache_size=4MB \
  tls-dh=/etc/squid/ssl_cert/dhparam.pem \
  options=NO_SSLv3,NO_TLSv1,NO_TLSv1_1

# Preserve original destination (required for intercept)
client_dst_passthru on

# DNS: use the container network gateway or a dedicated resolver
dns_nameservers <DNS_SERVER>
```

### SSL Bump policy (per-device ACLs)

```squid
# Device identification by source IP
acl device_a src <DEVICE_A_IP>
acl device_b src <DEVICE_B_IP>
acl all_clients src <CLIENT_SUBNET>

# Domain whitelists per device
acl device_a_domains ssl::server_name "/etc/squid/acl/device-a-domains.txt"
acl common_domains ssl::server_name "/etc/squid/acl/common-domains.txt"

# SSL Bump logic
ssl_bump peek step1
ssl_bump splice device_a device_a_domains port_intercept_https
ssl_bump splice device_a common_domains port_intercept_https
ssl_bump terminate port_intercept_https    # deny unlisted HTTPS
ssl_bump splice nobump_dst port_explicit_bump
ssl_bump bump port_explicit_bump
ssl_bump terminate all
```

> This allows per-device allow-listing: only approved domains get `splice`
> (pass-through), everything else is `terminate`d (blocked). For devices that
> need full internet, change `terminate` to `splice all_clients`.

---

<a id="step-6-validation--testing"></a>
## 9. Step 6: Validation & Testing

### 1. Verify REDIRECT rules in container netns

```bash
PID=$(sudo podman inspect squid --format '{{.State.Pid}}')
sudo nsenter -t $PID -n nft list ruleset
```

Expected:
```
table ip nat {
    chain prerouting {
        type nat hook prerouting priority dstnat; policy accept;
        tcp dport 80 redirect to :3129
        tcp dport 443 redirect to :3131
    }
}
```

### 2. Verify conntrack in container netns

```bash
sudo nsenter -t $PID -n conntrack -L | grep <CLIENT_IP>
```

Look for `ESTABLISHED` entries with reply `sport=3131` (confirms REDIRECT is
working):
```
tcp 6 431997 ESTABLISHED src=<CLIENT_IP> dst=<SERVER_IP> sport=X dport=443 \
  src=<PROXY_IP> dst=<CLIENT_IP> sport=3131 dport=X [ASSURED]
```

### 3. Verify VyOS firewall counters

```bash
sudo nft list chain ip vyos_filter NAME_<PROXY>-to-<CLIENT>
```

Both rules (31280 and 31281) should show non-zero packet counts.

### 4. tcpdump checkpoints

```bash
# On the bridge (pivot point): should see SYN from client AND SYN-ACK return
sudo tcpdump -i <PROXY_BRIDGE> -n host <CLIENT_IP> and port 443

# On the client VLAN: should see SYN out AND SYN-ACK back
sudo tcpdump -i <CLIENT_IFACE> -n host <CLIENT_IP> and port 443
```

### 5. End-to-end test from client

If the client has a shell:
```bash
curl -v https://example.com 2>&1 | head -20
```

Check Squid's access.log for the connection (look for `intercept_https` port
name and `splice` or `bump` mode).

---

<a id="gotchas"></a>
## 10. Gotchas

<a id="gotcha-1"></a>
### 1. Firewall source port mismatch (transparent return traffic)

**Symptom**: SYN-ACKs from squid visible on bridge (tcpdump) but never reach
the client. Firewall drop log shows `<PROXY>-to-<CLIENT>-default-D`.

**Cause**: In transparent intercept mode with `client_dst_passthru on`, the
response packet has **source port 443** (the original server's port), not
3128-3131. A firewall rule that only allows `sport @P_proxy` will drop these.

**Fix**: Add rule 31281 with `source group port-group all-http` (see Step 3).

---

<a id="gotcha-2"></a>
### 2. ip_forward=1 in container netns leaks packets

**Symptom**: Client SYNs appear in `Webproxy-to-WAN` drop logs with the
client's source IP (not the proxy IP). Packets "pass through" the container.

**Cause**: If `ip_forward=1` in the container netns AND a packet doesn't match
the REDIRECT rules (e.g., stale conntrack entry), the packet is forwarded via
the container's default route back to VyOS, which then tries to route it to WAN.

**Fix**: Disable IP forwarding in the container netns:
```bash
PID=$(sudo podman inspect squid --format '{{.State.Pid}}')
sudo nsenter -t $PID -n sysctl -w net.ipv4.ip_forward=0
```

Add this to the `ExecStart` of `squid-redirect.service` for persistence.

---

<a id="gotcha-3"></a>
### 3. systemd PartOf= does not propagate start to inactive services

**Symptom**: After `restart container squid` or a config commit that recreates
the container, `squid-redirect.service` stays `inactive (dead)`.

**Cause**: `PartOf=` propagates `stop` and `restart` to services that are
**currently active**. If the dependent service is already inactive (e.g., it
stopped when the container stopped), the subsequent `start` of the container
does NOT trigger it.

VyOS config commits do `stop` + `daemon-reload` + `start` (separate
transactions), not an atomic `restart`. The stop propagates (redirect stops),
but the later start is independent.

**Fix**: Add `WantedBy=vyos-container-squid.service` to the `[Install]` section
and re-enable. This creates a `.wants/` symlink that ensures start propagation
regardless of the dependent's current state.

---

### 4. Stale conntrack entries block REDIRECT

**Symptom**: Some client connections work, others don't. Working connections use
new source ports; failing ones keep retransmitting with the same source port.

**Cause**: If the container was running WITHOUT REDIRECT rules (e.g., after
restart before the injection service ran), client SYNs created conntrack entries
with NO NAT. These entries persist as long as the client retransmits (each
retransmission refreshes the entry's timeout). When REDIRECT rules are later
added, packets matching existing entries bypass NAT (it only applies to `NEW`
connections).

**Fix**: Flush conntrack in the container netns after injecting rules:
```bash
sudo nsenter -t $PID -n conntrack -F
```

---

### 5. Container restart recreates the netns (rules lost)

**Symptom**: REDIRECT rules disappear after any container restart.

**Cause**: Podman destroys and recreates the network namespace on each container
restart. The nft rules are ephemeral.

**Fix**: The injection service (`squid-redirect.service`) must re-run after
every container start. The `WantedBy=vyos-container-squid.service` approach
handles this. Alternatively, embed the rules in the container's init process
(Option B in Step 4).

---

### 6. VyOS-generated container unit lives in /run (runtime)

**Symptom**: Drop-ins or `.wants/` symlinks seem to be ignored after a VyOS
config commit.

**Cause**: VyOS generates `vyos-container-squid.service` in `/run/systemd/system/`
(runtime, recreated on each commit). However, systemd merges configurations from
`/etc/systemd/system/` (persistent) with `/run/systemd/system/` (runtime).
Symlinks in `/etc/systemd/system/vyos-container-squid.service.wants/` ARE
honored for runtime units.

**Fix**: After `systemctl enable squid-redirect.service`, verify the symlink
exists in `/etc/systemd/system/vyos-container-squid.service.wants/`.

---

### 7. CAP_NET_ADMIN required

**Symptom**: Squid fails with `comm_open_listener: cannot bind to [::]:3129`
or `SO_ORIGINAL_DST` returns 0.0.0.0.

**Cause**: `intercept` mode requires `CAP_NET_ADMIN` for transparent socket
options. Without it, Squid cannot retrieve the original destination.

**Fix**: `set container name squid cap-add 'net-admin'` in VyOS config.

---

<a id="debugging-toolkit"></a>
## 11. Debugging Toolkit

### Decision tree

```
Client has no internet
│
├─ tcpdump on <CLIENT_IFACE>: SYNs visible?
│  └─ NO → client routing/gateway issue (not a proxy problem)
│
├─ tcpdump on <PROXY_BRIDGE>: SYNs arrive?
│  └─ NO → PBR not working (check mark + table + interface policy)
│
├─ nft list ruleset in container netns: REDIRECT rules present?
│  └─ NO → injection service not running (systemctl status squid-redirect)
│
├─ conntrack -L in container netns: entry exists with sport=3131?
│  └─ NO → REDIRECT not matching (check for stale conntrack, flush it)
│
├─ tcpdump on <PROXY_BRIDGE>: SYN-ACK visible from proxy?
│  └─ NO → squid not accepting connection (check squid logs, port config)
│
├─ tcpdump on <CLIENT_IFACE>: SYN-ACK reaches client?
│  └─ NO → firewall blocking return path
│       └─ Check <PROXY>-to-<CLIENT> chain: is sport 80/443 allowed? (Gotcha #1)
│
└─ SYN-ACK reaches client but connection still fails
   └─ Check squid ACLs (ssl_bump terminate? http_access deny?)
```

### Key commands

```bash
# Container PID (needed for nsenter)
PID=$(sudo podman inspect squid --format '{{.State.Pid}}')

# nft rules in container
sudo nsenter -t $PID -n nft list ruleset

# Conntrack in container (filter by client)
sudo nsenter -t $PID -n conntrack -L | grep <CLIENT_IP>

# Flush stale conntrack (nuclear option)
sudo nsenter -t $PID -n conntrack -F

# Flush only one client's entries
sudo nsenter -t $PID -n conntrack -D -s <CLIENT_IP>

# VyOS firewall chain counters
sudo nft list chain ip vyos_filter NAME_<PROXY>-to-<CLIENT>

# tcpdump on bridge (the pivot point for all proxy traffic)
sudo tcpdump -i <PROXY_BRIDGE> -n host <CLIENT_IP> -c 30

# Check ip_forward in container
sudo nsenter -t $PID -n cat /proc/sys/net/ipv4/ip_forward

# Squid access log (last 20 lines)
sudo podman logs squid 2>&1 | tail -20

# Service status
systemctl status squid-redirect.service
sudo journalctl -u squid-redirect.service --no-pager -n 20
```

### Firewall log prefixes

| Prefix | Meaning |
|--------|---------|
| `NAM-<X>-to-<Y>-default-D` | Dropped by default action (no rule matched) |
| `NAM-<X>-to-<Y>-<N>` | Matched rule number N in chain X-to-Y |
| `INP-filter-default-D` | Dropped by INPUT chain (traffic to VyOS itself) |

---

<a id="appendix-dnat-vs-pbr-comparison"></a>
## 12. Appendix: DNAT vs PBR Comparison

| Feature | Host DNAT | PBR + In-Container REDIRECT |
|---------|-----------|------------------------------|
| HTTP transparent intercept | Works (if same host) | Works |
| HTTPS transparent intercept (ssl-bump) | **Broken** (SO_ORIGINAL_DST fails) | Works |
| Container isolation | Weak (host does NAT) | Strong (NAT in container netns) |
| Firewall complexity | Simple (one NAT rule) | Higher (PBR + zones + 2 return rules) |
| Dependency on host NAT | Yes | No |
| Works with Podman rootless | No | Yes (with CAP_NET_ADMIN) |
| Requires external service for rules | No | Yes (unless embedded in init) |
| Survives container restart | N/A (host NAT persists) | Requires re-injection |

### When to use which

- **DNAT**: HTTP-only interception, simple setups, squid on the same host as the
  firewall (not containerized), or when HTTPS is pass-through only.
- **PBR + REDIRECT**: Any setup requiring transparent HTTPS interception
  (ssl-bump) in a containerized Squid. This is the only working approach for
  Podman/Docker containers.

---

## References

- [Squid intercept mode documentation](http://www.squid-cache.org/Doc/config/http_port/)
- [VyOS Policy-Based Routing](https://docs.vyos.io/en/latest/configuration/policy/route.html)
- [nftables redirect](https://wiki.nftables.org/wiki-nftables/index.php/Performing_Network_Address_Translation_(NAT)#Redirect)
- [Linux conntrack and network namespaces](https://thermalcircle.de/doku.php?id=blog:linux:nftables_patterns_for_network_namespaces)
