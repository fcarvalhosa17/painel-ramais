#!/bin/sh
# Smoke do pacote DENTRO de um container Rocky (sem systemd rodando), como root.
# Uso: rpm-smoke.sh <rpm-antigo> <rpm-novo> <versao-release-do-novo> <instalar.py> <rpm-rc>
# Cobre: rpm -i, rpm -U, rpm -e, scriptlets sem systemd, instalar.py verificar. Sai != 0 na primeira falha.
set -u
ANTIGO=$1; NOVO=$2; VER=$3; INSTALADOR=$4; RC=${5:?rpm rc ausente}
LOG=${SMOKE_LOG:-/tmp/rpm-smoke}; mkdir -p "$LOG"
falha() { echo "FALHA: $*" >&2; exit 1; }
ok()    { echo "[ok] $*"; }

grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release
[ -d /run/systemd/system ] && falha "ambiente tem systemd rodando; o teste exige container sem systemd"
echo "systemctl: $(command -v systemctl || echo ausente)"

# rpm -i (instalacao nova): scriptlets tem de registrar o motivo de nao usar o systemd
rpm -i "$ANTIGO" > "$LOG/i.log" 2>&1 || { cat "$LOG/i.log"; falha "rpm -i"; }
cat "$LOG/i.log"
grep -q "systemd nao esta em execucao" "$LOG/i.log" || falha "%post nao registrou a ausencia do systemd"
grep -qi "not been booted\|Failed to connect to bus" "$LOG/i.log" && falha "%post chamou systemctl sem systemd"
rpm -q painel-ramais | grep -q "painel-ramais-0.0.9-1" || falha "versao antiga nao instalada"
getent passwd painel-ramais >/dev/null || falha "usuario painel-ramais nao criado"
[ -x /usr/bin/painel-ramais ] || falha "binario ausente"
[ -s /etc/painel-ramais/secret ] && [ -s /etc/painel-ramais/ami-secret ] || falha "segredos nao gerados"
SUM1=$(sha256sum /etc/painel-ramais/secret /etc/painel-ramais/ami-secret)
ok "rpm -i"

# rpm -U: dados e segredos preservados
rpm -U "$NOVO" > "$LOG/u.log" 2>&1 || { cat "$LOG/u.log"; falha "rpm -U"; }
cat "$LOG/u.log"
grep -q "systemd nao esta em execucao" "$LOG/u.log" || falha "%post (upgrade) nao registrou a ausencia do systemd"
grep -qi "not been booted\|Failed to connect to bus" "$LOG/u.log" && falha "upgrade chamou systemctl sem systemd"
[ "$(rpm -q --qf '%{VERSION}-%{RELEASE}' painel-ramais)" = "$VER" ] || falha "versao apos -U: $(rpm -q painel-ramais), esperado $VER"
[ "$SUM1" = "$(sha256sum /etc/painel-ramais/secret /etc/painel-ramais/ami-secret)" ] || falha "upgrade regenerou segredos"
/usr/bin/painel-ramais version | tee "$LOG/ver.txt"
grep -q "painel-ramais ${VER%%-*}" "$LOG/ver.txt" || falha "binario nao reporta a versao esperada"
ok "rpm -U"

# instalar.py verificar (Rocky 8: sem python3, tem de cair no platform-python)
if [ -x /usr/libexec/platform-python ] && ! command -v python3 >/dev/null 2>&1; then echo "python3 ausente; usando platform-python"; fi
cp "$INSTALADOR" /tmp/instalar.py && chmod 0755 /tmp/instalar.py
/tmp/instalar.py verificar > "$LOG/verificar.log" 2>&1; RCV=$?
cat "$LOG/verificar.log"
# 0 = sem erros; 1 = erros de ambiente (esperado aqui: nao ha Asterisk). 2/3/traceback = defeito do instalador.
[ "$RCV" -le 1 ] || falha "instalar.py verificar saiu com $RCV"
grep -q "Resultado:" "$LOG/verificar.log" || falha "instalar.py verificar nao chegou ao resultado"
grep -q "Traceback" "$LOG/verificar.log" && falha "traceback em instalar.py verificar"
ok "instalar.py verificar (rc=$RCV)"

# rpm -e: remove pacote, mantem dados (postremove documenta isso)
rpm -e painel-ramais > "$LOG/e.log" 2>&1 || { cat "$LOG/e.log"; falha "rpm -e"; }
cat "$LOG/e.log"
grep -qi "not been booted\|Failed to connect to bus" "$LOG/e.log" && falha "remocao chamou systemctl sem systemd"
rpm -q painel-ramais >/dev/null 2>&1 && falha "pacote ainda instalado"
[ -e /usr/bin/painel-ramais ] && falha "binario sobrou"
[ -s /etc/painel-ramais/secret ] || falha "remocao apagou o segredo (deveria manter)"
ok "rpm -e"
# pre-release: rc < final pela comparacao do proprio rpm, e rpm -U do rc para o final tem de funcionar
BASE=${VER%-*}
CMP=$(rpm --eval "%{lua: print(rpm.vercmp('$BASE-0.1.rc.1', '$VER'))}")
[ "$CMP" = "-1" ] || falha "rpm.vercmp($BASE-0.1.rc.1, $VER) = $CMP, esperado -1"
rpm -i "$RC" > "$LOG/rc.log" 2>&1 || { cat "$LOG/rc.log"; falha "rpm -i do rc"; }
rpm -U "$NOVO" > "$LOG/rcu.log" 2>&1 || { cat "$LOG/rcu.log"; falha "rpm -U do rc para o final"; }
[ "$(rpm -q --qf '%{VERSION}-%{RELEASE}' painel-ramais)" = "$VER" ] || falha "apos rc -> final: $(rpm -q painel-ramais)"
rpm -e painel-ramais || falha "rpm -e apos rc -> final"
ok "rc -> final (vercmp=-1)"
echo "SMOKE OK"
