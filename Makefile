.PHONY: help build up-transparent up-explicit up-all down logs ps ca scan sbom clean test

DC := docker compose

help:
	@echo "Cibles disponibles :"
	@echo "  make build           - Build des images hardenées"
	@echo "  make ca              - Génère la CA SSL Bump"
	@echo "  make up-transparent  - Démarre la stack en mode transparent"
	@echo "  make up-explicit     - Démarre la stack en mode HTTPS+bump"
	@echo "  make up-all          - Démarre les deux modes"
	@echo "  make down            - Arrête toute la stack"
	@echo "  make logs            - Tail des logs"
	@echo "  make ps              - État des conteneurs"
	@echo "  make test            - Test EICAR (proxy local)"
	@echo "  make scan            - Scan trivy des images"
	@echo "  make sbom            - Génération SBOM (syft)"
	@echo "  make clean           - Supprime volumes + images"

build:
	DOCKER_BUILDKIT=1 $(DC) build --pull

ca:
	./scripts/generate-ca.sh

up-transparent:
	$(DC) --profile transparent up -d

up-explicit: ca-check
	$(DC) --profile explicit up -d

up-all: ca-check
	$(DC) --profile all up -d

ca-check:
	@test -f certs/bump.pem || (echo "→ Génération CA"; ./scripts/generate-ca.sh)

down:
	$(DC) --profile all down

logs:
	$(DC) --profile all logs -f --tail=200

ps:
	$(DC) --profile all ps

test:
	./scripts/test-eicar.sh

scan:
	./scripts/deploy.sh scan

sbom:
	./scripts/deploy.sh sbom

clean:
	$(DC) --profile all down -v
	docker image rm localhost/squid-hardened:latest \
	                localhost/c-icap-hardened:latest \
	                localhost/clamav-hardened:latest 2>/dev/null || true
