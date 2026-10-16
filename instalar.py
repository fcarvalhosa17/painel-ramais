#!/bin/sh
"exec" "$(command -v python3 || echo /usr/libexec/platform-python)" "$0" "$@"
# Instalador do Painel de Ramais (Issabel 5 / Rocky Linux 8). Contrato: docs/CONTRATOS.md secao 15.
# Python 3.6+, somente biblioteca padrao. Poliglota: a linha 2 e comando sh e expressao python.
# Papel: orquestrar e validar. NUNCA edita manager_custom.conf, Apache ou SELinux (isso e o %post do RPM)
# e nunca grava no SQLite (usa a CLI do daemon).
# Uso: ./instalar.py {verificar|instalar|status|rollback|remover|homologar} [--respostas arq.ini] [--sim] [--purge] [--duracao MIN] [--saida DIR]

import argparse
import configparser
import datetime
import glob
import hashlib
import http.client
import io
import json
import os
import re
import secrets
import select
import shutil
import signal
import socket
import stat
import sqlite3
import ssl
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

LOG_DIR = "/var/log/painel-ramais"
LOG_FILE = LOG_DIR + "/instalacao.log"
STATE_DIR = "/var/lib/painel-ramais"
CONF_DIR = "/etc/painel-ramais"
CONF_FILE = CONF_DIR + "/config.yaml"
# Area do instalador: so do root, FORA de /var/lib/painel-ramais (que pertence ao usuario do servico).
INSTALADOR_DIR = "/var/lib/painel-ramais-instalador"
RPM_ANTERIOR = INSTALADOR_DIR + "/rpm-anterior"
RPM_ATUAL = INSTALADOR_DIR + "/rpm-instalado"
DB_FILE = STATE_DIR + "/painel.db"
MANAGER_CONF = "/etc/asterisk/manager.conf"
MANAGER_CUSTOM = "/etc/asterisk/manager_custom.conf"
PKG = "painel-ramais"
SOCKET_UNIT_PATH = "/usr/lib/systemd/system/painel-ramais.socket"
SOCKET_WANTS = "/etc/systemd/system/sockets.target.wants/painel-ramais.socket"
SOCKET = PKG + ".socket"  # socket activation: o systemd segura a porta (127.0.0.1:8790)
SVC_USER = "painel-ramais"
BIN = "/usr/bin/painel-ramais"
DEFAULT_PORT = 8790
MIN_ASTERISK = 13
MIN_DISK_ERRO = 512 * 1024 * 1024
MIN_DISK_AVISO = 2 * 1024 * 1024 * 1024
HEALTH_TIMEOUT = 60

# Codigos de saida (CONTRATOS 15.3)
EXIT_OK, EXIT_VERIFICAR, EXIT_FALHA, EXIT_USO, EXIT_CANCELADO = 0, 1, 2, 3, 4
# CLI do daemon (CONTRATOS 8): user add retorna 3 se o usuario ja existe.
CLI_EXISTE = 3

OK, AVISO, ERRO = "OK", "AVISO", "ERRO"
TECNOLOGIAS = ("auto", "sip", "pjsip")
CHAVES_RESPOSTAS = ("tecnologia", "admin_usuario", "admin_senha_arquivo", "grupos_csv")
CHAVES_IGNORADAS = ("admin_usuario", "admin_senha_arquivo")  # fase 1; a fase 2 nao pergunta senha (ADR-004)
USUARIO_EMERGENCIA = "emergencia"  # mesmo texto da constante do daemon (PLANO C5)
SENHA_EMERGENCIA = CONF_DIR + "/admin-emergencia"
CMD_ATUAL = ["-"]


class Falha(Exception):
    def __init__(self, msg, codigo=EXIT_FALHA):
        Exception.__init__(self, msg)
        self.codigo = codigo


def Uso(msg):
    return Falha(msg, EXIT_USO)


# ---------------------------------------------------------------- log / execucao

def _agora():
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def log(msg):
    try:
        os.makedirs(LOG_DIR, 0o700, exist_ok=True)
        fd = os.open(LOG_FILE, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o600)
        with os.fdopen(fd, "a") as f:
            f.write("[%s] [%s] %s\n" % (_agora(), CMD_ATUAL[0], msg))
        os.chmod(LOG_FILE, 0o600)  # forca 0600 mesmo se ja existia mais aberto (diretorio e root 0700)
    except (OSError, IOError):
        pass  # sem permissao (ex.: nao-root): so segue


def dizer(msg=""):
    print(msg)
    log(msg)


def _isolar(cmd, entrada=None):
    """Argumentos extras do subprocess quando o comando roda como OUTRO usuario (runuser): sessao nova (sem o terminal de
    controle, entao o outro usuario nao injeta teclas no terminal do root via TIOCSTI) e stdin fechado, salvo se ha entrada."""
    if not cmd or cmd[0] != "runuser":
        return {}
    kw = {"start_new_session": True}
    if entrada is None:
        kw["stdin"] = subprocess.DEVNULL
    return kw


def run(cmd, entrada=None, timeout=120, registrar=True):
    """Executa comando (lista). Retorna (rc, stdout+stderr). Tudo no log; a entrada (senha) nunca.
    registrar=False: nao grava no log (laco de snapshots do homologar, que roda milhares de vezes)."""
    if registrar:
        log("$ " + " ".join(cmd) + ("   [stdin omitido]" if entrada is not None else ""))
    try:
        p = subprocess.run(cmd, input=entrada, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           universal_newlines=True, encoding="utf-8", errors="replace", timeout=timeout,
                           **_isolar(cmd, entrada))
        rc, out = p.returncode, p.stdout or ""
    except FileNotFoundError:
        rc, out = 127, "comando nao encontrado: %s" % cmd[0]
    except subprocess.TimeoutExpired:
        rc, out = 124, "tempo esgotado (%ss)" % timeout
    if registrar:
        log("[rc=%s] %s" % (rc, out.rstrip()))
    return rc, out


def eh_root():
    return hasattr(os, "geteuid") and os.geteuid() == 0


def exigir_root():
    if not eh_root():
        raise Uso("Rode como root: sudo ./instalar.py <subcomando>")


# ---------------------------------------------------------------- leitura de estado

def _ver(txt):
    return tuple(int(x) for x in re.findall(r"\d+", txt)[:3])


def _rpm_tokens(txt):
    """Segmentos como o rpmvercmp: '~' < fim < letras < numeros; separadores sao ignorados.
    O marcador de fim em cada chave faz '1.2~rc1' < '1.2' e '1.2' < '1.2.1' com a comparacao de tuplas."""
    out = []
    for t in re.findall(r"~|\d+|[A-Za-z]+", txt):
        out.append((0,) if t == "~" else (3, int(t)) if t.isdigit() else (2, t))
    return tuple(out) + ((1,),)


def _vr(txt):
    """Chave de ordem de 'versao-release' como o RPM ordena (para upgrade/downgrade). Release de pre-lancamento
    '0.1.rc.N' fica ANTES da final '1': '0.1.2-1' > '0.1.2-0.1.rc.2' > '0.1.2-0.1.rc.1' > '0.1.1-1'; rc.10 > rc.2."""
    versao, _, release = txt.partition("-")
    return (_rpm_tokens(versao), _rpm_tokens(release))


def ler_config():
    try:
        with open(CONF_FILE) as f:
            return f.read()
    except (OSError, IOError):
        return ""


def ler_porta():
    m = re.search(r"^listen:\s*['\"]?[^\s'\"]*:(\d+)", ler_config(), re.M)
    return int(m.group(1)) if m else DEFAULT_PORT


def ler_host():
    m = re.search(r"^listen:\s*['\"]?(\[[^\]]*\]|[^\s'\":]+):\d+", ler_config(), re.M)
    return m.group(1) if m else "127.0.0.1"


def ler_pattern():
    m = re.search(r"^extension_pattern:\s*(?:'([^']*)'|\"([^\"]*)\"|(\S+))", ler_config(), re.M)
    pat = (m.group(1) or m.group(2) or m.group(3)) if m else r"^[0-9]+$"
    try:
        return re.compile("^(?:" + pat + ")$")
    except re.error:
        return re.compile(r"^[0-9]+$")


def versao_instalada():
    rc, out = run(["rpm", "-q", "--qf", "%{VERSION}-%{RELEASE}", PKG])
    return out.strip() if rc == 0 else None


def servico_ativo():
    return run(["systemctl", "is-active", PKG + ".service"])[0] == 0


def socket_ativo():
    return run(["systemctl", "is-active", SOCKET])[0] == 0


def achar_rpm():
    aqui = os.path.dirname(os.path.abspath(__file__))
    rpms = sorted(glob.glob(os.path.join(aqui, PKG + "-*.rpm")))
    if not rpms:
        raise Falha("RPM %s-*.rpm nao encontrado ao lado do instalar.py.\n"
                    "Se voce rodou a partir de um clone do repositorio: esta pasta e o codigo-fonte; baixe o pacote da Release "
                    "e rode o instalar.py de dentro dele:\n"
                    "  gh release download \"$TAG\" -R fcarvalhosa17/painel-ramais -p 'painel-ramais-*.tar.gz' -p SHA256SUMS\n"
                    "  (ou: curl -fLO https://github.com/fcarvalhosa17/painel-ramais/releases/download/<tag>/painel-ramais-<versao>.tar.gz)"
                    % PKG)
    return rpms[-1]


def versao_do_rpm(caminho):
    m = re.match(PKG + r"-(\d[^-]*)-(.+)\.[^.]+\.rpm$", os.path.basename(caminho))  # release pode ter pontos (0.1.rc.1)
    return (m.group(1) + "-" + m.group(2)) if m else None


def ler_health(porta):
    """Retorna o 'data' do envelope. 503 (degraded) tambem traz corpo valido."""
    try:
        r = urllib.request.urlopen("http://%s:%d/api/health" % (ler_host(), porta), timeout=5)
        corpo = r.read()
    except urllib.error.HTTPError as e:
        if e.code != 503:
            raise
        corpo = e.read()
    env = json.loads(corpo.decode("utf-8"))
    return env["data"]


def health_pronto(h):
    return bool(h.get("ami", {}).get("connected")) and h.get("extensions", {}).get("total", 0) > 0


def esperar_health(porta, limite=HEALTH_TIMEOUT):
    fim = time.time() + limite
    while True:
        try:
            h = ler_health(porta)
            if health_pronto(h):
                return h
        except (urllib.error.URLError, OSError, ValueError, KeyError):
            pass
        if time.time() >= fim:
            return None
        time.sleep(2)


# ---------------------------------------------------------------- verificacao

def c_root():
    if eh_root():
        return (OK, "Usuario root", "")
    return (ERRO, "Usuario root", "Rode como root: sudo ./instalar.py verificar")


def c_so():
    info = {}
    try:
        with open("/etc/os-release") as f:
            for ln in f:
                if "=" in ln:
                    k, v = ln.strip().split("=", 1)
                    info[k] = v.strip('"')
    except (OSError, IOError):
        return (ERRO, "Sistema operacional", "/etc/os-release ilegivel. Alvo suportado: Rocky Linux 8 com Issabel 5.")
    nome = info.get("PRETTY_NAME", info.get("ID", "?"))
    if info.get("ID") != "rocky" or not re.match(r"^8(\.|$)", info.get("VERSION_ID", "")):
        return (ERRO, "Sistema operacional (%s)" % nome, "Alvo suportado: Rocky Linux 8 com Issabel 5.")
    return (OK, "Sistema operacional (%s)" % nome, "")


def c_issabel():
    rc, out = run(["rpm", "-qa", "--qf", "%{NAME} %{VERSION}\\n", "issabel*"])
    pacotes = [l for l in out.splitlines() if l.strip()] if rc == 0 else []
    # ponytail: o nome do pacote base do Issabel 5 ainda nao foi confirmado (S9); heuristica nunca bloqueia.
    if not pacotes:
        return (AVISO, "Issabel", "Nao consegui determinar o Issabel por pacote (rpm -qa 'issabel*' vazio). "
                                  "Confirme manualmente que e Issabel 5; a instalacao nao e bloqueada por isso.")
    if not any(l.split()[-1].startswith("5") for l in pacotes):
        return (AVISO, "Issabel", "Versao encontrada nao parece 5.x: %s. Confirme manualmente." % pacotes[0])
    return (OK, "Issabel 5 presente", "")


def c_asterisk():
    rc, out = run(["asterisk", "-rx", "core show version"])
    m = re.search(r"Asterisk\s+(?:GIT-)?(\d+)(?:\.(\d+))?", out)
    if rc != 0 or not m:
        return (ERRO, "Asterisk", "Nao respondeu a 'asterisk -rx'. Inicie: systemctl start asterisk (ou amportal start).")
    v = int(m.group(1))
    if v < MIN_ASTERISK:
        return (ERRO, "Asterisk %d" % v, "Minimo suportado: %d." % MIN_ASTERISK)
    return (OK, "Asterisk %d em execucao" % v, out.strip().splitlines()[0] if out.strip() else "")


def tecnologia_ativa():
    """Retorna (sip_carregado, pjsip_carregado)."""
    out = run(["asterisk", "-rx", "module show like chan_"])[1]
    return "chan_sip.so" in out, "chan_pjsip.so" in out


def nomes_endpoints(pj):
    """Nomes de endpoint/peer listados pelo Asterisk (inclui troncos)."""
    if pj:
        out = run(["asterisk", "-rx", "pjsip show endpoints"])[1]
        return [m.group(1) for m in re.finditer(r"^\s*Endpoint:\s+([^/\s]+)", out, re.M)]
    out = run(["asterisk", "-rx", "sip show peers"])[1]
    nomes = []
    for ln in out.splitlines():
        m = re.match(r"^([^/\s]+)(?:/\S+)?\s+\S+\s", ln)
        if m and not ln.startswith("Name/"):
            nomes.append(m.group(1))
    return nomes


def c_tecnologia():
    sip, pj = tecnologia_ativa()
    if not sip and not pj:
        return (ERRO, "Tecnologia", "Nem chan_sip nem chan_pjsip carregado. Verifique o Asterisk."), None
    pat = ler_pattern()
    if sip and pj:
        # mesma regra do detect.go (modo auto): conta ramais das duas; sip so se sip > pjsip (empate = pjsip)
        np = len([n for n in nomes_endpoints(True) if pat.match(n)])
        ns = len([n for n in nomes_endpoints(False) if pat.match(n)])
        tec = "sip" if ns > np else "pjsip"
        return (AVISO, "Tecnologia",
                "chan_pjsip.so e chan_sip.so carregados juntos (padrao do Issabel 5); modo auto usa %s "
                "(ramais: pjsip=%d, sip=%d)." % (tec, np, ns)), tec
    nomes = nomes_endpoints(pj)
    ramais = [n for n in nomes if pat.match(n)]
    return (OK, "Tecnologia %s" % ("PJSIP" if pj else "SIP"),
            "%d ramais, %d troncos/outros; exemplos: %s" % (len(ramais), len(nomes) - len(ramais),
                                                         ", ".join(nomes[:5]))), ("pjsip" if pj else "sip")


def privilegio(acao):
    out = run(["asterisk", "-rx", "manager show command " + acao])[1]
    if re.search(r"no such command|not found", out, re.I):
        return None
    m = re.search(r"^\s*Privilege:\s*(.*)$", out, re.M)
    return m.group(1).strip() if m else "?"


def c_actions(tec):
    obrig = ["CoreSettings", "ModuleCheck", "CoreShowChannels", "DeviceStateList"]
    obrig += ["PJSIPShowEndpoints"] if tec == "pjsip" else ["SIPpeers"]
    opc = ["PJSIPShowContacts", "PJSIPShowAors"] if tec == "pjsip" else ["SIPshowpeer"]
    res, linhas, faltam, faltam_opc = [], [], [], []
    for a in obrig + opc:
        p = privilegio(a)
        linhas.append("%s: %s" % (a, "AUSENTE" if p is None else "Privilege " + p))
        if p is None:
            (faltam if a in obrig else faltam_opc).append(a)
    if tec == "pjsip" and "PJSIPShowContacts" in faltam_opc and "PJSIPShowAors" in faltam_opc:
        faltam.append("PJSIPShowAors (fallback de PJSIPShowContacts)")
    detalhe = "; ".join(linhas)
    if faltam:
        res.append((ERRO, "Actions AMI", "Faltam: %s. Versao do Asterisk incompativel. (%s)" % (", ".join(faltam), detalhe)))
    elif faltam_opc:
        res.append((AVISO, "Actions AMI", "Ausentes (ha fallback): %s. (%s)" % (", ".join(faltam_opc), detalhe)))
    else:
        res.append((OK, "Actions AMI", detalhe))
    return res


def c_eventos(tec):
    res = []
    checks = [("CoreShowChannel", ("BridgeId", "Duration"))]
    if tec == "pjsip":
        checks.append(("ContactStatus", ("EndpointName", "UserAgent", "RoundtripUsec")))
    for ev, campos in checks:
        out = run(["asterisk", "-rx", "manager show event " + ev])[1]
        falta = [c for c in campos if c not in out]
        if falta:
            res.append((AVISO, "Evento %s" % ev, "Campos nao encontrados: %s. Informe ao suporte (relatorio em /var/log/painel-ramais)." % ", ".join(falta)))
        else:
            res.append((OK, "Evento %s" % ev, "campos %s presentes" % ", ".join(campos)))
    return res


def c_manager_include():
    try:
        with open(MANAGER_CONF) as f:
            txt = f.read()
    except (OSError, IOError):
        return (ERRO, "manager.conf", "%s ilegivel. Verifique o Asterisk/Issabel e as permissoes." % MANAGER_CONF)
    if re.search(r'^\s*#(try)?include\s+"?manager_custom\.conf', txt, re.M):
        return (OK, "manager.conf inclui manager_custom.conf", "")
    return (ERRO, "manager.conf nao inclui manager_custom.conf",
            "O usuario AMI do painel nao seria carregado. Adicione '#include manager_custom.conf' (nao e feito por este instalador).")


def c_conflito_ami():
    try:
        with open(MANAGER_CUSTOM) as f:
            txt = f.read()
    except (OSError, IOError):
        return (OK, "manager_custom.conf", "sem conflito (arquivo ausente ou ilegivel)")
    fora = re.sub(r"^; BEGIN painel-ramais.*?^; END painel-ramais\s*$", "", txt, flags=re.M | re.S)
    if re.search(r"^\[painel-ramais\]", fora, re.M):
        return (ERRO, "Conflito AMI", "Existe [painel-ramais] em manager_custom.conf fora dos marcadores BEGIN/END. Remova ou renomeie essa secao manual.")
    return (OK, "manager_custom.conf", "sem conflito")


def c_perm_manager_custom():
    try:
        modo = os.stat(MANAGER_CUSTOM).st_mode
    except OSError:
        return (OK, "Permissao de manager_custom.conf", "arquivo ausente")
    if modo & 0o007:
        return (AVISO, "manager_custom.conf acessivel a outros (modo %o)" % (modo & 0o777),
                "Contem a senha AMI do painel. Rode: chmod o-rwx %s" % MANAGER_CUSTOM)
    return (OK, "Permissao de manager_custom.conf", "modo %o" % (modo & 0o777))


def c_httpd():
    if run(["systemctl", "is-active", "httpd"])[0] != 0:
        return [(ERRO, "httpd", "Apache parado. Inicie: systemctl start httpd")]
    res = [(OK, "httpd em execucao", "")]
    if "proxy_wstunnel_module" in run(["httpd", "-M"])[1]:
        res.append((OK, "mod_proxy_wstunnel carregado", ""))
    else:
        res.append((ERRO, "mod_proxy_wstunnel ausente",
                    "Habilite o modulo no Apache (LoadModule proxy_wstunnel_module ...) e valide com 'httpd -t'."))
    return res


def c_selinux():
    rc, out = run(["getenforce"])
    if rc != 0:
        return (OK, "SELinux", "getenforce ausente (SELinux nao instalado)")
    est = out.strip()
    if est == "Enforcing":
        bool_out = run(["getsebool", "httpd_can_network_connect"])[1]
        if " on" not in bool_out:
            return (AVISO, "SELinux Enforcing", "httpd_can_network_connect desligado; o RPM vai ligar (setsebool -P).")
    return (OK, "SELinux %s" % est, "")


def c_porta():
    p = ler_porta()
    rc, out = run(["ss", "-ltnp"])
    if rc != 0:
        return (AVISO, "Porta interna %d" % p, "ss indisponivel; nao consegui verificar se a porta esta livre.")
    for ln in out.splitlines():
        if re.search(r":%d\s" % p, ln):
            if PKG in ln:
                return (OK, "Porta interna %d" % p, "em uso pelo proprio painel-ramais")
            if "systemd" in ln and socket_ativo():
                return (OK, "Porta interna %d" % p, "mantida pelo %s (socket activation do painel-ramais)" % SOCKET)
            return (ERRO, "Porta interna %d ocupada" % p,
                    "Outro processo usa a porta. Veja: ss -ltnp | grep :%d ; ou mude 'listen:' no config.yaml." % p)
    return (OK, "Porta interna %d livre" % p, "")


def c_disco():
    base = STATE_DIR if os.path.isdir(STATE_DIR) else os.path.dirname(STATE_DIR)
    st = os.statvfs(base)
    livre = st.f_bavail * st.f_frsize
    mib = livre // (1024 * 1024)
    if livre < MIN_DISK_ERRO:
        return (ERRO, "Espaco em /var/lib", "%d MiB livres; minimo 512 MiB. Libere espaco." % mib)
    if livre < MIN_DISK_AVISO:
        return (AVISO, "Espaco em /var/lib", "%d MiB livres; recomendado 2 GiB ou mais." % mib)
    return (OK, "Espaco em /var/lib", "%d MiB livres" % mib)


def c_painel(rpm_bundle):
    v = versao_instalada()
    if not v:
        return (OK, "Painel nao instalado", "instalar fara instalacao nova")
    vb = versao_do_rpm(rpm_bundle) if rpm_bundle else None
    if vb and _vr(v) > _vr(vb):
        return (ERRO, "Versao instalada maior que a do bundle", "instalada %s, bundle %s. Use 'rollback' ou um bundle mais novo." % (v, vb))
    return (OK, "Painel ja instalado", "versao %s" % v)


def c_informativo():
    res = []
    for titulo, cmd in (("httpd", ["httpd", "-v"]), ("Issabel", ["rpm", "-qa", "issabel*"])):
        rc, out = run(cmd)
        if rc == 0 and out.strip():
            res.append((OK, "Versao " + titulo, " | ".join(out.strip().splitlines()[:4])))
    rc, out = run(["sh", "-c", "grep -rhE '^Timeout' /etc/httpd/conf /etc/httpd/conf.d 2>/dev/null | head -3"])
    res.append((OK, "Timeout do Apache", out.strip() or "(padrao)"))
    rc, out = run(["sh", "-c", "iptables -S 2>/dev/null | grep -E 'dport 443|dports.*443|^-P INPUT'"])
    res.append((OK, "Firewall (443)", " | ".join(out.strip().splitlines()[:6]) or "(sem regra visivel / iptables ausente)"))
    rc, out = run(["sh", "-c", "ls -l /etc/asterisk/manager*.conf 2>&1"])
    res.append((OK, "Arquivos manager*.conf", " | ".join(out.strip().splitlines())))
    return res


def executar_checagens(rpm_bundle=None):
    r = [c_root(), c_so(), c_issabel()]
    ast = c_asterisk()
    r.append(ast)
    tec = None
    if ast[0] == OK:
        item, tec = c_tecnologia()
        r.append(item)
        if tec:
            r.extend(c_actions(tec))
            r.extend(c_eventos(tec))
    r.extend([c_manager_include(), c_conflito_ami(), c_perm_manager_custom()])
    r.extend(c_httpd())
    r.extend([c_selinux(), c_porta(), c_disco(), c_painel(rpm_bundle)])
    r.extend(c_informativo())
    return r


def imprimir_relatorio(res):
    linhas = []
    for nivel, tit, det in res:
        if nivel == OK:
            linhas.append("[OK   ] %s%s" % (tit, (" - " + det) if det else ""))
        else:
            linhas.append("[%-5s] %s" % (nivel, tit))
            if det:
                linhas.append("         Como corrigir: " + det)
    for l in linhas:
        dizer(l)
    return "\n".join(linhas) + "\n"


def bundle_rpm_ou_none():
    try:
        return achar_rpm()
    except Falha:
        return None


def verificar_ambiente():
    """Roda as checagens, imprime e salva o relatorio. Retorna (resultados, n_erros)."""
    res = executar_checagens(bundle_rpm_ou_none())
    txt = imprimir_relatorio(res)
    try:
        os.makedirs(LOG_DIR, 0o700, exist_ok=True)
        arq = "%s/verificacao-%s.txt" % (LOG_DIR, datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
        fd = os.open(arq, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            f.write(txt)
        dizer("Relatorio salvo em " + arq)
    except (OSError, IOError):
        dizer("AVISO: nao foi possivel salvar o relatorio em " + LOG_DIR)
    erros = sum(1 for r in res if r[0] == ERRO)
    dizer("Resultado: %d erro(s), %d aviso(s)." % (erros, sum(1 for r in res if r[0] == AVISO)))
    return res, erros


def cmd_verificar(args):
    dizer("== Verificacao do ambiente ==")
    return EXIT_VERIFICAR if verificar_ambiente()[1] else EXIT_OK


# ---------------------------------------------------------------- respostas / entrada

def pedir(msg, padrao=None):
    if not sys.stdin.isatty():
        raise Uso("Resposta necessaria sem terminal: '%s'. Use --respostas arquivo.ini" % msg)
    v = input("%s%s: " % (msg, " [%s]" % padrao if padrao else "")).strip()
    return v or padrao


def coletar_respostas(args):
    """Todas as respostas antes de alterar qualquer coisa. Retorna dict tecnologia, csv. Nao pergunta senha."""
    r = {"tecnologia": "auto", "csv": None}
    if args.respostas:
        cp = configparser.ConfigParser()
        if not cp.read(args.respostas):
            raise Uso("Arquivo de respostas nao encontrado: " + args.respostas)
        if cp.sections() != ["painel"]:
            raise Uso("O arquivo de respostas deve ter exatamente a secao [painel].")
        desconhecidas = [k for k in cp["painel"] if k not in CHAVES_RESPOSTAS]
        if desconhecidas:
            raise Uso("Chave desconhecida no arquivo de respostas: " + ", ".join(desconhecidas))
        s = cp["painel"]
        r["tecnologia"] = s.get("tecnologia", "auto") or "auto"
        r["csv"] = s.get("grupos_csv") or None
        for k in CHAVES_IGNORADAS:
            if k in s:
                dizer("AVISO: '%s' ignorada: o painel nao pede mais usuario nem senha na instalacao "
                      "(acesso pelo menu do Issabel; senha de emergencia em %s)." % (k, SENHA_EMERGENCIA))
    else:
        r["tecnologia"] = pedir("Tecnologia (auto/sip/pjsip)", "auto")
        r["csv"] = pedir("Caminho do CSV de grupos (opcional, Enter para pular)", "") or None
    if r["tecnologia"] not in TECNOLOGIAS:
        raise Uso("tecnologia invalida '%s' (use auto, sip ou pjsip)." % r["tecnologia"])
    if r["csv"] and not os.path.isfile(r["csv"]):
        raise Uso("CSV de grupos nao encontrado: " + r["csv"])
    return r


def confirmar(msg, args):
    if args.sim:
        dizer(msg + " [sim, via --sim]")
        return
    if not sys.stdin.isatty():
        raise Uso("Confirmacao necessaria e nao ha terminal. Use --sim.")
    if input(msg + " [s/N]: ").strip().lower() not in ("s", "sim"):
        raise Falha("Cancelado pelo operador.", EXIT_CANCELADO)


# ---------------------------------------------------------------- passos do instalar

def dir_do_root_seguro(caminho):
    """Cria (se preciso) e valida um diretorio do root 0700 que nao seja symlink."""
    os.makedirs(caminho, 0o700, exist_ok=True)
    st = os.lstat(caminho)
    if stat.S_ISLNK(st.st_mode) or not stat.S_ISDIR(st.st_mode):
        raise Falha("%s nao e um diretorio comum (symlink?). Recusado." % caminho)
    if st.st_uid != 0 or (st.st_mode & 0o077):
        raise Falha("%s deve ser do root com modo 0700. Recusado." % caminho)


def sha256_arquivo(caminho):
    h = hashlib.sha256()
    with open(caminho, "rb") as f:
        for bloco in iter(lambda: f.read(1 << 20), b""):
            h.update(bloco)
    return h.hexdigest()


def verificar_sha256sums(rpm):
    """Confere o RPM do bundle contra o SHA256SUMS ao lado dele (L6)."""
    arq = os.path.join(os.path.dirname(os.path.abspath(rpm)), "SHA256SUMS")
    try:
        with open(arq) as f:
            linhas = f.read().splitlines()
    except (OSError, IOError):
        raise Falha("SHA256SUMS ausente ao lado do RPM: bundle incompleto ou adulterado.")
    nome = os.path.basename(rpm)
    esperado = None
    for ln in linhas:
        partes = ln.split()
        if len(partes) == 2 and partes[1].lstrip("*") == nome:
            esperado = partes[0].lower()
    if not esperado:
        raise Falha("%s nao consta no SHA256SUMS." % nome)
    if sha256_arquivo(rpm) != esperado:
        raise Falha("SHA-256 do %s nao confere com o SHA256SUMS: bundle corrompido ou adulterado." % nome)


def rpm_e_nosso(rpm, sha_esperado=None):
    """Antes de qualquer 'rpm -U': o arquivo e mesmo o painel-ramais e (se informado) tem o SHA-256 registrado."""
    rc, out = run(["rpm", "-qp", "--qf", "%{NAME}", rpm])
    if rc != 0 or out.strip() != PKG:
        raise Falha("%s nao e o pacote %s (rpm -qp: %r). Recusado." % (os.path.basename(rpm), PKG, out.strip()))
    if sha_esperado is not None and sha256_arquivo(rpm) != sha_esperado:
        raise Falha("SHA-256 de %s difere do registrado na instalacao. Recusado." % os.path.basename(rpm))


def sha_registrado(pasta):
    try:
        with open(pasta + "/SHA256") as f:
            return f.read().split()[0].lower()
    except (OSError, IOError, IndexError):
        return None


def guardar_rpm_anterior():
    """Move rpm-instalado/ para rpm-anterior/ (substitui). True se havia RPM guardado."""
    dir_do_root_seguro(INSTALADOR_DIR)
    if not glob.glob(RPM_ATUAL + "/*.rpm"):
        return False
    shutil.rmtree(RPM_ANTERIOR, ignore_errors=True)
    os.rename(RPM_ATUAL, RPM_ANTERIOR)
    return True


def registrar_rpm_atual(rpm):
    dir_do_root_seguro(INSTALADOR_DIR)
    shutil.rmtree(RPM_ATUAL, ignore_errors=True)
    os.makedirs(RPM_ATUAL, 0o700)
    destino = os.path.join(RPM_ATUAL, os.path.basename(rpm))
    shutil.copy2(rpm, destino)
    rc, out = run([BIN, "version"])
    with open(RPM_ATUAL + "/versao.txt", "w") as f:
        f.write(out.strip() + "\n" if rc == 0 else "")
    with open(RPM_ATUAL + "/SHA256", "w") as f:
        f.write("%s  %s\n" % (sha256_arquivo(destino), os.path.basename(rpm)))


def definir_tecnologia(tec):
    """Reescreve so a linha 'technology:' (raiz) se o valor mudou, com copia config.yaml.bak-<data>."""
    with open(CONF_FILE) as f:
        txt = f.read()
    m = re.search(r"^technology:\s*['\"]?(\w+)['\"]?\s*(#.*)?$", txt, re.M)
    if m and m.group(1) == tec:
        return False
    novo, n = re.subn(r"^technology:.*$", "technology: " + tec, txt, count=1, flags=re.M)
    if n == 0:
        novo = txt.rstrip("\n") + "\ntechnology: " + tec + "\n"
    shutil.copy2(CONF_FILE, "%s.bak-%s" % (CONF_FILE, datetime.datetime.now().strftime("%Y%m%d-%H%M%S")))
    with open(CONF_FILE, "w") as f:
        f.write(novo)
    return True


def daemon_cli(partes, entrada=None):
    return run(["runuser", "-u", SVC_USER, "--", BIN] + partes, entrada=entrada)


def _checar_arquivo_senha(st):
    if not stat.S_ISREG(st.st_mode):
        raise Falha("%s nao e um arquivo regular." % SENHA_EMERGENCIA)
    if hasattr(os, "geteuid"):  # bits POSIX; no Windows (desenvolvimento) nao existem
        if st.st_uid != 0:
            raise Falha("%s deve pertencer ao root." % SENHA_EMERGENCIA)
        if st.st_mode & 0o077:
            raise Falha("%s deve ter modo 0600 (chmod 600)." % SENHA_EMERGENCIA)


def _ler_senha_existente():
    """Reaproveita o arquivo existente: sem seguir symlink, do root e sem acesso para grupo/outros."""
    try:
        fd = os.open(SENHA_EMERGENCIA, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "r", encoding="utf-8") as f:
            st = os.fstat(f.fileno())
            _checar_arquivo_senha(st)
            senha = f.readline().rstrip("\r\n")
    except (OSError, UnicodeDecodeError) as e:
        raise Falha("Nao consegui ler %s: %s" % (SENHA_EMERGENCIA, e))
    if not senha:
        raise Falha("Arquivo da senha de emergencia vazio: %s (apague-o e rode de novo)." % SENHA_EMERGENCIA)
    return senha


def _apagar_senha_nova():
    try:
        os.unlink(SENHA_EMERGENCIA)
    except OSError:
        pass


def senha_de_emergencia():
    """(senha, nova). Arquivo root 0600 criado com O_EXCL so se nao existir; existente nunca e reescrito."""
    try:
        fd = os.open(SENHA_EMERGENCIA, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        return _ler_senha_existente(), False
    except OSError as e:
        raise Falha("Nao consegui criar %s: %s" % (SENHA_EMERGENCIA, e))
    senha = secrets.token_urlsafe(18)  # 24 caracteres
    f = None
    try:
        f = os.fdopen(fd, "w", encoding="utf-8")
        with f:
            f.write(senha + "\n")
    except OSError as e:
        if f is None:
            os.close(fd)
        _apagar_senha_nova()
        raise Falha("Nao consegui gravar %s: %s" % (SENHA_EMERGENCIA, e))
    return senha, True


def criar_admin():
    """Usuario 'emergencia' (unico login por senha). A senha so trafega pelo stdin; nunca em argv nem no log."""
    senha, nova = senha_de_emergencia()
    rc, out = daemon_cli(["user", "add", USUARIO_EMERGENCIA, "--password-stdin"], entrada=senha + "\n")
    if rc == CLI_EXISTE and nova:
        rc, out = daemon_cli(["user", "passwd", USUARIO_EMERGENCIA, "--password-stdin"], entrada=senha + "\n")
    elif rc == CLI_EXISTE:
        dizer("AVISO: usuario '%s' ja existe; senha mantida (arquivo %s)." % (USUARIO_EMERGENCIA, SENHA_EMERGENCIA))
        return
    if rc != 0:
        if nova:
            _apagar_senha_nova()  # senha que nao chegou ao daemon nao pode ficar como se valesse
        raise Falha("Falha ao criar o usuario de emergencia (rc=%d): %s\nDiagnostico: %s" % (rc, out.strip(), LOG_FILE))


def encerrar_sessoes_antigas():
    """Atualizacao: sessoes de senha da fase 1 nao valem mais. Idempotente."""
    rc, out = daemon_cli(["session", "purge", "--all"])
    if rc != 0:
        raise Falha("Falha ao encerrar as sessoes antigas (rc=%d): %s" % (rc, out.strip()))


def importar_grupos(csv):
    """CSV lido pelo root e entregue pelo stdin da CLI (roda como o dono): nada e gravado em /var/lib/painel-ramais."""
    with open(csv, "rb") as f:
        conteudo = f.read().decode("utf-8-sig")
    rc, out = daemon_cli(["group", "import", "--replace", "-"], entrada=conteudo)
    if rc != 0:
        raise Falha("Falha ao importar grupos (rc=%d): %s" % (rc, out.strip()))


def total_grupos():
    rc, out = daemon_cli(["group", "list"])
    return len([l for l in out.splitlines() if "\t" in l]) if rc == 0 else None


def http_get(url, ctx=None):
    """Retorna (codigo, corpo_texto) sem levantar para 4xx/5xx; None se nao conectou."""
    try:
        r = urllib.request.urlopen(url, timeout=10, context=ctx)
        return r.getcode(), r.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except (urllib.error.URLError, OSError):
        return None


def eh_json(txt):
    try:
        json.loads(txt)
        return True
    except ValueError:
        return False


def envelope_nao_autorizado(txt):
    try:
        env = json.loads(txt)
        return env["success"] is False and env["error"]["code"] == "unauthorized"
    except (ValueError, KeyError, TypeError):
        return False


def testar_apache():
    """/api/session responde 401 com o envelope do daemon (a raiz agora e HTML); /api/health responde 404 do Apache, sem JSON."""
    ctx = ssl._create_unverified_context()  # certificado do Issabel costuma ser autoassinado; so loopback
    base = "https://127.0.0.1/painel-ramais/"
    sess = http_get(base + "api/session", ctx)
    if sess is None:
        return False, "sem resposta em " + base + "api/session"
    if sess[0] != 401 or not envelope_nao_autorizado(sess[1]):
        return False, "%sapi/session deveria dar 401 com o envelope do daemon (obtido: %s): o proxy nao chega ao painel" % (base, sess[0])
    h = http_get(base + "api/health", ctx)
    if h is None or h[0] != 404 or eh_json(h[1]):
        return False, "%sapi/health deveria dar 404 do Apache sem JSON (obtido: %s)" % (base, h[0] if h else "sem resposta")
    return True, ""


def endereco_servidor():
    rc, out = run(["hostname", "-I"])
    if rc == 0 and out.split():
        return out.split()[0]
    return socket.gethostname()


def cmd_instalar(args):
    exigir_root()
    dizer("== Instalacao ==")
    res, erros = verificar_ambiente()
    if erros:
        dizer("Verificacao com ERRO: nada foi alterado. Corrija os itens acima e rode de novo.")
        return EXIT_VERIFICAR
    rpm = achar_rpm()
    verificar_sha256sums(rpm)
    rpm_e_nosso(rpm)
    resp = coletar_respostas(args)
    atual = versao_instalada()
    alvo = versao_do_rpm(rpm)
    upgrade = False
    if not atual:
        cmd = ["rpm", "-i", rpm]
    elif alvo and _vr(atual) == _vr(alvo):
        dizer("Mesma versao (%s): reaplicando." % atual)
        cmd = ["rpm", "-U", "--replacepkgs", rpm]
    else:
        dizer("Upgrade %s -> %s." % (atual, alvo))
        upgrade = True
        cmd = ["rpm", "-U", rpm]
    passo = "pacote"
    try:
        rc, out = run(cmd, timeout=300)
        if rc != 0:
            raise Falha("rpm falhou (rc=%d): %s\nDiagnostico: %s" % (rc, out.strip(), " ".join(cmd)))
        if upgrade and not guardar_rpm_anterior():
            # so agora: se o rpm -U tivesse falhado, a versao antiga continua instalada e nao e "anterior"
            dizer("AVISO: nao havia RPM guardado da versao anterior (pacote instalado a mao): rollback indisponivel.")
        registrar_rpm_atual(rpm)
        passo = "tecnologia"
        mudou = definir_tecnologia(resp["tecnologia"])
        passo = "usuario de emergencia"
        criar_admin()
        if atual:
            passo = "sessoes antigas"
            encerrar_sessoes_antigas()
        if resp["csv"]:
            passo = "grupos"
            importar_grupos(resp["csv"])
        passo = "servico"
        if run(["systemctl", "enable", "--now", SOCKET])[0] != 0:
            raise Falha("systemctl enable --now %s falhou.\nDiagnostico: journalctl -u %s -n 50 --no-pager" % (SOCKET, SOCKET))
        if run(["systemctl", "enable", "--now", PKG + ".service"])[0] != 0:
            raise Falha("systemctl enable --now %s falhou.\nDiagnostico: journalctl -u %s -n 50 --no-pager" % (PKG, PKG))
        if mudou and run(["systemctl", "restart", PKG])[0] != 0:
            raise Falha("systemctl restart falhou.\nDiagnostico: journalctl -u %s -n 50 --no-pager" % PKG)
        passo = "health"
        dizer("Aguardando o painel (ate %ds)..." % HEALTH_TIMEOUT)
        h = esperar_health(ler_porta())
        if not h:
            raise Falha("O painel nao ficou pronto (AMI conectado + ramais carregados).\n"
                        "Diagnostico: journalctl -u %s -n 50 --no-pager ; ./instalar.py status" % PKG)
        passo = "apache"
        ok, motivo = testar_apache()
        if not ok:
            raise Falha("Teste do Apache falhou: %s\nDiagnostico: httpd -t ; cat /etc/httpd/conf.d/painel-ramais.conf ; "
                        "tail /var/log/httpd/error_log" % motivo)
    except Falha as e:
        if e.codigo == EXIT_FALHA:
            dizer("Falhou no passo: %s (nada foi desfeito)." % passo)
        raise
    ng = total_grupos()
    dizer("")
    dizer("== Resumo ==")
    dizer("Versao:      " + (versao_instalada() or "?"))
    dizer("Tecnologia:  " + str(h.get("ami", {}).get("technology", "?")))
    dizer("Ramais:      " + json.dumps(h.get("extensions", {})))
    dizer("Grupos:      " + (str(ng) if ng is not None else "?"))
    dizer("Acesso:      https://%s/painel-ramais/" % endereco_servidor())
    dizer("Senha de emergencia: " + SENHA_EMERGENCIA)
    dizer("Log:         " + LOG_FILE)
    return EXIT_OK


# ---------------------------------------------------------------- status / rollback / remover

METRICAS = "/run/painel-ramais/metrics.json"  # escrito pelo daemon a cada 30 s (unit: RuntimeDirectory=painel-ramais)
METRICAS_VELHA = 120  # s: arquivo mais antigo que isso = daemon parado ou travado


def _mib(n):
    return "%.1f MiB" % (n / 1048576.0)


def _duracao(seg):
    seg = int(seg)
    d, r = divmod(seg, 86400)
    h, r = divmod(r, 3600)
    return "%dd %02dh %02dmin" % (d, h, r // 60)


def memoria_do_daemon(pid, proc="/proc", metricas=METRICAS, agora=None):
    """Linhas de 'RSS / Heap / Uptime' do daemon. RSS e uptime vem de /proc/<pid>; heap pos-GC do metrics.json.
    Cada fonte que falhar vira 'indisponivel' (nunca derruba o status)."""
    rss = heap = up = "indisponivel"
    try:
        with open("%s/%s/status" % (proc, pid)) as f:
            m = re.search(r"^VmRSS:\s+(\d+) kB", f.read(), re.M)
        if m:
            rss = _mib(int(m.group(1)) * 1024)
        with open("%s/%s/stat" % (proc, pid)) as f:
            inicio = int(f.read().rsplit(")", 1)[1].split()[19])  # starttime (campo 22), em ticks
        with open("%s/uptime" % proc) as f:
            up = _duracao(float(f.read().split()[0]) - inicio / os.sysconf("SC_CLK_TCK"))
    except (OSError, ValueError, IndexError):
        pass
    try:
        with open(metricas) as f:
            d = json.load(f)
        idade = (agora if agora is not None else time.time()) - d["updated"]
        if d["pid"] != int(pid):
            heap = "indisponivel (arquivo de outro processo)"
        elif idade > METRICAS_VELHA:
            heap = "indisponivel (arquivo com %ds)" % idade
        else:
            heap = "%s pos-GC (em uso %s)" % (_mib(d["heap_live"]), _mib(d["heap_inuse"]))
    except (OSError, ValueError, KeyError, TypeError):
        pass
    return ["Memoria RSS:     " + rss, "Heap Go:         " + heap, "Uptime do daemon: " + up]


def cmd_status(args):
    v = versao_instalada()
    if not v:
        dizer("Painel nao instalado.")
        return EXIT_FALHA
    dizer("Versao instalada: " + v)
    sock = socket_ativo()
    dizer("Socket (%s): %s" % (SOCKET, "ativo" if sock else "PARADO"))
    ativo = servico_ativo() and sock
    dizer("Servico:          " + ("ativo" if servico_ativo() else "PARADO"))
    rc, pid = run(["systemctl", "show", "-p", "MainPID", "--value", PKG + ".service"])
    pid = pid.strip()
    if rc == 0 and pid.isdigit() and pid != "0":
        linhas = memoria_do_daemon(pid)
    else:
        linhas = ["Memoria RSS:     indisponivel", "Heap Go:         indisponivel", "Uptime do daemon: indisponivel"]
    for linha in linhas:
        dizer(linha)
    try:
        h = ler_health(ler_porta())
    except (urllib.error.URLError, OSError, ValueError, KeyError) as e:
        dizer("Health: sem resposta (%s). Diagnostico: journalctl -u %s -n 50 --no-pager" % (e, PKG))
        return EXIT_FALHA
    ami = h.get("ami", {})
    dizer("Daemon:           %s (status %s)" % (h.get("daemon_version", "?"), h.get("status", "?")))
    dizer("AMI:              conectado=%s tecnologia=%s asterisk=%s" % (ami.get("connected"), ami.get("technology"), ami.get("asterisk_version")))
    dizer("Ramais:           " + json.dumps(h.get("extensions", {})))
    dizer("Clientes WS:      %s" % h.get("ws_clients", "?"))
    for d in h.get("deps", []):
        dizer("Dependencia %-24s %s  %s" % (d.get("name"), d.get("status"), d.get("detail", "")))
    return EXIT_OK if ativo and h.get("status") == "ok" else EXIT_FALHA


def schema_de(versao_txt):
    try:
        with open(versao_txt) as f:
            m = re.search(r"schema\s+(\d+)", f.read())
        return int(m.group(1)) if m else None
    except (OSError, IOError):
        return None


_SCHEMA_PY = ("import sqlite3,sys;c=sqlite3.connect('file:%s?mode=ro'%sys.argv[1],uri=True);"
              "print(c.execute('PRAGMA user_version').fetchone()[0])")


def como_dono(cmd, **kw):
    return run(["runuser", "-u", SVC_USER, "--"] + cmd, **kw)


def schema_do_banco():
    """Le o user_version como o dono do banco (nao abre arquivo controlado pelo servico como root)."""
    rc, out = como_dono([sys.executable, "-I", "-c", _SCHEMA_PY, DB_FILE])
    if rc != 0:
        raise OSError("nao consegui ler o esquema do banco")
    return int(out.strip().splitlines()[-1])


def trocar_diretorios_rpm():
    tmp = INSTALADOR_DIR + "/rpm-troca"
    shutil.rmtree(tmp, ignore_errors=True)
    os.rename(RPM_ATUAL, tmp)
    os.rename(RPM_ANTERIOR, RPM_ATUAL)
    os.rename(tmp, RPM_ANTERIOR)


def cmd_rollback(args):
    exigir_root()
    rpms = sorted(glob.glob(RPM_ANTERIOR + "/*.rpm"))
    vtxt = RPM_ANTERIOR + "/versao.txt"
    if not rpms or not os.path.isfile(vtxt):
        raise Falha("Nao ha versao anterior guardada em %s: rollback indisponivel." % RPM_ANTERIOR)
    sch_ant = schema_de(vtxt)
    try:
        sch_db = schema_do_banco()
    except (sqlite3.Error, OSError):
        sch_db = None
    if como_dono(["test", "-e", DB_FILE])[0] == 0 and (sch_db is None or sch_ant is None):
        raise Falha("Nao consegui determinar o esquema do banco (%s) ou da versao anterior (%s). "
                    "Rollback abortado sem alterar nada." % (sch_db, sch_ant))
    precisa_restaurar = sch_ant is not None and sch_db is not None and sch_db > sch_ant
    bak = "%s.bak-v%s" % (DB_FILE, sch_ant)
    dizer("Plano: reinstalar %s (rpm -U --oldpackage), preservando dados." % os.path.basename(rpms[-1]))
    if precisa_restaurar:
        if como_dono(["test", "-f", bak])[0] != 0:
            raise Falha("O banco esta no esquema %d, mais novo que o da versao anterior (%d), e nao existe o backup %s. "
                        "Rollback nao e seguro; nada foi alterado." % (sch_db, sch_ant, bak))
        dizer("ATENCAO: o banco sera trocado pelo backup %s. O historico, as sessoes e as mudancas de usuario e de grupo "
              "feitas depois do upgrade serao PERDIDOS." % os.path.basename(bak))
    dir_do_root_seguro(INSTALADOR_DIR)
    sha = sha_registrado(RPM_ANTERIOR)
    if not sha:
        # fail-closed (decisao 3): sem SHA-256 registrado na instalacao nao ha como provar que o RPM guardado e o original
        raise Falha("Nao ha SHA-256 registrado para o RPM anterior em %s/SHA256: rollback abortado sem alterar nada." % RPM_ANTERIOR)
    rpm_e_nosso(rpms[-1], sha)
    confirmar("Confirma o rollback?", args)
    run(["systemctl", "stop", PKG + ".service"])  # o .socket continua segurando a porta
    rc, out = run(["rpm", "-U", "--oldpackage", rpms[-1]], timeout=300)
    if rc != 0:
        raise Falha("rpm falhou (rc=%d):\n%s" % (rc, out.strip()))
    if precisa_restaurar:
        # tudo como o dono do banco: um symlink plantado em /var/lib/painel-ramais so alcanca o que o dono ja alcanca
        stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
        for suf in ("", "-wal", "-shm"):
            if como_dono(["test", "-e", DB_FILE + suf])[0] == 0:
                if como_dono(["mv", "-T", "--", DB_FILE + suf, "%s.rollback-%s%s" % (DB_FILE, stamp, suf)])[0] != 0:
                    raise Falha("Nao consegui guardar %s; rollback do banco interrompido." % (DB_FILE + suf))
        if como_dono(["cp", "--no-preserve=mode", "--", bak, DB_FILE])[0] != 0:
            raise Falha("Nao consegui restaurar o backup %s." % bak)
    run(["systemctl", "daemon-reload"])
    if os.path.exists(SOCKET_UNIT_PATH):
        run(["systemctl", "start", SOCKET])
    else:
        # a versao anterior nao fornece a unit de socket: o .socket carregado seguraria a 8790 e o service antigo nao a usa
        dizer("AVISO: a versao anterior nao tem unit de socket; parando e desabilitando o %s." % SOCKET)
        run(["systemctl", "stop", SOCKET])
        run(["systemctl", "disable", SOCKET])
        try:
            os.remove(SOCKET_WANTS)  # symlink de enable que sobra apontando para a unit removida
        except OSError:
            pass
        run(["systemctl", "daemon-reload"])
    if run(["systemctl", "start", PKG + ".service"])[0] != 0:
        raise Falha("Servico nao subiu apos o rollback.\nDiagnostico: journalctl -u %s -n 50 --no-pager" % PKG)
    if not esperar_health(ler_porta()):
        raise Falha("Rollback aplicado, mas o painel nao ficou pronto.\nDiagnostico: journalctl -u %s -n 50 --no-pager" % PKG)
    trocar_diretorios_rpm()
    dizer("Rollback concluido.")
    return cmd_status(args)


def cmd_remover(args):
    exigir_root()
    confirmar("Remover o painel-ramais%s?" % (" e APAGAR TODOS OS DADOS" if args.purge else ""), args)
    if versao_instalada():
        rc, out = run(["rpm", "-e", PKG])
        if rc != 0:
            raise Falha("rpm -e falhou (rc=%d):\n%s" % (rc, out.strip()))
    else:
        dizer("Pacote ja removido.")
    if args.purge:
        if not args.sim:
            nome = socket.gethostname()
            if input("Para apagar %s e %s digite o nome deste servidor (%s): " % (STATE_DIR, CONF_DIR, nome)).strip() != nome:
                raise Falha("Nome incorreto: dados mantidos.", EXIT_CANCELADO)
        for d in (STATE_DIR, CONF_DIR, INSTALADOR_DIR):
            shutil.rmtree(d, ignore_errors=True)
        dizer("Dados apagados (%s, %s). Usuario de sistema mantido: userdel %s" % (STATE_DIR, CONF_DIR, SVC_USER))
    else:
        dizer("Dados mantidos em %s. Para apagar tudo: ./instalar.py remover --purge" % STATE_DIR)
    return EXIT_OK


# ---------------------------------------------------------------- homologar

DURACAO_HOMOLOGAR_MIN = 45
DURACAO_HOMOLOGAR_MAX_MIN = 720      # mesmo teto do `painel-ramais capturar`
INTERVALO_SNAPSHOT = 2.0             # segundos entre snapshots (os testes encurtam)
ESPERA_CAPTURA_FINAL = 180           # o capturar grava um snapshot final (ate 2 min) depois do prazo
COOKIE_PAINEL = "painel_session"
WORK_BASE = "/var/tmp"
MARCA_ARQUIVO = b"@@@@PAINEL-HOMOLOGACAO-ARQUIVO@@@@ "
# Filtro unico de mascara: o proprio binario do painel (internal/capture). Roda como "nobody": ele so le o stdin e escreve
# o stdout (arquivos abertos pelo root), sem precisar do config, do banco nem do segredo do painel.
FILTRO_CMD = ["runuser", "-u", "nobody", "--", BIN, "filtrar"]
FILTRO_TIMEOUT = 1800

LEGENDA_ROTEIRO = [
    "A, B e C = tres ramais de teste (telefones ou softphones registrados).",
    "T = tronco  |  X = seu celular (numero externo)  |  F = fila  |  R = ring group.",
]
# (titulo, o que fazer). docs/homologacao/ROTEIRO-MANUAL.md repete estes passos (um teste confere os titulos).
PASSOS_HOMOLOGACAO = [
    ("Chamada interna", "A liga para B, B atende, conversam 20 s, A desliga."),
    ("Entrada por tronco", "De X, ligue para o numero do tronco T; A atende, 20 s, desligue."),
    ("Saida por tronco", "A liga para X (com o prefixo de saida), atenda, 20 s, desligue."),
    ("Transferencia cega", "X liga para A; A atende e transfere (cega) para B; B atende, 10 s, desliga. "
                           "Repita com chamada interna: B liga para A e A transfere para C."),
    ("Transferencia assistida", "X liga para A; A consulta C (assistida), C atende, A completa a transferencia; "
                                "10 s, desliga. Repita cancelando a consulta (A volta para X)."),
    ("Chamada em espera", "A em chamada com B coloca B em espera, 15 s, retoma, desliga."),
    ("Fila", "De X, ligue para F; um agente atende; 15 s; desligue. Faca outra deixando tocar ate o transbordo."),
    ("Ring group", "Ligue para R; um dos ramais atende; 15 s; desligue. Faca outra em que ninguem atende."),
    ("Chamada nao atendida", "A liga para B e B nao atende (deixe cair no timeout ou no correio de voz). "
                             "Repita desligando do lado de A antes de tocar muito."),
    ("Ramal desregistrando e voltando", "Tire o telefone de C da rede por 40 s, religue e espere registrar. "
                                        "Repita com C em chamada."),
    ("Restart do Asterisk no meio de uma chamada", "Com A falando com B e X ligando para F (tocando), rode em OUTRO "
                                                   "terminal: systemctl restart asterisk. Espere o painel reconectar."),
]

LEIA_ME_PACOTE = """Pacote de homologacao / coleta de suporte do Painel de Ramais
=============================================================
Gerado por: ./instalar.py homologar. Tudo aqui passou pelo filtro `painel-ramais filtrar`
(as mesmas regras do `painel-ramais capturar`): senhas e segredos viram ***; numeros externos, nomes,
usuarios, hosts e IPs viram valores ficticios (o mesmo original = o mesmo ficticio em todo o pacote).

Arquivos
  meta.json        versao, tecnologia, duracao, como terminou
  pre.txt          estado antes do roteiro: versao do Asterisk, `manager show command <Action>` de cada action da
                   lista fixa, getenforce, systemctl status (socket e servico), systemd-analyze security,
                   resultado do `verificar`, config.yaml
  snapshots.txt    a cada 2 s: estado do painel (/api/snapshot) + `core show channels concise` + contatos do
                   Asterisk (`pjsip show contacts` ou `sip show peers`). Secao "igual" = sem mudanca desde a anterior
  roteiro.log      hora de cada passo do roteiro e das suas anotacoes
  captura.txt      captura de AMI (`painel-ramais capturar --anonimizar`, formato do CONTRATOS secao 18). Foi anonimizada
                   pelo proprio capturar, com mapeamento PROPRIO: o mesmo IP/numero NAO tem o mesmo ficticio dos
                   outros arquivos (nao cruze a captura com o snapshots.txt por valor)
  capturar.err     mensagens do capturar
  journal.txt      journal do servico e do socket
  instalacao.log   log do instalador

A mascara nao cobre: segredo que ocupa varias linhas (chave PEM), segredo em argv/ps fora de par chave=valor e texto
livre sem chave (nome de pessoa, numero curto). Revise antes de compartilhar.

Antes de enviar: analise com `python3 tools/analisar_homologacao.py <este arquivo.tar.gz>`; ele lista divergencias
painel x Asterisk e as linhas em que a anonimizacao pode ter falhado.
"""


def _utc():
    d = datetime.datetime.now(datetime.timezone.utc)
    return d.strftime("%Y-%m-%dT%H:%M:%S.") + "%03dZ" % (d.microsecond // 1000)


def _bloco(titulo, cmd, timeout=60):
    """Roda cmd e devolve uma secao de texto do pre.txt (a saida e dado, nunca instrucao)."""
    rc, out = run(cmd, timeout=timeout, registrar=False)  # a saida vai so para o pre.txt (que passa pelo filtro)
    return "=== %s ===\n$ %s\n[rc=%s]\n%s\n\n" % (titulo, " ".join(cmd), rc, out.rstrip())


def coletar_pre():
    """Estado antes do roteiro. Falha se o binario do painel nao responde (sem ele nao ha filtro)."""
    rc, out = daemon_cli(["actions"])
    acoes = out.split() if rc == 0 else []
    if not acoes:
        raise Falha("%s actions falhou (rc=%d): %s\nO painel esta instalado? Rode ./instalar.py instalar antes."
                    % (BIN, rc, out.strip()))
    if not all(re.match(r"^[A-Za-z]+$", a) for a in acoes):  # vira argumento de `asterisk -rx`
        raise Falha("%s actions devolveu um nome de action invalido: %r" % (BIN, acoes))
    ast = ["asterisk", "-rx"]
    partes = [_bloco("Versao do Asterisk", ast + ["core show version"])]
    for a in acoes:  # a lista fixa vem do proprio binario (internal/ami/actions.go)
        partes.append(_bloco("manager show command " + a, ast + ["manager show command " + a]))
    partes += [
        _bloco("getenforce", ["getenforce"]),
        _bloco("systemctl status " + SOCKET, ["systemctl", "status", SOCKET, "--no-pager", "-l"]),
        _bloco("systemctl status " + PKG + ".service", ["systemctl", "status", PKG + ".service", "--no-pager", "-l"]),
        _bloco("systemd-analyze security " + PKG, ["systemd-analyze", "security", PKG, "--no-pager"]),
        _bloco("Versao do painel", ["rpm", "-q", PKG]),
        _bloco("painel-ramais version", ["runuser", "-u", SVC_USER, "--", BIN, "version"]),
    ]
    dizer("-- verificar --")
    texto = imprimir_relatorio(executar_checagens(bundle_rpm_ou_none()))
    partes.append("=== verificar ===\n%s\n" % texto)
    partes.append("=== config.yaml ===\n%s\n" % (ler_config() or "(ilegivel ou ausente)"))
    return "".join(partes)


def tecnologia_do_painel():
    try:
        t = ler_health(ler_porta())["ami"]["technology"]
    except (urllib.error.URLError, OSError, ValueError, KeyError, TypeError):
        t = None
    if t in ("sip", "pjsip"):
        return t
    sip, pj = tecnologia_ativa()
    return "sip" if sip and not pj else "pjsip"


class SessaoPainel(object):
    """Le /api/snapshot pela porta interna com um token de uso unico (CLI `token`) trocado por sessao."""

    def __init__(self, porta):
        self.url = "http://%s:%d" % (ler_host(), porta)
        self.cookie = None

    def _abrir(self):
        # direto no subprocess: o token nao vai para o log do instalador
        p = subprocess.run(["runuser", "-u", SVC_USER, "--", BIN, "token", "--sub", "homologacao", "--ttl", "60s"],
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, universal_newlines=True,
                           encoding="utf-8", errors="replace", timeout=15, **_isolar(["runuser"]))
        if p.returncode != 0:
            raise OSError("painel-ramais token falhou (rc=%d): %s" % (p.returncode, p.stderr.strip()[:200]))
        req = urllib.request.Request(self.url + "/api/token", data=json.dumps({"token": p.stdout.strip()}).encode(),
                                     headers={"Content-Type": "application/json"})
        r = urllib.request.urlopen(req, timeout=5)
        m = re.search(COOKIE_PAINEL + r"=([^;\s]+)", r.headers.get("Set-Cookie", ""))
        if not m:
            raise OSError("o painel nao devolveu cookie de sessao")
        self.cookie = m.group(1)

    def fechar(self):
        """POST /api/logout: a sessao do homologar nao fica viva no banco. Qualquer falha e ignorada."""
        cookie, self.cookie = self.cookie, None
        if not cookie:
            return
        try:
            req = urllib.request.Request(self.url + "/api/logout", data=b"{}",
                                         headers={"Content-Type": "application/json", "Cookie": COOKIE_PAINEL + "=" + cookie})
            urllib.request.urlopen(req, timeout=5).read()
        except (OSError, ValueError, http.client.HTTPException):
            pass

    def snapshot(self):
        """Texto JSON do /api/snapshot. Uma renovacao de sessao se vier 401."""
        for tentativa in (1, 2):
            if not self.cookie:
                self._abrir()
            req = urllib.request.Request(self.url + "/api/snapshot", headers={"Cookie": COOKIE_PAINEL + "=" + self.cookie})
            try:
                return urllib.request.urlopen(req, timeout=5).read().decode("utf-8", "replace")
            except urllib.error.HTTPError as e:
                if e.code != 401 or tentativa == 2:
                    raise
                self.cookie = None


def _compactar_painel(corpo):
    """(linha JSON compacta, chave de comparacao sem server_time) do /api/snapshot."""
    obj = json.loads(corpo)
    dados = obj.get("data") if isinstance(obj, dict) else None
    base = dict(dados) if isinstance(dados, dict) else {}
    base.pop("server_time", None)
    chave = json.dumps(base, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False), chave


class Snapshots(object):
    """snapshots.txt: secoes "@@ <hora UTC> <painel|canais|contatos> [igual|erro]" seguidas do texto."""

    def __init__(self, caminho, painel, tec):
        self.f = io.open(caminho, "a", encoding="utf-8")
        self.painel, self.ultimo = painel, {}
        self.contatos_cmd = ["asterisk", "-rx", "pjsip show contacts" if tec == "pjsip" else "sip show peers"]

    def fechar(self):
        self.f.close()

    def _secao(self, ts, nome, texto, chave=None, erro=False):
        chave = (("ERRO:" if erro else "") + texto) if chave is None else chave
        if self.ultimo.get(nome) == chave:
            self.f.write("@@ %s %s igual\n" % (ts, nome))
            return
        self.ultimo[nome] = chave
        self.f.write("@@ %s %s%s\n%s\n" % (ts, nome, " erro" if erro else "", texto.rstrip("\n")))

    def tirar(self):
        ts = _utc()
        try:
            linha, chave = _compactar_painel(self.painel.snapshot())
            self._secao(ts, "painel", linha, chave)
        except (OSError, ValueError, http.client.HTTPException, subprocess.SubprocessError) as e:  # URLError/HTTPError sao OSError
            self._secao(ts, "painel", "%s: %s" % (type(e).__name__, e), erro=True)
        for nome, cmd in (("canais", ["asterisk", "-rx", "core show channels concise"]), ("contatos", self.contatos_cmd)):
            rc, out = run(cmd, timeout=10, registrar=False)
            self._secao(ts, nome, out if rc == 0 else "rc=%s\n%s" % (rc, out), erro=rc != 0)
        self.f.flush()


def _esperar_linha(timeout):
    """Uma linha digitada no terminal, ou None ao fim de `timeout` s. Sem terminal, so espera."""
    timeout = max(0.0, timeout)
    try:
        interativo = sys.stdin.isatty()
    except (AttributeError, ValueError):
        interativo = False
    if not interativo:
        time.sleep(timeout)
        return None
    pronto = select.select([sys.stdin], [], [], timeout)[0]
    return sys.stdin.readline().rstrip("\r\n") if pronto else None


def _tela_passo(i):
    titulo, texto = PASSOS_HOMOLOGACAO[i]
    return "\n>>> PASSO %d/%d: %s\n    %s\n" % (i + 1, len(PASSOS_HOMOLOGACAO), titulo, texto)


def iniciar_captura(minutos, fout, ferr):
    """`painel-ramais capturar` como o usuario do servico; a captura sai pelo stdout (arquivo aberto pelo root),
    entao o root nunca le arquivo dentro de area do usuario do servico."""
    cmd = ["runuser", "-u", SVC_USER, "--", BIN, "capturar", "--minutos", "%.3f" % minutos, "--saida", "-", "--anonimizar"]
    log("$ " + " ".join(cmd))
    return subprocess.Popen(cmd, stdout=fout, stderr=ferr, **_isolar(cmd))


def _sinalizar(proc, sig):
    try:
        proc.send_signal(sig)
    except (OSError, ValueError):
        pass


def matar_captura(cap):
    """terminate e espera curta; kill se nao sair. Nunca levanta."""
    try:
        if cap.poll() is None:
            cap.terminate()
        cap.wait(timeout=10)
    except subprocess.TimeoutExpired:
        try:
            cap.kill()
            cap.wait(timeout=10)
        except (OSError, subprocess.TimeoutExpired):
            pass
    except OSError:
        pass


def encerrar_captura(cap, motivo):
    """Fim do roteiro: deixa o capturar gravar o snapshot final e espera (ate ESPERA_CAPTURA_FINAL s). Como o capturar
    roda em sessao propria, o Ctrl-C do terminal nao chega nele: no fim antecipado o SIGINT e enviado aqui."""
    if cap.poll() is None:
        if motivo != "prazo":
            _sinalizar(cap, signal.SIGINT)
        dizer("Aguardando o capturar gravar o snapshot final (ate %d s). Ctrl-C de novo abandona a captura e segue."
              % ESPERA_CAPTURA_FINAL)
    try:
        cap.wait(timeout=ESPERA_CAPTURA_FINAL)
    except KeyboardInterrupt:
        dizer("Captura abandonada a seu pedido; seguindo com o que foi gravado.")
        matar_captura(cap)
    except subprocess.TimeoutExpired:
        dizer("O capturar nao fechou a tempo; encerrando.")
        matar_captura(cap)


class SinalRecebido(BaseException):
    """SIGTERM/SIGHUP: sobe ate o finally do homologar (apaga os dados brutos, para a captura) sem gerar pacote."""

    def __init__(self, signum):
        BaseException.__init__(self, signum)
        self.signum = signum


def _ao_sinal(signum, frame):
    raise SinalRecebido(signum)


def _instalar_sinais():
    antigos = {}
    for nome in ("SIGTERM", "SIGHUP"):
        sig = getattr(signal, nome, None)
        if sig is not None:
            antigos[sig] = signal.signal(sig, _ao_sinal)
    return antigos


def _restaurar_sinais(antigos):
    for sig, antigo in antigos.items():
        try:
            signal.signal(sig, antigo)
        except (ValueError, OSError, TypeError):
            pass


def executar_roteiro(wd, duracao_s, snaps, cap=None):
    """Laco principal: snapshots a cada INTERVALO_SNAPSHOT, relogio e passo atual no terminal. Retorna como terminou."""
    tty = sys.stdout.isatty()
    roteiro = io.open(os.path.join(wd, "roteiro.log"), "a", encoding="utf-8")

    def anotar(msg):
        roteiro.write("%s %s\n" % (_utc(), msg))
        roteiro.flush()

    passo, motivo = 0, "prazo"
    t0 = time.time()
    fim, proximo, ultimo_status = t0 + duracao_s, t0, 0
    cap_avisado = False
    anotar("inicio duracao_s=%.1f intervalo_s=%.1f" % (duracao_s, INTERVALO_SNAPSHOT))
    for ln in LEGENDA_ROTEIRO:
        dizer(ln)
    dizer("Enter = proximo passo; qualquer texto + Enter = anotacao; Ctrl-C = encerra e gera o pacote.")
    anotar("passo 1/%d %s" % (len(PASSOS_HOMOLOGACAO), PASSOS_HOMOLOGACAO[0][0]))
    print(_tela_passo(0))
    try:
        while time.time() < fim:
            agora = time.time()
            if agora >= proximo:
                snaps.tirar()
                proximo += INTERVALO_SNAPSHOT
                if proximo < agora:  # tick lento: nao acumula atraso
                    proximo = agora + INTERVALO_SNAPSHOT
            if cap is not None and not cap_avisado and cap.poll() is not None:
                cap_avisado = True
                aviso = ("AVISO: o capturar terminou (rc=%s) antes do fim; os snapshots seguem. Veja capturar.err no pacote."
                         % cap.returncode)
                print("\n" + aviso)
                anotar(aviso)
            restante = max(0, int(fim - time.time()))
            status = "[%s] restam %02d:%02d | passo %d/%d: %s | Enter = proximo" % (
                time.strftime("%H:%M:%S"), restante // 60, restante % 60, passo + 1, len(PASSOS_HOMOLOGACAO),
                PASSOS_HOMOLOGACAO[passo][0])
            if tty:
                sys.stdout.write("\r" + status.ljust(100)[:100])
                sys.stdout.flush()
            elif agora - ultimo_status >= 60:
                print(status)
                ultimo_status = agora
            linha = _esperar_linha(min(1.0, proximo - time.time(), fim - time.time()))
            if linha is None:
                continue
            if linha.strip() == "":
                if passo + 1 < len(PASSOS_HOMOLOGACAO):
                    passo += 1
                    anotar("passo %d/%d %s" % (passo + 1, len(PASSOS_HOMOLOGACAO), PASSOS_HOMOLOGACAO[passo][0]))
                    print("\n" + _tela_passo(passo))
                else:
                    print("\nRoteiro concluido. Aguarde o relogio zerar ou use Ctrl-C para encerrar agora.")
            else:
                anotar("anotacao " + linha.strip())
    except KeyboardInterrupt:
        motivo = "interrupcao"
    except BaseException:  # SinalRecebido ou erro inesperado: sobe, mas sem deixar o arquivo aberto
        roteiro.close()
        raise
    snaps.tirar()  # estado final
    anotar("fim motivo=" + motivo)
    roteiro.close()
    print("")
    return motivo


class FalhaMascara(Falha):
    pass


# Nome curto comum: trocar "issabel"/"asterisk"/"pbx" em todo o texto estraga o diagnostico ("host-001.invalid -rx core show
# version"), entao so o FQDN (com ponto) vai para o --host; o nome curto so se nao estiver nesta lista.
NOMES_CURTOS_COMUNS = ("issabel", "asterisk", "pbx", "elastix", "freepbx", "localhost")


def nomes_da_maquina():
    """hostname e FQDN deste servidor para o `filtrar --host`."""
    out = []
    for f in (socket.getfqdn, socket.gethostname):
        try:
            n = f().strip()
        except (OSError, UnicodeError):
            n = ""
        if not n or n.lower().startswith("localhost") or n in out:
            continue
        if "." not in n and n.lower() in NOMES_CURTOS_COMUNS:
            continue
        out.append(n)
    return out


def _filtrar(extra, fin, fout):
    """Roda o filtro (stdin/stdout em arquivos abertos pelo root; sessao nova). Qualquer falha e FalhaMascara."""
    try:
        p = subprocess.run(FILTRO_CMD + extra, stdin=fin, stdout=fout, stderr=subprocess.PIPE, timeout=FILTRO_TIMEOUT,
                           start_new_session=True)
    except subprocess.TimeoutExpired:
        raise FalhaMascara("O filtro de mascara excedeu %d s." % FILTRO_TIMEOUT)
    except OSError as e:
        raise FalhaMascara("Nao consegui executar o filtro de mascara: %s" % e)
    if p.returncode != 0:
        raise FalhaMascara("O filtro de mascara falhou (rc=%d): %s"
                           % (p.returncode, p.stderr.decode("utf-8", "replace").strip()[:300]))


def montar_pacote(wd, nomes, leia_me, destino_dir, selo):
    """Passa os arquivos brutos de wd pelo filtro (UMA execucao: o mesmo original vira o mesmo ficticio em todos),
    junta a captura (so segredos, ja anonimizada na origem) e escreve homologacao-<selo>.tar.gz com modo 0600."""
    fluxo, saida = os.path.join(wd, "fluxo.bruto"), os.path.join(wd, "fluxo.limpo")
    with open(fluxo, "wb") as f:
        for nome in nomes:
            f.write(MARCA_ARQUIVO + nome.encode() + b"\n")
            with open(os.path.join(wd, nome), "rb") as src:
                dados = src.read()
            f.write(dados if dados.endswith(b"\n") or not dados else dados + b"\n")
    hosts = []
    for h in nomes_da_maquina():
        hosts += ["--host", h]
    with open(fluxo, "rb") as i, open(saida, "wb") as o:
        _filtrar(["--anonimizar"] + hosts, i, o)
    limpo = {}
    atual = None
    with open(saida, "rb") as f:
        for ln in f:
            if ln.startswith(MARCA_ARQUIVO):
                atual = ln[len(MARCA_ARQUIVO):].strip().decode()
                limpo[atual] = []
            elif atual is not None:
                limpo[atual].append(ln)
    if sorted(limpo) != sorted(nomes):
        raise FalhaMascara("O filtro devolveu arquivos diferentes dos enviados (%s)." % sorted(limpo))
    # captura: ja anonimizada pelo capturar; passa de novo pelo filtro so para garantir a mascara de segredos
    with open(os.path.join(wd, "captura.txt"), "rb") as i, open(os.path.join(wd, "captura.limpa"), "wb") as o:
        _filtrar([], i, o)
    base = "homologacao-" + selo
    destino = os.path.join(destino_dir, base + ".tar.gz")
    fd = os.open(destino, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "wb") as raw, tarfile.open(fileobj=raw, mode="w:gz") as tar:
            itens = [(n, b"".join(limpo[n])) for n in nomes]
            with open(os.path.join(wd, "captura.limpa"), "rb") as cf:
                itens.append(("captura.txt", cf.read()))
            mtime = int(time.time())
            for nome, dados in itens + [("LEIA-ME.txt", leia_me.encode("utf-8"))]:
                ti = tarfile.TarInfo(base + "/" + nome)
                ti.size, ti.mtime, ti.mode = len(dados), mtime, 0o600
                tar.addfile(ti, io.BytesIO(dados))
    except BaseException:
        try:
            os.remove(destino)  # nunca deixa um pacote pela metade
        except OSError:
            pass
        raise
    return destino


def copiar_log_instalador(destino):
    try:
        shutil.copyfile(LOG_FILE, destino)
    except (OSError, IOError):
        with open(destino, "w") as f:
            f.write("(sem %s)\n" % LOG_FILE)


def copiar_log_instalador(destino):
    try:
        shutil.copyfile(LOG_FILE, destino)
    except (OSError, IOError):
        with open(destino, "w") as f:
            f.write("(sem %s)\n" % LOG_FILE)


def cmd_homologar(args):
    exigir_root()
    minutos = args.duracao
    if not 0 < minutos <= DURACAO_HOMOLOGAR_MAX_MIN:
        raise Uso("--duracao deve ficar entre 0 e %d minutos." % DURACAO_HOMOLOGAR_MAX_MIN)
    destino_dir = args.saida or os.getcwd()
    if not os.path.isdir(destino_dir):
        raise Uso("--saida: diretorio inexistente: " + destino_dir)
    if not versao_instalada():
        raise Falha("Painel nao instalado: rode ./instalar.py instalar antes.")
    dizer("== Homologacao (%g min; snapshots a cada %g s) ==" % (minutos, INTERVALO_SNAPSHOT))
    selo = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    antigos = _instalar_sinais()  # SIGTERM/SIGHUP (ssh caiu): sobe pelo finally, apaga os dados brutos e para a captura
    wd, manter, painel, snaps = None, False, None, None
    try:
        wd = tempfile.mkdtemp(prefix="painel-homologacao-", dir=WORK_BASE)  # root, 0700: dados brutos so existem aqui
        inicio = _utc()
        dizer("Coletando o estado antes do roteiro (leva alguns segundos)...")
        with open(os.path.join(wd, "pre.txt"), "w", encoding="utf-8") as f:
            f.write(coletar_pre())
        tec = tecnologia_do_painel()
        painel = SessaoPainel(ler_porta())
        snaps = Snapshots(os.path.join(wd, "snapshots.txt"), painel, tec)
        with open(os.path.join(wd, "captura.txt"), "wb") as cap_out, open(os.path.join(wd, "capturar.err"), "wb") as cap_err:
            try:
                cap = iniciar_captura(minutos, cap_out, cap_err)
            except OSError as e:
                raise Falha("Nao consegui iniciar o capturar: %s" % e)
            try:
                motivo = executar_roteiro(wd, minutos * 60.0, snaps, cap)
                snaps.fechar()
                painel.fechar()  # logout: a sessao do homologar nao fica viva
                encerrar_captura(cap, motivo)
            except BaseException:
                matar_captura(cap)
                raise
        dizer("Captura encerrada (rc=%s). Montando o pacote..." % cap.returncode)
        if cap.returncode != 0:
            dizer("AVISO: o capturar nao terminou bem; veja capturar.err no pacote (os snapshots continuam validos).")
        desde = (datetime.datetime.now() - datetime.timedelta(minutes=minutos + 15)).strftime("%Y-%m-%d %H:%M:%S")
        rc, out = run(["journalctl", "-u", PKG + ".service", "-u", SOCKET, "--since", desde, "--no-pager", "-o", "short-iso"],
                      timeout=120, registrar=False)
        with open(os.path.join(wd, "journal.txt"), "w", encoding="utf-8") as f:
            f.write("[rc=%s]\n%s" % (rc, out))
        copiar_log_instalador(os.path.join(wd, "instalacao.log"))
        meta = {"inicio_utc": inicio, "fim_utc": _utc(), "duracao_min": minutos, "intervalo_s": INTERVALO_SNAPSHOT,
                "tecnologia": tec, "extension_pattern": ler_pattern().pattern, "versao_painel": versao_instalada(),
                "encerrado_por": motivo, "captura_rc": cap.returncode, "passos": [p[0] for p in PASSOS_HOMOLOGACAO]}
        with open(os.path.join(wd, "meta.json"), "w", encoding="utf-8") as f:
            json.dump(meta, f, indent=1, sort_keys=True)
        nomes = ["meta.json", "pre.txt", "snapshots.txt", "roteiro.log", "capturar.err", "journal.txt", "instalacao.log"]
        pacote = montar_pacote(wd, nomes, LEIA_ME_PACOTE, destino_dir, selo)
    except FalhaMascara:
        manter = True
        dizer("Os dados BRUTOS (sem mascara, NAO envie) ficaram em %s para nova tentativa." % wd)
        raise
    finally:
        if snaps is not None:
            snaps.fechar()
        if painel is not None:
            painel.fechar()
        if wd and not manter:
            shutil.rmtree(wd, ignore_errors=True)
        _restaurar_sinais(antigos)
    dizer("")
    dizer("Pacote: %s (%d KiB, modo 0600)" % (pacote, os.path.getsize(pacote) // 1024))
    dizer("Mascara aplicada a todo o conteudo. Antes de enviar, analise: python3 tools/analisar_homologacao.py " + pacote)
    return EXIT_OK


# ---------------------------------------------------------------- main

class _Parser(argparse.ArgumentParser):
    def error(self, message):
        self.print_usage(sys.stderr)
        sys.stderr.write("instalar.py: erro: %s\n" % message)
        sys.exit(EXIT_USO)


def montar_parser():
    p = _Parser(prog="instalar.py", description="Instalador do Painel de Ramais")
    p.add_argument("comando", choices=["verificar", "instalar", "status", "rollback", "remover", "homologar"])
    p.add_argument("--respostas", metavar="ARQ.ini", help="respostas nao interativas (senha por ARQUIVO 0600)")
    p.add_argument("--sim", action="store_true", help="aceita as confirmacoes")
    p.add_argument("--purge", action="store_true", help="(remover) apaga tambem dados e configuracao")
    p.add_argument("--duracao", type=float, default=DURACAO_HOMOLOGAR_MIN, metavar="MIN",
                   help="(homologar) duracao em minutos (padrao %d)" % DURACAO_HOMOLOGAR_MIN)
    p.add_argument("--saida", metavar="DIR", help="(homologar) pasta do .tar.gz (padrao: pasta atual)")
    return p


def main(argv=None):
    if (getattr(sys.stdout, "encoding", None) or "").lower() != "utf-8" and hasattr(sys.stdout, "buffer"):
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
    args = montar_parser().parse_args(argv)
    cmds = {"verificar": cmd_verificar, "instalar": cmd_instalar, "status": cmd_status,
            "rollback": cmd_rollback, "remover": cmd_remover, "homologar": cmd_homologar}
    CMD_ATUAL[0] = args.comando
    log("=== instalar.py %s ===" % args.comando)
    try:
        if args.comando == "verificar":
            return cmds["verificar"](args)
        if args.comando != "instalar" and args.respostas:
            raise Uso("--respostas so vale para 'instalar'.")
        return cmds[args.comando](args)
    except Falha as e:
        dizer("FALHA: %s" % e)
        dizer("Log completo: " + LOG_FILE)
        return e.codigo
    except KeyboardInterrupt:
        dizer("Interrompido.")
        return 130
    except SinalRecebido as e:
        log("encerrado pelo sinal %s" % e.signum)
        return 128 + e.signum


if __name__ == "__main__":
    sys.exit(main())
