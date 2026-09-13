SHELL := /bin/bash
.PHONY: build clean install uninstall run test release deploy-vm vm-start vm-stop vm-status vm-logs vm-shell setup-incus create-incus deploy-incus update-incus start-incus stop-incus status-incus logs-incus shell-incus create-server delete-server list-servers shell-server enroll-server link-server restart-incus

BINARY_NAME  = autohost-agent
INSTALL_PATH = /usr/local/bin
CONFIG_PATH  = /etc/autohost
SERVICE_PATH = /etc/systemd/system

# Target instance name (defaults to autohost-test, customizable via INSTANCE=... or NAME=...)
INSTANCE ?= autohost-test
NAME     ?= $(INSTANCE)
RAM      ?= 512MiB
CPU      ?= 1

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  = -s -w -X main.Version=$(VERSION)
PLATFORMS = linux/amd64 linux/arm64

build:
	@echo "🔨 Building $(BINARY_NAME) $(VERSION)..."
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) cmd/agent/main.go
	@echo "✅ Build complete: ./$(BINARY_NAME)"

release:
	@CURRENT=$$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
	echo "📌 Versión actual: $$CURRENT"; \
	printf "🔖 Nueva versión (ej. v1.2.3): "; \
	read NEW_VERSION; \
	if [ -z "$$NEW_VERSION" ]; then echo "❌ La versión no puede estar vacía"; exit 1; fi; \
	echo "🏷️  Creando tag $$NEW_VERSION..."; \
	git tag -a "$$NEW_VERSION" -m "Release $$NEW_VERSION"; \
	echo "🚀 Compilando release $$NEW_VERSION para: $(PLATFORMS)"; \
	mkdir -p dist; \
	for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*}; GOARCH=$${platform#*/}; \
		out="dist/$(BINARY_NAME)-$${GOOS}-$${GOARCH}"; \
		echo "  → $$out"; \
		GOOS=$$GOOS GOARCH=$$GOARCH go build -ldflags "-s -w -X main.Version=$$NEW_VERSION" -o "$$out" cmd/agent/main.go; \
	done; \
	echo "🔐 Generando checksums..."; \
	cd dist && sha256sum $(BINARY_NAME)-* > "checksums_$${NEW_VERSION}.txt"; \
	echo "✅ Artefactos en dist/"; \
	ls -lh dist/; \
	echo ""; \
	git push origin "$$NEW_VERSION"; \
	echo "🎉 Release $$NEW_VERSION creada y subida a GitHub"

# ─── Incus Infrastructure ───────────────────────────────────────────────────

setup-incus:
	@echo "📦 Instalando Incus..."
	@if command -v incus >/dev/null 2>&1; then \
		echo "✅ Incus ya está instalado: $$(incus --version)"; \
	else \
		sudo apt-get update -qq && sudo apt-get install -y incus; \
	fi
	@echo "⚙️  Inicializando Incus..."
	@if ! incus info >/dev/null 2>&1; then \
		echo "{}" | sudo incus admin init --preseed; \
	else \
		echo "✅ Incus ya está inicializado"; \
	fi
	@echo "👤 Añadiendo usuario $$(whoami) al grupo incus..."
	@if ! id -nG $$(whoami) | grep -qw incus; then \
		sudo usermod -aG incus $$(whoami); \
		echo "✅ Usuario añadido al grupo incus (ejecuta: newgrp incus)"; \
	else \
		echo "✅ Ya perteneces al grupo incus"; \
	fi

create-server:
	@echo "🚀 Creando servidor '$(NAME)' con $(RAM) de RAM y $(CPU) CPU..."
	@if incus info $(NAME) >/dev/null 2>&1; then \
		echo "✅ La instancia '$(NAME)' ya existe"; \
	else \
		incus launch images:ubuntu/24.04 $(NAME) -c limits.memory=$(RAM) -c limits.cpu=$(CPU) -c security.nesting=true; \
		echo "⏳ Esperando arranque..."; \
		sleep 4; \
		echo "📦 Instalando Docker y dependencias en $(NAME)..."; \
		incus exec $(NAME) -- apt-get update -qq; \
		incus exec $(NAME) -- apt-get install -y -qq docker.io curl wireguard-tools; \
		echo "✅ Servidor '$(NAME)' listo."; \
	fi

create-incus: create-server

delete-server:
	@echo "🗑️  Eliminando servidor '$(NAME)'..."
	-incus stop $(NAME) --force 2>/dev/null || true
	-incus delete $(NAME) 2>/dev/null || true
	@echo "✅ Servidor '$(NAME)' eliminado."

list-servers:
	@incus list

shell-server:
	@INSTANCES=($$(incus list -c n --format csv)); \
	if [ $${#INSTANCES[@]} -eq 0 ]; then \
		echo "❌ No hay instancias Incus disponibles."; \
		exit 1; \
	elif [ "$$NAME" != "autohost-test" ] && [ -n "$$NAME" ] && incus info "$$NAME" >/dev/null 2>&1; then \
		echo "🚀 Conectando a '$$NAME'..."; \
		incus exec "$$NAME" -- bash; \
	elif [ $${#INSTANCES[@]} -eq 1 ]; then \
		echo "🚀 Conectando a $${INSTANCES[0]}..."; \
		incus exec "$${INSTANCES[0]}" -- bash; \
	else \
		echo "📋 Servidores Incus disponibles:"; \
		echo ""; \
		for i in "$${!INSTANCES[@]}"; do \
			STATUS=$$(incus list "$${INSTANCES[$$i]}" -c s --format csv); \
			IPV4=$$(incus list "$${INSTANCES[$$i]}" -c 4 --format csv | head -n1); \
			printf "  [\033[1;32m%d\033[0m] %-20s (\033[1;34m%s\033[0m, %s)\n" "$$((i+1))" "$${INSTANCES[$$i]}" "$$STATUS" "$$IPV4"; \
		done; \
		echo ""; \
		printf "👉 Selecciona el número de servidor [1-%d]: " "$${#INSTANCES[@]}"; \
		read -r CHOICE; \
		if ! [[ "$$CHOICE" =~ ^[0-9]+$$ ]] || [ "$$CHOICE" -lt 1 ] || [ "$$CHOICE" -gt "$${#INSTANCES[@]}" ]; then \
			echo "❌ Selección inválida"; \
			exit 1; \
		fi; \
		SELECTED="$${INSTANCES[$$((CHOICE-1))]}"; \
		echo "🚀 Conectando a $$SELECTED..."; \
		incus exec "$$SELECTED" -- bash; \
	fi

# ─── Deployment & Lifecycle Management ──────────────────────────────────────

deploy-incus: build
	@echo "🚀 Desplegando autohost-agent en servidor '$(NAME)'..."
	@incus info $(NAME) >/dev/null 2>&1 || $(MAKE) create-server NAME=$(NAME)
	@echo "1. Preparando entorno y usuario del sistema en '$(NAME)'..."
	incus exec $(NAME) -- sudo id -u autohost >/dev/null 2>&1 || incus exec $(NAME) -- sudo useradd --system --no-create-home --shell /usr/sbin/nologin autohost
	incus exec $(NAME) -- sudo usermod -aG docker autohost 2>/dev/null || true
	incus exec $(NAME) -- sudo mkdir -p /etc/autohost /var/lib/autohost
	incus exec $(NAME) -- sudo chown autohost:autohost /var/lib/autohost
	@echo "2. Transfiriendo binario y configuración..."
	incus file push $(BINARY_NAME) $(NAME)/usr/local/bin/$(BINARY_NAME) --mode=0755
	incus file push configs/agent.yaml $(NAME)/etc/autohost/config.yaml --mode=0640
	incus exec $(NAME) -- sudo chown root:autohost /etc/autohost/config.yaml
	incus file push autohost-agent.service $(NAME)/etc/systemd/system/autohost-agent.service --mode=0644
	incus exec $(NAME) -- sudo systemctl daemon-reload
	@echo ""
	@echo "✅ Despliegue completado en '$(NAME)'!"
	@echo "   Para iniciar: make start-incus NAME=$(NAME)"
	@echo "   Para ver estado: make status-incus NAME=$(NAME)"

update-incus: build
	@INSTANCES=($$(incus list -c n --format csv)); \
	if [ $${#INSTANCES[@]} -eq 0 ]; then \
		echo "❌ No hay instancias Incus disponibles."; \
		exit 1; \
	elif [ "$$NAME" = "all" ] || [ "$$NAME" = "ALL" ]; then \
		echo "⚡ Actualizando binario en TODOS los servidores ($${#INSTANCES[@]})..."; \
		for inst in "$${INSTANCES[@]}"; do \
			echo "📦 Transfiriendo y reiniciando en '$$inst'..."; \
			incus file push $(BINARY_NAME) "$$inst/tmp/$(BINARY_NAME)" --mode=0755; \
			incus exec "$$inst" -- sudo mv -f /tmp/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME); \
			incus exec "$$inst" -- sudo systemctl restart autohost-agent; \
			echo "   ✅ '$$inst' actualizado."; \
		done; \
		echo "🎉 Todos los servidores actualizados con éxito."; \
	elif [ "$$NAME" != "autohost-test" ] && [ -n "$$NAME" ] && incus info "$$NAME" >/dev/null 2>&1; then \
		echo "🔄 Actualizando binario en '$$NAME'..."; \
		incus file push $(BINARY_NAME) "$$NAME/tmp/$(BINARY_NAME)" --mode=0755; \
		incus exec "$$NAME" -- sudo mv -f /tmp/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME); \
		incus exec "$$NAME" -- sudo systemctl restart autohost-agent; \
		echo "✅ Agente actualizado y reiniciado en '$$NAME'."; \
	else \
		echo "📋 Servidores Incus disponibles para actualizar:"; \
		echo ""; \
		printf "  [\033[1;33m0\033[0m] \033[1;33m⚡ ACTUALIZAR EN TODOS LOS SERVIDORES A LA VEZ\033[0m\n"; \
		for i in "$${!INSTANCES[@]}"; do \
			STATUS=$$(incus list "$${INSTANCES[$$i]}" -c s --format csv); \
			IPV4=$$(incus list "$${INSTANCES[$$i]}" -c 4 --format csv | head -n1); \
			printf "  [\033[1;32m%d\033[0m] %-20s (\033[1;34m%s\033[0m, %s)\n" "$$((i+1))" "$${INSTANCES[$$i]}" "$$STATUS" "$$IPV4"; \
		done; \
		echo ""; \
		printf "👉 Selecciona el número de servidor [0-%d]: " "$${#INSTANCES[@]}"; \
		read -r CHOICE; \
		if [ "$$CHOICE" = "0" ] || [ "$$CHOICE" = "all" ] || [ "$$CHOICE" = "ALL" ]; then \
			echo ""; \
			echo "⚡ Actualizando binario en TODOS los servidores ($${#INSTANCES[@]})..."; \
			for inst in "$${INSTANCES[@]}"; do \
				echo "📦 Transfiriendo y reiniciando en '$$inst'..."; \
				incus file push $(BINARY_NAME) "$$inst/tmp/$(BINARY_NAME)" --mode=0755; \
				incus exec "$$inst" -- sudo mv -f /tmp/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME); \
				incus exec "$$inst" -- sudo systemctl restart autohost-agent; \
				echo "   ✅ '$$inst' actualizado."; \
			done; \
			echo "🎉 Todos los servidores actualizados con éxito."; \
		elif ! [[ "$$CHOICE" =~ ^[0-9]+$$ ]] || [ "$$CHOICE" -lt 1 ] || [ "$$CHOICE" -gt "$${#INSTANCES[@]}" ]; then \
			echo "❌ Selección inválida"; \
			exit 1; \
		else \
			SELECTED="$${INSTANCES[$$((CHOICE-1))]}"; \
			echo "🔄 Actualizando binario en '$$SELECTED'..."; \
			incus file push $(BINARY_NAME) "$$SELECTED/tmp/$(BINARY_NAME)" --mode=0755; \
			incus exec "$$SELECTED" -- sudo mv -f /tmp/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME); \
			incus exec "$$SELECTED" -- sudo systemctl restart autohost-agent; \
			echo "✅ Agente actualizado y reiniciado en '$$SELECTED'."; \
		fi; \
	fi


start-incus:
	@echo "▶️  Iniciando autohost-agent en '$(NAME)'..."
	incus exec $(NAME) -- sudo systemctl enable --now autohost-agent
	@echo "✅ Servicio iniciado en '$(NAME)'."

stop-incus:
	@echo "⏹  Deteniendo autohost-agent en '$(NAME)'..."
	incus exec $(NAME) -- sudo systemctl stop autohost-agent
	@echo "✅ Servicio detenido."

restart-incus:
	@echo "🔄 Reiniciando autohost-agent en '$(NAME)'..."
	incus exec $(NAME) -- sudo systemctl restart autohost-agent
	@echo "✅ Servicio reiniciado en '$(NAME)'."

status-incus:
	incus exec $(NAME) -- sudo systemctl status autohost-agent

logs-incus:
	incus exec $(NAME) -- sudo journalctl -u autohost-agent -f

shell-incus: shell-server

# ─── Enrollment Helpers ─────────────────────────────────────────────────────

enroll-server: deploy-incus
	@echo "🔗 Enrolando servidor '$(NAME)' en AutoHost..."
	@GATEWAY=$$(incus exec $(NAME) -- sh -c "ip route show default" | awk '/default/{print $$3}' | head -1); \
	 echo "   Host Gateway: $$GATEWAY"; \
	 echo "🔨 Compilando autohost-cli..."; \
	 (cd ../autohost-cli && go build -o dist/autohost main.go); \
	 incus file push ../autohost-cli/dist/autohost $(NAME)/usr/local/bin/autohost --mode=0755; \
	 echo "🚀 Iniciando asistente 'autohost up'..."; \
	 incus exec $(NAME) -- env AUTOHOST_CLOUD_URL="http://localhost:3000" AUTOHOST_API_URL="http://$$GATEWAY:8080" autohost up

link-server: deploy-incus
	@if [ -z "$(TOKEN)" ]; then echo "❌ Error: Especifica el token con TOKEN=<tu_token>"; exit 1; fi
	@GATEWAY=$$(incus exec $(NAME) -- sh -c "ip route show default" | awk '/default/{print $$3}' | head -1); \
	 echo "   Host Gateway: $$GATEWAY"; \
	 echo "🔨 Compilando autohost-cli..."; \
	 (cd ../autohost-cli && go build -o dist/autohost main.go); \
	 incus file push ../autohost-cli/dist/autohost $(NAME)/usr/local/bin/autohost --mode=0755; \
	 echo "🚀 Vinculando nodo con token..."; \
	 incus exec $(NAME) -- autohost enroll link --api http://$$GATEWAY:8080 --token $(TOKEN) --name $(NAME); \
	 incus exec $(NAME) -- sudo systemctl restart autohost-agent; \
	 echo "✅ Servidor '$(NAME)' vinculado y activo en AutoHost."
