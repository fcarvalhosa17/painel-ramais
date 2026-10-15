#!/usr/bin/env python3
"""Analisa o pacote gerado por `./instalar.py homologar` (arquivo .tar.gz ou a pasta ja extraida).

1. Compara, instante a instante, o estado do painel com o que o Asterisk devolveu na mesma amostra:
   registro, IP, quantidade de chamadas, estado da chamada, outro lado e direcao. Cada divergencia sai com a hora
   (UTC), o passo do roteiro em andamento e a evidencia (as linhas do Asterisk).
2. Lista as linhas em que a anonimizacao pode ter falhado (IP, numero longo, segredo, host/e-mail, nome sem
   mascara) para voce revisar ANTES de qualquer trecho ir para testdata/.

Uso: python3 tools/analisar_homologacao.py <arquivo.tar.gz | pasta> [--pattern REGEX]
Saida 0 = nada a revisar; 1 = ha divergencia ou possivel vazamento; 2 = uso/pacote invalido.
Python 3.6+, somente biblioteca padrao. Nao grava nada.

Limites (heuristica, confirme na evidencia impressa): o painel usa o ViaAddr do contato PJSIP e o `pjsip show contacts`
mostra o host do Uri (podem diferir com NAT); "em espera" nao aparece no `core show channels concise`; o numero externo
de chamada de SAIDA nao e comparado (prefixo de discagem x ConnectedLine). A mascara troca o mesmo original pelo mesmo
ficticio em todo o pacote, entao a igualdade painel x Asterisk se preserva.
"""
import argparse
import io
import json
import os
import re
import sys
import tarfile

CABECALHO = re.compile(r"^@@ (\S+) (painel|canais|contatos)(?: (igual|erro))?$")
PASSO = re.compile(r"^(\S+) passo (\d+)/(\d+) (.*)$")
SEM_PATTERN = r"^[0-9]+$"
# O extension_pattern do meta.json vem do pacote (dado nao confiavel): so e EXIBIDO no relatorio, nunca compilado. Quem
# decide o padrao e o --pattern explicito; os nomes de ramal acima de MAX_NOME_RAMAL caracteres nunca passam pelo regex.
MAX_NOME_RAMAL = 32


def _compilar_padrao(txt):
    try:
        return re.compile(txt) if txt else None
    except re.error:
        return None


# ------------------------------------------------------------------ leitura do pacote

def _texto(dados):
    return dados.decode("utf-8", "replace")


def _normalizar(arq):
    """CRLF (frames da captura; arquivos gerados no Windows) vira LF."""
    return {k: v.replace("\r\n", "\n") for k, v in arq.items()}


# Tetos de leitura (o pacote e dado nao confiavel): estourou, codigo de saida 2.
MAX_MEMBRO = 64 * 1024 * 1024
MAX_TOTAL = 256 * 1024 * 1024
MAX_MEMBROS = 64
MAX_LINHA = 1024 * 1024


def _teto(nome, tamanho, total, n):
    if n > MAX_MEMBROS:
        raise ValueError("pacote com mais de %d arquivos" % MAX_MEMBROS)
    if tamanho > MAX_MEMBRO:
        raise ValueError("%s tem %d bytes (maximo %d por arquivo)" % (nome, tamanho, MAX_MEMBRO))
    if total + tamanho > MAX_TOTAL:
        raise ValueError("pacote passa de %d bytes no total" % MAX_TOTAL)


def ler_pacote(caminho):
    """{nome_relativo: texto}. Aceita .tar.gz (com ou sem a pasta raiz) ou diretorio. So arquivos comuns."""
    arq, total = {}, 0
    if os.path.isdir(caminho):
        for raiz, _, nomes in os.walk(caminho):
            for n in nomes:
                p = os.path.join(raiz, n)
                if os.path.islink(p) or not os.path.isfile(p):
                    continue
                tam = os.path.getsize(p)
                _teto(p, tam, total, len(arq) + 1)
                total += tam
                with open(p, "rb") as f:
                    arq[os.path.relpath(p, caminho).replace(os.sep, "/")] = _texto(f.read(MAX_MEMBRO))
        return arq
    try:
        with tarfile.open(caminho) as tar:
            for m in tar:  # iterando, sem getmembers(): um tar com milhoes de cabecalhos para no teto
                if not m.isfile():
                    continue
                _teto(m.name, m.size, total, len(arq) + 1)
                total += m.size
                nome = m.name.split("/", 1)[1] if "/" in m.name else m.name
                arq[nome] = _texto(tar.extractfile(m).read(MAX_MEMBRO))
    except (tarfile.TarError, OSError, EOFError) as e:
        raise ValueError("pacote ilegivel (%s): %s" % (caminho, e))
    return arq


def ler_ticks(texto):
    """[(ts, {secao: (estado, texto)})] em ordem; 'igual' repete a secao anterior; estado 'ok' ou 'erro'."""
    ticks, ultimo, atual = [], {}, None
    pend = None  # (ts, nome, marca, linhas)

    def fechar():
        nonlocal pend, atual
        if pend is None:
            return
        ts, nome, marca, linhas = pend
        if marca == "igual":
            val = ultimo.get(nome, ("erro", "(sem secao anterior)"))
        else:
            val = ("erro" if marca == "erro" else "ok", "\n".join(linhas))
            ultimo[nome] = val
        if atual is None or atual[0] != ts:
            atual = (ts, {})
            ticks.append(atual)
        atual[1][nome] = val
        pend = None

    for ln in texto.split("\n"):
        m = CABECALHO.match(ln)
        if m:
            fechar()
            pend = (m.group(1), m.group(2), m.group(3), [])
        elif pend is not None:
            pend[3].append(ln)
    fechar()
    return ticks


def ler_passos(texto):
    return [(m.group(1), int(m.group(2)), m.group(4)) for m in (PASSO.match(ln) for ln in texto.split("\n")) if m]


def passo_em(passos, ts):
    atual = None
    for t, n, titulo in passos:
        if t <= ts:
            atual = "%d (%s)" % (n, titulo)
    return atual or "-"


# ------------------------------------------------------------------ o que o Asterisk disse

def host_do_uri(uri):
    m = re.match(r"^sips?:(?:[^@]*@)?(\[[^\]]+\]|[^:;>?]+)", uri)
    return m.group(1) if m else uri


def contatos_pjsip(texto):
    """{aor: (registro, ip, linha)}; `Contact:  <aor>/<uri>  <hash>  <Avail|Unavail|NonQual|...>  <rtt>`."""
    por = {}
    for ln in texto.split("\n"):
        m = re.match(r"^\s*Contact:\s+([^/\s]+)/(\S+)\s+\S+\s+(Avail|Unavail|NonQual|Unknown|Created|Removed)\b", ln)
        if m and m.group(3) != "Removed":
            por.setdefault(m.group(1), []).append((m.group(2), m.group(3), ln.strip()))
    res = {}
    for aor, cs in por.items():
        cs.sort()
        vivos = [c for c in cs if c[1] != "Unavail"]  # o painel escolhe o contato registrado de menor Uri
        melhor = vivos[0] if vivos else cs[0]
        res[aor] = ("registered" if vivos else "unreachable", host_do_uri(melhor[0]), "\n".join(c[2] for c in cs))
    return res


def contatos_sip(texto):
    """{peer: (registro, ip, linha)} de `sip show peers` (colunas: Name/username Host Dyn ... Port Status)."""
    res = {}
    for ln in texto.split("\n"):
        t = ln.split()
        if len(t) < 3 or t[0] == "Name/username" or re.match(r"^\d+ sip peers", ln):
            continue
        nome, host = t[0].split("/")[0], t[1]
        resto = " ".join(t[2:]).upper()
        if host == "(UNSPECIFIED)" or host.upper() == "(UNSPECIFIED)":
            res[nome] = ("unregistered", None, ln.strip())
        elif "UNREACHABLE" in resto:
            res[nome] = ("unreachable", host, ln.strip())
        else:
            res[nome] = ("registered", host, ln.strip())
    return res


def canais_concise(texto):
    """Lista de canais de `core show channels concise` (campos separados por '!')."""
    out = []
    for ln in texto.split("\n"):
        f = ln.split("!")
        if len(f) < 8:
            continue
        m = re.match(r"^(?:PJSIP|SIP)/(.+)-[0-9a-f]{8}$", f[0])
        out.append({"nome": f[0], "endpoint": m.group(1) if m else None, "estado": f[4], "app": f[5], "dados": f[6],
                    "callerid": f[7], "bridge": f[-2] if len(f) >= 14 else "", "linha": ln.strip(),
                    "duracao": int(f[11]) if len(f) >= 12 and f[11].isdigit() else 0})
    return out


def alvos_dial(dados):
    return set(re.findall(r"(?:PJSIP|SIP)/([^,&/@\s]+)", dados or ""))


def esperado_das_chamadas(canais, eh_ramal):
    """{ramal: [chamada esperada]} derivado dos canais; chamada = estado, direcao, kind, number, evidencia."""
    por = {}
    for c in canais:
        if not c["endpoint"] or not eh_ramal(c["endpoint"]):
            continue
        far = [x for x in canais if x is not c and x["bridge"] and x["bridge"] == c["bridge"] and x["endpoint"]]
        via_bridge = bool(far)
        if not far:
            far = [x for x in canais if x is not c and x["endpoint"] and
                   (c["endpoint"] in alvos_dial(x["dados"]) or x["endpoint"] in alvos_dial(c["dados"]))]
        far = far[0] if far else None
        if c["estado"] != "Up" or (far and not via_bridge and far["estado"] != "Up"):
            estado = "ringing"
        else:
            estado = "talking"
        if far is None:
            direcao, kind, numero = "internal", "unknown", None
        elif eh_ramal(far["endpoint"]):
            direcao, kind, numero = "internal", "extension", far["endpoint"]
        elif far["app"] == "AppDial" or far["dados"] == "(Outgoing Line)":
            direcao, kind, numero = "outbound", "external", None  # numero de saida nao e comparado
        else:
            direcao, kind, numero = "inbound", "external", far["callerid"] or None
        evid = c["linha"] + ("\n" + far["linha"] if far else "")
        por.setdefault(c["endpoint"], []).append(
            {"estado": estado, "direcao": direcao, "kind": kind, "numero": numero, "evidencia": evid, "duracao": c["duracao"]})
    for v in por.values():
        v.sort(key=lambda x: -x["duracao"])
    return por


# ------------------------------------------------------------------ comparacao

def comparar_tick(painel_txt, canais_txt, contatos_txt, tec, eh_ramal):
    """Lista de divergencias [(ramal, campo, painel, asterisco, evidencia)] de UMA amostra."""
    dados = json.loads(painel_txt).get("data") or {}
    if dados.get("ami_connected") is False:
        return None  # painel sem AMI: nada a comparar (restart do Asterisk, por exemplo)
    painel = {e["number"]: e for e in dados.get("extensions", [])}
    ast = (contatos_pjsip if tec == "pjsip" else contatos_sip)(contatos_txt)
    calls = esperado_das_chamadas(canais_concise(canais_txt), eh_ramal)
    divs = []
    for num in sorted(set(painel) | set(n for n in ast if eh_ramal(n)) | set(calls)):
        p, a = painel.get(num), ast.get(num)
        if p is None:
            divs.append((num, "ramal", "ausente no painel", "presente no Asterisk", (a or ("", "", ""))[2]))
            continue
        reg_a, ip_a, ev = (a[0], a[1], a[2]) if a else ("unregistered", None, "(sem linha em contatos)")
        if p.get("registration") != reg_a:
            divs.append((num, "registro", p.get("registration"), reg_a, ev))
        if p.get("ip") != ip_a:
            divs.append((num, "ip", p.get("ip"), ip_a, ev))
        pc, ac = p.get("calls") or [], calls.get(num, [])
        if len(pc) != len(ac):
            divs.append((num, "chamadas", "%d chamada(s)" % len(pc), "%d chamada(s)" % len(ac),
                         "\n".join(x["evidencia"] for x in ac) or "(nenhum canal do ramal)"))
            continue
        for c, e in zip(sorted(pc, key=lambda x: x.get("started_at") or ""), ac):
            peer = c.get("peer") or {}
            if c.get("state") != e["estado"] and not (c.get("state") == "hold" and e["estado"] == "talking"):
                divs.append((num, "estado_chamada", c.get("state"), e["estado"], e["evidencia"]))
            if c.get("direction") != e["direcao"]:
                divs.append((num, "direcao", c.get("direction"), e["direcao"], e["evidencia"]))
            if peer.get("kind") != e["kind"]:
                divs.append((num, "outro_lado", "%s %s" % (peer.get("kind"), peer.get("number")), e["kind"], e["evidencia"]))
            elif e["numero"] is not None and peer.get("number") != e["numero"]:
                divs.append((num, "outro_lado", "%s %s" % (peer.get("kind"), peer.get("number")),
                             "%s %s" % (e["kind"], e["numero"]), e["evidencia"]))
    return divs


def divergencias(arq, pattern=None):
    """(episodios, amostras_total, amostras_comparadas). Episodio = mesma divergencia em amostras seguidas."""
    arq = _normalizar(arq)
    meta = json.loads(arq.get("meta.json") or "{}")
    tec = meta.get("tecnologia", "pjsip")
    rx = _compilar_padrao(pattern) or re.compile(SEM_PATTERN)  # meta.json["extension_pattern"] nao e usado
    ticks = ler_ticks(arq.get("snapshots.txt", ""))
    passos = ler_passos(arq.get("roteiro.log", ""))
    abertos, episodios, comparadas = {}, [], 0
    for ts, secoes in ticks:
        ok = all(secoes.get(n, ("erro",))[0] == "ok" for n in ("painel", "canais", "contatos"))
        divs = None
        if ok:
            try:
                divs = comparar_tick(secoes["painel"][1], secoes["canais"][1], secoes["contatos"][1], tec,
                                     lambda n: len(n) <= MAX_NOME_RAMAL and bool(rx.match(n)))
            except (ValueError, KeyError, TypeError):
                divs = None
        if divs is not None:
            comparadas += 1
        chaves = set()
        for ramal, campo, vp, va, ev in divs or []:
            k = (ramal, campo, str(vp), str(va))
            chaves.add(k)
            if k in abertos:
                abertos[k]["fim"], abertos[k]["n"] = ts, abertos[k]["n"] + 1
            else:
                abertos[k] = {"inicio": ts, "fim": ts, "n": 1, "ramal": ramal, "campo": campo, "painel": vp,
                              "asterisk": va, "evidencia": ev, "passo": passo_em(passos, ts)}
        for k in list(abertos):
            if k not in chaves and divs is not None:  # amostra sem comparacao nao fecha nem abre
                episodios.append(abertos.pop(k))
    episodios.extend(abertos.values())
    episodios.sort(key=lambda e: (e["inicio"], e["ramal"], e["campo"]))
    return episodios, len(ticks), comparadas


# ------------------------------------------------------------------ possiveis vazamentos

IPV4 = re.compile(r"(?<![\w.])(?:\d{1,3}\.){3}\d{1,3}(?![\w])")
IPV6 = re.compile(r"(?i)(?<![\w:])(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?![\w:])")
NUMERO = re.compile(r"(?<![\d-])\d{7,}(?!\d)")
# Detector de segredo INDEPENDENTE do filtro (codigo e lista proprios, mais largos): procura "chave secreta" seguida de
# ":" ou "=" em QUALQUER ponto da linha, inclusive dentro de aspas, e nao reaproveita o parser do filtro.
_QUALQUER = r"secret|passw|passphrase|pwd|senha|token|cookie|hmac|signature|credential|md5|challenge|nonce|expectedresponse|auth"
_NO_FIM = r"key|hash|pass|session|sessionid|session_id"
CHAVE_SECRETA = re.compile(r"(?i)(?<![\w.-])(?:[\w.-]*(?:%s)[\w.-]*|[\w.-]*(?:%s))[\"']?\s*[:=]\s*(\"[^\"]*\"?|'[^']*'?|[^\s,;&}\]\"']*)"
                           % (_QUALQUER, _NO_FIM))
CABECALHO_SECRETO = re.compile(r"(?i)^\s*(set-cookie|cookie|authorization|proxy-authorization)\s*:\s*(.*)$")
BEARER = re.compile(r"(?i)\b(?:bearer|basic)\s+([A-Za-z0-9._~+/=-]{6,})")
SENHA_ARROBA = re.compile(r":([^@\s]+)@")  # qualquer ":qualquer-coisa@": senha de userinfo
ESQUEMAS = ("sip", "sips", "tel", "mailto", "http", "https", "iax", "iax2", "ws", "wss", "ftp", "sftp", "tcp", "udp", "tls",
            "h323", "xmpp", "ldap")
DIGEST = re.compile(r"(?i)\b(?:response|nonce|cnonce|opaque)\s*=\s*\"?([^\",\s]+)")


def _segredos(ln):
    """[(tipo, trecho)] de segredo que nao esta mascarado, incluindo texto colado depois do ***."""
    out = []
    for m in CHAVE_SECRETA.finditer(ln):
        val = m.group(1).strip("\"'")
        if val == "":
            continue
        if val != "***":
            out.append(("segredo", m.group(0)[:60]))
            continue
        cauda = ln[m.end():]  # "***" mascara tudo o que vem depois: sobra de palavra e vazamento
        if m.group(1)[0] not in "\"'" and (re.match(r"[,;&]\w", cauda) or re.match(r"\s+\w", cauda)):
            out.append(("segredo-resto", (m.group(0) + cauda)[:60]))
    h = CABECALHO_SECRETO.match(ln)
    if h and h.group(2).strip().strip("\"") not in ("", "***"):
        out.append(("segredo-cabecalho", ln.strip()[:60]))
    for m in BEARER.finditer(ln):
        if m.group(1) != "***":
            out.append(("segredo-bearer", m.group(0)))
    for m in SENHA_ARROBA.finditer(ln):
        esq = re.search(r"([^\s<>\"]*)$", ln[:m.start()]).group(1).lower()  # trecho que vem antes do ":"
        senha = m.group(1)
        if esq in ESQUEMAS:  # "sip:usuario@": o que vem depois do ":" e usuario; so uma senha segunda ainda conta
            if ":" not in senha:
                continue
            senha = senha.split(":", 1)[1]
        if senha and senha != "***":
            out.append(("segredo-userinfo", ln[max(0, m.start() - 12):m.end()]))
    if re.search(r"(?i)\bdigest\b", ln):
        for m in DIGEST.finditer(ln):
            if m.group(1) != "***":
                out.append(("segredo-digest", m.group(0)))
    return out


HOST = re.compile(r"@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+)")
NOME_CHAVE = re.compile(r"(?i)(?:idname|linename|fullname|displayname|cnam|\"name\")\"?\s*[:=]\s*\"?([^\",}\r\n]*)")
NOME_ASPAS = re.compile(r"\"([^\"]+)\"\s*<")
CHAVE_SEGURA = re.compile(r"(?i)^(event|response|action|actionid|uniqueid|linkedid|destuniqueid|destlinkedid|bridge\w*|"
                          r"timestamp|privilege|sequence\w*|duration|\w*usec|\w*time|\w*date|\w*expire|\w*count|epoch|id|version)$")
FALSO_NUMERO = re.compile(r"^\d0{3,}\d{0,4}$")  # primeiro digito + zeros + contador: o formato do ficticio


def _ip_ok(ip):
    return ip.startswith(("127.", "10.200.", "0.0.0.0", "255.")) or any(int(x) > 255 for x in ip.split("."))


def suspeitas_da_linha(ln):
    """[(tipo, trecho)] de uma linha."""
    out = []
    k, _, v = ln.partition(":")
    seguro = bool(CHAVE_SEGURA.match(k.strip().strip('"')))
    for m in IPV4.finditer(ln):
        if not _ip_ok(m.group(0)):
            out.append(("ip", m.group(0)))
    for m in IPV6.finditer(ln):
        g = m.group(0)
        if (g.count(":") >= 5 or "::" in g) and not g.lower().startswith(("fd00:200:", "::1", "::")) and g != "::":
            out.append(("ipv6/mac", g))
    if not seguro:
        for m in NUMERO.finditer(ln):
            if not FALSO_NUMERO.match(m.group(0)):
                out.append(("numero", m.group(0)))
    out.extend(_segredos(ln))
    for m in HOST.finditer(ln):
        h = m.group(1)
        if not IPV4.fullmatch(h) and not h.endswith(".invalid"):
            out.append(("host/e-mail", "@" + h))
    for m in NOME_CHAVE.finditer(ln):
        n = m.group(1).strip()
        if n and not re.fullmatch(r"Pessoa \d+|<unknown>|unknown|null", n):
            out.append(("nome", n))
    for m in NOME_ASPAS.finditer(ln):
        if not re.fullmatch(r"Pessoa \d+", m.group(1)):
            out.append(("nome", m.group(1)))
    return out


def vazamentos(arq):
    """[(arquivo, linha, hora_ou_None, tipo, trecho, texto_da_linha)]."""
    res = []
    arq = _normalizar(arq)
    for nome in sorted(arq):
        if nome == "LEIA-ME.txt":
            continue
        hora = None
        for i, ln in enumerate(arq[nome].split("\n"), 1):
            m = CABECALHO.match(ln)
            if m:
                hora = m.group(1)
            elif nome == "captura.txt":
                f = re.match(r"^[<>] (\S+) \d+$", ln)
                if f:
                    hora = f.group(1)
            if len(ln) > MAX_LINHA:  # nao roda regex em linha de mais de 1 MiB
                res.append((nome, i, hora, "linha-longa", "%d caracteres" % len(ln), ln[:60]))
                continue
            for tipo, trecho in suspeitas_da_linha(ln):
                res.append((nome, i, hora, tipo, trecho, ln.strip()[:160]))
    return res


# ------------------------------------------------------------------ saida

CONTROLES = re.compile(r"[\x00-\x08\x0b-\x1f\x7f]")


def _esc(txt):
    """Caracteres de controle (ESC, CR, NUL...) do pacote nao chegam crus ao terminal."""
    return CONTROLES.sub(lambda m: "\\x%02x" % ord(m.group(0)), txt)


def relatorio(arq, pattern=None, out=sys.stdout):
    eps, total, comp = divergencias(arq, pattern)
    vaz = vazamentos(arq)

    def w(txt):
        out.write(_esc(txt))
    try:
        meta_pat = str(json.loads(_normalizar(arq).get("meta.json") or "{}").get("extension_pattern") or "")
    except (ValueError, AttributeError):
        meta_pat = ""
    w("== Painel x Asterisk ==\n%d amostra(s); %d comparada(s) (as demais tiveram erro de coleta ou painel sem AMI).\n"
      % (total, comp))
    w("Ramais = %s (use --pattern se o config.yaml do cliente tem outro; extension_pattern do meta.json, so informativo: %s)\n"
      % (pattern or SEM_PATTERN, meta_pat[:80] or "-"))
    if not eps:
        w("Nenhuma divergencia.\n")
    for e in eps:
        quando = e["inicio"] if e["inicio"] == e["fim"] else "%s ate %s" % (e["inicio"], e["fim"])
        w("\n[DIVERGENCIA] %s | ramal %s | %s | painel=%s asterisk=%s | %d amostra(s)%s | passo %s\n" % (
            quando, e["ramal"], e["campo"], e["painel"], e["asterisk"], e["n"],
            " (transitoria: pode ser atraso entre as duas leituras)" if e["n"] == 1 else "", e["passo"]))
        for ln in (e["evidencia"] or "").split("\n"):
            w("    asterisk> %s\n" % ln)
    w("\n== Possiveis falhas de anonimizacao (revise ANTES de usar em testdata/) ==\n")
    if not vaz:
        w("Nenhuma linha suspeita.\n")
    for nome, linha, hora, tipo, trecho, texto in vaz:
        w("[SUSPEITA:%s] %s:%d%s | %s | %s\n" % (tipo, nome, linha, " | t=" + hora if hora else "", trecho, texto))
    w("\nResumo: %d divergencia(s), %d linha(s) suspeita(s).\n" % (len(eps), len(vaz)))
    return len(eps), len(vaz)


def main(argv=None):
    ap = argparse.ArgumentParser(description="Analisa o pacote de `instalar.py homologar`")
    ap.add_argument("pacote", help="arquivo .tar.gz ou pasta ja extraida")
    ap.add_argument("--pattern", help="regex de ramal (padrao: a do config.yaml registrada no meta.json)")
    a = ap.parse_args(argv)
    try:
        arq = ler_pacote(a.pacote)
    except (ValueError, OSError) as e:
        sys.stderr.write("analisar_homologacao: %s\n" % e)
        return 2
    if "snapshots.txt" not in arq:
        sys.stderr.write("analisar_homologacao: sem snapshots.txt: nao e um pacote do homologar\n")
        return 2
    if hasattr(sys.stdout, "buffer") and (sys.stdout.encoding or "").lower() != "utf-8":
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")
    d, v = relatorio(arq, a.pattern, sys.stdout)
    return 1 if d or v else 0


if __name__ == "__main__":
    sys.exit(main())
