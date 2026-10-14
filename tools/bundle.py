# Gera dist/painel-ramais-<versao>.tar.gz (offline) com LF nos textos e modo explicito.
# Uso: python tools/bundle.py <versao> [--rpm caminho.rpm] [--saida dist]
import argparse
import glob
import hashlib
import io
import os
import sys
import tarfile
import time

RAIZ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def adicionar(tar, pasta, nome, dados, modo):
    ti = tarfile.TarInfo("%s/%s" % (pasta, nome))
    ti.size = len(dados)
    ti.mode = modo
    ti.mtime = int(time.time())
    ti.uid = ti.gid = 0
    ti.uname = ti.gname = "root"
    tar.addfile(ti, io.BytesIO(dados))


def texto_lf(caminho):
    with open(caminho, "rb") as f:
        return f.read().replace(b"\r\n", b"\n").replace(b"\r", b"\n")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("versao")
    p.add_argument("--rpm")
    p.add_argument("--saida", default=os.path.join(RAIZ, "dist"))
    a = p.parse_args()
    rpm = a.rpm
    if not rpm:
        achados = sorted(glob.glob(os.path.join(a.saida, "painel-ramais-%s-*.rpm" % a.versao)))
        if not achados:
            sys.exit("ERRO: RPM nao encontrado em %s (rode .\build.ps1 rpm antes)." % a.saida)
        rpm = achados[-1]
    pasta = "painel-ramais-%s" % a.versao
    os.makedirs(a.saida, exist_ok=True)
    destino = os.path.join(a.saida, pasta + ".tar.gz")
    with tarfile.open(destino, "w:gz") as tar:
        adicionar(tar, pasta, "instalar.py", texto_lf(os.path.join(RAIZ, "instalar.py")), 0o755)
        with open(rpm, "rb") as f:
            rpm_bytes = f.read()
        adicionar(tar, pasta, os.path.basename(rpm), rpm_bytes, 0o644)
        soma = "%s  %s\n" % (hashlib.sha256(rpm_bytes).hexdigest(), os.path.basename(rpm))
        adicionar(tar, pasta, "SHA256SUMS", soma.encode("ascii"), 0o644)
        adicionar(tar, pasta, "grupos-exemplo.csv", texto_lf(os.path.join(RAIZ, "packaging", "grupos-exemplo.csv")), 0o644)
        adicionar(tar, pasta, "LEIAME-INSTALACAO.md", texto_lf(os.path.join(RAIZ, "packaging", "LEIAME-INSTALACAO.md")), 0o644)
    print("OK: " + destino)


main()
