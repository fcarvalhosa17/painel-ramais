"""tools/analisar_homologacao.py contra um pacote SINTETICO (nenhum dado real): 1 divergencia e 1 vazamento plantados."""
import io
import json
import os
import shutil
import sys
import tarfile
import tempfile
import unittest

RAIZ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(RAIZ, "tools"))
import analisar_homologacao as an  # noqa: E402

T = ["2026-10-15T14:30:%02d.000Z" % s for s in (0, 2, 4, 6, 8, 10)]
VAZADO = "198.51.100.77"  # IP de documentacao plantado sem mascara

PRE = "=== Versao do Asterisk ===\nAsterisk 18.20.0 built by root\n"
ROTEIRO = ("2026-10-15T14:29:59.000Z inicio duracao_s=12 intervalo_s=2\n"
           "2026-10-15T14:29:59.500Z passo 1/11 Chamada interna\n"
           "2026-10-15T14:30:03.000Z passo 2/11 Entrada por tronco\n")
CANAIS = ("PJSIP/2001-00000010!from-internal!2002!1!Up!Dial!PJSIP/2002,30!2001!!!3!20!br-1!1000000001.1\n"
          "PJSIP/2002-00000011!from-internal!2002!1!Up!AppDial!(Outgoing Line)!2002!!!3!20!br-1!1000000001.2\n")
CONTATOS = ("  Contact:  2001/sip:***@10.200.0.1:5060;ob  a1b2c3d4e5  Avail  12.345\n"
            "  Contact:  2002/sip:***@10.200.0.2:5060;ob  b7e2cc0011  Avail  9.100\n")


def painel(reg2="registered", direcao="internal"):
    ext = lambda n, reg, ip, calls: {"number": n, "name": None, "registration": reg, "ip": ip, "calls": calls}  # noqa: E731
    chamada = lambda outro: [{"id": "1000000001.1", "direction": direcao, "state": "talking", "started_at": T[0],  # noqa: E731
                              "peer": {"kind": "extension", "number": outro, "name": None}}]
    return json.dumps({"success": True, "data": {"ami_connected": True, "technology": "pjsip", "extensions": [
        ext("2001", "registered", "10.200.0.1", chamada("2002")),
        ext("2002", reg2, "10.200.0.2", chamada("2001")),
        ext("2003", "unregistered", None, [])]}}, separators=(",", ":"))


def secao(ts, nome, texto=None):
    return "@@ %s %s igual\n" % (ts, nome) if texto is None else "@@ %s %s\n%s\n" % (ts, nome, texto.rstrip("\n"))


def pacote(divergencia=True, vazamento=True, direcao="internal"):
    """Conteudo do pacote como {nome: texto}. A divergencia: nas amostras 2 e 3 o painel diz que 2002 esta
    unreachable e o Asterisk diz registered. O vazamento: um IP sem mascara no `pjsip show contacts` da amostra 4."""
    s, antes = "", {}
    for i, ts in enumerate(T):
        textos = {"painel": painel("unreachable" if divergencia and i in (2, 3) else "registered", direcao),
                  "canais": CANAIS, "contatos": CONTATOS}
        if vazamento and i == 4:
            textos["contatos"] += "  Contact:  trunk-acme/sip:***@%s:5060  ab12  Avail  3.1\n" % VAZADO
        for nome, texto in textos.items():
            s += secao(ts, nome, None if antes.get(nome) == texto else texto)  # como o homologar: "igual" se nao mudou
            antes[nome] = texto
    meta = {"tecnologia": "pjsip", "extension_pattern": "^[0-9]+$", "encerrado_por": "prazo"}
    return {"meta.json": json.dumps(meta), "pre.txt": PRE, "snapshots.txt": s, "roteiro.log": ROTEIRO,
            "journal.txt": "[rc=0]\n", "captura.txt": "#PAINEL-RAMAIS-CAPTURA 1\n#meta {}\n"}


class Analise(unittest.TestCase):
    def setUp(self):
        self.base = tempfile.mkdtemp(prefix="an-")
        self.addCleanup(shutil.rmtree, self.base, True)

    def pasta(self, arq, nome="homologacao-x"):
        d = os.path.join(self.base, nome)
        os.makedirs(d)
        for n, t in arq.items():
            with io.open(os.path.join(d, n), "w", encoding="utf-8", newline="") as f:
                f.write(t)
        return d

    def targz(self, arq):
        d = self.pasta(arq)
        out = os.path.join(self.base, "homologacao-x.tar.gz")
        with tarfile.open(out, "w:gz") as tar:
            tar.add(d, arcname="homologacao-x")
        return out

    def rodar(self, caminho):
        out = io.StringIO()
        rc_old, sys.stdout = sys.stdout, out
        try:
            rc = an.main([caminho])
        finally:
            sys.stdout = rc_old
        return rc, out.getvalue()

    def test_pacote_limpo_nao_tem_divergencia_nem_suspeita(self):
        rc, txt = self.rodar(self.pasta(pacote(divergencia=False, vazamento=False)))
        self.assertEqual(rc, 0, txt)
        self.assertIn("Resumo: 0 divergencia(s), 0 linha(s) suspeita(s).", txt)
        self.assertIn("6 amostra(s); 6 comparada(s)", txt)

    def test_divergencia_e_vazamento_plantados_sao_reportados_com_hora_e_linha(self):
        arq = pacote()
        for caminho in (self.pasta(arq, "dir"), self.targz(arq)):  # pasta e .tar.gz
            rc, txt = self.rodar(caminho)
            self.assertEqual(rc, 1, txt)
            self.assertIn("Resumo: 1 divergencia(s), 1 linha(s) suspeita(s).", txt)
            # divergencia: ramal, campo, valores, intervalo (amostras 2 e 3), passo do roteiro e a evidencia do Asterisco
            self.assertIn("[DIVERGENCIA] %s ate %s | ramal 2002 | registro | painel=unreachable asterisk=registered | 2 amostra(s) | passo 2 (Entrada por tronco)"
                          % (T[2], T[3]), txt)
            self.assertIn("asterisk> Contact:  2002/sip:***@10.200.0.2:5060", txt)
            # vazamento: arquivo, numero da linha (conferido no texto), hora da amostra
            linha = next(i for i, ln in enumerate(arq["snapshots.txt"].split("\n"), 1) if VAZADO in ln)
            self.assertIn("[SUSPEITA:ip] snapshots.txt:%d | t=%s | %s" % (linha, T[4], VAZADO), txt)

    def test_direcao_errada_no_painel_e_divergencia(self):
        eps, _, _ = an.divergencias(pacote(divergencia=False, vazamento=False, direcao="outbound"))
        self.assertEqual(sorted((e["ramal"], e["campo"], e["painel"], e["asterisk"]) for e in eps),
                         [("2001", "direcao", "outbound", "internal"), ("2002", "direcao", "outbound", "internal")])

    def test_amostra_com_erro_de_coleta_ou_painel_sem_ami_nao_e_comparada(self):
        arq = pacote(divergencia=True, vazamento=False)
        arq["snapshots.txt"] += "@@ 2026-10-15T14:30:12.000Z canais erro\nrc=1\nUnable to connect to remote asterisk\n"
        arq["snapshots.txt"] += "@@ 2026-10-15T14:30:14.000Z painel\n" + json.dumps({"data": {"ami_connected": False, "extensions": []}}) + "\n"
        eps, total, comp = an.divergencias(arq)
        self.assertEqual((total, comp, len(eps)), (8, 6, 1))

    def test_sip_show_peers(self):
        sip = ("Name/username             Host          Dyn Forcerport Comedia    ACL Port     Status      Description\n"
               "2001/2001                 10.200.0.1     D  Yes        Yes            5060     OK (12 ms)\n"
               "2002/2002                 (Unspecified)  D  Yes        Yes            0        UNKNOWN\n"
               "2003/2003                 10.200.0.3     D  Yes        Yes            5060     UNREACHABLE\n"
               "3 sip peers [Monitored: 1 online, 2 offline Unmonitored: 0 online, 0 offline]\n")
        got = an.contatos_sip(sip)
        self.assertEqual((got["2001"][0], got["2001"][1]), ("registered", "10.200.0.1"))
        self.assertEqual((got["2002"][0], got["2002"][1]), ("unregistered", None))
        self.assertEqual(got["2003"][0], "unreachable")

    def test_suspeitas_ignoram_o_que_o_filtro_gera(self):
        limpo = ['{"ip":"10.200.0.1","name":"Pessoa 001","secret":"***","id":"1000000002.10"}',
                 "Secret: ***", "PJSIP/2001-00000010!x!0000000004!1", "Uniqueid: 1791902000.10", "14:30:02 ::1 127.0.0.1",
                 'Callerid: "Pessoa 002" <10000000003>', "Host: host-001.invalid", "Contact: sip:2001@host-002.invalid"]
        for ln in limpo:
            self.assertEqual(an.suspeitas_da_linha(ln), [], ln)
        tipos = {t for ln in ('Secret: hunter2', "peer 203.0.113.9", "ligou 11987654321", 'Callerid: "Maria Souza" <10000000003>',
                              "mail maria@empresa.com.br", "token=abc123", "fe80::1c2b:3d4e:5f60:7182") for t, _ in an.suspeitas_da_linha(ln)}
        self.assertEqual(tipos, {"segredo", "ip", "numero", "nome", "host/e-mail", "ipv6/mac"})

    def test_linhas_que_vazavam_no_filtro_antigo_agora_geram_suspeita(self):
        vazadas = [
            "password=*** def ghi",                                       # valor com espaco: sobrava o resto
            "secret=***;def",                                             # sufixo depois do ;
            "token=***abc",                                               # texto colado no ***
            "Cookie: sid=abc123; outro=def",
            "Set-Cookie: painel_session=abc; Path=/",
            "Authorization: Bearer abcdef123456",
            "register => 2001:S3nha@pbx.exemplo.invalid/2001",
            "Contact: <sip:maria:Seg9@host-001.invalid:5060>",
            'Authorization: Digest username="x", response="deadbeefcafe"',
            'WWW-Authenticate: Digest realm="a", nonce="9f8e7d"',
            "sessionid=abc123",
            "AMPMGRPASS=s3gr3d0",
            "api_key: xyz789",
            "secret=*** user=Conta 001",                                  # o filtro novo mascara ate o fim da linha
        ]
        for ln in vazadas:
            tipos = {t for t, _ in an.suspeitas_da_linha(ln)}
            self.assertTrue(any(t.startswith("segredo") for t in tipos), "nao detectou: %r (%s)" % (ln, tipos))
        limpas = ["secret=***", "get https://h.exemplo/p?session=***", "erro=\"dial tcp: secret=***\"", "dial IAX2/tcp:***@h.exemplo/r",
                  "{'secret': '***', 'ok': True}", "PrivateTmp=yes", "session_ttl: 12h", '{"secret":"***","ok":true}', "Secret: ***", "Cookie: ***", "Authorization: ***",
                  "register => 2001:***@pbx.invalid/2001", "Contact: sip:2001@10.200.0.1:5060", "password=***", "Auths: ***",
                  'WWW-Authenticate: ***', "Response: Success", "token=\"***\", user=x"]
        for ln in limpas:
            self.assertEqual([t for t in an.suspeitas_da_linha(ln) if t[0].startswith("segredo")], [], ln)

    def test_formas_cruas_que_o_filtro_vazava_sao_acusadas_pelo_detector(self):
        cruas = [
            "password=abc def ghi", "secret=abc;def,ghi", "token=abc&x=1", "Cookie: sid=abc123; outro=def",
            "Set-Cookie: painel_session=abc; Path=/", "Authorization: Bearer abcdef123456",
            "register => 2001:S3nha@pbx.exemplo.com/2001", 'Authorization: Digest username="x", response="deadbeefcafe"',
            'WWW-Authenticate: Digest realm="a", nonce="9f8e7d"', "sessionid=abc123", "AMPMGRPASS=s3gr3d0", "api_key: xyz789",
            "pwd: 'com espaco; e ponto'", '{"secret":"ab\\"c,d","ok":true}', "Contact: <sip:maria:Seg9@203.0.113.9:5060>",
            "register => 1001:p@sX@host.exemplo/1001", "dial IAX2/tcp:X@host.exemplo/ramal", 'erro="dial tcp: secret=X"',
            "get https://h.exemplo/p?session=X", "{'secret': 'X'}", "passphrase=frase aqui", "Hash: deadbeef",
            "secret=abc def=X", "dial IAX2/sip:X@host.exemplo/ramal", "register => 1001:ab/X@host.exemplo/1001",
        ]
        self.assertGreaterEqual(len(cruas), 19)
        for ln in cruas:
            self.assertTrue([t for t in an.suspeitas_da_linha(ln) if t[0].startswith("segredo")], "nao detectou: %r" % ln)

    def _tar(self, membros):
        caminho = os.path.join(self.base, "b.tar.gz")
        with tarfile.open(caminho, "w:gz") as tar:
            for nome, dados in membros:
                ti = tarfile.TarInfo(nome)
                ti.size = len(dados)
                tar.addfile(ti, io.BytesIO(dados))
        return caminho

    def test_tetos_de_tamanho_e_de_arquivos_viram_codigo_2(self):
        orig = (an.MAX_MEMBRO, an.MAX_TOTAL, an.MAX_MEMBROS)
        self.addCleanup(lambda: (setattr(an, "MAX_MEMBRO", orig[0]), setattr(an, "MAX_TOTAL", orig[1]), setattr(an, "MAX_MEMBROS", orig[2])))
        an.MAX_MEMBRO, an.MAX_TOTAL, an.MAX_MEMBROS = 100, 150, 3
        grande = self._tar([("p/snapshots.txt", b"x" * 200)])
        self.assertEqual(self.rodar(grande)[0], 2)  # um arquivo acima do teto
        muitos = self._tar([("p/f%d.txt" % i, b"x") for i in range(4)])
        self.assertEqual(self.rodar(muitos)[0], 2)  # arquivos demais
        soma = self._tar([("p/a.txt", b"x" * 90), ("p/b.txt", b"x" * 90)])
        self.assertEqual(self.rodar(soma)[0], 2)  # total acima do teto
        pasta = self.pasta({"snapshots.txt": "x" * 200}, "grande")
        self.assertEqual(self.rodar(pasta)[0], 2)  # pasta tambem
        ok = self._tar([("p/snapshots.txt", b"@@ 2026-10-15T14:30:00.000Z painel\n{}\n")])
        self.assertNotEqual(self.rodar(ok)[0], 2)

    def test_controles_do_pacote_sao_escapados_na_saida(self):
        arq = pacote(divergencia=False, vazamento=False)
        arq["journal.txt"] = "peer 198.51.100.9 \x1b[2J\x1b]0;titulo\x07 fim\r\x00\n"
        rc, txt = self.rodar(self.pasta(arq))
        self.assertEqual(rc, 1)
        self.assertNotRegex(txt, "[\x00-\x08\x0b-\x1f\x7f]")
        self.assertIn("\\x1b[2J", txt)
        self.assertIn("198.51.100.9", txt)

    def test_extension_pattern_do_meta_so_e_exibido_e_nome_longo_nao_roda_regex(self):
        import time
        longo = "a" * 40 + "!"
        arq = pacote(divergencia=False, vazamento=False)
        meta = json.loads(arq["meta.json"])
        meta["extension_pattern"] = "(a+)+$"  # perigoso: nunca e compilado
        arq["meta.json"] = json.dumps(meta)
        arq["snapshots.txt"] = arq["snapshots.txt"].replace("Contact:  2001/", "Contact:  %s/" % longo)
        t0 = time.time()
        an.divergencias(arq)  # usa [0-9]+, ignora o do meta
        an.divergencias(arq, pattern="(a+)+$")  # explicito, mas o nome tem mais de 32 caracteres: nao passa pelo regex
        self.assertLess(time.time() - t0, 5)
        out = io.StringIO()
        an.relatorio(arq, None, out)
        self.assertIn("so informativo: (a+)+$", out.getvalue())
        self.assertIsNone(an._compilar_padrao("(") )
        self.assertIsNotNone(an._compilar_padrao("^1[0-9]{3}$"))

    def test_linha_gigante_nao_roda_regex(self):
        orig = an.MAX_LINHA
        self.addCleanup(setattr, an, "MAX_LINHA", orig)
        an.MAX_LINHA = 50
        arq = pacote(divergencia=False, vazamento=False)
        arq["journal.txt"] = "x" * 80 + " 198.51.100.9\n"
        v = [x for x in an.vazamentos(arq) if x[0] == "journal.txt"]
        self.assertEqual([x[3] for x in v], ["linha-longa"])

    def test_pacote_invalido_e_codigo_2(self):
        self.assertEqual(self.rodar(os.path.join(self.base, "nao-existe.tar.gz"))[0], 2)
        self.assertEqual(self.rodar(self.pasta({"x.txt": "oi"}, "vazio"))[0], 2)


if __name__ == "__main__":
    unittest.main()
