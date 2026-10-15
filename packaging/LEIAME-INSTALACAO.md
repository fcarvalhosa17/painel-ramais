# Painel de Ramais - instalacao em campo

O instalador e offline: nada e baixado da internet durante a instalacao (o download do pacote e o passo 1). Requisitos: Issabel 5 em Rocky Linux 8 ou 9,
acesso root. O Python 3 nao precisa estar instalado (no Rocky 8 minimo o instalador usa o platform-python).

## Passo a passo (um servidor)

1. Obtenha o pacote. Em qualquer caso, confira o SHA256 ANTES de rodar o instalador.

   **(a) PBX com internet** (baixar direto no servidor, como root):

       VERSAO=0.1.1   # troque pela versao desejada
       BASE=https://github.com/fcarvalhosa17/painel-ramais/releases/download/v${VERSAO}
       curl -fLO ${BASE}/painel-ramais-${VERSAO}.tar.gz
       curl -fLO ${BASE}/SHA256SUMS
       sha256sum -c SHA256SUMS --ignore-missing    # deve mostrar: painel-ramais-${VERSAO}.tar.gz: OK

   **(b) PBX sem internet** (baixar no computador e copiar com scp):

       # no computador, com VERSAO e BASE definidos como acima
       curl -fLO ${BASE}/painel-ramais-${VERSAO}.tar.gz
       curl -fLO ${BASE}/SHA256SUMS
       sha256sum -c SHA256SUMS --ignore-missing    # conferir no computador
       scp painel-ramais-${VERSAO}.tar.gz SHA256SUMS root@SERVIDOR:/root/
       # no servidor: confira de novo, a copia tambem pode corromper
       ssh root@SERVIDOR 'cd /root && sha256sum -c SHA256SUMS --ignore-missing'

   Se a linha nao terminar em OK, pare e baixe de novo. Depois extraia (no servidor, em /root):

       tar xzf painel-ramais-${VERSAO}.tar.gz && cd painel-ramais-${VERSAO}

2. Verifique o ambiente (nao altera nada; relatorio em /var/log/painel-ramais/):

       ./instalar.py verificar

   Cada item sai como OK, AVISO ou ERRO, com a explicacao de como corrigir. Corrija todos os ERROs.

3. Instale (interativo: tecnologia, usuario admin, senha, CSV de grupos opcional):

       ./instalar.py instalar

   Ao final aparece o resumo e a URL: `https://<ip-do-servidor>/painel-ramais/`.
   Se algo falhar, nada e removido; a mensagem mostra o comando de diagnostico.

4. Conferir depois: `./instalar.py status`

Rodar `instalar` de novo faz upgrade (guarda o RPM anterior). Voltar a versao anterior: `./instalar.py rollback`.
Remover mantendo os dados: `./instalar.py remover`. Remover tudo: `./instalar.py remover --purge`
(pede para digitar o nome do servidor; com `--sim` nao pergunta). Todo comando executado fica em `/var/log/painel-ramais/instalacao.log`.

## Modo nao interativo

Arquivo `respostas.ini`:

    [painel]
    tecnologia = auto
    admin_usuario = admin
    admin_senha_arquivo = /root/senha-painel.txt
    grupos_csv = /root/grupos.csv

A senha nunca vai na linha de comando (apareceria no `ps`). Ela fica em arquivo do root com permissao 0600 ou 0400 (minimo 10 caracteres):

    install -m 600 /dev/null /root/senha-painel.txt && vi /root/senha-painel.txt
    ./instalar.py instalar --respostas respostas.ini --sim

O codigo de saida e 0 em sucesso e diferente de zero em falha.

## Varios servidores via ssh

    for h in $(cat servidores.txt); do
      scp painel-ramais-VERSAO.tar.gz respostas.ini senha.txt grupos.csv root@$h:/root/
      ssh root@$h 'chmod 600 /root/senha.txt && cd /root && tar xzf painel-ramais-VERSAO.tar.gz &&
        cd painel-ramais-VERSAO && ./instalar.py instalar --respostas /root/respostas.ini --sim' \
        && echo "$h OK" || echo "$h FALHOU"
    done

(No respostas.ini use caminhos `/root/senha.txt` e `/root/grupos.csv`.)

Formato do CSV de grupos: `grupo,ramal` onde ramal pode ser faixa (`2000-2099`). Veja `grupos-exemplo.csv`.

## Seguranca (leia antes de instalar em producao)

- **Conta AMI com `write = system,reporting` (decisao do usuario: mantida).** A classe `system` e exigida pelas consultas PJSIP e `ModuleCheck`.
  Risco residual aceito: se o daemon for comprometido ou o segredo (`/etc/painel-ramais/ami-secret`, 0600) vazar, essa classe da acesso a actions que o painel
  nunca usa (`GetConfig`, `ModuleUnload`, `Reload`, `DBPut`, `PJSIPNotify`, `Hangup`, `MixMonitor` com `Command`). Controles: lista fixa de actions no codigo
  (recusa antes do socket), `permit` so 127.0.0.1, segredo 0600, unit endurecida. Validar a unit no laboratorio: `systemd-analyze security painel-ramais`.
- A porta 127.0.0.1:8790 e mantida pelo `painel-ramais.socket` (systemd), mesmo com o daemon parado. Qualquer usuario local alcanca a porta em TCP.
- O instalador guarda o RPM anterior em `/var/lib/painel-ramais-instalador/` (root 0700; aceito pelo usuario). `remover --purge` apaga esse diretorio tambem.
- O RPM e conferido pelo `SHA256SUMS` do pacote antes de instalar; guarde o tar.gz de origem confiavel.
