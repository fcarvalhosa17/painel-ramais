package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"painel-ramais/internal/amisim"
)

func ms(d time.Duration) string {
	if d == 0 {
		return "0 ms"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond))
	}
	if d < 10*time.Second {
		return fmt.Sprintf("%.0f ms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

func kib(n int) string { return fmt.Sprintf("%.1f KiB", float64(n)/1024) }

func verdict(ok bool) string {
	if ok {
		return "PASSA"
	}
	return "**FALHA**"
}

type table struct {
	b     strings.Builder
	modes []string
}

func newTable(title string, modes []string) *table {
	t := &table{modes: modes}
	t.b.WriteString("| " + title + " | " + strings.Join(modes, " | ") + " |\n")
	t.b.WriteString("|---|" + strings.Repeat("---|", len(modes)) + "\n")
	return t
}

func (t *table) row(label string, rs []*Result, f func(*Result) string) {
	t.b.WriteString("| " + label)
	for _, r := range rs {
		v := "n/a"
		if r != nil && (r.Err == "" || r.Boot > 0) {
			v = f(r)
		}
		t.b.WriteString(" | " + v)
	}
	t.b.WriteString(" |\n")
}

func (t *table) String() string { return t.b.String() + "\n" }

// criteria avalia os criterios de aceite da fase 1 sobre um resultado.
type criterion struct {
	Name string
	OK   bool
	Val  string
	Soft bool // falha vira RESSALVA e nao reprova (comportamento fora dos criterios de aceite da especificacao)
}

func criteria(r *Result) []criterion {
	var c []criterion
	add := func(n string, ok bool, f string, a ...any) {
		c = append(c, criterion{Name: n, OK: ok, Val: fmt.Sprintf(f, a...)})
	}
	burst := r.Lat["rajada"]
	add("p99 evento->delta na rajada de desregistros < 1 s", burst.N > 0 && burst.P99 < time.Second, "p99=%s (n=%d)", ms(burst.P99), burst.N)
	add("p99 evento->delta em todas as fases < 1 s", r.LatAll.N > 0 && r.LatAll.P99 < time.Second, "p99=%s (n=%d)", ms(r.LatAll.P99), r.LatAll.N)
	add("rajada: todos os desregistros chegaram ao cliente", r.Burst.AllSeen && r.Burst.Stuck == 0, "%d/%d em %s", r.Burst.N-r.Burst.Stuck, r.Burst.N, ms(r.Burst.Converge))
	add("nenhuma perda de estado: snapshot final == verdade", r.Final.Compared > 0 && r.Final.SnapDiffExts == 0, "%d divergentes de %d", r.Final.SnapDiffExts, r.Final.Compared)
	add("nenhuma perda de estado: estado do cliente WS == verdade", r.Final.Compared > 0 && r.Final.WSDiffExts == 0 && r.Final.Gaps == 0, "%d divergentes, %d lacunas de versao", r.Final.WSDiffExts, r.Final.Gaps)
	c = append(c, criterion{Name: "nenhum estado fugaz errado visto no WS (nem de um instante, nem de outro)", OK: r.Final.Unexpected == 0, Val: fmt.Sprintf("%d fugazes errados; %d so com dispositivo/chamadas de instantes diferentes (aceitos)", r.Final.Unexpected, r.Final.Torn), Soft: true})
	fl := r.Fill
	if p, ok := r.Lat["carga"]; ok {
		add("p99 evento->delta DURANTE o preenchimento de nomes < 1 s", p.N > 0 && p.P99 < time.Second, "p99=%s (n=%d) enquanto os nomes eram preenchidos a %d/s", ms(p.P99), p.N, fl.rateCfg)
	} else {
		add("p99 evento->delta DURANTE o preenchimento de nomes < 1 s", false, "sem amostras na fase de preenchimento")
	}
	add("preenchimento de nomes completo e correto (callerid de cada ramal)", fl.Complete && fl.EndCorrect == fl.Total && fl.Total > 0, "%d corretos de %d ao fim; completo em %s desde o daemon pronto", fl.EndCorrect, fl.Total, ms(fl.DoneAfterBoot))
	d := r.Drop
	add("queda do AMI: nenhum ramal 'unregistered' (snapshots com ami_connected=false)", d.Ran && d.SnapshotsWhileDown > 0 && d.StaleSnapshots == 0 && !d.Regressed && d.MaxUnregWhileDown == 0, "%d snapshots; %d ainda com estado antigo; max unregistered=%d", d.SnapshotsWhileDown, d.StaleSnapshots, d.MaxUnregWhileDown)
	add("queda do AMI: todos 'unknown', sem chamadas", d.Ran && d.SnapshotsWhileDown > 0 && d.StaleSnapshots == 0 && !d.Regressed && d.MinUnknownWhileDown == r.SnapIdle.Exts && d.MaxCallsWhileDown == 0, "min unknown=%d de %d, max chamadas=%d, tudo 'unknown' apos %s", d.MinUnknownWhileDown, r.SnapIdle.Exts, d.MaxCallsWhileDown, ms(d.ConsistentAfter))
	add("queda do AMI: epoch novo na queda e outro na volta", d.EpochDown != "" && d.EpochDown != d.EpochBefore && d.EpochUp != "" && d.EpochUp != d.EpochDown, "%s -> %s -> %s", d.EpochBefore, d.EpochDown, d.EpochUp)
	add("queda do AMI: cliente WS recebeu reload e voltou ao ar", len(d.Reloads) >= 1 && d.WSConvergedAfter, "reloads=%v fechamentos=%v ressincronizacoes=%d", d.Reloads, d.CloseCodes, d.Resyncs)
	add("queda do AMI: snapshot refeito (ramais registrados de volta)", d.ExtsAfter == r.SnapIdle.Exts && d.RegisteredAfter > 0, "%d registrados de %d", d.RegisteredAfter, d.ExtsAfter)
	ok, tot := 0, len(r.Rows)
	for _, w := range r.Rows {
		if w.OK {
			ok++
		}
	}
	add("'com quem fala' correto em cada passo de cada cenario", tot > 0 && ok == tot, "%d/%d passos", ok, tot)
	rok := 0
	for _, w := range r.Restore {
		if w.OK {
			rok++
		}
	}
	c = append(c, criterion{"chamadas em andamento restauradas corretamente pelo snapshot apos reconexao (tocando, atendida, transferida)", len(r.Restore) > 0 && rok == len(r.Restore), fmt.Sprintf("%d/%d cenario x rodada", rok, len(r.Restore)), true})
	add("log do simulador so com actions da allowlist", r.Actions.OnlyAllow && len(r.Actions.Counts) > 0, "violacoes=%v", r.Actions.Violations)
	for _, ch := range r.Checks {
		add(ch.Name, ch.OK, "%s", ch.Detail)
	}
	return c
}

// failed diz se algum criterio dos que sao decididos no Windows falhou.
func failed(r *Result) bool {
	if r.Err != "" {
		return true
	}
	for _, c := range criteria(r) {
		if !c.OK && !c.Soft {
			return true
		}
	}
	return false
}

func writeReport(path string, o opts, rs []*Result, daemonErr string) error {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	osName := map[string]string{"windows": "Windows", "linux": "Linux"}[runtime.GOOS]
	if osName == "" {
		osName = runtime.GOOS
	}
	w("# Relatorio de carga: Painel de Ramais, Fase 1\n\n")
	w("Gerado por `cmd/loadtest` em %s. **Medido no %s** (%s/%s, %s).\n\n", time.Now().Format("2006-01-02 15:04"), osName, runtime.GOOS, runtime.GOARCH, runtime.Version())
	w("Oraculo: `cmd/amisim` embutido no mesmo processo do cliente WebSocket (mesmo relogio). O simulador foi escrito a partir do protocolo AMI do Asterisk 13/16/18 e de `docs/CONTRATOS.md`, sem importar `internal/ami` nem `internal/state`.\n\n")
	if daemonErr != "" {
		w("## PENDENTE: o teste de carga nao rodou\n\n%s\n\nNenhum numero foi estimado ou inventado. Para gerar este relatorio:\n\n```\ngo build -o painel-ramais ./cmd/painel-ramais\ngo run ./cmd/loadtest -daemon ./painel-ramais\n```\n", daemonErr)
		return os.WriteFile(path, []byte(b.String()), 0o644)
	}
	var modes []string
	for _, r := range rs {
		modes = append(modes, r.Mode)
	}
	w("## Parametros desta execucao\n\n")
	w("- Daemon: `%s` (%s; versao %s)\n", o.daemon, o.daemonInfo, firstNonEmpty(rs, func(r *Result) string { return r.DaemonVersion }))
	w("- Ramais: %d; chamadas simultaneas alvo: %d (conversa media %s); regime medido por %s; rajada: %d desregistros; AMI fora por %s\n", o.exts, o.calls, o.talk, o.duration, o.burst, o.dropFor)
	w("- Asterisk simulado: %s. Daemon em `technology: auto` nos dois modos.\n", o.asterisk)
	w("- Daemon rodou como processo separado (binario), falando com o simulador por TCP (AMI) e com o cliente por HTTP e WebSocket; o teste nao importa nada de `internal/` do daemon.\n\n")

	w("## Resumo dos criterios de aceite\n\n")
	for _, r := range rs {
		w("### %s\n\n", r.Mode)
		if r.Err != "" {
			w("**ERRO FATAL**: %s\n\n", r.Err)
		}
		w("| Criterio | Resultado | Medido |\n|---|---|---|\n")
		for _, c := range criteria(r) {
			v := verdict(c.OK)
			if c.Soft && !c.OK {
				v = "**RESSALVA**"
			}
			w("| %s | %s | %s |\n", c.Name, v, c.Val)
		}
		m := r.Mem
		w("| memoria sem crescimento continuo | **NAO CONCLUSIVO no Windows** | %.2f MiB/min em %s de regime (exige 30 min em Linux) |\n\n", m.SlopeMiBPerMin, ms(m.SteadyDur))
	}

	if len(capResults) > 0 {
		totOK, tot, viol := 0, 0, 0
		for _, c := range capResults {
			ok, n := c.okCount()
			totOK, tot, viol = totOK+ok, tot+n, viol+len(c.Violations)
		}
		w("### Capturas reproduzidas\n\n| Criterio | Resultado | Medido |\n|---|---|---|\n")
		w("| \"com quem fala\" correto em cada ponto de cada captura (sinteticas) | %s | %d/%d pontos em %d capturas |\n", verdict(totOK == tot && tot > 0), totOK, tot, len(capResults))
		w("| log do simulador (reproducao) so com actions da allowlist | %s | violacoes=%d |\n\n", verdict(viol == 0), viol)
	}

	w("## Latencia evento -> delta (WebSocket)\n\n")
	w("Da gravacao do evento AMI no socket do simulador ate o ramal aparecer no estado entregue pelo WebSocket com o estado esperado (ou um posterior, se coalescido). Lote de 250 ms do daemon incluido.\n\n")
	t := newTable("Fase", modes)
	for _, ph := range []string{"isolada", "carga", "carga-pos", "rajada", "restauracao", "queda"} {
		ph := ph
		for _, q := range []struct {
			n string
			f func(Stats) time.Duration
		}{{"p50", func(s Stats) time.Duration { return s.P50 }}, {"p99", func(s Stats) time.Duration { return s.P99 }}, {"max", func(s Stats) time.Duration { return s.Max }}} {
			q := q
			t.row(fmt.Sprintf("%s %s", ph, q.n), rs, func(r *Result) string {
				s, ok := r.Lat[ph]
				if !ok {
					return "sem amostras"
				}
				return ms(q.f(s))
			})
		}
		t.row(ph+" amostras", rs, func(r *Result) string { return fmt.Sprint(r.Lat[ph].N) })
	}
	t.row("**todas (exceto queda) p50**", rs, func(r *Result) string { return ms(r.LatAll.P50) })
	t.row("**todas (exceto queda) p99**", rs, func(r *Result) string { return ms(r.LatAll.P99) })
	w("%s", t)
	w("Fases: `isolada` = um evento por vez (cenarios passo a passo); `carga` = regime com %d chamadas; `rajada` = %d desregistros de uma vez; `restauracao` = os mesmos reregistrando; `queda` = o que chegou na janela depois da volta do AMI (inclui o tempo de reconexao e de ressincronizacao; fora dos criterios).\n\n", o.calls, o.burst)

	w("## Snapshot (GET /api/snapshot)\n\n")
	t = newTable("Medida", modes)
	t.row("ocioso: ramais / chamadas", rs, func(r *Result) string { return fmt.Sprintf("%d / %d", r.SnapIdle.Exts, r.SnapIdle.Calls) })
	t.row("ocioso: tamanho cru", rs, func(r *Result) string { return kib(r.SnapIdle.Raw) })
	t.row("ocioso: tamanho com gzip", rs, func(r *Result) string { return kib(r.SnapIdle.Gz) })
	t.row("ocioso: tempo da requisicao (sem gzip)", rs, func(r *Result) string { return ms(r.SnapIdle.Fetch) })
	t.row("sob carga: ramais / chamadas", rs, func(r *Result) string { return fmt.Sprintf("%d / %d", r.SnapLoad.Exts, r.SnapLoad.Calls) })
	t.row("sob carga: tamanho cru", rs, func(r *Result) string { return kib(r.SnapLoad.Raw) })
	t.row("sob carga: tamanho com gzip", rs, func(r *Result) string { return kib(r.SnapLoad.Gz) })
	t.row("sob carga: tempo da requisicao (sem gzip)", rs, func(r *Result) string { return ms(r.SnapLoad.Fetch) })
	t.row("ramais com `name` preenchido (ocioso)", rs, func(r *Result) string { return fmt.Sprintf("%d de %d", r.NamesFilled, r.SnapIdle.Exts) })
	t.row("ramais com `user_agent` preenchido (ocioso)", rs, func(r *Result) string { return fmt.Sprintf("%d de %d", r.UAFilled, r.SnapIdle.Exts) })
	t.row("ramais com `name` preenchido (no fim do teste)", rs, func(r *Result) string { return fmt.Sprintf("%d de %d", r.NamesFinal, r.SnapIdle.Exts) })
	t.row("ramais com `user_agent` preenchido (no fim do teste)", rs, func(r *Result) string { return fmt.Sprintf("%d de %d", r.UAFinal, r.SnapIdle.Exts) })
	w("%s", t)

	w("## Deltas no WebSocket\n\n")
	t = newTable("Fase / medida", modes)
	for _, ph := range []string{"carga", "carga-pos", "rajada", "restauracao"} {
		ph := ph
		t.row(ph+": deltas recebidos", rs, func(r *Result) string { return fmt.Sprint(r.Deltas[ph].N) })
		t.row(ph+": tamanho medio", rs, func(r *Result) string { return fmt.Sprintf("%.0f B", r.Deltas[ph].AvgBytes) })
		t.row(ph+": p50 / p99 / max", rs, func(r *Result) string {
			d := r.Deltas[ph]
			return fmt.Sprintf("%d / %d / %d B", d.P50, d.P99, d.Max)
		})
		t.row(ph+": ramais por delta (media)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Deltas[ph].AvgExts) })
		t.row(ph+": bytes por ramal", rs, func(r *Result) string { return fmt.Sprintf("%.0f B", r.Deltas[ph].BytesPerExt) })
		t.row(ph+": vazao no WS", rs, func(r *Result) string { return fmt.Sprintf("%.1f KiB/s", r.Deltas[ph].BytesPerS/1024) })
	}
	w("%s", t)

	w("## Preenchimento de nomes em segundo plano (%d consultas/s, `ami.name_fill_per_second`)\n\n", o.fillRate)
	w("O daemon pergunta o callerid de cada ramal (`PJSIPShowEndpoint` ou `SIPshowpeer`) depois do snapshot, sem rajada. Aqui o daemon comecou com o banco vazio (sem cache de nomes), e a carga de chamadas e as demais medidas rodaram enquanto os nomes eram preenchidos.\n\n")
	t = newTable("Medida", modes)
	t.row("nomes preenchidos no inicio do regime sob carga", rs, func(r *Result) string { return fmt.Sprintf("%d de %d", r.Fill.StartFilled, r.Fill.Total) })
	t.row("nomes preenchidos e corretos ao fim do teste", rs, func(r *Result) string { return fmt.Sprintf("%d de %d", r.Fill.EndCorrect, r.Fill.Total) })
	t.row("preenchimento completo", rs, func(r *Result) string { return fmt.Sprint(r.Fill.Complete) })
	t.row("tempo do daemon pronto ate o ultimo nome", rs, func(r *Result) string { return ms(r.Fill.DoneAfterBoot) })
	t.row("taxa medida (nomes/s, entre as amostras)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Fill.Rate) })
	t.row("duracao do regime sob carga (preenchimento + regime com nomes completos)", rs, func(r *Result) string { return ms(r.Fill.RegimeDur) })
	t.row("latencia evento->delta durante o preenchimento: p50 / p99 / max", rs, func(r *Result) string {
		s := r.Lat["carga"]
		return fmt.Sprintf("%s / %s / %s (n=%d)", ms(s.P50), ms(s.P99), ms(s.Max), s.N)
	})
	t.row("latencia evento->delta depois do preenchimento: p50 / p99 / max", rs, func(r *Result) string {
		s := r.Lat["carga-pos"]
		return fmt.Sprintf("%s / %s / %s (n=%d)", ms(s.P50), ms(s.P99), ms(s.Max), s.N)
	})
	t.row("CPU do daemon durante / depois do preenchimento (% de 1 nucleo, Windows)", rs, func(r *Result) string { return fmt.Sprintf("%.1f%% / %.1f%%", r.Mem.CPUFill, r.Mem.CPUPost) })
	t.row("RSS ao fim do preenchimento / no fim do regime (MiB, Windows)", rs, func(r *Result) string { return fmt.Sprintf("%.1f / %.1f", r.Mem.RSSFillEnd, r.Mem.LoadEnd) })
	w("%s", t)
	for _, r := range rs {
		if n := len(r.Fill.Samples); n > 0 {
			w("Progresso (%s), nomes preenchidos por tempo desde o daemon pronto:", r.Mode)
			step := n/12 + 1
			for i := 0; i < n; i += step {
				w(" %s=%d;", ms(r.Fill.Samples[i].At), r.Fill.Samples[i].Filled)
			}
			last := r.Fill.Samples[n-1]
			w(" %s=%d.\n\n", ms(last.At), last.Filled)
		}
	}

	if len(capResults) > 0 {
		w("## \"Com quem fala\" sobre capturas reproduzidas (`amisim -replay`)\n\n")
		w("Cada captura de `testdata/capturas` e reproduzida pelo simulador (respostas na ordem de fila, eventos no tempo gravado dividido por %g) para o daemon real, que roda em `technology: auto`. Em cada ponto do arquivo o estado do cliente WebSocket tem de igualar o oraculo (`.esperado.json`): registro, dispositivo, nome e, por chamada, id, direcao, estado e com quem fala. **Todas as capturas atuais sao sinteticas** (ver `testdata/capturas/README.md`); capturas reais virao da homologacao.\n\n", o.replaySpeed)
		w("| Captura | Tecnologia | Pontos ok / total | Actions fora da allowlist | Erro |\n|---|---|---|---|---|\n")
		totOK, tot := 0, 0
		for _, c := range capResults {
			ok, n := c.okCount()
			totOK, tot = totOK+ok, tot+n
			w("| %s | %s | %d / %d | %v | %s |\n", c.Name, c.Mode, ok, n, c.Violations, firstStr(c.Err, "nenhum"))
		}
		w("| **total** | | **%d / %d** | | |\n\n", totOK, tot)
		for _, c := range capResults {
			for _, row := range c.Rows {
				if !row.OK {
					w("- FALHA `%s` em `%s` (%d quadros): %s\n", c.Name, row.Label, row.Frames, row.Detail)
				}
			}
		}
		w("\n")
	}

	w("## Rajada de desregistros e queda do AMI\n\n")
	t = newTable("Medida", modes)
	t.row("rajada: ramais desregistrados", rs, func(r *Result) string { return fmt.Sprint(r.Burst.N) })
	t.row("rajada: tempo para o simulador emitir tudo", rs, func(r *Result) string { return ms(r.Burst.Emit) })
	t.row("rajada: tempo ate o ultimo ramal aparecer 'unregistered' no cliente", rs, func(r *Result) string { return ms(r.Burst.Converge) })
	t.row("restauracao: tempo ate todos 'registered' no cliente", rs, func(r *Result) string { return ms(r.Burst.RestoreAfter) })
	t.row("queda: daemon acusou ami.connected=false em", rs, func(r *Result) string { return ms(r.Drop.DetectDown) })
	t.row("queda: snapshots lidos com AMI fora", rs, func(r *Result) string { return fmt.Sprint(r.Drop.SnapshotsWhileDown) })
	t.row("queda: ramais 'unregistered' (maximo visto)", rs, func(r *Result) string { return fmt.Sprint(r.Drop.MaxUnregWhileDown) })
	t.row("queda: ramais 'unknown' (minimo visto)", rs, func(r *Result) string { return fmt.Sprint(r.Drop.MinUnknownWhileDown) })
	t.row("queda: chamadas visiveis (maximo)", rs, func(r *Result) string { return fmt.Sprint(r.Drop.MaxCallsWhileDown) })
	t.row("queda: daemon reconectou apos a volta do AMI em", rs, func(r *Result) string { return ms(r.Drop.RecoverAfter) })
	t.row("queda: reloads recebidos / codigos de fechamento", rs, func(r *Result) string { return fmt.Sprintf("%v / %v", r.Drop.Reloads, r.Drop.CloseCodes) })
	t.row("queda: snapshots com ami_connected=false e ramais ainda com estado antigo", rs, func(r *Result) string { return fmt.Sprint(r.Drop.StaleSnapshots) })
	t.row("queda: epoch novo publicado no /api/health apos o AMI cair (resolucao 50 ms)", rs, func(r *Result) string { return ms(r.Drop.EpochVisibleAfter) })
	t.row("queda: 1o snapshot todo 'unknown' (limite superior: cada leitura demora ~2 requisicoes)", rs, func(r *Result) string { return ms(r.Drop.ConsistentAfter) })
	t.row("queda: snapshot passou a ami_connected=false apos o health acusar (snapshots velhos antes disso)", rs, func(r *Result) string {
		return fmt.Sprintf("%s (%d snapshots antes)", ms(r.Drop.FlagFlipAfter), r.Drop.FlagStale)
	})
	t.row("queda: erro", rs, func(r *Result) string { return firstStr(r.Drop.Err, "nenhum") })
	w("%s", t)

	for _, r := range rs {
		if len(r.Drop.Samples) > 0 {
			w("Snapshots lidos durante a queda (%s), a cada ~200 ms desde a primeira deteccao:\n\n| t | registered | unregistered | unknown | chamadas | epoch |\n|---|---|---|---|---|---|\n", r.Mode)
			for _, sm := range r.Drop.Samples {
				w("| %s | %d | %d | %d | %d | %s |\n", ms(sm.At), sm.Registered, sm.Unreg, sm.Unknown, sm.Calls, sm.Epoch)
			}
			w("\n")
		}
	}

	w("## Cenarios de chamada: \"com quem fala\"\n\n")
	w("Cada passo do cenario muda a verdade no simulador, e o teste espera o estado do cliente WebSocket igualar a verdade (registro, dispositivo, e por chamada: id, direcao, estado, outro lado). Passos: %d cenarios.\n\n", len(amisim.Kinds))
	t = newTable("Cenario (passos ok / total)", modes)
	for _, k := range amisim.Kinds {
		k := k
		t.row(k, rs, func(r *Result) string {
			ok, n := 0, 0
			for _, row := range r.Rows {
				if row.Scenario == k {
					n++
					if row.OK {
						ok++
					}
				}
			}
			return fmt.Sprintf("%d / %d", ok, n)
		})
	}
	w("%s", t)
	for _, r := range rs {
		for _, row := range r.Rows {
			if !row.OK {
				w("- FALHA (%s) `%s` passo `%s`: %s\n", r.Mode, row.Scenario, row.Step, row.Detail)
			}
		}
	}
	w("\n")
	t = newTable("Convergencia por passo (isolado)", modes)
	t.row("p50", rs, func(r *Result) string { return ms(rowStats(r).P50) })
	t.row("p99", rs, func(r *Result) string { return ms(rowStats(r).P99) })
	w("%s", t)

	w("## Snapshot com chamadas em andamento (reconexao do AMI)\n\n")
	w("Todos os cenarios parados no mesmo ponto, conexao AMI derrubada, o daemon reconecta e refaz o snapshot (`CoreShowChannels` + `DeviceStateList`); o estado restaurado e comparado com a verdade. `hold` nao e comparado como `hold`: o `CoreShowChannels` nao informa espera (limite do protocolo AMI).\n\n")
	t = newTable("Cenario / rodada", modes)
	for _, k := range amisim.Kinds {
		for _, rd := range []string{"tocando", "atendida", "apos transferencia/hold/otimizacao"} {
			k, rd := k, rd
			t.row(k+" / "+rd, rs, func(r *Result) string {
				for _, row := range r.Restore {
					if row.Scenario == k && row.Round == rd {
						if row.OK {
							return "ok"
						}
						return "divergiu: " + row.Detail
					}
				}
				return "n/a"
			})
		}
	}
	w("%s", t)

	w("## Estado final x verdade do simulador\n\n")
	t = newTable("Medida", modes)
	t.row("ramais comparados", rs, func(r *Result) string { return fmt.Sprint(r.Final.Compared) })
	t.row("divergentes no snapshot", rs, func(r *Result) string { return fmt.Sprint(r.Final.SnapDiffExts) })
	t.row("divergentes no estado do cliente WS", rs, func(r *Result) string { return fmt.Sprint(r.Final.WSDiffExts) })
	t.row("chamadas restantes no fim (deve ser 0)", rs, func(r *Result) string { return fmt.Sprint(r.Final.CallsAtEnd) })
	t.row("lacunas de versao no WS", rs, func(r *Result) string { return fmt.Sprint(r.Final.Gaps) })
	t.row("estados fugazes so com dispositivo/chamadas de instantes diferentes (aceitos)", rs, func(r *Result) string { return fmt.Sprint(r.Final.Torn) })
	t.row("estados fugazes errados (fora da janela de ressincronizacao)", rs, func(r *Result) string { return fmt.Sprint(r.Final.Unexpected) })
	t.row("estados inesperados dentro da janela de 3 s apos ressincronizar", rs, func(r *Result) string { return fmt.Sprint(r.Final.Graced) })
	t.row("estados inesperados herdados de chamadas restauradas por snapshot (ver secao anterior)", rs, func(r *Result) string { return fmt.Sprint(r.Final.Tainted) })
	t.row("mutacoes sem estado correspondente visto ate o fim", rs, func(r *Result) string { return fmt.Sprint(r.Final.Pending) })
	w("%s", t)
	for _, r := range rs {
		for _, d := range r.Final.SnapDiffs {
			w("- (%s, snapshot) ramal %s: %s\n", r.Mode, d.Ext, d.What)
		}
		for _, d := range r.Final.WSDiffs {
			w("- (%s, WS) ramal %s: %s\n", r.Mode, d.Ext, d.What)
		}
		for _, u := range r.Final.UnexpSamples {
			w("- (%s) inesperado: %s\n", r.Mode, u)
		}
	}
	w("\n")

	w("## Actions recebidas pelo simulador (log do endpoint HTTP exclusivo `/actions`)\n\n")
	t = newTable("Action", modes)
	seen := map[string]bool{}
	for _, r := range rs {
		for a := range r.Actions.Counts {
			seen[a] = true
		}
	}
	var names []string
	for a := range seen {
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		a := a
		t.row(a, rs, func(r *Result) string { return fmt.Sprint(r.Actions.Counts[a]) })
	}
	t.row("**fora da allowlist**", rs, func(r *Result) string { return fmt.Sprintf("%v", r.Actions.Violations) })
	w("%s", t)

	w("## CPU e memoria do daemon (%s)\n\n", osName)
	if runtime.GOOS != "linux" {
		w("> **Medido no %s, nao em Linux.** Working set e tempo de CPU lidos do processo do daemon (build para %s). Valem como ordem de grandeza e para deteccao de vazamento grosseiro; o criterio de memoria da especificacao (sem crescimento continuo em 30 min) e a medicao de CPU so valem em Linux (WSL2, container ou Rocky), com `-duration 30m`.\n\n", osName, runtime.GOOS)
	}
	t = newTable("Medida", modes)
	t.row("amostras de RSS (a cada 2 s)", rs, func(r *Result) string { return fmt.Sprint(r.Mem.Samples) })
	t.row("RSS logo apos subir (MiB)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Mem.Boot) })
	t.row("RSS apos snapshot/cenarios ociosos (MiB)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Mem.AfterSnap) })
	t.row("RSS no inicio do regime (MiB)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Mem.LoadStart) })
	t.row("RSS no fim do regime (MiB)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Mem.LoadEnd) })
	t.row("pico de RSS (MiB)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Mem.Peak) })
	t.row("inclinacao do RSS no regime (MiB/min, sem o primeiro terco)", rs, func(r *Result) string {
		return fmt.Sprintf("%.2f em %s", r.Mem.SlopeMiBPerMin, ms(r.Mem.SteadyDur))
	})
	t.row("CPU do daemon no regime inteiro (% de 1 nucleo)", rs, func(r *Result) string { return fmt.Sprintf("%.1f%%", r.Mem.CPUPct) })
	t.row("CPU durante o preenchimento de nomes (% de 1 nucleo)", rs, func(r *Result) string { return fmt.Sprintf("%.1f%%", r.Mem.CPUFill) })
	t.row("CPU depois do preenchimento, nomes completos (% de 1 nucleo)", rs, func(r *Result) string { return fmt.Sprintf("%.1f%%", r.Mem.CPUPost) })
	t.row("RSS quando o preenchimento terminou (MiB)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Mem.RSSFillEnd) })
	t.row("erro de leitura de processo", rs, func(r *Result) string { return firstStr(r.Mem.Err, "nenhum") })
	w("%s", t)

	w("## /api/health durante o teste\n\n")
	t = newTable("Medida", modes)
	t.row("amostras", rs, func(r *Result) string { return fmt.Sprint(r.Health.Samples) })
	t.row("eventos/s (maximo da media de 60 s)", rs, func(r *Result) string { return fmt.Sprintf("%.1f", r.Health.MaxEventsPS) })
	t.row("fila interna (maximo)", rs, func(r *Result) string { return fmt.Sprint(r.Health.MaxQueue) })
	t.row("lag de processamento (maximo)", rs, func(r *Result) string { return fmt.Sprintf("%.0f ms", r.Health.MaxLagMs) })
	t.row("estouros de fila", rs, func(r *Result) string { return fmt.Sprint(r.Health.Overflows) })
	t.row("reconexoes AMI", rs, func(r *Result) string { return fmt.Sprint(r.Health.Reconnects) })
	t.row("status / dependencias na ultima amostra", rs, func(r *Result) string { return r.Health.LastStatus + " / " + r.Health.DepsSummary })
	t.row("Asterisk lido pelo daemon", rs, func(r *Result) string { return r.AsteriskVersion })
	t.row("tecnologia detectada em `auto`", rs, func(r *Result) string { return r.DetectedTech })
	t.row("tempo ate o daemon ficar pronto (10.000 ramais)", rs, func(r *Result) string { return ms(r.Boot) })
	w("%s", t)

	w("## O que so vale em Linux\n\n")
	w("- Memoria: o criterio \"sem crescimento continuo\" pede 30 min de carga em Linux; aqui foi uma execucao curta no %s e a inclinacao acima nao e conclusiva.\n", osName)
	w("- CPU do daemon: medida pelo tempo de CPU do processo; o agendador e o custo de syscalls do Windows diferem do Linux. Refazer em Linux.\n")
	w("- `rpm -i/-U/-e`, systemd, SELinux, Apache e `instalar.py`: fora do alcance deste teste (criterios C5, C6, A1 a A9).\n")
	w("- Reproduzir em Linux (WSL2/container/Rocky), com o simulador e o cliente no mesmo processo:\n\n```\nGOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o painel-ramais ./cmd/painel-ramais\nGOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o loadtest ./cmd/loadtest\n./loadtest -daemon ./painel-ramais -duration 30m -out docs/relatorio-carga-linux.md\n```\n\n")

	w("## Notas do oraculo\n\n")
	for _, r := range rs {
		n := r.Actions.Counts["SIPshowpeer"] + r.Actions.Counts["PJSIPShowEndpoint"]
		if n > 0 {
			w("- (%s) `name` vem de `PJSIPShowEndpoint` (pjsip) ou `SIPshowpeer` (sip), em segundo plano e com taxa limitada (%d/s configurados): %d consultas no teste inteiro. O snapshot ocioso foi lido logo depois de o daemon subir e por isso mostra preenchimento parcial; veja a secao do preenchimento de nomes.\n", r.Mode, o.fillRate, n)
		}
	}
	for _, r := range rs {
		if r.Boot > 0 && r.NamesFinal == 0 {
			w("- (%s) `name` terminou vazio em todos os ramais: o preenchimento em segundo plano nao entregou nenhum nome.\n", r.Mode)
		}
		if r.Health.LastStatus == "degraded" && strings.Contains(r.Health.DepsSummary, "=fail") {
			w("- (%s) `/api/health` ficou `degraded` por `deps` em `fail` (%s): esperado fora de um Issabel (nao existem manager.conf nem conf do Apache nesta maquina). Nao afeta o resultado do teste.\n", r.Mode, r.Health.DepsSummary)
		}
	}
	for _, r := range rs {
		for _, n := range r.Notes {
			w("- (%s) %s\n", r.Mode, n)
		}
	}
	w("- Duracao total por modo: ")
	for _, r := range rs {
		w("%s %s; ", r.Mode, ms(r.Wall))
	}
	w("\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func rowStats(r *Result) Stats {
	var d []time.Duration
	for _, row := range r.Rows {
		if row.OK {
			d = append(d, row.Conv)
		}
	}
	return statsOf(d)
}

func firstStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstNonEmpty(rs []*Result, f func(*Result) string) string {
	for _, r := range rs {
		if s := f(r); s != "" {
			return s
		}
	}
	return "desconhecida"
}
