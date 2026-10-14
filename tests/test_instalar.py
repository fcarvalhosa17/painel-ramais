import argparse
import io
import os
import sys
import tempfile
import unittest
import urllib.error
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import instalar  # noqa: E402

RAIZ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
_LOGTMP = tempfile.TemporaryDirectory()


def setUpModule():
    # nunca gravar no /var/log real durante os testes
    instalar.LOG_DIR = _LOGTMP.name
    instalar.LOG_FILE = _LOGTMP.name + "/instalacao.log"


def tearDownModule():
    _LOGTMP.cleanup()


def fake_run(table):
    """table: lista de (substring_do_comando, rc, saida); a primeira que casar vence. Registra chamadas."""
    chamadas = []

    def _run(cmd, entrada=None, timeout=120):
        chamadas.append((list(cmd), entrada))
        s = " ".join(cmd)
        for chave, rc, out in table:
            if chave in s:
                return rc, out
        return 0, ""
    _run.chamadas = chamadas
    return _run


def args(**kw):
    base = dict(respostas=None, sim=False, purge=False, comando="instalar")
    base.update(kw)
    return argparse.Namespace(**base)


class Cabecalho(unittest.TestCase):
    def test_poliglota_e_lf(self):
        with open(os.path.join(RAIZ, "instalar.py"), "rb") as f:
            dados = f.read()
        self.assertNotIn(b"\r", dados)
        l1, l2 = dados.split(b"\n")[:2]
        self.assertEqual(l1, b"#!/bin/sh")
        self.assertIn(b"python3", l2)
        self.assertIn(b"/usr/libexec/platform-python", l2)


class Verificacoes(unittest.TestCase):
    def test_asterisk_versao_ok(self):
        with mock.patch.object(instalar, "run", fake_run([("core show version", 0, "Asterisk 18.20.0 built by x")])):
            self.assertEqual(instalar.c_asterisk()[0], instalar.OK)

    def test_asterisk_abaixo_do_minimo_e_erro(self):
        with mock.patch.object(instalar, "run", fake_run([("core show version", 0, "Asterisk 11.25.3 built")])):
            self.assertEqual(instalar.c_asterisk()[0], instalar.ERRO)

    def test_asterisk_parado_e_erro(self):
        with mock.patch.object(instalar, "run", fake_run([("core show version", 1, "Unable to connect")])):
            self.assertEqual(instalar.c_asterisk()[0], instalar.ERRO)

    def test_wstunnel_ausente_e_erro(self):
        t = [("is-active httpd", 0, "active"), ("httpd -M", 0, " proxy_module (shared)")]
        with mock.patch.object(instalar, "run", fake_run(t)):
            res = instalar.c_httpd()
        self.assertEqual([r[0] for r in res], [instalar.OK, instalar.ERRO])
        self.assertIn("wstunnel", res[1][1])

    def test_wstunnel_presente(self):
        t = [("is-active httpd", 0, ""), ("httpd -M", 0, " proxy_wstunnel_module (shared)")]
        with mock.patch.object(instalar, "run", fake_run(t)):
            self.assertTrue(all(r[0] == instalar.OK for r in instalar.c_httpd()))

    def test_httpd_parado(self):
        with mock.patch.object(instalar, "run", fake_run([("is-active httpd", 3, "inactive")])):
            self.assertEqual(instalar.c_httpd()[0][0], instalar.ERRO)

    def test_tecnologia_pjsip(self):
        eps = " Endpoint:  2000/2000   Not in use\n Endpoint:  2001/2001   Not in use\n Endpoint:  tronco1   Not in use\n"
        t = [("module show like", 0, "chan_pjsip.so  1"), ("pjsip show endpoints", 0, eps)]
        with mock.patch.object(instalar, "run", fake_run(t)):
            r, tec = instalar.c_tecnologia()
        self.assertEqual((r[1], tec), ("Tecnologia PJSIP", "pjsip"))
        self.assertIn("2 ramais, 1 troncos", r[2])

    def test_tecnologia_nenhuma_e_erro(self):
        with mock.patch.object(instalar, "run", fake_run([])):
            self.assertEqual(instalar.c_tecnologia()[0][0], instalar.ERRO)

    def test_tecnologia_ambas_e_aviso(self):
        t = [("module show like", 0, "chan_sip.so\nchan_pjsip.so")]
        with mock.patch.object(instalar, "run", fake_run(t)):
            self.assertEqual(instalar.c_tecnologia()[0][0], instalar.AVISO)

    def test_manager_include(self):
        with tempfile.TemporaryDirectory() as d:
            ok = os.path.join(d, "ok.conf")
            ruim = os.path.join(d, "ruim.conf")
            with open(ok, "w") as f:
                f.write('[general]\n#include "manager_custom.conf"\n')
            with open(ruim, "w") as f:
                f.write("[general]\n; #include manager_custom.conf\n")
            with mock.patch.object(instalar, "MANAGER_CONF", ok):
                self.assertEqual(instalar.c_manager_include()[0], instalar.OK)
            with mock.patch.object(instalar, "MANAGER_CONF", ruim):
                self.assertEqual(instalar.c_manager_include()[0], instalar.ERRO)

    def test_conflito_ami(self):
        with tempfile.TemporaryDirectory() as d:
            ok = os.path.join(d, "ok.conf")
            ruim = os.path.join(d, "ruim.conf")
            with open(ok, "w") as f:
                f.write("; BEGIN painel-ramais\n[painel-ramais]\nsecret=x\n; END painel-ramais\n")
            with open(ruim, "w") as f:
                f.write("[painel-ramais]\nsecret=manual\n")
            with mock.patch.object(instalar, "MANAGER_CUSTOM", ok):
                self.assertEqual(instalar.c_conflito_ami()[0], instalar.OK)
            with mock.patch.object(instalar, "MANAGER_CUSTOM", ruim):
                self.assertEqual(instalar.c_conflito_ami()[0], instalar.ERRO)

    def test_issabel_indeterminado_nunca_e_erro(self):
        with mock.patch.object(instalar, "run", fake_run([("rpm -qa", 0, "")])):
            self.assertEqual(instalar.c_issabel()[0], instalar.AVISO)
        with mock.patch.object(instalar, "run", fake_run([("rpm -qa", 0, "issabel-pbx 4.0.0\n")])):
            self.assertEqual(instalar.c_issabel()[0], instalar.AVISO)

    def test_issabel5_ok(self):
        with mock.patch.object(instalar, "run", fake_run([("rpm -qa", 0, "issabel-pbx 5.0.0\n")])):
            self.assertEqual(instalar.c_issabel()[0], instalar.OK)

    def test_selinux_enforcing_com_booleano_off_e_so_aviso(self):
        t = [("getenforce", 0, "Enforcing\n"), ("getsebool", 0, "httpd_can_network_connect --> off")]
        with mock.patch.object(instalar, "run", fake_run(t)):
            self.assertEqual(instalar.c_selinux()[0], instalar.AVISO)

    def test_porta_ocupada_por_outro_e_pelo_proprio(self):
        outro = 'LISTEN 0 128 127.0.0.1:8790 0.0.0.0:* users:(("nginx",pid=1,fd=3))'
        proprio = 'LISTEN 0 128 127.0.0.1:8790 0.0.0.0:* users:(("painel-ramais",pid=1,fd=3))'
        with mock.patch.object(instalar, "ler_porta", return_value=8790):
            with mock.patch.object(instalar, "run", fake_run([("ss", 0, outro)])):
                self.assertEqual(instalar.c_porta()[0], instalar.ERRO)
            with mock.patch.object(instalar, "run", fake_run([("ss", 0, proprio)])):
                self.assertEqual(instalar.c_porta()[0], instalar.OK)
            with mock.patch.object(instalar, "run", fake_run([("ss", 0, "")])):
                self.assertEqual(instalar.c_porta()[0], instalar.OK)

    def test_porta_mantida_pelo_systemd_socket_e_ok(self):
        systemd = 'LISTEN 0 128 127.0.0.1:8790 0.0.0.0:* users:(("systemd",pid=1,fd=40))'
        with mock.patch.object(instalar, "ler_porta", return_value=8790):
            t = [("ss", 0, systemd), ("is-active painel-ramais.socket", 0, "active")]
            with mock.patch.object(instalar, "run", fake_run(t)):
                self.assertEqual(instalar.c_porta()[0], instalar.OK)
            t = [("ss", 0, systemd), ("is-active painel-ramais.socket", 3, "inactive")]
            with mock.patch.object(instalar, "run", fake_run(t)):
                self.assertEqual(instalar.c_porta()[0], instalar.ERRO)

    def test_actions_ausente_obrigatoria_e_erro(self):
        def priv(a):
            return None if a == "PJSIPShowEndpoints" else "system,reporting,all"
        with mock.patch.object(instalar, "privilegio", side_effect=priv):
            self.assertEqual(instalar.c_actions("pjsip")[0][0], instalar.ERRO)

    def test_actions_sem_contacts_e_aviso(self):
        def priv(a):
            return None if a == "PJSIPShowContacts" else "system"
        with mock.patch.object(instalar, "privilegio", side_effect=priv):
            self.assertEqual(instalar.c_actions("pjsip")[0][0], instalar.AVISO)

    def test_versao_instalada_maior_que_bundle_e_erro(self):
        with mock.patch.object(instalar, "run", fake_run([("rpm -q", 0, "0.2.0-1")])):
            self.assertEqual(instalar.c_painel("/x/painel-ramais-0.1.0-1.x86_64.rpm")[0], instalar.ERRO)
            self.assertEqual(instalar.c_painel("/x/painel-ramais-0.2.0-1.x86_64.rpm")[0], instalar.OK)

    def test_ler_porta(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "config.yaml")
            with open(p, "w") as f:
                f.write('listen: "127.0.0.1:9100"\n')
            with mock.patch.object(instalar, "CONF_FILE", p):
                self.assertEqual(instalar.ler_porta(), 9100)
        with mock.patch.object(instalar, "CONF_FILE", "/nao/existe"):
            self.assertEqual(instalar.ler_porta(), 8790)
            self.assertEqual(instalar.DEFAULT_PORT, 8790)


class Respostas(unittest.TestCase):
    def _ini(self, d, senha_modo=0o600, tec="auto"):
        senha = os.path.join(d, "senha")
        with open(senha, "w") as f:
            f.write("segredo-longo-123\n")
        os.chmod(senha, senha_modo)
        ini = os.path.join(d, "r.ini")
        with open(ini, "w") as f:
            f.write("[painel]\ntecnologia = %s\nadmin_usuario = admin\nadmin_senha_arquivo = %s\n" % (tec, senha))
        return ini

    @unittest.skipIf(os.name == "nt", "modo POSIX")
    def test_senha_permissao_frouxa_recusada(self):
        with tempfile.TemporaryDirectory() as d:
            with self.assertRaises(instalar.Falha):
                instalar.coletar_respostas(args(respostas=self._ini(d, 0o644)))

    @unittest.skipIf(os.name == "nt" or not hasattr(os, "geteuid") or os.geteuid() != 0, "exige root em POSIX")
    def test_respostas_ok(self):
        with tempfile.TemporaryDirectory() as d:
            r = instalar.coletar_respostas(args(respostas=self._ini(d)))
        self.assertEqual((r["usuario"], r["senha"], r["tecnologia"]), ("admin", "segredo-longo-123", "auto"))

    @unittest.skipIf(os.name == "nt", "modo POSIX")
    def test_chave_desconhecida_e_codigo_3(self):
        with tempfile.TemporaryDirectory() as d:
            ini = self._ini(d)
            with open(ini, "a") as f:
                f.write("senha = x\n")
            with self.assertRaises(instalar.Falha) as cm:
                instalar.coletar_respostas(args(respostas=ini))
            self.assertEqual(cm.exception.codigo, instalar.EXIT_USO)

    def test_senha_curta_recusada(self):
        with self.assertRaises(instalar.Falha):
            instalar.validar_senha("curta")
        instalar.validar_senha("uma-senha-boa")

    def test_arquivo_inexistente(self):
        with self.assertRaises(instalar.Falha) as cm:
            instalar.coletar_respostas(args(respostas="/nao/existe.ini"))
        self.assertEqual(cm.exception.codigo, instalar.EXIT_USO)

    def test_sem_terminal_e_sem_respostas_e_codigo_3(self):
        with mock.patch.object(instalar.sys.stdin, "isatty", return_value=False):
            with self.assertRaises(instalar.Falha) as cm:
                instalar.coletar_respostas(args())
        self.assertEqual(cm.exception.codigo, instalar.EXIT_USO)


class Config(unittest.TestCase):
    def test_definir_tecnologia_idempotente(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "config.yaml")
            with open(p, "w") as f:
                f.write('listen: "127.0.0.1:8790"\ntechnology: auto\n')
            with mock.patch.object(instalar, "CONF_FILE", p):
                self.assertTrue(instalar.definir_tecnologia("pjsip"))
                self.assertFalse(instalar.definir_tecnologia("pjsip"))
            with open(p) as f:
                self.assertEqual(f.read().count("technology:"), 1)
            baks = [n for n in os.listdir(d) if n.startswith("config.yaml.bak-")]
            self.assertEqual(len(baks), 1)

    def test_exemplo_de_config_tem_porta_e_chaves_do_contrato(self):
        with open(os.path.join(RAIZ, "packaging", "config.yaml.example")) as f:
            txt = f.read()
        self.assertIn('listen: "127.0.0.1:8790"', txt)
        for chave in ("ami:", "  address:", "auth:", "  secret_file:", "history:", "  retention_days:"):
            self.assertIn(chave, txt)
        self.assertNotIn("8765", txt)


class Health(unittest.TestCase):
    def test_pronto(self):
        self.assertTrue(instalar.health_pronto({"ami": {"connected": True}, "extensions": {"total": 10}}))
        self.assertFalse(instalar.health_pronto({"ami": {"connected": False}, "extensions": {"total": 10}}))
        self.assertFalse(instalar.health_pronto({"ami": {"connected": True}, "extensions": {"total": 0}}))

    def test_esperar_health_estoura(self):
        with mock.patch.object(instalar, "ler_health", side_effect=OSError("recusado")), \
                mock.patch.object(instalar.time, "sleep"):
            self.assertIsNone(instalar.esperar_health(1, limite=0))

    def test_503_degraded_traz_corpo(self):
        corpo = b'{"success": true, "data": {"status": "degraded", "ami": {"connected": false}}}'
        err = urllib.error.HTTPError("u", 503, "x", {}, io.BytesIO(corpo))
        with mock.patch.object(instalar.urllib.request, "urlopen", side_effect=err):
            self.assertEqual(instalar.ler_health(8790)["status"], "degraded")


class Fluxos(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.rpm = os.path.join(self.tmp.name, "painel-ramais-0.2.0-1.x86_64.rpm")
        open(self.rpm, "w").close()
        for nome, valor in (("STATE_DIR", self.tmp.name + "/state"), ("CONF_DIR", self.tmp.name + "/etc"),
                            ("INSTALADOR_DIR", self.tmp.name + "/inst"),
                            ("RPM_ANTERIOR", self.tmp.name + "/inst/rpm-anterior"),
                            ("RPM_ATUAL", self.tmp.name + "/inst/rpm-instalado"),
                            ("DB_FILE", self.tmp.name + "/state/painel.db")):
            p = mock.patch.object(instalar, nome, valor)
            p.start()
            self.addCleanup(p.stop)
        # bits POSIX de dono/modo nao existem no Windows: os testes de seguranca abaixo exercitam as funcoes reais
        for nome in ("dir_do_root_seguro", "rpm_e_nosso", "verificar_sha256sums"):
            p = mock.patch.object(instalar, nome)
            p.start()
            self.addCleanup(p.stop)
        os.makedirs(self.tmp.name + "/state")
        os.makedirs(self.tmp.name + "/inst")

    def test_instalar_aborta_com_erro_sem_tocar_no_sistema(self):
        fr = fake_run([])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "verificar_ambiente", return_value=([], 1)):
            self.assertEqual(instalar.cmd_instalar(args()), instalar.EXIT_VERIFICAR)
        self.assertEqual(fr.chamadas, [])

    def test_instalar_nao_root_e_codigo_3(self):
        with mock.patch.object(instalar, "eh_root", return_value=False):
            with self.assertRaises(instalar.Falha) as cm:
                instalar.cmd_instalar(args())
        self.assertEqual(cm.exception.codigo, instalar.EXIT_USO)

    def _instalar_com(self, instalado, user_rc=0):
        fr = fake_run([("rpm -q --qf", 0 if instalado else 1, instalado or "not installed"),
                       ("user add", user_rc, ""), ("group list", 0, "Vendas\t2000-2099\n"),
                       ("version", 0, "painel-ramais 0.2.0 schema 1")])
        resp = {"tecnologia": "auto", "usuario": "admin", "senha": "senha-bem-longa", "csv": None}
        health = {"ami": {"connected": True, "technology": "pjsip"}, "extensions": {"total": 2}}
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "verificar_ambiente", return_value=([], 0)), \
                mock.patch.object(instalar, "achar_rpm", return_value=self.rpm), \
                mock.patch.object(instalar, "coletar_respostas", return_value=resp), \
                mock.patch.object(instalar, "definir_tecnologia", return_value=False), \
                mock.patch.object(instalar, "esperar_health", return_value=health), \
                mock.patch.object(instalar, "testar_apache", return_value=(True, "")), \
                mock.patch.object(instalar, "endereco_servidor", return_value="10.0.0.5"), \
                mock.patch.object(instalar, "ler_porta", return_value=8790):
            rc = instalar.cmd_instalar(args(sim=True))
        return rc, fr.chamadas

    def test_instalacao_nova_usa_rpm_i_e_senha_so_via_stdin(self):
        rc, ch = self._instalar_com(None)
        self.assertEqual(rc, 0)
        self.assertTrue(any(c[0][:2] == ["rpm", "-i"] for c in ch))
        self.assertFalse(any("senha-bem-longa" in " ".join(c[0]) for c in ch), "senha apareceu em argv")
        add = [c for c in ch if "add" in c[0]][0]
        self.assertEqual(add[1], "senha-bem-longa\n")
        self.assertEqual(add[0][:4], ["runuser", "-u", "painel-ramais", "--"])
        self.assertIn("--password-stdin", add[0])
        self.assertTrue(os.path.isfile(instalar.RPM_ATUAL + "/versao.txt"))

    def test_mesma_versao_usa_replacepkgs(self):
        rc, ch = self._instalar_com("0.2.0-1")
        self.assertTrue(any(c[0][:3] == ["rpm", "-U", "--replacepkgs"] for c in ch))

    def test_upgrade_guarda_anterior(self):
        os.makedirs(instalar.RPM_ATUAL)
        open(os.path.join(instalar.RPM_ATUAL, "painel-ramais-0.1.0-1.x86_64.rpm"), "w").close()
        rc, ch = self._instalar_com("0.1.0-1")
        self.assertTrue(any(c[0][:2] == ["rpm", "-U"] and "--replacepkgs" not in c[0] for c in ch))
        self.assertEqual(os.listdir(instalar.RPM_ANTERIOR), ["painel-ramais-0.1.0-1.x86_64.rpm"])
        self.assertIn(os.path.basename(self.rpm), os.listdir(instalar.RPM_ATUAL))

    def test_upgrade_com_rpm_u_falho_nao_vira_anterior(self):
        os.makedirs(instalar.RPM_ATUAL)
        open(os.path.join(instalar.RPM_ATUAL, "painel-ramais-0.1.0-1.x86_64.rpm"), "w").close()
        fr = fake_run([("rpm -q --qf", 0, "0.1.0-1"), ("rpm -U", 1, "erro")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "verificar_ambiente", return_value=([], 0)), \
                mock.patch.object(instalar, "achar_rpm", return_value=self.rpm), \
                mock.patch.object(instalar, "coletar_respostas", return_value={"tecnologia": "auto", "usuario": "a", "senha": "x" * 12, "csv": None}):
            with self.assertRaises(instalar.Falha):
                instalar.cmd_instalar(args(sim=True))
        self.assertFalse(os.path.exists(instalar.RPM_ANTERIOR))
        self.assertEqual(os.listdir(instalar.RPM_ATUAL), ["painel-ramais-0.1.0-1.x86_64.rpm"])

    def test_mesma_versao_release_diferente_e_upgrade(self):
        self.assertTrue(instalar._vr("0.1.0-2") > instalar._vr("0.1.0-1"))
        rc, ch = self._instalar_com("0.2.0-0")
        self.assertTrue(any(c[0][:2] == ["rpm", "-U"] and "--replacepkgs" not in c[0] for c in ch))

    def test_rollback_de_banco_schema_2_para_binario_schema_1_restaura_bak_v1(self):
        self._prepara_anterior(schema_txt=1)  # versao anterior (0.1.0) conhece schema 1; o daemon 0.1.1 migrou para 2
        fr = fake_run([("rpm -q --qf", 0, "0.1.1-1")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", return_value=2), \
                mock.patch.object(instalar, "esperar_health", return_value={"status": "ok"}), \
                mock.patch.object(instalar, "cmd_status", return_value=0):
            instalar.cmd_rollback(args(sim=True))
        cps = [c[0] for c in fr.chamadas if len(c[0]) > 4 and c[0][4] == "cp"]
        self.assertEqual(len(cps), 1)
        self.assertTrue(cps[0][-2].endswith("painel.db.bak-v1"))
        self.assertTrue(cps[0][-1].endswith("painel.db"))

    def test_schema_de_versao_txt_0_1_1(self):
        with tempfile.TemporaryDirectory() as d:
            arq = os.path.join(d, "versao.txt")
            with open(arq, "w") as f:
                f.write("painel-ramais 0.1.1 schema 2\n")
            self.assertEqual(instalar.schema_de(arq), 2)

    def test_rollback_sem_sha256_registrado_aborta_sem_tocar_em_nada(self):
        self._prepara_anterior(schema_txt=1, sha=False)
        fr = fake_run([])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", return_value=1), \
                mock.patch.object(instalar, "rpm_e_nosso", side_effect=AssertionError("nao deveria nem chegar")):
            with self.assertRaises(instalar.Falha) as cm:
                instalar.cmd_rollback(args(sim=True))
        self.assertIn("SHA-256", str(cm.exception))
        self.assertFalse(any(c[0][0] in ("rpm", "systemctl") for c in fr.chamadas))

    def _rollback_com_socket(self, existe):
        self._prepara_anterior(schema_txt=1)
        unit = os.path.join(self.tmp.name, "painel-ramais.socket")
        if existe:
            open(unit, "w").close()
        fr = fake_run([("rpm -q --qf", 0, "0.1.1-1")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", return_value=1), \
                mock.patch.object(instalar, "SOCKET_UNIT_PATH", unit), \
                mock.patch.object(instalar, "SOCKET_WANTS", os.path.join(self.tmp.name, "wants-inexistente")), \
                mock.patch.object(instalar, "esperar_health", return_value={"status": "ok"}), \
                mock.patch.object(instalar, "cmd_status", return_value=0):
            instalar.cmd_rollback(args(sim=True))
        return [" ".join(c[0]) for c in fr.chamadas]

    def test_rollback_para_versao_sem_unit_de_socket_para_e_desabilita_o_socket(self):
        cmds = self._rollback_com_socket(existe=False)
        self.assertIn("systemctl stop painel-ramais.socket", cmds)
        self.assertIn("systemctl disable painel-ramais.socket", cmds)
        self.assertNotIn("systemctl start painel-ramais.socket", cmds)
        self.assertIn("systemctl start painel-ramais.service", cmds)

    def test_rollback_para_versao_com_unit_de_socket_sobe_o_socket(self):
        cmds = self._rollback_com_socket(existe=True)
        self.assertIn("systemctl start painel-ramais.socket", cmds)
        self.assertNotIn("systemctl disable painel-ramais.socket", cmds)

    def test_preremove_tira_conf_do_apache_antes_de_parar_o_socket(self):
        with open(os.path.join(RAIZ, "packaging", "scripts", "preremove")) as f:
            txt = f.read()
        self.assertLess(txt.index("rm -f /etc/httpd/conf.d/painel-ramais.conf"), txt.index("disable --now painel-ramais.socket"))
        self.assertLess(txt.index("systemctl reload httpd"), txt.index("disable --now painel-ramais.service"))

    def test_rollback_aborta_se_esquema_do_banco_ilegivel(self):
        self._prepara_anterior(schema_txt=1)
        fr = fake_run([])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", side_effect=OSError("ilegivel")):
            with self.assertRaises(instalar.Falha):
                instalar.cmd_rollback(args(sim=True))
        self.assertFalse(any(c[0][0] in ("rpm", "systemctl") for c in fr.chamadas))
        self.assertFalse(any(len(c[0]) > 4 and c[0][4] in ("mv", "cp") for c in fr.chamadas))

    def test_usuario_existente_rc3_nao_e_falha(self):
        rc, ch = self._instalar_com(None, user_rc=3)
        self.assertEqual(rc, 0)

    def test_usuario_erro_rc1_e_falha(self):
        with self.assertRaises(instalar.Falha):
            self._instalar_com(None, user_rc=1)

    def test_rollback_sem_rpm_anterior(self):
        with mock.patch.object(instalar, "eh_root", return_value=True):
            with self.assertRaises(instalar.Falha):
                instalar.cmd_rollback(args(sim=True))

    def _prepara_anterior(self, schema_txt=1, sha=True):
        os.makedirs(instalar.RPM_ANTERIOR)
        os.makedirs(instalar.RPM_ATUAL)
        open(os.path.join(instalar.RPM_ANTERIOR, "painel-ramais-0.1.0-1.x86_64.rpm"), "w").close()
        with open(instalar.RPM_ANTERIOR + "/versao.txt", "w") as f:
            f.write("painel-ramais 0.1.0 schema %d\n" % schema_txt)
        if sha:
            rpm = os.path.join(instalar.RPM_ANTERIOR, "painel-ramais-0.1.0-1.x86_64.rpm")
            with open(instalar.RPM_ANTERIOR + "/SHA256", "w") as f:
                f.write("%s  %s\n" % (instalar.sha256_arquivo(rpm), os.path.basename(rpm)))

    def test_rollback_usa_oldpackage_e_troca_diretorios(self):
        self._prepara_anterior()
        fr = fake_run([("rpm -q --qf", 0, "0.2.0-1")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", return_value=1), \
                mock.patch.object(instalar, "esperar_health", return_value={"status": "ok"}), \
                mock.patch.object(instalar, "cmd_status", return_value=0):
            instalar.cmd_rollback(args(sim=True))
        self.assertTrue(any("--oldpackage" in c[0] for c in fr.chamadas))
        self.assertTrue(os.path.isfile(instalar.RPM_ATUAL + "/painel-ramais-0.1.0-1.x86_64.rpm"))

    def test_rollback_banco_mais_novo_sem_backup_aborta_sem_mexer(self):
        self._prepara_anterior(schema_txt=1)
        fr = fake_run([("test -f", 1, "")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", return_value=2):
            with self.assertRaises(instalar.Falha):
                instalar.cmd_rollback(args(sim=True))
        self.assertFalse(any(c[0][0] in ("rpm", "systemctl") for c in fr.chamadas))

    def test_rollback_restaura_banco_so_como_dono_sem_tocar_arquivo_como_root(self):
        self._prepara_anterior(schema_txt=1)
        fr = fake_run([("rpm -q --qf", 0, "0.2.0-1")])
        proibido = mock.Mock(side_effect=AssertionError("root nao pode copiar/mover dentro de /var/lib/painel-ramais"))
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "schema_do_banco", return_value=2), \
                mock.patch.object(instalar, "esperar_health", return_value={"status": "ok"}), \
                mock.patch.object(instalar, "cmd_status", return_value=0), \
                mock.patch.object(instalar.shutil, "copy2", proibido), mock.patch.object(instalar.shutil, "move", proibido):
            instalar.cmd_rollback(args(sim=True))
        usos = [c[0] for c in fr.chamadas if len(c[0]) > 4 and c[0][4] in ("mv", "cp")]
        self.assertTrue(usos)
        for cmd in usos:
            self.assertEqual(cmd[:4], ["runuser", "-u", "painel-ramais", "--"])

    def test_importar_grupos_via_stdin_sem_arquivo_em_var_lib(self):
        csv = os.path.join(self.tmp.name, "g.csv")
        with open(csv, "wb") as f:
            f.write(b"grupo,ramal\nA,1000\n")
        fr = fake_run([])
        antes = set(os.listdir(instalar.STATE_DIR))
        with mock.patch.object(instalar, "run", fr):
            instalar.importar_grupos(csv)
        cmd, entrada = fr.chamadas[0]
        self.assertEqual(cmd[-3:], ["import", "--replace", "-"])
        self.assertEqual(cmd[:4], ["runuser", "-u", "painel-ramais", "--"])
        self.assertIn("A,1000", entrada)
        self.assertEqual(set(os.listdir(instalar.STATE_DIR)), antes)

    def test_remover_sem_purge_mantem_dados(self):
        fr = fake_run([("rpm -q --qf", 0, "0.1.0-1")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True):
            instalar.cmd_remover(args(sim=True))
        self.assertTrue(any(c[0][:2] == ["rpm", "-e"] for c in fr.chamadas))
        self.assertTrue(os.path.isdir(instalar.STATE_DIR))

    def test_remover_purge_apaga(self):
        os.makedirs(instalar.CONF_DIR)
        fr = fake_run([("rpm -q --qf", 1, "")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True):
            instalar.cmd_remover(args(sim=True, purge=True))
        self.assertFalse(os.path.exists(instalar.STATE_DIR))
        self.assertFalse(os.path.exists(instalar.CONF_DIR))

    def test_purge_apaga_tambem_a_area_do_instalador(self):
        os.makedirs(instalar.CONF_DIR)
        os.makedirs(instalar.RPM_ATUAL)
        fr = fake_run([("rpm -q --qf", 1, "")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True):
            instalar.cmd_remover(args(sim=True, purge=True))
        self.assertFalse(os.path.exists(instalar.INSTALADOR_DIR))
        self.assertFalse(os.path.exists(instalar.STATE_DIR))

    def test_remover_sem_purge_mantem_area_do_instalador(self):
        os.makedirs(instalar.RPM_ATUAL)
        fr = fake_run([("rpm -q --qf", 0, "0.1.0-1")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True):
            instalar.cmd_remover(args(sim=True))
        self.assertTrue(os.path.isdir(instalar.INSTALADOR_DIR))

    def test_instalar_habilita_socket_antes_do_servico(self):
        rc, ch = self._instalar_com(None)
        enables = [c[0] for c in ch if c[0][:2] == ["systemctl", "enable"]]
        self.assertEqual([e[-1] for e in enables], ["painel-ramais.socket", "painel-ramais.service"])

    def test_purge_interativo_exige_hostname(self):
        fr = fake_run([("rpm -q --qf", 0, "0.1.0-1")])
        with mock.patch.object(instalar, "run", fr), mock.patch.object(instalar, "eh_root", return_value=True), \
                mock.patch.object(instalar, "confirmar"), mock.patch("builtins.input", return_value="outro-nome"):
            with self.assertRaises(instalar.Falha) as cm:
                instalar.cmd_remover(args(purge=True))
        self.assertEqual(cm.exception.codigo, instalar.EXIT_CANCELADO)
        self.assertTrue(os.path.isdir(instalar.STATE_DIR))


class Seguranca(unittest.TestCase):
    """Funcoes reais (sem os mocks do Fluxos): symlink plantado, RPM falso, SHA-256."""

    def test_dir_symlink_recusado(self):
        st = mock.Mock(st_mode=0o120777, st_uid=0)
        with mock.patch.object(instalar.os, "makedirs"), mock.patch.object(instalar.os, "lstat", return_value=st):
            with self.assertRaises(instalar.Falha):
                instalar.dir_do_root_seguro("/var/lib/painel-ramais-instalador")

    def test_dir_de_outro_dono_ou_modo_frouxo_recusado(self):
        for uid, modo in ((1000, 0o040700), (0, 0o040755)):
            st = mock.Mock(st_mode=modo, st_uid=uid)
            with mock.patch.object(instalar.os, "makedirs"), mock.patch.object(instalar.os, "lstat", return_value=st):
                with self.assertRaises(instalar.Falha):
                    instalar.dir_do_root_seguro("/x")

    def test_dir_root_0700_aceito(self):
        st = mock.Mock(st_mode=0o040700, st_uid=0)
        with mock.patch.object(instalar.os, "makedirs"), mock.patch.object(instalar.os, "lstat", return_value=st):
            instalar.dir_do_root_seguro("/x")

    def test_rpm_falso_recusado(self):
        with mock.patch.object(instalar, "run", fake_run([("rpm -qp", 0, "outro-pacote")])):
            with self.assertRaises(instalar.Falha):
                instalar.rpm_e_nosso("/x/painel-ramais-0.1.0-1.x86_64.rpm")

    def test_rpm_com_sha_diferente_recusado(self):
        with tempfile.TemporaryDirectory() as d:
            rpm = os.path.join(d, "painel-ramais-0.1.0-1.x86_64.rpm")
            with open(rpm, "wb") as f:
                f.write(b"adulterado")
            with mock.patch.object(instalar, "run", fake_run([("rpm -qp", 0, "painel-ramais")])):
                with self.assertRaises(instalar.Falha):
                    instalar.rpm_e_nosso(rpm, "0" * 64)
                instalar.rpm_e_nosso(rpm, instalar.sha256_arquivo(rpm))

    def test_rollback_com_rpm_falso_nao_executa_rpm_u(self):
        with tempfile.TemporaryDirectory() as d:
            ant = os.path.join(d, "rpm-anterior")
            os.makedirs(ant)
            open(os.path.join(ant, "painel-ramais-0.1.0-1.x86_64.rpm"), "w").close()
            with open(ant + "/versao.txt", "w") as f:
                f.write("painel-ramais 0.1.0 schema 1\n")
            falso = os.path.join(ant, "painel-ramais-0.1.0-1.x86_64.rpm")
            with open(ant + "/SHA256", "w") as f:  # SHA registrado confere; o que reprova e o nome do pacote
                f.write("%s  x\n" % instalar.sha256_arquivo(falso))
            fr = fake_run([("rpm -qp", 0, "malware")])
            with mock.patch.multiple(instalar, run=fr, eh_root=lambda: True, RPM_ANTERIOR=ant, INSTALADOR_DIR=d,
                                     dir_do_root_seguro=mock.Mock(), schema_do_banco=mock.Mock(return_value=1)):
                with self.assertRaises(instalar.Falha):
                    instalar.cmd_rollback(args(sim=True))
            self.assertFalse(any(c[0][:2] == ["rpm", "-U"] for c in fr.chamadas))
            self.assertFalse(any(c[0][0] == "systemctl" for c in fr.chamadas))

    def test_sha256sums_ok_ausente_e_adulterado(self):
        with tempfile.TemporaryDirectory() as d:
            rpm = os.path.join(d, "painel-ramais-0.1.0-1.x86_64.rpm")
            with open(rpm, "wb") as f:
                f.write(b"conteudo")
            with self.assertRaises(instalar.Falha):
                instalar.verificar_sha256sums(rpm)
            with open(os.path.join(d, "SHA256SUMS"), "w") as f:
                f.write("%s  %s\n" % (instalar.sha256_arquivo(rpm), os.path.basename(rpm)))
            instalar.verificar_sha256sums(rpm)
            with open(rpm, "wb") as f:
                f.write(b"outro")
            with self.assertRaises(instalar.Falha):
                instalar.verificar_sha256sums(rpm)

    @unittest.skipIf(os.name == "nt", "modo POSIX")
    def test_permissao_manager_custom_aviso_se_legivel_por_outros(self):
        with tempfile.TemporaryDirectory() as d:
            arq = os.path.join(d, "m.conf")
            open(arq, "w").close()
            for modo, esperado in ((0o640, instalar.OK), (0o644, instalar.AVISO)):
                os.chmod(arq, modo)
                with mock.patch.object(instalar, "MANAGER_CUSTOM", arq):
                    self.assertEqual(instalar.c_perm_manager_custom()[0], esperado)

    @unittest.skipIf(os.name == "nt", "modo POSIX")
    def test_log_forca_0600(self):
        with tempfile.TemporaryDirectory() as d:
            arq = os.path.join(d, "instalacao.log")
            open(arq, "w").close()
            os.chmod(arq, 0o644)
            with mock.patch.object(instalar, "LOG_DIR", d), mock.patch.object(instalar, "LOG_FILE", arq):
                instalar.log("x")
            self.assertEqual(os.stat(arq).st_mode & 0o777, 0o600)

    def test_scripts_do_pacote_nao_gravam_fora_de_etc_asterisk(self):
        with open(os.path.join(RAIZ, "packaging", "scripts", "postinstall")) as f:
            post = f.read()
        self.assertNotIn("/etc/asterisk/.painel", post)
        self.assertIn("mktemp -d /run/", post)
        self.assertIn("runuser -u asterisk", post)
        self.assertIn("chmod o-rwx", post)
        with open(os.path.join(RAIZ, "packaging", "painel-ramais.service")) as f:
            unit = f.read()
        for dif in ("CapabilityBoundingSet=", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "ProtectKernelLogs=yes",
                    "MemoryDenyWriteExecute=yes", "UMask=0077", "RestrictNamespaces=yes", "LockPersonality=yes"):
            self.assertIn(dif, unit)


class Pacote(unittest.TestCase):
    def _ler(self, *partes):
        with open(os.path.join(RAIZ, *partes)) as f:
            return f.read()

    def test_postinstall_e_postremove_preservam_original_se_gravacao_falhar(self):
        for nome in ("postinstall", "postremove"):
            txt = self._ler("packaging", "scripts", nome)
            self.assertIn("KEEP_TMPD=1", txt)
            self.assertIn('[ -n "$KEEP_TMPD" ] || rm -rf "$TMPD"', txt)
            self.assertIn("$TMPD/orig", txt)

    def test_socket_activation_nas_units_e_no_rpm(self):
        sock = self._ler("packaging", "painel-ramais.socket")
        self.assertIn("ListenStream=127.0.0.1:8790", sock)
        unit = self._ler("packaging", "painel-ramais.service")
        self.assertIn("Requires=painel-ramais.socket", unit)
        self.assertIn("painel-ramais.socket", unit.split("[Service]")[0])
        self.assertIn("/usr/lib/systemd/system/painel-ramais.socket", self._ler("packaging", "nfpm.yaml"))
        self.assertIn("painel-ramais.socket", self._ler("packaging", "scripts", "postinstall"))
        self.assertIn("painel-ramais.socket", self._ler("packaging", "scripts", "preremove"))

    def test_unit_nao_reinicia_em_esquema_novo(self):
        self.assertIn("RestartPreventExitStatus=4", self._ler("packaging", "painel-ramais.service"))

    def test_postinstall_entende_listen_ipv6(self):
        txt = self._ler("packaging", "scripts", "postinstall")
        self.assertIn("@HOST@", self._ler("packaging", "httpd.conf.tmpl"))
        self.assertIn('${LISTEN##*:}', txt)
        self.assertIn('${LISTEN%:*}', txt)


class Apache(unittest.TestCase):
    def test_proxy_ok_quando_raiz_json_e_health_404_sem_json(self):
        resp = {"https://127.0.0.1/painel-ramais/": (404, '{"success": false}'),
                "https://127.0.0.1/painel-ramais/api/health": (404, "<html>Not Found</html>")}
        with mock.patch.object(instalar, "http_get", side_effect=lambda u, c=None: resp[u]):
            self.assertTrue(instalar.testar_apache()[0])

    def test_falha_se_health_vaza_pelo_proxy(self):
        resp = {"https://127.0.0.1/painel-ramais/": (404, '{"success": false}'),
                "https://127.0.0.1/painel-ramais/api/health": (200, '{"success": true}')}
        with mock.patch.object(instalar, "http_get", side_effect=lambda u, c=None: resp[u]):
            self.assertFalse(instalar.testar_apache()[0])


class Main(unittest.TestCase):
    def test_falha_vira_exit_2(self):
        with mock.patch.object(instalar, "cmd_status", side_effect=instalar.Falha("x")), \
                mock.patch.object(instalar, "log"):
            self.assertEqual(instalar.main(["status"]), 2)

    def test_uso_invalido_vira_exit_3(self):
        with self.assertRaises(SystemExit) as cm, mock.patch("sys.stderr"):
            instalar.main(["comando-que-nao-existe"])
        self.assertEqual(cm.exception.code, 3)


if __name__ == "__main__":
    unittest.main()
