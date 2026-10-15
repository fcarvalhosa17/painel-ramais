"""instalar.py homologar com comandos externos simulados e o filtro de mascara REAL (o binario do painel).

O filtro e `painel-ramais filtrar` (Go, internal/capture): os testes que provam a mascara compilam o binario com o
Go do PATH ou de .tools/ e pulam (skip) se nao houver Go. O resto roda sempre.
"""
import argparse
import io
import json
import os
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import instalar  # noqa: E402

sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "tools"))
import analisar_homologacao as analisar  # noqa: E402

RAIZ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SEGREDO = "SEGREDO-PLANTADO-9f3a"
_TMP = tempfile.TemporaryDirectory()
_BINARIO = []

ACOES = ["Login", "Logoff", "Ping", "CoreSettings", "ModuleCheck", "PJSIPShowEndpoint", "PJSIPShowEndpoints",
         "PJSIPShowContacts", "PJSIPShowAors", "SIPpeers", "SIPshowpeer", "DeviceStateList", "CoreShowChannels"]


def setUpModule():
    instalar.LOG_DIR = _TMP.name
    instalar.LOG_FILE = _TMP.name + "/instalacao.log"


def tearDownModule():
    _TMP.cleanup()


def binario_do_painel():
    """Compila cmd/painel-ramais uma vez. None se nao ha Go."""
    if _BINARIO:
        return _BINARIO[0]
    exe = ".exe" if os.name == "nt" else ""
    go = shutil.which("go")
    local = os.path.join(RAIZ, ".tools", "go", "bin", "go" + exe)
    if not go and os.path.isfile(local):
        go = local
    destino = None
    if go:
        env = dict(os.environ)
        if os.path.isdir(os.path.join(RAIZ, ".tools", "gopath")):  # caches do build.ps1
            env.update(GOPATH=os.path.join(RAIZ, ".tools", "gopath"), GOCACHE=os.path.join(RAIZ, ".tools", "gocache"),
                       GOMODCACHE=os.path.join(RAIZ, ".tools", "gopath", "pkg", "mod"))
        destino = os.path.join(_TMP.name, "painel-ramais" + exe)
        p = subprocess.run([go, "build", "-o", destino, "./cmd/painel-ramais"], cwd=RAIZ, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT, universal_newlines=True)
        if p.returncode != 0:
            raise RuntimeError("go build falhou:\n" + p.stdout)
    _BINARIO.append(destino)
    return destino


# Dados da PBX simulada. IP e numeros ficticios (documentacao), o segredo e plantado em varios formatos.
PAINEL = {"success": True, "data": {"epoch": "1791900000123-3", "version": 7, "server_time": "T", "technology": "pjsip",
                                    "ami_connected": True, "secret": SEGREDO, "extensions": [
    {"number": "2001", "name": "Maria Souza", "registration": "registered", "ip": "203.0.113.45", "calls": [
        {"id": "1791902000.10", "direction": "outbound", "state": "talking",
         "peer": {"kind": "external", "number": "11987654321", "name": "ACME LTDA"}}]}]}, "error": None, "meta": {}}
CANAIS = ("PJSIP/2001-00000010!from-internal!011987654321!1!Up!Dial!PJSIP/trunk-acme/sip:011987654321@198.51.100.7,30!2001!!!3!125"
          "!7c1e9a2e-0001!1791902000.10\n")
CONTATOS = "  Contact:  2001/sip:2001@203.0.113.45:5060;ob  a1b2c3d4e5  Avail  12.345\n"


class PainelFalso(object):
    def __init__(self, *a, **kw):
        self.n = 0
        self.fechou = 0

    def fechar(self):
        self.fechou += 1

    def snapshot(self):
        self.n += 1
        p = json.loads(json.dumps(PAINEL))
        p["data"]["server_time"] = "2026-10-15T14:30:%02d.000Z" % self.n  # muda a cada leitura; o dedupe ignora
        return json.dumps(p)


def tabela(extra=None):
    t = list(extra or []) + [
        ("painel-ramais actions", 0, "\n".join(ACOES) + "\n"),
        ("manager show command", 0, "Action: x\nPrivilege: system,reporting\nSecret: %s\n" % SEGREDO),
        ("core show version", 0, "Asterisk 18.20.0 built by root\n"),
        ("core show channels concise", 0, CANAIS),
        ("pjsip show contacts", 0, CONTATOS),
        ("getenforce", 0, "Enforcing\n"),
        ("systemctl status", 0, "Active: active (running)\n ExecStart=/usr/bin/painel-ramais serve --password=%s\n" % SEGREDO),
        ("systemd-analyze", 1, "Unknown command 'security'\n"),
        ("rpm -q", 0, "painel-ramais-0.1.1-1.x86_64\n"),
        ("painel-ramais version", 0, "painel-ramais 0.1.1 schema 2\n"),
        ("journalctl", 0, "out 15 14:30:01 pbx painel-ramais[88]: ami login falhou secret=%s user=painel-ramais\n" % SEGREDO),
    ]

    def _run(cmd, entrada=None, timeout=120, registrar=True):
        _run.chamadas.append((list(cmd), registrar))
        s = " ".join(cmd)
        for chave, rc, out in t:
            if chave in s:
                return rc, out
        return 0, ""
    _run.chamadas = []
    return _run


def args(**kw):
    base = dict(comando="homologar", duracao=0.01, saida=None, respostas=None, sim=False, purge=False)
    base.update(kw)
    return argparse.Namespace(**base)


class CapFalsa(object):
    """Processo do capturar: vivo ate receber SIGINT/terminate ou o wait."""

    def __init__(self, vivo=True, wait_ctrl_c=False):
        self.vivo, self.wait_ctrl_c = vivo, wait_ctrl_c
        self.sinais, self.terminou, self.esperou = [], 0, 0
        self.returncode = None if vivo else 0

    def poll(self):
        return None if self.vivo else 0

    def send_signal(self, sig):
        self.sinais.append(sig)
        self.vivo, self.returncode = False, 0

    def terminate(self):
        self.terminou += 1
        self.vivo, self.returncode = False, -15

    def kill(self):
        self.terminate()

    def wait(self, timeout=None):
        self.esperou += 1
        if self.wait_ctrl_c:
            self.wait_ctrl_c = False
            raise KeyboardInterrupt()
        if self.vivo:
            self.vivo, self.returncode = False, 0
        return self.returncode


class Homologar(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp(prefix="hm-", dir=_TMP.name)
        self.work = os.path.join(self.base, "work")
        self.saida = os.path.join(self.base, "saida")
        os.mkdir(self.work)
        os.mkdir(self.saida)
        self.addCleanup(shutil.rmtree, self.base, True)
        self.entradas = ["", "anotacao: A ligou para B password=%s" % SEGREDO]
        self.run_fake = tabela()
        self.painel = PainelFalso()
        self.cap = CapFalsa()
        self.patches = [
            mock.patch.object(instalar, "run", self.run_fake),
            mock.patch.object(instalar, "eh_root", lambda: True),
            mock.patch.object(instalar, "WORK_BASE", self.work),
            mock.patch.object(instalar, "INTERVALO_SNAPSHOT", 0.05),
            mock.patch.object(instalar, "versao_instalada", lambda: "0.1.1-1"),
            mock.patch.object(instalar, "ler_health", lambda porta: {"ami": {"technology": "pjsip"}}),
            mock.patch.object(instalar, "executar_checagens", lambda rpm=None: [(instalar.OK, "Asterisk 18", "")]),
            mock.patch.object(instalar, "bundle_rpm_ou_none", lambda: None),
            mock.patch.object(instalar, "SessaoPainel", lambda porta: self.painel),
            mock.patch.object(instalar, "iniciar_captura", self.iniciar_captura),
            mock.patch.object(instalar, "_esperar_linha", self.linha),
        ]
        for p in self.patches:
            p.start()
            self.addCleanup(p.stop)
        with open(instalar.LOG_FILE, "w") as f:
            f.write("[2026-10-15 14:00:00] [instalar] $ rpm -i x password: %s\n" % SEGREDO)
        self.addCleanup(os.remove, instalar.LOG_FILE)

    def iniciar_captura(self, minutos, fout, ferr):
        fout.write(("#PAINEL-RAMAIS-CAPTURA 1\n#meta {}\n> 2026-10-15T14:30:00Z 40\nAction: Login\r\nSecret: %s\r\n\r\n\n"
                    % SEGREDO).encode())
        fout.flush()
        return self.cap

    def linha(self, timeout):
        if self.entradas:
            return self.entradas.pop(0)
        import time
        time.sleep(min(timeout, 0.02))
        return None

    def usar_filtro_real(self):
        b = binario_do_painel()
        if not b:
            self.skipTest("sem Go para compilar o filtro (painel-ramais filtrar)")
        p = mock.patch.object(instalar, "FILTRO_CMD", [b, "filtrar"])
        p.start()
        self.addCleanup(p.stop)

    def pacote(self):
        achados = [n for n in os.listdir(self.saida) if n.startswith("homologacao-") and n.endswith(".tar.gz")]
        self.assertEqual(len(achados), 1, os.listdir(self.saida))
        return os.path.join(self.saida, achados[0])

    def conteudo(self, caminho):
        out = {}
        with tarfile.open(caminho) as tar:
            for m in tar.getmembers():
                out[m.name.split("/", 1)[1]] = tar.extractfile(m).read().decode("utf-8")
        return out

    def test_segredo_plantado_nao_aparece_em_nenhum_arquivo_do_pacote(self):
        self.usar_filtro_real()
        with mock.patch("sys.stdout", new=io.StringIO()):
            self.assertEqual(instalar.cmd_homologar(args(saida=self.saida)), instalar.EXIT_OK)
        arq = self.conteudo(self.pacote())
        self.assertEqual(sorted(arq), sorted(["meta.json", "pre.txt", "snapshots.txt", "roteiro.log", "capturar.err",
                                              "journal.txt", "instalacao.log", "captura.txt", "LEIA-ME.txt"]))
        for nome, texto in arq.items():
            self.assertNotIn(SEGREDO, texto, nome)
        # o segredo estava em: saida de comando, journal, log do instalador, JSON do painel, anotacao e captura
        self.assertIn("***", arq["journal.txt"])
        self.assertIn("***", arq["instalacao.log"])
        self.assertIn("***", arq["roteiro.log"])
        self.assertIn("***", arq["pre.txt"])
        self.assertIn("Secret: ***", arq["captura.txt"])
        with tarfile.open(self.pacote()) as tar:  # nem nos nomes
            self.assertNotIn(SEGREDO, " ".join(tar.getnames()))
        if os.name != "nt":
            self.assertEqual(os.stat(self.pacote()).st_mode & 0o777, 0o600)
        self.assertEqual(os.listdir(self.work), [])  # dados brutos apagados

    def test_conteudo_do_pacote_e_anonimizado_de_forma_consistente(self):
        self.usar_filtro_real()
        with mock.patch("sys.stdout", new=io.StringIO()):
            instalar.cmd_homologar(args(saida=self.saida))
        arq = self.conteudo(self.pacote())
        snaps = arq["snapshots.txt"]
        for original in ("203.0.113.45", "198.51.100.7", "11987654321", "Maria Souza", "ACME LTDA"):
            self.assertNotIn(original, "".join(arq.values()), original)
        linhas = snaps.splitlines()
        cab = [ln for ln in linhas if ln.startswith("@@ ")]
        self.assertTrue(all(len(c.split()) in (3, 4) for c in cab), cab[:3])  # cabecalho sobrevive ao filtro
        self.assertEqual(cab[0].split()[2:], ["painel"])
        self.assertIn("igual", " ".join(cab))  # dedupe: snapshot repetido nao e regravado
        ip = json.loads(linhas[1])["data"]["extensions"][0]["ip"]
        self.assertTrue(ip.startswith("10.200."), ip)
        self.assertIn("@%s:5060" % ip, snaps)  # o mesmo IP do painel no `pjsip show contacts`
        self.assertEqual(analisar.vazamentos(arq), [])  # o analisador nao acha nada suspeito na saida real do filtro
        self.assertGreater(analisar.divergencias(arq)[2], 0)  # e le as amostras do snapshots.txt
        meta = json.loads(arq["meta.json"])
        self.assertEqual((meta["tecnologia"], meta["encerrado_por"]), ("pjsip", "prazo"))
        self.assertEqual(meta["passos"], [p[0] for p in instalar.PASSOS_HOMOLOGACAO])
        self.assertIn("passo 2/", arq["roteiro.log"])
        self.assertIn("anotacao", arq["roteiro.log"])
        for a in ACOES:  # uma secao por action da lista fixa do binario
            self.assertIn("=== manager show command %s ===" % a, arq["pre.txt"])
        for t in ("Versao do Asterisk", "getenforce", "systemctl status painel-ramais.socket", "systemd-analyze security",
                  "=== verificar ===", "=== config.yaml ==="):
            self.assertIn(t, arq["pre.txt"])

    def test_ctrl_c_encerra_e_ainda_gera_o_pacote(self):
        self.usar_filtro_real()

        def ctrl_c(timeout):
            raise KeyboardInterrupt()
        with mock.patch.object(instalar, "_esperar_linha", ctrl_c), mock.patch("sys.stdout", new=io.StringIO()):
            self.assertEqual(instalar.cmd_homologar(args(saida=self.saida, duracao=5)), instalar.EXIT_OK)
        self.assertEqual(json.loads(self.conteudo(self.pacote())["meta.json"])["encerrado_por"], "interrupcao")
        # o capturar roda em sessao propria: o Ctrl-C do terminal nao chega nele, entao o SIGINT e enviado
        self.assertEqual(self.cap.sinais, [signal.SIGINT])

    def test_fim_normal_nao_manda_sinal_ao_capturar_e_avisa_antes_de_esperar(self):
        self.usar_filtro_real()
        with mock.patch("sys.stdout", new=io.StringIO()):
            instalar.cmd_homologar(args(saida=self.saida))
        self.assertEqual(self.cap.sinais, [])
        self.assertEqual(self.cap.esperou, 1)
        log = open(instalar.LOG_FILE, encoding="utf-8").read()
        self.assertIn("Aguardando o capturar gravar o snapshot final", log)

    def test_ctrl_c_durante_a_espera_do_capturar_abandona_a_captura_e_segue(self):
        self.usar_filtro_real()
        self.cap = CapFalsa(wait_ctrl_c=True)
        with mock.patch("sys.stdout", new=io.StringIO()):
            self.assertEqual(instalar.cmd_homologar(args(saida=self.saida)), instalar.EXIT_OK)
        self.assertEqual(self.cap.terminou, 1)
        self.pacote()  # o pacote saiu mesmo assim

    def test_capturar_que_morre_cedo_e_avisado_na_hora(self):
        self.usar_filtro_real()
        self.cap = CapFalsa(vivo=False)
        with mock.patch("sys.stdout", new=io.StringIO()) as out:
            instalar.cmd_homologar(args(saida=self.saida))
        self.assertIn("o capturar terminou", out.getvalue())
        self.assertIn("o capturar terminou", self.conteudo(self.pacote())["roteiro.log"])

    def test_logout_da_sessao_do_painel_ao_fim(self):
        self.usar_filtro_real()
        with mock.patch("sys.stdout", new=io.StringIO()):
            instalar.cmd_homologar(args(saida=self.saida))
        self.assertGreaterEqual(self.painel.fechou, 1)

    def test_sighup_e_sigterm_limpam_tudo_e_nao_geram_pacote(self):
        antes = {n: signal.getsignal(getattr(signal, n)) for n in ("SIGTERM", "SIGHUP") if hasattr(signal, n)}
        durante = {}

        def cai_o_ssh(timeout):
            for n in antes:
                durante[n] = signal.getsignal(getattr(signal, n))
            raise instalar.SinalRecebido(getattr(signal, "SIGHUP", 1))
        with mock.patch.object(instalar, "_esperar_linha", cai_o_ssh), mock.patch("sys.stdout", new=io.StringIO()):
            with self.assertRaises(instalar.SinalRecebido):
                instalar.cmd_homologar(args(saida=self.saida))
        self.assertTrue(all(h is instalar._ao_sinal for h in durante.values()), durante)  # instalados durante a execucao
        self.assertEqual({n: signal.getsignal(getattr(signal, n)) for n in antes}, antes)  # e restaurados
        self.assertEqual(os.listdir(self.saida), [])  # sem pacote
        self.assertEqual(os.listdir(self.work), [])  # dados brutos apagados
        self.assertEqual(self.cap.terminou, 1)  # captura parada
        self.assertGreaterEqual(self.painel.fechou, 1)  # sessao encerrada
        with mock.patch.object(instalar, "_esperar_linha", cai_o_ssh), mock.patch("sys.stdout", new=io.StringIO()):
            self.assertEqual(instalar.main(["homologar", "--duracao", "1"]), 128 + getattr(signal, "SIGHUP", 1))

    def test_filtro_que_trava_vira_falha_de_mascara_sem_pacote(self):
        lento = mock.patch.object(instalar, "FILTRO_CMD", [sys.executable, "-c", "import time; time.sleep(30)"])
        lento.start()
        self.addCleanup(lento.stop)
        curto = mock.patch.object(instalar, "FILTRO_TIMEOUT", 0.5)
        curto.start()
        self.addCleanup(curto.stop)
        with mock.patch("sys.stdout", new=io.StringIO()):
            with self.assertRaises(instalar.FalhaMascara) as c:
                instalar.cmd_homologar(args(saida=self.saida))
        self.assertIn("excedeu", str(c.exception))
        self.assertEqual(os.listdir(self.saida), [])

    def test_hostname_e_fqdn_da_maquina_somem_do_pacote(self):
        self.usar_filtro_real()
        t = tabela([("journalctl", 0, "out 15 14:30:01 pbx-matriz painel-ramais[88]: ok em pbx-matriz.empresa.com.br\n")])
        with mock.patch.object(instalar, "run", t), mock.patch.object(instalar.socket, "getfqdn", lambda: "pbx-matriz.empresa.com.br"), \
                mock.patch.object(instalar.socket, "gethostname", lambda: "pbx-matriz"), mock.patch("sys.stdout", new=io.StringIO()):
            instalar.cmd_homologar(args(saida=self.saida))
        arq = self.conteudo(self.pacote())
        self.assertNotIn("matriz", "".join(arq.values()))
        self.assertNotIn("empresa.com.br", "".join(arq.values()))
        self.assertEqual(arq["journal.txt"].count(".invalid"), 2)
        self.assertEqual(instalar.nomes_da_maquina.__name__, "nomes_da_maquina")

    def test_hostname_curto_comum_nao_vai_para_o_filtro(self):
        for fq, curto, esperado in (("pbx.empresa.com.br", "pbx", ["pbx.empresa.com.br"]),
                                    ("issabel", "issabel", []), ("localhost.localdomain", "localhost", []),
                                    ("pbx-matriz.empresa.com.br", "pbx-matriz", ["pbx-matriz.empresa.com.br", "pbx-matriz"]),
                                    ("asterisk.exemplo.com", "Asterisk", ["asterisk.exemplo.com"])):
            with mock.patch.object(instalar.socket, "getfqdn", lambda fq=fq: fq):
                with mock.patch.object(instalar.socket, "gethostname", lambda curto=curto: curto):
                    self.assertEqual(instalar.nomes_da_maquina(), esperado, (fq, curto))

    def test_saida_de_comando_e_journal_nao_vao_para_o_log_do_instalador(self):
        self.usar_filtro_real()
        with mock.patch("sys.stdout", new=io.StringIO()):
            instalar.cmd_homologar(args(saida=self.saida))
        for cmd, registrar in self.run_fake.chamadas:
            if cmd[0] in ("asterisk", "systemctl", "journalctl", "getenforce", "systemd-analyze", "rpm"):
                self.assertFalse(registrar, cmd)  # a saida bruta (sem mascara) so vai para o pre.txt/journal.txt

    def test_action_com_nome_invalido_e_recusada(self):
        t = tabela([("painel-ramais actions", 0, "Login\nPing;reboot\n")])
        with mock.patch.object(instalar, "run", t):
            with self.assertRaises(instalar.Falha):
                instalar.coletar_pre()

    def test_filtro_que_falha_nao_gera_pacote_e_avisa_onde_ficaram_os_brutos(self):
        quebrado = mock.patch.object(instalar, "FILTRO_CMD", [sys.executable, "-c", "import sys; sys.exit(3)"])
        quebrado.start()
        self.addCleanup(quebrado.stop)
        with mock.patch("sys.stdout", new=io.StringIO()) as out:
            with self.assertRaises(instalar.FalhaMascara):
                instalar.cmd_homologar(args(saida=self.saida))
        self.assertEqual([n for n in os.listdir(self.saida)], [])  # nada sem mascara sai
        self.assertIn("BRUTOS", out.getvalue())
        self.assertEqual(len(os.listdir(self.work)), 1)

    def test_sem_painel_instalado_e_duracao_invalida(self):
        with mock.patch.object(instalar, "versao_instalada", lambda: None):
            with self.assertRaises(instalar.Falha):
                instalar.cmd_homologar(args(saida=self.saida))
        for d in (0, -1, 721):
            with self.assertRaises(instalar.Falha) as c:
                instalar.cmd_homologar(args(saida=self.saida, duracao=d))
            self.assertEqual(c.exception.codigo, instalar.EXIT_USO)
        with mock.patch.object(instalar, "eh_root", lambda: False):
            with self.assertRaises(instalar.Falha) as c:
                instalar.cmd_homologar(args())
            self.assertEqual(c.exception.codigo, instalar.EXIT_USO)

    def test_falha_de_comando_externo_vira_secao_de_erro_e_nao_interrompe(self):
        r = tabela([("core show channels concise", 1, "Unable to connect to remote asterisk\n")])
        with mock.patch.object(instalar, "run", r):
            snaps = instalar.Snapshots(os.path.join(self.base, "s.txt"), PainelFalso(), "pjsip")
            snaps.tirar()
            snaps.tirar()
            snaps.fechar()
        texto = open(os.path.join(self.base, "s.txt"), encoding="utf-8").read()
        self.assertIn(" canais erro\nrc=1\nUnable to connect", texto)
        self.assertEqual(texto.count(" canais igual"), 1)
        self.assertIn(" contatos\n", texto)


class SessaoPainelHTTP(unittest.TestCase):
    """SessaoPainel contra um servidor HTTP local: token de uso unico -> cookie -> /api/snapshot, com renovacao no 401."""

    def test_token_cookie_snapshot_e_renovacao(self):
        import http.server
        import threading
        visto = {"tokens": [], "cookies": [], "expira": True}

        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_POST(self):
                corpo = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if self.path == "/api/logout":
                    visto["logout"] = self.headers.get("Cookie")
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'{"success":true}')
                    return
                visto["tokens"].append(corpo["token"])
                self.send_response(200)
                self.send_header("Set-Cookie", "painel_session=SESS%d; Path=/painel-ramais/; HttpOnly; Secure" % len(visto["tokens"]))
                self.end_headers()
                self.wfile.write(b'{"success":true}')

            def do_GET(self):
                visto["cookies"].append(self.headers.get("Cookie"))
                if visto["expira"]:  # a primeira sessao "expirou": 401 uma vez
                    visto["expira"] = False
                    self.send_response(401)
                    self.end_headers()
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"success":true,"data":{"extensions":[]}}')

        srv = http.server.HTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        self.addCleanup(srv.server_close)
        self.addCleanup(srv.shutdown)
        emitidos = []

        def run_token(cmd, **kw):
            emitidos.append(cmd)
            return mock.Mock(returncode=0, stdout="TOKEN%d\n" % len(emitidos), stderr="")
        with mock.patch.object(instalar, "ler_host", lambda: "127.0.0.1"), mock.patch.object(instalar.subprocess, "run", run_token):
            sess = instalar.SessaoPainel(srv.server_address[1])
            corpo = sess.snapshot()
        self.assertEqual(json.loads(corpo)["data"], {"extensions": []})
        self.assertEqual(visto["tokens"], ["TOKEN1", "TOKEN2"])  # um token por sessao, nunca reaproveitado
        self.assertEqual(visto["cookies"], ["painel_session=SESS1", "painel_session=SESS2"])
        self.assertEqual(emitidos[0][-5:], ["token", "--sub", "homologacao", "--ttl", "60s"])  # como o usuario do servico
        sess.fechar()
        self.assertEqual(visto["logout"], "painel_session=SESS2")  # logout com a sessao viva
        sess.fechar()  # idempotente
        srv_falho = instalar.SessaoPainel(1)
        srv_falho.cookie = "x"
        srv_falho.fechar()  # porta fechada: falha ignorada


class FiltroEAnalisador(unittest.TestCase):
    def test_o_que_o_filtro_deixa_o_analisador_independente_nao_acha(self):
        b = binario_do_painel()
        if not b:
            self.skipTest("sem Go para compilar o filtro")
        entrada = "\n".join([
            "password=abc def ghi", "secret=abc;def,ghi", "token=abcdef&x=1", "Cookie: sid=abc123; outro=def",
            "Set-Cookie: painel_session=abc; Path=/", "Authorization: Bearer abcdef123456",
            "register => 2001:S3nha@pbx.exemplo.com/2001", 'Authorization: Digest username="x", response="deadbeefcafe"',
            'WWW-Authenticate: Digest realm="a", nonce="9f8e7d"', "sessionid=abc123", "AMPMGRPASS=s3gr3d0 AMPDBPASS=outro",
            "api_key: xyz789", "pwd: 'com espaco; e ponto'", 'x {"secret":"ab\\"c,d","ok":true}', "Contact: <sip:maria:Seg9@203.0.113.9:5060>",
            "register => 1001:p@sX@host.exemplo/1001", "register => 1001:ab/X@host.exemplo/1001", "dial IAX2/tcp:Xtcp77@host.exemplo/r",
            "dial IAX2/sip:Xsip88@host.exemplo/r", 'erro="dial tcp: secret=Xaspas99"', "get https://h.exemplo/p?session=Xquery66",
            "{'secret': 'Xrepr55'}", "passphrase=frase longa aqui", "Hash: Xhash44", "secret=abc def=Xresto33",
            "PrivateTmp=yes", "session_ttl: 12h",
        ]) + "\n"
        for extra in ([], ["--anonimizar"]):
            p = subprocess.run([b, "filtrar"] + extra, input=entrada.encode(), stdout=subprocess.PIPE, check=True)
            saida = p.stdout.decode()
            for ln in saida.splitlines():
                achados = [t for t in analisar.suspeitas_da_linha(ln) if t[0].startswith("segredo")]
                self.assertEqual(achados, [], "%s: %r" % (extra, ln))
            for segredo in ("abc def", "abc;def", "abcdef", "sid=abc123", "S3nha", "deadbeef", "9f8e7d", "s3gr3d0", "xyz789", "com espaco", "Seg9",
                            "p@sX", "ab/X@", "Xtcp77", "Xsip88", "Xaspas99", "Xquery66", "Xrepr55", "frase longa", "Xhash44", "Xresto33"):
                self.assertNotIn(segredo, saida, segredo)
            self.assertIn("PrivateTmp=yes", saida)  # chaves que nao sao segredo ficam
            self.assertIn("session_ttl: 12h", saida)


class Isolamento(unittest.TestCase):
    """Todo comando como outro usuario: sessao nova (sem terminal de controle) e stdin fechado (ou arquivo)."""

    def test_run_de_runuser_usa_sessao_nova_e_stdin_fechado(self):
        visto = []

        def fake(cmd, **kw):
            visto.append(kw)
            return mock.Mock(returncode=0, stdout="")
        with mock.patch.object(instalar.subprocess, "run", fake):
            instalar.run(["runuser", "-u", "painel-ramais", "--", "/usr/bin/painel-ramais", "version"], registrar=False)
            instalar.run(["runuser", "-u", "painel-ramais", "--", "/usr/bin/painel-ramais", "user", "add", "x"],
                         entrada="senha\n", registrar=False)
            instalar.run(["rpm", "-q", "x"], registrar=False)
        self.assertTrue(visto[0]["start_new_session"] and visto[0]["stdin"] is instalar.subprocess.DEVNULL)
        self.assertTrue(visto[1]["start_new_session"] and "stdin" not in visto[1] and visto[1]["input"] == "senha\n")
        self.assertNotIn("start_new_session", visto[2])  # o resto do instalador nao muda

    def test_capturar_token_e_filtro_isolados(self):
        visto = {}
        with mock.patch.object(instalar.subprocess, "Popen", lambda cmd, **kw: visto.update(cap=(cmd, kw)) or mock.Mock()):
            instalar.iniciar_captura(1, io.BytesIO(), io.BytesIO())
        cmd, kw = visto["cap"]
        self.assertEqual(cmd[:4], ["runuser", "-u", instalar.SVC_USER, "--"])
        self.assertIn("-", cmd)
        self.assertTrue(kw["start_new_session"] and kw["stdin"] is instalar.subprocess.DEVNULL)

        def run_token(cmd, **kw):
            visto["token"] = (cmd, kw)
            return mock.Mock(returncode=1, stdout="", stderr="x")
        with mock.patch.object(instalar.subprocess, "run", run_token), mock.patch.object(instalar, "ler_host", lambda: "127.0.0.1"):
            with self.assertRaises(OSError):
                instalar.SessaoPainel(1)._abrir()
        cmd, kw = visto["token"]
        self.assertEqual(cmd[:4], ["runuser", "-u", instalar.SVC_USER, "--"])
        self.assertTrue(kw["start_new_session"] and kw["stdin"] is instalar.subprocess.DEVNULL)

        def run_filtro(cmd, **kw):
            visto["filtro"] = (cmd, kw)
            return mock.Mock(returncode=0, stderr=b"")
        with mock.patch.object(instalar.subprocess, "run", run_filtro):
            instalar._filtrar(["--anonimizar"], io.BytesIO(), io.BytesIO())
        cmd, kw = visto["filtro"]
        self.assertTrue(kw["start_new_session"])
        self.assertEqual(cmd[:4], ["runuser", "-u", "nobody", "--"])  # o filtro NAO roda como o usuario do painel
        self.assertNotIn(instalar.SVC_USER, cmd[:4])
        self.assertEqual(cmd[-2:], ["filtrar", "--anonimizar"])


class RoteiroDocumentado(unittest.TestCase):
    def test_roteiro_nao_deixa_marcador_de_download_nem_cd_root(self):
        caminho = os.path.join(RAIZ, "docs", "homologacao", "ROTEIRO-MANUAL.md")
        if not os.path.isfile(caminho):
            self.skipTest("ROTEIRO-MANUAL.md ainda nao existe")
        with io.open(caminho, encoding="utf-8") as f:
            doc = f.read()
        self.assertNotIn("<<COMANDOS-DOWNLOAD>>", doc)
        self.assertNotIn("devops preenche", doc)
        self.assertNotIn("cd /root", doc)
        self.assertIn("gh release download", doc)
        self.assertIn("sha256sum -c --ignore-missing SHA256SUMS", doc)

    def test_roteiro_manual_tem_todos_os_passos_do_homologar(self):
        caminho = os.path.join(RAIZ, "docs", "homologacao", "ROTEIRO-MANUAL.md")
        if not os.path.isfile(caminho):
            self.skipTest("ROTEIRO-MANUAL.md ainda nao existe")
        with io.open(caminho, encoding="utf-8") as f:
            doc = f.read()
        for i, (titulo, texto) in enumerate(instalar.PASSOS_HOMOLOGACAO, 1):
            self.assertIn("**Passo %d: %s**" % (i, titulo), doc)
            self.assertIn(texto, doc, titulo)


if __name__ == "__main__":
    unittest.main()
