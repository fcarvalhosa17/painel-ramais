# Capturas de AMI em testdata

**Todas as capturas desta pasta sao SINTETICAS.** Foram geradas pelo simulador (`internal/amisim`,
`gen_test.go`), nao vieram de um Asterisk real. Elas existem para provar o mecanismo (formato da secao 18
do `docs/CONTRATOS.md`, modo de reproducao do `amisim`, comparacao do estado do daemon). Capturas reais virao
da homologacao (`docs/ROTEIRO-CAPTURA.md`) e entram aqui ao lado destas, sem mudar nada no teste.

Arquivos (um par por grupo e tecnologia):

- `sintetica-<grupo>-<sip|pjsip>.txt`: a captura (formato `#PAINEL-RAMAIS-CAPTURA 1`). Tem o login, o
  snapshot (as mesmas actions do daemon, inclusive `PJSIPShowEndpoint`/`SIPshowpeer` de cada ramal) e os
  eventos de cada passo dos cenarios. Os quadros usam CRLF, como no socket.
- `sintetica-<grupo>-<sip|pjsip>.esperado.json`: o oraculo. Depois de N quadros, o que cada ramal envolvido deve
  mostrar (registro, dispositivo e, por chamada: id, direcao, estado e com quem fala).
- Grupos: `interna` (interna, em espera, nao atendida, ramal desregistrando e voltando), `entrada`, `saida`,
  `local` (Local channel de entrada e interna), `transferencia` (cega e assistida, por "merge" e por "link", com
  chamada interna, de entrada e de saida).

Importante para o git: estes `.txt` precisam de CRLF e de tamanho exato por quadro. O `.gitattributes` da raiz
tem `* text=auto eol=lf`; sem a linha `testdata/capturas/*.txt binary` o git reescreve o arquivo e o leitor
(`internal/capture`) passa a recusa-lo.

Gerar de novo: `GERAR_CAPTURAS=<pasta> go test -run TestGerarCapturas ./internal/amisim`.
Reproduzir a mao: `amisim -replay arquivo.txt -speed 10`. Rodar o "com quem fala" contra o daemon:
`loadtest -daemon <binario> -captures testdata/capturas`.
