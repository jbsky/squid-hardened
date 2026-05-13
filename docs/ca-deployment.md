# Déploiement de la CA SSL Bump sur les clients

Sans installation de la CA, **tout** site HTTPS apparaîtra avec une erreur de certificat aux utilisateurs du LAN explicite. Cette étape est **obligatoire** pour le mode bump.

Fichier à distribuer : `certs/bump.crt` (au format PEM).

⚠️ **Ne distribuer JAMAIS le `bump.pem` ou `bump.key`** – ce sont les clés privées de la CA.

## Windows (AD/Domaine)

Via **GPO** :
1. Console **Group Policy Management**
2. Nouvelle GPO ou existante → `Computer Configuration` → `Policies` → `Windows Settings` → `Security Settings` → `Public Key Policies` → `Trusted Root Certification Authorities`
3. Clic droit → **Import** → sélectionner `bump.crt`
4. Force update : `gpupdate /force` côté client

Vérification :
```cmd
certutil -store -enterprise root | findstr /i "Internal Squid Bump CA"
```

## Windows hors domaine

```cmd
certutil -addstore -f "ROOT" bump.crt
```
(à exécuter en admin)

## Firefox (Windows/Linux/macOS)

Firefox utilise **NSS** et son propre store. Deux options :

### Option A – Politique Entreprise
Créer `policies.json` dans le dossier d'installation de Firefox (`distribution/`) :
```json
{
  "policies": {
    "Certificates": {
      "ImportEnterpriseRoots": true,
      "Install": ["bump.crt"]
    }
  }
}
```

### Option B – Manuel
Préférences → Vie privée → Certificats → Afficher les certificats → Autorités → Importer.

## macOS

```bash
sudo security add-trusted-cert -d -r trustRoot \
     -k /Library/Keychains/System.keychain bump.crt
```

Via MDM (Jamf, Mosyle…) : déployer un profil de configuration `Certificate Payload`.

## iOS / iPadOS

Via MDM : profil de configuration avec le PEM converti en `.cer`.

Manuel :
1. Envoyer `bump.crt` par mail / l'héberger sur un site interne
2. Sur l'appareil : **Réglages** → profil téléchargé → **Installer**
3. **Réglages** → **Général** → **Information** → **Réglages de confiance des certificats** → activer la CA

## Android

⚠️ Depuis Android 7, les apps ne font confiance qu'aux CA système par défaut. Pour qu'une CA utilisateur soit acceptée par toutes les apps, il faut soit :

- Avoir un **MDM/EMM** qui pousse en CA système (Workspace ONE, Intune Android Enterprise)
- Soit modifier le `network_security_config.xml` de chaque app (impossible sur apps tierces)

Manuel (CA utilisateur seulement) :
- **Paramètres** → **Sécurité** → **Chiffrement et identifiants** → **Installer un certificat** → **Certificat CA**

## Linux (Debian/Ubuntu)

```bash
sudo cp bump.crt /usr/local/share/ca-certificates/internal-bump.crt
sudo update-ca-certificates
```

Pour Firefox/Chromium NSS :
```bash
certutil -A -n "Internal Bump CA" -t "CT,C,C" \
         -d sql:$HOME/.pki/nssdb -i bump.crt
```

## Linux (RHEL/Rocky/Fedora)

```bash
sudo cp bump.crt /etc/pki/ca-trust/source/anchors/internal-bump.crt
sudo update-ca-trust
```

## Conteneurs Docker (apps internes derrière le proxy)

Dans le Dockerfile de l'app :
```dockerfile
COPY bump.crt /usr/local/share/ca-certificates/
RUN update-ca-certificates
```

Pour Python `requests` : variable `REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt`.
Pour Node.js : `NODE_EXTRA_CA_CERTS=/path/to/bump.crt`.
Pour Java : importer dans le keystore JVM (`keytool -importcert`).

## Rotation de la CA

Avant expiration (par défaut 10 ans) :
1. Générer la nouvelle CA : `./scripts/generate-ca.sh` (renommer l'ancienne d'abord)
2. La déployer **en parallèle** de l'ancienne pendant 30 jours
3. Configurer Squid pour signer avec la nouvelle (changer `cert=`)
4. Reload Squid (`squid -k reconfigure`)
5. Vérifier puis retirer l'ancienne CA des clients

## Inspection / Audit utilisateur

Pour des raisons légales (RGPD, code du travail), une bannière HTML peut être injectée par C-ICAP pour informer l'utilisateur que son trafic HTTPS est inspecté.

Voir module `squidclamav` + module ICAP custom pour ré-écriture HTML.
