# Architecture

## Vue d'ensemble

```
                          ┌──────────────────────────────────────┐
                          │  Hôte Docker (192.168.99.10)         │
                          │                                       │
   ┌──────────┐  HTTP/80  │   ┌─────────┐  ICAP   ┌─────────┐    │   Internet
   │  Client  │──────────►│   │  Squid  │────────►│ C-ICAP  │    │      ▲
   │  LAN A   │           │   │  3128   │         │  1344   │    │      │
   │ (transp) │           │   └─────────┘         └────┬────┘    │      │
   └──────────┘           │        ▲                   │ TCP     │      │
        ▲                 │        │   reqmod/respmod  ▼ 3310    │      │
        │                 │        │              ┌─────────┐    │      │
        │ DNAT (VyOS)     │        │              │ ClamAV  │    │      │
        │                 │        │              │  clamd  │    │      │
        │                 │        │              └─────────┘    │      │
   ┌──────────┐ HTTPS/443 │        │                              │      │
   │  Client  │──────────►│        │ 3128 explicite               │      │
   │  LAN B   │           │        │ 3129 SSL Bump                │      │
   │(explicit)│ via WPAD  │        │                              │      │
   └──────────┘           └────────┴──────────────────────────────┘      │
                                                                          │
                                          ┌──────────┐                    │
                                          │  VyOS    │────────────────────┘
                                          │ Router/FW│
                                          └──────────┘
```

## Flux mode transparent

```
Client (LAN A, 192.168.10.x)
   │  GET http://example.com/ → port 80
   ▼
VyOS eth1
   │  DNAT règle 110 :
   │     dst:80 → 192.168.99.10:3128
   │  SNAT règle 100 (hairpin) si besoin
   ▼
Squid (3128 intercept)
   │  - Retrouve la dest originale via SO_ORIGINAL_DST
   │  - Envoie reqmod en ICAP → C-ICAP
   │  - C-ICAP → clamd : scan URL (anti-phishing) + futures réponses
   ▼
Internet
   │  ← réponse HTTP
   ▼
Squid récupère le body
   │  - respmod ICAP : analyse antivirus du contenu
   │  - si virus → page "blocked" renvoyée au client
   │  - sinon → cache + relay au client
   ▼
Client
```

## Flux mode explicite + SSL Bump

```
Client (LAN B, 192.168.20.x)
   │  1. DHCP → reçoit option 252 WPAD
   │  2. GET http://wpad.lan.local/wpad.dat
   │  3. PAC dit : "PROXY proxy.lan.local:3129 pour https://"
   │  4. CONNECT example.com:443 HTTP/1.1
   ▼
Squid (3129 ssl-bump)
   │  Étape 1 (peek) : récupère le SNI (ex: "banque.fr")
   │  Étape 2        : si SNI ∈ nobump_domains → splice (pass-through)
   │  Étape 3        : sinon, bump :
   │                    - termine TLS côté client avec cert forgé
   │                      (signé par la CA bump.pem)
   │                    - ré-initie TLS côté serveur réel
   │                    - voit le HTTP en clair entre les deux
   │                    - envoie respmod ICAP → C-ICAP/clamd
   ▼
Internet
```

## Choix d'architecture

### Pourquoi C-ICAP et pas direct ?

Squid sait parler ICAP nativement (`icap_service`) mais ne sait pas parler ClamAV directement. **C-ICAP** est un serveur ICAP avec une API modules ; le module **squidclamav** (Gilles Darold) fait le pont avec ClamAV. Cette séparation permet :

- Mise à jour indépendante de chaque composant
- Possibilité d'ajouter d'autres modules ICAP (URL filtering, content rewriting, DLP)
- Scaling horizontal possible (plusieurs c-icap derrière le même Squid)

### Pourquoi pas du host networking ?

L'isolation est meilleure en bridge dédié (`br-proxy`). Les ports nécessaires (3128/3129) sont publiés explicitement. C-ICAP et ClamAV ne sont **pas** exposés à l'extérieur.

### Pourquoi 2 confs Squid distinctes (et pas une) ?

- Le mode `intercept` exige `cap_add: NET_ADMIN` qui n'est pas souhaitable en explicit
- Les directives `ssl_bump` n'ont aucun sens en mode transparent (sans CA)
- Confs séparées = lecture plus facile, lint plus simple, moins de bugs

### Pourquoi FROM scratch ?

Les images finales sont construites `FROM scratch` (tier Platine) :
- Zero shell, zero package manager, zero surface d'attaque residuelle
- Entrypoints Go statiques (init + healthcheck, stdlib uniquement)
- tini-static comme PID 1 (signal forwarding + zombie reaping)
- Seules les libs runtime strictement necessaires sont copiees depuis le stage builder

## Capacité

Sur un hôte 4 vCPU / 8 Go :
- ~500 req/s en HTTP transparent
- ~200 req/s en HTTPS bump (coût TLS double)
- ~50-100 Mo/s antivirus scanning (selon types fichiers)

Tuning recommandé : `cache_mem`, `cache_dir`, `MaxThreads` ClamAV, `ThreadsPerChild` C-ICAP – voir confs.
