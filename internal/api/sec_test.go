package api

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"painel-ramais/internal/auth"
	"painel-ramais/internal/model"
)

// Auditoria M3: 40 logins paralelos com senha errada. A tentativa e reservada antes do
// bcrypt, entao no maximo 5 chegam a ser comparadas (401); o resto leva 429.
func TestLoginParallelWrongPasswordHitsLimitOnce(t *testing.T) {
	e := newEnv(t)
	auth.BcryptCost = 12 // hash real do contrato: a comparacao custa centenas de ms
	e.addUser("emergencia", "senha-correta-123")
	auth.BcryptCost = bcrypt.MinCost
	codes := parallelLogins(e, 40, `{"username":"emergencia","password":"errada-xxxxxx"}`)
	t.Logf("40 tentativas paralelas: %v", codes)
	if codes[401] > 5 || codes[401]+codes[429] != 40 {
		t.Errorf("codigos: %v (no maximo 5x401, o resto 429)", codes)
	}
	// Consome o que sobrou da cota (a semaforo recusa sem contar); o total de 401 nunca passa de 5.
	total := codes[401]
	for i := 0; i < 5; i++ {
		res := e.do("POST", "/api/login", `{"username":"emergencia","password":"errada-xxxxxx"}`, jsonHdr)
		res.Body.Close()
		if res.StatusCode == 401 {
			total++
		}
	}
	if total != 5 {
		t.Errorf("total de 401 = %d, queria exatamente 5", total)
	}
	// A conta segue bloqueada ate a janela passar, mesmo com a senha certa.
	e.expect(e.do("POST", "/api/login", `{"username":"emergencia","password":"senha-correta-123"}`, jsonHdr), 429, "too_many_attempts")
}

func parallelLogins(e *env, n int, body string) map[int]int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := e.do("POST", "/api/login", body, jsonHdr)
			res.Body.Close()
			mu.Lock()
			codes[res.StatusCode]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return codes
}

// Teto de 4 bcrypt simultaneos: com todos os lugares ocupados, o login responde 429
// sem gastar tentativa do usuario; liberado, o mesmo login passa.
func TestLoginBcryptConcurrencyCap(t *testing.T) {
	e := newEnv(t)
	e.addUser("emergencia", "uma-senha-longa")
	for i := 0; i < maxConcurrentLogins; i++ {
		e.srv.loginSem <- struct{}{}
	}
	ok := `{"username":"emergencia","password":"uma-senha-longa"}`
	for i := 0; i < 10; i++ {
		e.expect(e.do("POST", "/api/login", ok, jsonHdr), 429, "too_many_attempts")
	}
	for i := 0; i < maxConcurrentLogins; i++ {
		<-e.srv.loginSem
	}
	e.expect(e.do("POST", "/api/login", ok, jsonHdr), 200, "")
}

// Auditoria L4: nosniff em toda resposta.
func TestNosniffHeader(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/health", "/api/snapshot", "/nada"} {
		res := e.do("GET", p, "", nil)
		res.Body.Close()
		if res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s sem nosniff", p)
		}
	}
}

// Auditoria L4: o ReadTimeout do http.Server nao pode derrubar o WebSocket.
func TestWSSurvivesServerReadTimeout(t *testing.T) {
	e := newEnv(t)
	cookie := e.login()
	e.ts.Close()
	e.ts = httptest.NewUnstartedServer(e.srv.Handler())
	e.ts.Config.ReadTimeout = 300 * time.Millisecond
	e.ts.Start()
	e.reset("100-1", 1, ext("2001", "Suporte", model.RegRegistered))
	c, _, err := dialWS(t, e, cookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, c, "100-1", 1)
	time.Sleep(900 * time.Millisecond) // 3x o ReadTimeout
	e.batch("100-1", 1, 2, ext("2001", "Suporte", model.RegUnreachable))
	if f := readFrame(t, c); f.Type != "delta" || f.To != 2 {
		t.Fatalf("WS caiu com o ReadTimeout: %+v", f)
	}
}

// useRealClock liga o servidor ao relogio real com sessao curta (decisao 5: inatividade).
func useRealClock(e *env, ttl, renewEvery time.Duration) {
	e.srv.Now = time.Now
	e.srv.SessionTTL, e.srv.RenewEvery = ttl, renewEvery
}

// Tela de NOC: com o WebSocket ativo a sessao se renova sozinha e nunca expira.
func TestWSKeepsSessionAliveWhileConnected(t *testing.T) {
	e := newEnv(t)
	useRealClock(e, 3*time.Second, 300*time.Millisecond) // a sessao guarda segundos inteiros
	cookie := e.login()
	e.reset("100-1", 1, ext("2001", "Suporte", model.RegRegistered))
	c, _, err := dialWS(t, e, cookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, c, "100-1", 1)
	time.Sleep(7 * time.Second) // mais de 2x a inatividade permitida
	e.batch("100-1", 1, 2, ext("2001", "Suporte", model.RegUnreachable))
	if f := readFrame(t, c); f.Type != "delta" || f.To != 2 {
		t.Fatalf("WS deveria seguir vivo: %+v", f)
	}
	e.expect(e.do("GET", "/api/groups", "", cookie), 200, "") // e a sessao continua valida no REST
}

// Sem renovacao, a sessao vence no meio da conexao: aviso tipado e 4001.
func TestWSWarnsBeforeClosingExpiredSession(t *testing.T) {
	e := newEnv(t)
	useRealClock(e, 2*time.Second, time.Hour)
	cookie := e.login()
	e.reset("100-1", 1)
	c, _, _ := dialWS(t, e, cookie, nil)
	hello(t, c, "100-1", 1)
	if f := readFrameWithin(t, c, 5*time.Second); f.Type != "session_expired" || f.Reason != "expired" {
		t.Fatalf("aviso: %+v", f)
	}
	expectClose(t, c, 4001)
}

// Uso do REST tambem conta como atividade: a sessao desliza depois da metade do prazo.
func TestRESTSlidesSession(t *testing.T) {
	e := newEnv(t)
	useRealClock(e, 4*time.Second, time.Hour)
	cookie := e.login()
	time.Sleep(2500 * time.Millisecond)
	e.expect(e.do("GET", "/api/groups", "", cookie), 200, "") // passou da metade: renova
	time.Sleep(2500 * time.Millisecond)                       // 5 s desde o login, acima do prazo original
	e.expect(e.do("GET", "/api/groups", "", cookie), 200, "")
	time.Sleep(5 * time.Second) // inatividade real
	e.expect(e.do("GET", "/api/groups", "", cookie), 401, "unauthorized")
}

// O prazo do WebSocket usa o relogio do servidor (injetavel), nao o de parede: com o
// relogio do servidor num passado distante a conexao nao pode expirar na hora. (Regressao:
// os testes de WS quebraram quando a hora real passou da data fixa das fixtures.)
func TestWSDeadlineUsesServerClock(t *testing.T) {
	e := newEnv(t)
	e.now = time.Date(1999, 1, 1, 12, 0, 0, 0, time.UTC) // bem antes da hora real
	cookie := e.login()
	e.reset("100-1", 1, ext("2001", "Suporte", model.RegRegistered))
	c, _, err := dialWS(t, e, cookie, nil)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, c, "100-1", 1)
	time.Sleep(300 * time.Millisecond)
	e.batch("100-1", 1, 2, ext("2001", "Suporte", model.RegUnreachable))
	if f := readFrame(t, c); f.Type != "delta" {
		t.Fatalf("WS expirou pelo relogio de parede: %+v", f)
	}
}
