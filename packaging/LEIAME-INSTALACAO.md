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

## Homologacao e coleta de suporte (`homologar`)

Serve para validar o painel num Issabel real (siga `docs/homologacao/ROTEIRO-MANUAL.md`) e tambem para **coletar dados de suporte em um cliente**:
um arquivo unico que voce pode enviar sem expor senhas nem dados dos clientes finais.

    sudo ./instalar.py homologar                    # 45 minutos (padrao)
    sudo ./instalar.py homologar --duracao 10       # coleta curta de suporte
    sudo ./instalar.py homologar --saida /root      # onde gravar o arquivo (padrao: pasta atual)

**O que faz.** Antes de comecar registra: `manager show command <Action>` de cada action que o painel usa, versao do Asterisk, `getenforce`,
`systemctl status` do socket e do servico, `systemd-analyze security painel-ramais` (no Rocky 8 o comando pode nao existir: a mensagem entra no pacote),
o resultado do `verificar` e o `config.yaml`. Durante a duracao pedida roda `painel-ramais capturar --anonimizar` e, a cada 2 s, guarda o estado do painel
(`/api/snapshot`) junto de `core show channels concise` e `pjsip show contacts` (ou `sip show peers`). O terminal mostra o relogio e o passo atual do roteiro
de chamadas (Enter = proximo passo; qualquer texto + Enter = anotacao com a hora). Ctrl-C encerra antes e ainda gera o arquivo.

**O que entrega.** UM arquivo `homologacao-AAAAMMDD-HHMMSS.tar.gz` (modo 0600) com: `meta.json`, `pre.txt`, `snapshots.txt`, `roteiro.log`, `captura.txt`,
`capturar.err`, `journal.txt` (servico e socket) e `instalacao.log` (log do instalador). Os dados brutos ficam so em uma pasta temporaria do root e
sao apagados no fim.
Amostra igual a anterior nao e regravada (`igual` no `snapshots.txt`), mas cada amostra diferente guarda o estado completo do painel e a mascara processa
perto de 2 MB por segundo: em PBX com milhares de ramais use `--duracao` curta (10 a 15 minutos) para o arquivo e a espera final nao crescerem.

**O que e mascarado.** Todo o texto do arquivo passa pelo `painel-ramais filtrar`, as mesmas regras do `capturar`: senhas e segredos viram `***`
(`Secret`, `Password`, `Md5Cred`, `Challenge`, `token`... em `Chave: valor`, `chave=valor` e JSON); numeros externos (7 digitos ou mais), nomes, usuarios,
hosts, IPv4 e IPv6 viram valores ficticios, o mesmo ficticio para o mesmo original em todo o arquivo (assim painel e Asterisk continuam comparaveis).
O nome e o FQDN deste servidor tambem sao trocados em qualquer ponto (`filtrar --host`). A mascara roda como o usuario `nobody`, nao como o do painel.
Alem de `Secret`/`Password`, sao mascarados `passphrase`, `pass`, `pwd`, `senha`, `auth`, `cookie`, `session`/`sessionid`, `hmac`, `signature`, `credential`, `*key`, `*hash`, o valor inteiro
(com aspas, ate a aspa que fecha; sem aspas, ate o FIM DA LINHA, entao o que vinha depois na mesma linha tambem some: escolha de seguranca), inclusive dentro de aspas, em URL com `?session=X` e em repr de Python, cabecalhos `Cookie`/`Set-Cookie`/`Authorization`, a senha de `usuario:senha@host` e `response`/`nonce` de Digest.
**A mascara NAO cobre:** segredo que ocupa varias linhas (chave PEM), segredo em argv/`ps` fora de par chave=valor e texto livre sem chave (nome de pessoa, numero curto).
O `captura.txt` foi anonimizado pelo proprio `capturar`, com mapeamento PROPRIO: o mesmo IP ou numero tem outro ficticio nos demais arquivos.
Ramais internos curtos ficam como estao. A mascara e por padrao e e melhor esforco: texto livre com nome de pessoa e numeros curtos podem passar. Se o filtro falhar,
nenhum arquivo e gerado (os dados brutos ficam na pasta temporaria indicada na mensagem; nao envie).

**Antes de enviar.** No computador com este repositorio: `python3 tools/analisar_homologacao.py homologacao-AAAAMMDD-HHMMSS.tar.gz` lista as divergencias painel x Asterisk
e as linhas em que a mascara pode ter falhado; revise essas linhas antes de compartilhar o arquivo.

## Seguranca (leia antes de instalar em producao)

- **Conta AMI com `write = system,reporting` (decisao do usuario: mantida).** A classe `system` e exigida pelas consultas PJSIP e `ModuleCheck`.
  Risco residual aceito: se o daemon for comprometido ou o segredo (`/etc/painel-ramais/ami-secret`, 0600) vazar, essa classe da acesso a actions que o painel
  nunca usa (`GetConfig`, `ModuleUnload`, `Reload`, `DBPut`, `PJSIPNotify`, `Hangup`, `MixMonitor` com `Command`). Controles: lista fixa de actions no codigo
  (recusa antes do socket), `permit` so 127.0.0.1, segredo 0600, unit endurecida. Validar a unit no laboratorio: `systemd-analyze security painel-ramais`.
- A porta 127.0.0.1:8790 e mantida pelo `painel-ramais.socket` (systemd), mesmo com o daemon parado. Qualquer usuario local alcanca a porta em TCP.
- O instalador guarda o RPM anterior em `/var/lib/painel-ramais-instalador/` (root 0700; aceito pelo usuario). `remover --purge` apaga esse diretorio tambem.
- O RPM e conferido pelo `SHA256SUMS` do pacote antes de instalar; guarde o tar.gz de origem confiavel.
