# Interception HTTPS transparente sur VyOS avec conteneurs Podman

> **Resume** -- Le DNAT classique au niveau de l'hote casse `SO_ORIGINAL_DST`
> entre namespaces reseau. La solution : utiliser le Policy-Based Routing (PBR)
> pour acheminer les paquets intacts dans le netns du conteneur, puis appliquer
> `nft redirect` *a l'interieur* de ce namespace. Ce guide couvre l'installation
> complete sur VyOS 1.5+ avec `squid-hardened`.

---

## Table des matieres

1. [Le probleme : SO_ORIGINAL_DST entre namespaces](#le-probleme)
2. [Architecture](#architecture)
3. [Pre-requis](#pre-requis)
4. [Etape 1 : Reseau conteneur et definition](#etape-1)
5. [Etape 2 : Policy-Based Routing](#etape-2)
6. [Etape 3 : Zones et chaines firewall](#etape-3)
7. [Etape 4 : Injection nft REDIRECT](#etape-4)
8. [Etape 5 : Configuration Squid](#etape-5)
9. [Etape 6 : Validation et tests](#etape-6)
10. [Pieges courants](#pieges-courants)
11. [Boite a outils de debug](#boite-a-outils)
12. [Annexe : Comparaison DNAT vs PBR](#annexe)

---

<a id="le-probleme"></a>
## 1. Le probleme : SO_ORIGINAL_DST entre namespaces

Le mode `intercept` de Squid utilise `getsockopt(SO_ORIGINAL_DST)` pour
retrouver la destination originale du client apres un REDIRECT/DNAT. Cet appel
systeme interroge la table conntrack **locale** du namespace reseau ou vit le
socket.

```
Approche DNAT classique (CASSEE pour les conteneurs) :

  Client ---> DNAT VyOS (netns hote) ---> Port conteneur
                 |                            |
                 | entree conntrack ICI       | SO_ORIGINAL_DST cherche ICI
                 | (netns hote)              | (netns conteneur)
                 |__ INCOHERENCE ____________|
```

L'entree conntrack est creee dans le netns **hote** (ou le DNAT s'applique),
mais Squid appelle `SO_ORIGINAL_DST` dans le netns du **conteneur** -- il ne
trouve rien.

### La solution : PBR + REDIRECT dans le conteneur

Au lieu de DNATer au niveau hote, on :

1. **Route** le paquet original (destination intacte) dans le netns du conteneur
   via Policy-Based Routing
2. **REDIRECT** le port destination a l'interieur du netns via nftables

Le conntrack et `SO_ORIGINAL_DST` vivent desormais dans le meme namespace :

```
Approche PBR + REDIRECT (FONCTIONNELLE) :

  Client ---> VyOS PBR (route vers IP conteneur) ---> Netns conteneur
                                                          |
                                                     nft REDIRECT
                                                     (conntrack ICI)
                                                          |
                                                     SO_ORIGINAL_DST
                                                     (lecture ICI)
                                                          |
                                                     OK -- COHERENT
```

---

<a id="architecture"></a>
## 2. Architecture

### Flux de paquets (exemple interception HTTPS)

```
                        Routeur VyOS
+-----------+     +----------------------------------------------+     +----------+
|  Client   |     |                                              |     | Internet |
|<C_SUBNET> |---->| eth0.X --> mark PBR --> table <T>            |     |          |
|  :443     |     |              |                               |     |          |
+-----------+     |              v                               |     |          |
     ^            |   route vers <PROXY_IP> via <BRIDGE>         |     |          |
     |            |              |                               |     |          |
     |            |              v                               |     |          |
     |            |   +---------------------------+              |     |          |
     |            |   | Netns conteneur (squid)   |              |     |          |
     |            |   |                           |              |     |          |
     |            |   | nft: dport 443 --> :3131  |              |     |          |
     |            |   | nft: dport 80  --> :3129  |              |     |          |
     |            |   |                           |              |     |          |
     |            |   | Squid intercept :         |              |     |          |
     |            |   |   :3129 (HTTP)            |--upstream--->|---->|          |
     |            |   |   :3131 (HTTPS bump)      |              |     |          |
     |            |   |                           |              |     |          |
     |            |   +-------------+-------------+              |     |          |
     |            |                 | reponse                    |     |          |
     |            |                 v                            |     |          |
     |            |   reverse NAT (conntrack)                    |     |          |
     |            |   src=<DST_ORIG>:443 --> client              |     |          |
     |            |                 |                            |     |          |
     |            |                 v                            |     |          |
     |            |   <BRIDGE> --> VyOS forward --> eth0.X       |     |          |
     +------------+----------------------------------------------+     +----------+
```

### Detail du chemin retour

Quand Squid renvoie la reponse au client, le paquet dans le netns conteneur a :
- src = `<PROXY_IP>:3131` (port intercept de squid)
- dst = `<CLIENT_IP>:<PORT_CLIENT>`

Le conntrack nft dans le netns du conteneur effectue le **reverse NAT** :
- src devient `<DST_ORIGINALE>:443` (l'IP reelle du serveur voulu par le client)
- dst reste `<CLIENT_IP>:<PORT_CLIENT>`

Ce paquet "spoofed" sort du conteneur sur le bridge, entre dans VyOS pour etre
forward vers le VLAN client. Du point de vue du conntrack hote VyOS, c'est la
**reponse** au flux original (client -> serveur) route via PBR -- donc il est
marque `ESTABLISHED`.

---

<a id="pre-requis"></a>
## 3. Pre-requis

| Composant | Exigence |
|-----------|----------|
| VyOS | 1.5 Circinus / rolling (nftables, podman 4.x+) |
| Image | `jbsky/squid-hardened` avec ports intercept configures |
| Reseau | Reseau Podman (bridge) pour les conteneurs proxy |
| CA | CA SSL Bump generee et deployee (pour interception HTTPS) |
| Client | Le trafic du sous-reseau client doit transiter par VyOS comme gateway |

### Allocation des ports

| Port | Mode | Description |
|------|------|-------------|
| 3128 | Explicite | Proxy forward standard (pas d'interception) |
| 3129 | Intercept HTTP | HTTP transparent (via REDIRECT depuis port 80) |
| 3130 | Explicite + SSL Bump | Proxy forward avec dechiffrement HTTPS |
| 3131 | Intercept HTTPS | HTTPS transparent avec ssl-bump (via REDIRECT depuis 443) |

---

<a id="etape-1"></a>
## 4. Etape 1 : Reseau conteneur et definition

### Creer le reseau Podman

```vyos
configure

set container network <NETWORK_NAME> prefix <PROXY_NETWORK>

# Exemple :
# set container network webproxy prefix 172.20.0.0/24
```

### Definir le conteneur Squid

```vyos
set container name squid image 'docker.io/jbsky/squid-hardened:<VERSION>'
set container name squid network <NETWORK_NAME> address <PROXY_IP>
set container name squid cap-add 'net-admin'

# Volumes
set container name squid volume squid-conf source '/config/containers/squid'
set container name squid volume squid-conf destination '/etc/squid'
set container name squid volume squid-ssldb source '/config/containers/squid_ssldb'
set container name squid volume squid-ssldb destination '/var/lib/ssl_db'

# Limite memoire (ajuster selon besoins)
set container name squid memory '512'

commit
```

> **Note** : `cap-add net-admin` est necessaire pour le mode `intercept`
> (`SO_ORIGINAL_DST`) et optionnellement pour l'auto-injection des regles nft.

---

<a id="etape-2"></a>
## 5. Etape 2 : Policy-Based Routing

Le PBR marque les paquets du sous-reseau client et les route vers l'IP du
conteneur via le bridge Podman, en preservant la destination originale.

### Marque firewall (mangle)

```vyos
configure

# Marquer le trafic HTTP/HTTPS du sous-reseau client
set firewall ipv4 name PBR-intercept rule 10 action 'accept'
set firewall ipv4 name PBR-intercept rule 10 source address '<CLIENT_SUBNET>'
set firewall ipv4 name PBR-intercept rule 10 protocol 'tcp'
set firewall ipv4 name PBR-intercept rule 10 destination port '80,443'
set firewall ipv4 name PBR-intercept rule 10 set mark '<PBR_MARK>'
set firewall ipv4 name PBR-intercept rule 10 description 'Mark client HTTP/S for intercept'

# Exclure le trafic vers le proxy lui-meme (eviter les boucles)
set firewall ipv4 name PBR-intercept rule 5 action 'return'
set firewall ipv4 name PBR-intercept rule 5 destination address '<PROXY_NETWORK>'
set firewall ipv4 name PBR-intercept rule 5 description 'Exclude proxy network from PBR'

commit
```

### Appliquer sur l'interface client (prerouting)

```vyos
set firewall ipv4 prerouting raw rule 10 action 'jump'
set firewall ipv4 prerouting raw rule 10 jump-target 'PBR-intercept'
set firewall ipv4 prerouting raw rule 10 inbound-interface name '<CLIENT_IFACE>'

commit
```

### Table de routage

```vyos
set protocols static table <PBR_TABLE> route 0.0.0.0/0 next-hop <PROXY_IP>

# Route policy : les paquets marques utilisent cette table
set policy route PBR-to-proxy rule 10 set table '<PBR_TABLE>'
set policy route PBR-to-proxy rule 10 match mark '<PBR_MARK>'

# Attacher a l'interface client
set interfaces ethernet <CLIENT_IFACE> policy route 'PBR-to-proxy'

commit
```

> **Fonctionnement** : Les paquets marques obtiennent un routage alternatif --
> au lieu de la route par defaut (WAN), ils sont envoyes vers `<PROXY_IP>` comme
> next-hop. Comme `<PROXY_IP>` est sur le bridge Podman, VyOS envoie une trame
> L2 adressee au MAC du veth du conteneur. Le paquet entre dans le netns du
> conteneur avec l'**IP destination originale intacte**.

---

<a id="etape-3"></a>
## 6. Etape 3 : Zones et chaines firewall

### Definitions de zones

```vyos
configure

# Zone client (ex: VLAN IoT)
set firewall zone <CLIENT_ZONE> interface '<CLIENT_IFACE>'
set firewall zone <CLIENT_ZONE> default-action 'drop'
set firewall zone <CLIENT_ZONE> default-log

# Zone proxy (bridge Podman)
set firewall zone <PROXY_ZONE> interface '<PROXY_BRIDGE>'
set firewall zone <PROXY_ZONE> default-action 'drop'
set firewall zone <PROXY_ZONE> default-log

commit
```

### Groupes de ports

```vyos
# Ports proxy (reponses mode explicite)
set firewall group port-group P_proxy port '3128-3131'

# Ports HTTP/HTTPS (reponses intercept transparent)
set firewall group port-group all-http port '80'
set firewall group port-group all-http port '443'

commit
```

### Chaine : Client-vers-Proxy (entrant vers squid)

```vyos
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 action 'accept'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 protocol 'tcp'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 destination group port-group 'all-http'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 80 description 'Allow HTTP/S to proxy (PBR intercept)'

set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 action 'accept'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 protocol 'tcp'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 destination group port-group 'P_proxy'
set firewall ipv4 name <CLIENT>-to-<PROXY> rule 31280 description 'Allow explicit proxy ports'

# Attacher a la zone
set firewall zone <PROXY_ZONE> from <CLIENT_ZONE> firewall name '<CLIENT>-to-<PROXY>'

commit
```

### Chaine : Proxy-vers-Client (trafic retour depuis squid)

> **CRITIQUE** : Cette chaine necessite DEUX regles. Les reponses intercept
> transparent ont un `port source 80/443` (serveur original spoofe), PAS
> `port source 3128-3131`.

```vyos
# Regle 1 : Reponses proxy explicite (sport = ports proxy)
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 action 'accept'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 state 'established'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 state 'related'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 protocol 'tcp'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 source group port-group 'P_proxy'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31280 description 'Explicit proxy return'

# Regle 2 : Reponses intercept transparent (sport = 80/443, IP source SPOOFEE)
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 action 'accept'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 state 'established'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 state 'related'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 protocol 'tcp'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 source group port-group 'all-http'
set firewall ipv4 name <PROXY>-to-<CLIENT> rule 31281 description 'Intercept transparent return (splice/bump)'

# Attacher a la zone
set firewall zone <CLIENT_ZONE> from <PROXY_ZONE> firewall name '<PROXY>-to-<CLIENT>'

commit
```

### Chaine : Proxy-vers-WAN (squid upstream)

Squid a besoin d'un acces internet sortant pour recuperer le contenu des
serveurs d'origine :

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

<a id="etape-4"></a>
## 7. Etape 4 : Injection nft REDIRECT

Les regles REDIRECT doivent etre appliquees **a l'interieur** du namespace
reseau du conteneur apres son demarrage. Deux approches :

### Option A : Service systemd oneshot (recommandee pour VyOS)

Creer `/config/etc/services/squid-redirect.service` :

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

Activation :
```bash
sudo ln -sf /config/etc/services/squid-redirect.service \
  /etc/systemd/system/squid-redirect.service
sudo systemctl daemon-reload
sudo systemctl enable --now squid-redirect.service
```

> **Important** : Le `WantedBy=vyos-container-squid.service` garantit le
> demarrage du service a chaque start du conteneur (y compris apres un commit
> VyOS qui recree le conteneur). Voir [Piege n°3](#piege-3) pour comprendre
> pourquoi `PartOf=` seul est insuffisant.

### Option B : Auto-injection via Go init (avance)

Si le conteneur dispose de `CAP_NET_ADMIN`, le binaire Go init peut injecter les
regles REDIRECT au demarrage via la librairie `github.com/google/nftables`. Cela
elimine entierement le service systemd externe. Voir les sources de
`squid-hardened` pour les details d'implementation.

---

<a id="etape-5"></a>
## 8. Etape 5 : Configuration Squid

### Directives squid.conf pertinentes

```squid
# Proxy explicite (pas d'interception)
http_port 3128 name=explicit

# Intercept HTTP transparent (recoit le REDIRECT depuis port 80)
http_port 3129 intercept name=intercept_http

# Explicite + SSL Bump (clients opt-in avec CA installee)
http_port 3130 name=explicit_bump \
  ssl-bump \
  cert=/etc/squid/ssl_cert/bump.pem \
  generate-host-certificates=on \
  dynamic_cert_mem_cache_size=16MB \
  tls-dh=/etc/squid/ssl_cert/dhparam.pem \
  options=NO_SSLv3,NO_TLSv1,NO_TLSv1_1

# Intercept HTTPS transparent (recoit le REDIRECT depuis port 443)
https_port 3131 intercept name=intercept_https \
  ssl-bump \
  cert=/etc/squid/ssl_cert/bump.pem \
  generate-host-certificates=off \
  dynamic_cert_mem_cache_size=4MB \
  tls-dh=/etc/squid/ssl_cert/dhparam.pem \
  options=NO_SSLv3,NO_TLSv1,NO_TLSv1_1

# Preserver la destination originale (necessaire pour intercept)
client_dst_passthru on

# DNS : utiliser la gateway du reseau conteneur ou un resolveur dedie
dns_nameservers <DNS_SERVER>
```

### Politique SSL Bump (ACLs par appareil)

```squid
# Identification des appareils par IP source
acl device_a src <DEVICE_A_IP>
acl device_b src <DEVICE_B_IP>
acl all_clients src <CLIENT_SUBNET>

# Listes blanches de domaines par appareil
acl device_a_domains ssl::server_name "/etc/squid/acl/device-a-domains.txt"
acl common_domains ssl::server_name "/etc/squid/acl/common-domains.txt"

# Logique SSL Bump
ssl_bump peek step1
ssl_bump splice device_a device_a_domains port_intercept_https
ssl_bump splice device_a common_domains port_intercept_https
ssl_bump terminate port_intercept_https    # refuser HTTPS non liste
ssl_bump splice nobump_dst port_explicit_bump
ssl_bump bump port_explicit_bump
ssl_bump terminate all
```

> Cela permet un whitelist par appareil : seuls les domaines approuves passent
> en `splice` (pass-through), tout le reste est `terminate` (bloque). Pour les
> appareils necessitant un acces internet complet, remplacer `terminate` par
> `splice all_clients`.

---

<a id="etape-6"></a>
## 9. Etape 6 : Validation et tests

### 1. Verifier les regles REDIRECT dans le netns conteneur

```bash
PID=$(sudo podman inspect squid --format '{{.State.Pid}}')
sudo nsenter -t $PID -n nft list ruleset
```

Attendu :
```
table ip nat {
    chain prerouting {
        type nat hook prerouting priority dstnat; policy accept;
        tcp dport 80 redirect to :3129
        tcp dport 443 redirect to :3131
    }
}
```

### 2. Verifier le conntrack dans le netns conteneur

```bash
sudo nsenter -t $PID -n conntrack -L | grep <CLIENT_IP>
```

Chercher des entrees `ESTABLISHED` avec reply `sport=3131` (confirme que le
REDIRECT fonctionne) :
```
tcp 6 431997 ESTABLISHED src=<CLIENT_IP> dst=<SERVER_IP> sport=X dport=443 \
  src=<PROXY_IP> dst=<CLIENT_IP> sport=3131 dport=X [ASSURED]
```

### 3. Verifier les compteurs firewall VyOS

```bash
sudo nft list chain ip vyos_filter NAME_<PROXY>-to-<CLIENT>
```

Les deux regles (31280 et 31281) doivent afficher des compteurs non nuls.

### 4. Points de capture tcpdump

```bash
# Sur le bridge (point pivot) : on doit voir SYN du client ET SYN-ACK retour
sudo tcpdump -i <PROXY_BRIDGE> -n host <CLIENT_IP> and port 443

# Sur le VLAN client : on doit voir SYN sortant ET SYN-ACK retour
sudo tcpdump -i <CLIENT_IFACE> -n host <CLIENT_IP> and port 443
```

### 5. Test de bout en bout depuis le client

Si le client dispose d'un shell :
```bash
curl -v https://example.com 2>&1 | head -20
```

Verifier l'access.log de Squid (chercher le nom de port `intercept_https` et
le mode `splice` ou `bump`).

---

<a id="pieges-courants"></a>
## 10. Pieges courants

<a id="piege-1"></a>
### 1. Incoherence de port source firewall (trafic retour transparent)

**Symptome** : Les SYN-ACK de squid sont visibles sur le bridge (tcpdump) mais
n'atteignent jamais le client. Le log firewall affiche
`<PROXY>-to-<CLIENT>-default-D`.

**Cause** : En mode intercept transparent avec `client_dst_passthru on`, le
paquet reponse a un **port source 443** (le port du serveur original), pas
3128-3131. Une regle firewall n'autorisant que `sport @P_proxy` les droppe.

**Correctif** : Ajouter la regle 31281 avec `source group port-group all-http`
(voir Etape 3).

---

<a id="piege-2"></a>
### 2. ip_forward=1 dans le netns conteneur fait fuir les paquets

**Symptome** : Des SYN du client apparaissent dans les logs drop
`Webproxy-to-WAN` avec l'IP source du client (pas celle du proxy). Les paquets
"traversent" le conteneur.

**Cause** : Si `ip_forward=1` dans le netns conteneur ET qu'un paquet ne matche
pas les regles REDIRECT (ex: entree conntrack stale), le paquet est forward via
la route par defaut du conteneur vers VyOS, qui tente alors de le router vers
le WAN.

**Correctif** : Desactiver le forwarding IP dans le netns conteneur :
```bash
PID=$(sudo podman inspect squid --format '{{.State.Pid}}')
sudo nsenter -t $PID -n sysctl -w net.ipv4.ip_forward=0
```

Ajouter ceci dans le `ExecStart` de `squid-redirect.service` pour persister.

---

<a id="piege-3"></a>
### 3. systemd PartOf= ne propage pas le start aux services inactifs

**Symptome** : Apres `restart container squid` ou un commit VyOS qui recree le
conteneur, `squid-redirect.service` reste `inactive (dead)`.

**Cause** : `PartOf=` propage `stop` et `restart` aux services **actuellement
actifs**. Si le service dependant est deja inactif (il s'est arrete quand le
conteneur s'est arrete), le `start` ulterieur du conteneur ne le declenche PAS.

Les commits VyOS font `stop` + `daemon-reload` + `start` (transactions
separees), pas un `restart` atomique. Le stop se propage (redirect s'arrete),
mais le start ulterieur est independant.

**Correctif** : Ajouter `WantedBy=vyos-container-squid.service` dans la section
`[Install]` et re-activer. Cela cree un symlink `.wants/` qui assure la
propagation du start quel que soit l'etat actuel du dependant.

---

### 4. Entrees conntrack stale bloquent le REDIRECT

**Symptome** : Certaines connexions client fonctionnent, d'autres non. Les
connexions fonctionnelles utilisent de nouveaux ports source ; les echouees
retransmettent avec le meme port source indefiniment.

**Cause** : Si le conteneur tournait SANS regles REDIRECT (ex: apres restart
avant le lancement du service d'injection), les SYN clients ont cree des entrees
conntrack sans NAT. Ces entrees persistent tant que le client retransmet (chaque
retransmission rafraichit le timeout). Quand les regles REDIRECT sont ajoutees,
les paquets matchant des entrees existantes contournent le NAT (il ne s'applique
qu'aux connexions `NEW`).

**Correctif** : Flusher le conntrack dans le netns conteneur apres injection :
```bash
sudo nsenter -t $PID -n conntrack -F
```

---

### 5. Le restart du conteneur recree le netns (regles perdues)

**Symptome** : Les regles REDIRECT disparaissent apres tout restart conteneur.

**Cause** : Podman detruit et recree le namespace reseau a chaque restart. Les
regles nft sont ephemeres.

**Correctif** : Le service d'injection (`squid-redirect.service`) doit se
re-executer apres chaque start du conteneur. L'approche
`WantedBy=vyos-container-squid.service` gere cela. Alternativement, embarquer
les regles dans le processus init du conteneur (Option B de l'Etape 4).

---

### 6. L'unit conteneur VyOS vit dans /run (runtime)

**Symptome** : Les drop-ins ou symlinks `.wants/` semblent ignores apres un
commit VyOS.

**Cause** : VyOS genere `vyos-container-squid.service` dans
`/run/systemd/system/` (runtime, recree a chaque commit). Cependant, systemd
fusionne les configurations depuis `/etc/systemd/system/` (persistant) avec
`/run/systemd/system/` (runtime). Les symlinks dans
`/etc/systemd/system/vyos-container-squid.service.wants/` SONT honores pour
les units runtime.

**Correctif** : Apres `systemctl enable squid-redirect.service`, verifier que
le symlink existe dans `/etc/systemd/system/vyos-container-squid.service.wants/`.

---

### 7. CAP_NET_ADMIN necessaire

**Symptome** : Squid echoue avec `comm_open_listener: cannot bind to [::]:3129`
ou `SO_ORIGINAL_DST` retourne 0.0.0.0.

**Cause** : Le mode `intercept` necessite `CAP_NET_ADMIN` pour les options de
socket transparent. Sans cela, Squid ne peut pas recuperer la destination
originale.

**Correctif** : `set container name squid cap-add 'net-admin'` dans la config
VyOS.

---

<a id="boite-a-outils"></a>
## 11. Boite a outils de debug

### Arbre de decision

```
Le client n'a pas internet
|
+-- tcpdump sur <CLIENT_IFACE> : SYN visibles ?
|   +-- NON --> probleme routage/gateway client (pas un probleme proxy)
|
+-- tcpdump sur <PROXY_BRIDGE> : SYN arrivent ?
|   +-- NON --> PBR ne fonctionne pas (verifier mark + table + policy interface)
|
+-- nft list ruleset dans le netns conteneur : regles REDIRECT presentes ?
|   +-- NON --> service d'injection pas demarre (systemctl status squid-redirect)
|
+-- conntrack -L dans le netns conteneur : entree existe avec sport=3131 ?
|   +-- NON --> REDIRECT ne matche pas (entrees conntrack stale, flusher)
|
+-- tcpdump sur <PROXY_BRIDGE> : SYN-ACK visible depuis le proxy ?
|   +-- NON --> squid n'accepte pas la connexion (verifier logs squid, config ports)
|
+-- tcpdump sur <CLIENT_IFACE> : SYN-ACK atteint le client ?
|   +-- NON --> firewall bloque le chemin retour
|        +-- Verifier chaine <PROXY>-to-<CLIENT> : sport 80/443 autorise ? (Piege #1)
|
+-- SYN-ACK atteint le client mais connexion echoue quand meme
    +-- Verifier ACLs squid (ssl_bump terminate ? http_access deny ?)
```

### Commandes essentielles

```bash
# PID du conteneur (necessaire pour nsenter)
PID=$(sudo podman inspect squid --format '{{.State.Pid}}')

# Regles nft dans le conteneur
sudo nsenter -t $PID -n nft list ruleset

# Conntrack dans le conteneur (filtrer par client)
sudo nsenter -t $PID -n conntrack -L | grep <CLIENT_IP>

# Flusher le conntrack stale (option nucleaire)
sudo nsenter -t $PID -n conntrack -F

# Flusher uniquement les entrees d'un client
sudo nsenter -t $PID -n conntrack -D -s <CLIENT_IP>

# Compteurs de chaine firewall VyOS
sudo nft list chain ip vyos_filter NAME_<PROXY>-to-<CLIENT>

# tcpdump sur le bridge (point pivot de tout le trafic proxy)
sudo tcpdump -i <PROXY_BRIDGE> -n host <CLIENT_IP> -c 30

# Verifier ip_forward dans le conteneur
sudo nsenter -t $PID -n cat /proc/sys/net/ipv4/ip_forward

# Log d'acces Squid (20 dernieres lignes)
sudo podman logs squid 2>&1 | tail -20

# Statut du service
systemctl status squid-redirect.service
sudo journalctl -u squid-redirect.service --no-pager -n 20
```

### Prefixes de log firewall

| Prefixe | Signification |
|---------|---------------|
| `NAM-<X>-to-<Y>-default-D` | Droppe par action par defaut (aucune regle matchee) |
| `NAM-<X>-to-<Y>-<N>` | Matche la regle numero N dans la chaine X-to-Y |
| `INP-filter-default-D` | Droppe par chaine INPUT (trafic vers VyOS lui-meme) |

---

<a id="annexe"></a>
## 12. Annexe : Comparaison DNAT vs PBR

| Fonctionnalite | DNAT hote | PBR + REDIRECT dans conteneur |
|----------------|-----------|-------------------------------|
| Intercept HTTP transparent | Fonctionne (si meme hote) | Fonctionne |
| Intercept HTTPS transparent (ssl-bump) | **Casse** (SO_ORIGINAL_DST echoue) | Fonctionne |
| Isolation conteneur | Faible (NAT sur l'hote) | Forte (NAT dans le netns conteneur) |
| Complexite firewall | Simple (une regle NAT) | Plus elevee (PBR + zones + 2 regles retour) |
| Dependance au NAT hote | Oui | Non |
| Fonctionne en Podman rootless | Non | Oui (avec CAP_NET_ADMIN) |
| Necessite service externe pour les regles | Non | Oui (sauf si embarque dans l'init) |
| Survit au restart conteneur | N/A (NAT hote persiste) | Necessite re-injection |

### Quand utiliser quelle approche

- **DNAT** : Interception HTTP uniquement, setups simples, squid sur le meme
  hote que le firewall (non containerise), ou quand HTTPS est en pass-through.
- **PBR + REDIRECT** : Tout setup necessitant une interception HTTPS transparente
  (ssl-bump) dans un Squid containerise. C'est la seule approche fonctionnelle
  pour les conteneurs Podman/Docker.

---

## References

- [Documentation mode intercept Squid](http://www.squid-cache.org/Doc/config/http_port/)
- [VyOS Policy-Based Routing](https://docs.vyos.io/en/latest/configuration/policy/route.html)
- [nftables redirect](https://wiki.nftables.org/wiki-nftables/index.php/Performing_Network_Address_Translation_(NAT)#Redirect)
- [Linux conntrack et network namespaces](https://thermalcircle.de/doku.php?id=blog:linux:nftables_patterns_for_network_namespaces)
