# Adendo à FASE 1: instalador em Python para os servidores PBX

Vou instalar o painel em vários servidores de clientes. Criar um instalador em Python que eu rodo no servidor do PBX. Ele substitui o discovery.sh: a verificação vira um subcomando do instalador. Se o discovery.sh já existir, remover e atualizar README e critérios.

## Pacote de distribuição
- Novo alvo de build "bundle", que gera dist/painel-ramais-<versao>.tar.gz contendo:
  - instalar.py;
  - o RPM;
  - grupos-exemplo.csv;
  - LEIAME-INSTALACAO.md.
- Funciona offline: nada é baixado da internet no servidor.
- Uso no servidor: copiar o tar.gz, extrair e rodar "./instalar.py <subcomando>".

## Compatibilidade (obrigatório)
- Python 3.6 ou superior, só biblioteca padrão. Proibido depender de pip.
- Sem recursos acima do 3.6: nada de dataclasses e nada de subprocess.run(capture_output=...).
- Validar com "vermin --target=3.6-" no ambiente de desenvolvimento.
- Primeira linha em formato poliglota sh/python, que executa com python3 se existir e, se não existir, com /usr/libexec/platform-python. Rodar "./instalar.py" precisa funcionar em Rocky 8 mínimo e em Rocky 9.
- Arquivo com fim de linha LF e permissão de execução dentro do tar.gz.

## Subcomandos
1. **verificar** (somente leitura, não altera nada)
   - Checagens:
     - root;
     - Rocky 8 ou superior;
     - Issabel 5 presente;
     - Asterisk rodando e com versão 13 ou superior;
     - tecnologia ativa (SIP ou PJSIP) e quantidade de ramais;
     - manager.conf incluindo o manager_custom.conf;
     - httpd rodando e com mod_proxy_wstunnel;
     - estado do SELinux;
     - porta interna do daemon livre;
     - espaço em disco em /var/lib;
     - versão do painel já instalada, se houver.
   - Cada item sai como OK, AVISO ou ERRO, com a explicação de como corrigir.
   - Salva o relatório em /var/log/painel-ramais/verificacao-<data-hora>.txt.
2. **instalar**
   - Roda verificar antes e aborta se houver qualquer ERRO.
   - Se o painel já estiver instalado, faz upgrade (rpm -U). Se não estiver, faz a instalação nova.
   - Pergunta: tecnologia (auto como padrão), usuário admin e senha (getpass) e, opcionalmente, o caminho do CSV de grupos.
   - Configura via CLI do próprio daemon, sem gravar direto no SQLite.
   - Habilita e inicia o serviço.
   - Espera até 60 s pelo /api/health com AMI conectado e ramais carregados.
   - Testa o acesso via Apache em https://127.0.0.1/painel-ramais/.
   - No fim, mostra um resumo: versão, tecnologia, ramais, grupos, URL de acesso e o caminho do log.
   - Em falha, não remove nada sozinho. Mostra o que falhou e o comando para diagnosticar.
3. **status**: versão, serviço, AMI, ramais por estado, clientes conectados e verificação de dependências, lidos do /api/health.
4. **rollback**
   - Antes de todo upgrade, o instalador guarda o RPM da versão anterior em /var/lib/painel-ramais/rpm-anterior/.
   - O rollback reinstala essa versão (rpm -U --oldpackage) preservando dados.
5. **remover**
   - Executa rpm -e.
   - Com --purge, apaga também /var/lib/painel-ramais e /etc/painel-ramais, mas só depois de confirmação digitada.

## Modo não interativo (para vários servidores)
- Flag --respostas arquivo.ini com tecnologia, usuário admin, caminho de um arquivo com a senha e CSV de grupos.
- A senha nunca vai na linha de comando, porque aparece no ps. Ou é digitada (getpass) ou vem de arquivo com permissão 0600.
- Flag --sim para aceitar confirmações.
- Código de saída: 0 quando der sucesso, diferente de zero quando falhar. Assim posso rodar em laço via ssh.

## Regras
- Idempotente: rodar instalar duas vezes não duplica usuário, grupos nem configuração.
- Não repetir lógica do RPM: o instalador nunca edita manager_custom.conf, Apache ou SELinux. Isso é papel do %post. O instalador só orquestra e valida.
- Todo comando executado e toda saída vão para /var/log/painel-ramais/instalacao.log.
- Mensagens em português, claras para um técnico de campo.

## Critérios de aceite adicionais
- Nos containers Rocky 8 mínimo e Rocky 9, com o simulador no lugar do Asterisk, testar:
  - verificar;
  - instalar (interativo e com --respostas);
  - instalar de novo (upgrade);
  - status;
  - rollback;
  - remover e remover --purge.
- Rocky 8 mínimo sem o pacote python3 instalado: o instalador roda via platform-python.
- Forçar uma falha de verificação, por exemplo com mod_proxy_wstunnel ausente: instalar aborta antes de tocar no sistema.
- LEIAME-INSTALACAO.md com o passo a passo de campo e um exemplo de laço ssh para vários servidores.
