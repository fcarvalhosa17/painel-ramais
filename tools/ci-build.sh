#!/bin/sh
# Build manual em Linux/container (uso opcional; nao ha pipeline que o execute): binario, RPM da versao <versao>, RPM rc (Release 0.1.rc.1), RPM antigo 0.0.9 (para testar rpm -U) e tar.gz.
# Uso: tools/ci-build.sh <versao>. Saida em dist/. nfpm fixado (mesma versao usada em .tools\ no Windows).
set -eu
VER=${1:?uso: tools/ci-build.sh <versao>}
NFPM=github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0
cd "$(dirname "$0")/.."
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$VER" -o dist/painel-ramais ./cmd/painel-ramais
VERSION=$VER RELEASE=1 go run "$NFPM" pkg --packager rpm --config packaging/nfpm.yaml --target dist/
VERSION=$VER RELEASE=0.1.rc.1 go run "$NFPM" pkg --packager rpm --config packaging/nfpm.yaml --target dist/
VERSION=0.0.9 RELEASE=1 go run "$NFPM" pkg --packager rpm --config packaging/nfpm.yaml --target dist/
${PYTHON:-python3} -I tools/bundle.py "$VER" --rpm "dist/painel-ramais-$VER-1.x86_64.rpm"
ls -l dist
