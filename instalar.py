#!/bin/sh
"exec" "$(command -v python3 || echo /usr/libexec/platform-python)" "$0" "$@"
# Instalador do Painel de Ramais (Issabel 5 / Rocky 8+). Contrato: docs/CONTRATOS.md secao 15.
# Python 3.6+, somente biblioteca padrao. Poliglota: a linha 2 e comando sh e expressao python.
# Papel: orquestrar e validar. NUNCA edita manager_custom.conf, Apache ou SELinux (isso e o %post do RPM)
# e nunca grava no SQLite (usa a CLI do daemon).
# Uso: ./instalar.py {verificar|instalar|status|rollback|remover} [--respostas arq.ini] [--sim] [--purge]

import argparse
import configparser
import datetime
import getpass
import glob
import hashlib
import io
import json
import os
import re
import shutil
import socket
import stat
import sqlite3
import ssl
import subprocess
import sys
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


def run(cmd, entrada=None, timeout=120):
    """Executa comando (lista). Retorna (rc, stdout+stderr). Tudo no log; a entrada (senha) nunca."""
    log("$ " + " ".join(cmd) + ("   [stdin omitido]" if entrada is not None else ""))
    try:
        p = subprocess.run(cmd, input=entrada, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           universal_newlines=True, timeout=timeout)
        rc, out = p.returncode, p.stdout or ""
    except FileNotFoundError:
        rc, out = 127, "comando nao encontrado: %s" % cmd[0]
    except subprocess.TimeoutExpired:
        rc, out = 124, "tempo esgotado (%ss)" % timeout
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


def _vr(txt):
    """Versao-release completa ('0.1.0-2' -> (0, 1, 0, 2)) para comparar upgrade/downgrade."""
    return tuple(int(x) for x in re.findall(r"\d+", txt))


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
        raise Falha("RPM %s-*.rpm nao encontrado ao lado do instalar.py." % PKG)
    return rpms[-1]


def versao_do_rpm(caminho):
    m = re.match(PKG + r"-(\d[^-]*)-(\d+)\.", os.path.basename(caminho))
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
        return (ERRO, "Sistema operacional", "/etc/os-release ilegivel. Alvo suportado: Rocky Linux 8 ou superior.")
    major = (_ver(info.get("VERSION_ID", "0")) or (0,))[0]
    nome = info.get("PRETTY_NAME", info.get("ID", "?"))
    familia = "rhel" in info.get("ID_LIKE", "") or info.get("ID") in ("rhel", "rocky")
    if not familia or major < 8:
        return (ERRO, "Sistema operacional (%s)" % nome, "Requer Rocky Linux 8 ou superior (familia EL).")
    if info.get("ID") != "rocky":
        return (AVISO, "Sistema operacional (%s)" % nome, "Nao e Rocky; so Rocky 8+ e suportado oficialmente.")
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
    if sip and pj:
        return (AVISO, "Tecnologia",
                "chan_sip e chan_pjsip carregados juntos; o painel usara a que tiver ramais (o padrao e usar so uma)."), "pjsip"
    nomes = nomes_endpoints(pj)
    pat = ler_pattern()
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

def ler_senha_arquivo(caminho):
    try:
        st = os.stat(caminho)
    except OSError:
        raise Uso("Arquivo de senha nao encontrado: " + caminho)
    if (st.st_mode & 0o777) not in (0o600, 0o400):
        raise Uso("%s deve ter permissao 0600 ou 0400 (chmod 600)." % caminho)
    if hasattr(os, "geteuid") and st.st_uid != 0:
        raise Uso("%s deve pertencer ao root." % caminho)
    with open(caminho) as f:
        s = f.readline().rstrip("\r\n")
    if not s:
        raise Uso("Arquivo de senha vazio: " + caminho)
    return s


def validar_senha(s):
    if len(s) < 10 or len(s.encode("utf-8")) > 72:
        raise Uso("Senha do admin: minimo 10 caracteres e maximo 72 bytes.")


def pedir(msg, padrao=None):
    if not sys.stdin.isatty():
        raise Uso("Resposta necessaria sem terminal: '%s'. Use --respostas arquivo.ini" % msg)
    v = input("%s%s: " % (msg, " [%s]" % padrao if padrao else "")).strip()
    return v or padrao


def coletar_respostas(args):
    """Todas as respostas antes de alterar qualquer coisa. Retorna dict tecnologia, usuario, senha, csv."""
    r = {"tecnologia": "auto", "usuario": None, "senha": None, "csv": None}
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
        r["usuario"] = s.get("admin_usuario")
        r["csv"] = s.get("grupos_csv") or None
        if not s.get("admin_senha_arquivo"):
            raise Uso("Resposta faltando: admin_senha_arquivo")
        r["senha"] = ler_senha_arquivo(s.get("admin_senha_arquivo"))
        if not r["usuario"]:
            raise Uso("Resposta faltando: admin_usuario")
    else:
        r["tecnologia"] = pedir("Tecnologia (auto/sip/pjsip)", "auto")
        r["usuario"] = pedir("Usuario admin do painel")
        r["senha"] = getpass.getpass("Senha do admin: ")
        if r["senha"] != getpass.getpass("Repita a senha: "):
            raise Uso("As senhas nao conferem.")
        r["csv"] = pedir("Caminho do CSV de grupos (opcional, Enter para pular)", "") or None
    if r["tecnologia"] not in TECNOLOGIAS:
        raise Uso("tecnologia invalida '%s' (use auto, sip ou pjsip)." % r["tecnologia"])
    if not r["usuario"] or not re.match(r"^[A-Za-z0-9_.-]{1,64}$", r["usuario"]):
        raise Uso("Usuario admin invalido (use letras, numeros, . _ -).")
    validar_senha(r["senha"] or "")
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


def criar_admin(usuario, senha):
    rc, out = daemon_cli(["user", "add", usuario, "--password-stdin"], entrada=senha + "\n")
    if rc == CLI_EXISTE:
        dizer("AVISO: usuario '%s' ja existe; senha mantida (use 'painel-ramais user passwd' para trocar)." % usuario)
    elif rc != 0:
        raise Falha("Falha ao criar o usuario admin (rc=%d): %s\nDiagnostico: %s" % (rc, out.strip(), LOG_FILE))


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


def testar_apache():
    """Raiz do painel responde o envelope JSON do daemon; /api/health responde 404 do Apache, sem JSON (D5)."""
    ctx = ssl._create_unverified_context()  # certificado do Issabel costuma ser autoassinado; so loopback
    base = "https://127.0.0.1/painel-ramais/"
    raiz = http_get(base, ctx)
    if raiz is None:
        return False, "sem resposta em " + base
    if not eh_json(raiz[1]):
        return False, "%s respondeu %s sem JSON do daemon: o proxy nao chega ao painel" % (base, raiz[0])
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
        passo = "usuario admin"
        criar_admin(resp["usuario"], resp["senha"])
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
    dizer("Log:         " + LOG_FILE)
    return EXIT_OK


# ---------------------------------------------------------------- status / rollback / remover

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


# ---------------------------------------------------------------- main

class _Parser(argparse.ArgumentParser):
    def error(self, message):
        self.print_usage(sys.stderr)
        sys.stderr.write("instalar.py: erro: %s\n" % message)
        sys.exit(EXIT_USO)


def montar_parser():
    p = _Parser(prog="instalar.py", description="Instalador do Painel de Ramais")
    p.add_argument("comando", choices=["verificar", "instalar", "status", "rollback", "remover"])
    p.add_argument("--respostas", metavar="ARQ.ini", help="respostas nao interativas (senha por ARQUIVO 0600)")
    p.add_argument("--sim", action="store_true", help="aceita as confirmacoes")
    p.add_argument("--purge", action="store_true", help="(remover) apaga tambem dados e configuracao")
    return p


def main(argv=None):
    if (getattr(sys.stdout, "encoding", None) or "").lower() != "utf-8" and hasattr(sys.stdout, "buffer"):
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
    args = montar_parser().parse_args(argv)
    cmds = {"verificar": cmd_verificar, "instalar": cmd_instalar, "status": cmd_status,
            "rollback": cmd_rollback, "remover": cmd_remover}
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


if __name__ == "__main__":
    sys.exit(main())
