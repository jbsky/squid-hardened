# Guide pratique – Intégration VyOS

## Prérequis VyOS

- VyOS 1.4 (Sagitta) ou 1.5 (Circinus) – firewall basé nftables
- 3 interfaces minimum :
  - `eth0` – WAN
  - `eth1` – LAN transparent (192.168.10.0/24)
  - `eth2` – LAN explicite (192.168.20.0/24)
- L'hôte Docker (Squid) sur un segment dédié, idéalement DMZ : 192.168.99.0/24 via interface `eth3` ou VLAN

## Étape 1 – Routage de base

```vyos
configure
set interfaces ethernet eth0 description 'WAN'
set interfaces ethernet eth0 address 'dhcp'
set interfaces ethernet eth1 description 'LAN-TRANSPARENT'
set interfaces ethernet eth1 address '192.168.10.1/24'
set interfaces ethernet eth2 description 'LAN-EXPLICIT'
set interfaces ethernet eth2 address '192.168.20.1/24'
set interfaces ethernet eth3 description 'DMZ-PROXY'
set interfaces ethernet eth3 address '192.168.99.1/24'
commit
```

## Étape 2 – DNAT transparent

Charger `vyos-transparent.config` :

```vyos
configure
source /config/scripts/squid-stack/vyos-transparent.config
# Vérifier les variables
show nat destination
show firewall
commit
save
```

Test depuis un client du LAN transparent :
```bash
curl -v http://example.com/   # doit passer par le proxy
```

Côté VyOS, surveiller :
```vyos
monitor traffic interface eth1 filter 'port 80'
show nat destination statistics
```

## Étape 3 – Mode explicite

Charger `vyos-explicit.config` puis :

```vyos
configure
set service dns forwarding system
set service dns forwarding name-server '1.1.1.1'
set service dns forwarding name-server '9.9.9.9'
commit
```

Test :
```bash
# Sur un client du LAN explicite
http_proxy=http://proxy.lan.local:3128 curl -v http://example.com/
https_proxy=http://proxy.lan.local:3129 curl -kv https://example.com/
```

Le second requiert que la CA `certs/bump.crt` soit dans le store du client (ou `-k` pour ignorer temporairement).

## Étape 4 – Servir le fichier WPAD

Le plus simple : ajouter un volume nginx au compose et l'héberger en 80 sur l'hôte Docker.

Alternative VyOS native : `service https` peut servir des fichiers statiques.

Solution minimale ajoutée à `docker-compose.yml` (à compléter selon besoin) :
```yaml
  wpad:
    image: nginx:alpine
    container_name: wpad
    volumes:
      - ./vyos/wpad-example.dat:/usr/share/nginx/html/wpad.dat:ro
      - ./vyos/wpad-example.dat:/usr/share/nginx/html/proxy.pac:ro
    ports:
      - "80:80"
    networks:
      - proxy_net
```

## Étape 5 – Conntrack en mode transparent

Le mode intercept Squid exige que le conntrack remonte la destination originale. Sur VyOS :

```vyos
set system conntrack expect-table-size 4096
set system conntrack hash-size 32768
set system conntrack table-size 262144
set system conntrack modules ftp
set system conntrack modules h323
set system conntrack modules tftp
```

## Étape 6 – Logs centralisés

```vyos
set system syslog host 192.168.99.20 facility all level info
set system syslog host 192.168.99.20 protocol udp
set system syslog host 192.168.99.20 port 514
```

Côté hôte Docker, on peut ajouter un `rsyslog` ou un `vector` en sidecar pour collecter à la fois les logs VyOS et les logs Docker.

## Étape 7 – Haute disponibilité (optionnel)

VyOS supporte VRRP :
```vyos
set high-availability vrrp group LAN10 interface eth1
set high-availability vrrp group LAN10 virtual-address '192.168.10.1/24'
set high-availability vrrp group LAN10 vrid 10
```

Pour le proxy lui-même : déployer la stack en double, monter un keepalived ou un load-balancer L4 (HAProxy) en amont.

## Dépannage VyOS

| Problème | Commande |
|---|---|
| Voir les règles nft générées | `sudo nft list ruleset` |
| Stats DNAT | `show nat destination statistics` |
| Conntrack actifs | `show conntrack table ipv4` |
| Trace paquet | `monitor traffic interface eth1 filter 'host X'` |
| Reset firewall | `delete firewall && commit` (⚠️ destructif) |

## Migration depuis l'ancien Squid

1. Garder l'IP de l'ancien Squid telle quelle (ex: 192.168.99.5)
2. Déployer la nouvelle stack sur 192.168.99.10
3. Changer la règle DNAT VyOS : `set nat destination rule 110 translation address '192.168.99.10'` puis `commit`
4. Validation 10-15 min sur logs
5. Si rollback : `set ... translation address '192.168.99.5'` + `commit`
6. Après validation longue (24-48h) : décommissionner l'ancien
