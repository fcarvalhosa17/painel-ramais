package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()
var now = time.Date(2026, 10, 13, 14, 30, 0, 0, time.UTC)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationAndDowngrade(t *testing.T) {
	d := t.TempDir()
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	var fk int
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if v != SchemaVersion || fk != 1 {
		t.Fatalf("user_version=%d foreign_keys=%d", v, fk)
	}
	s.db.Exec("PRAGMA user_version = 99")
	s.Close()
	if _, err := Open(ctx, d); err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("banco mais novo deve recusar: %v", err)
	}
}

func TestUsersAndSessions(t *testing.T) {
	s := open(t)
	if err := s.CreateUser(ctx, "Fabio", "$2a$hash", now); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, "fabio", "$2a$hash", now); !errors.Is(err, ErrUserExists) {
		t.Fatalf("duplicado (sem diferenciar maiusculas): %v", err)
	}
	if err := s.CreateUser(ctx, "x", "texto-puro", now); err == nil {
		t.Error("CHECK deve recusar hash que nao e bcrypt")
	}
	u, err := s.UserByName(ctx, "FABIO")
	if err != nil || u.Username != "Fabio" {
		t.Fatalf("%+v %v", u, err)
	}
	var h [32]byte
	h[0] = 1
	se := Session{IDHash: h, Kind: "password", UserID: &u.ID, Subject: "Fabio", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.CreateSession(ctx, se); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, h, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, h, now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Error("sessao vencida nao vale")
	}
	// token: sem user_id; password sem user_id viola o CHECK
	h2 := [32]byte{2}
	if err := s.CreateSession(ctx, Session{IDHash: h2, Kind: "token", Subject: "issabel:admin", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, Session{IDHash: [32]byte{3}, Kind: "password", Subject: "x", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err == nil {
		t.Error("password sem user_id deve violar o CHECK")
	}
	if err := s.SetPassword(ctx, "fabio", "$2a$novo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, h, now); !errors.Is(err, ErrNotFound) {
		t.Error("user passwd apaga as sessoes do usuario")
	}
	if _, err := s.GetSession(ctx, h2, now); err != nil {
		t.Error("sessao de token nao e afetada")
	}
	// del: cascade
	s.CreateSession(ctx, se)
	if err := s.DeleteUser(ctx, "fabio"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, h, now); !errors.Is(err, ErrNotFound) {
		t.Error("user del apaga sessoes por cascade")
	}
	if err := s.DeleteUser(ctx, "fabio"); !errors.Is(err, ErrNotFound) {
		t.Error("del de inexistente")
	}
	if n, _ := s.PurgeSessions(ctx, now.Add(2*time.Hour)); n != 1 {
		t.Errorf("purge removeu %d", n)
	}
}

func parse(t *testing.T, csv string) ([]GroupRow, error) {
	t.Helper()
	return ParseGroupsCSV(strings.NewReader(csv))
}

func TestParseCSV(t *testing.T) {
	ok := map[string]string{
		"virgula com cabecalho": "# comentario\ngrupo,ramal\nSuporte,2000-2099\nSuporte,2150\n\nVendas,3000-3999\n",
		"ponto e virgula":       "Suporte;2000-2099\nSuporte;2150\nVendas;3000-3999\n",
		"BOM e CRLF":            "\xef\xbb\xbfSuporte,2000-2099\r\nSuporte,2150\r\nVendas,3000-3999\r\n",
	}
	for name, in := range ok {
		rows, err := parse(t, in)
		if err != nil || len(rows) != 3 || rows[0].Name != "Suporte" || rows[0].End != 2099 || rows[1].Start != 2150 {
			t.Errorf("%s: %+v %v", name, rows, err)
		}
	}
	rows, _ := parse(t, "# c\ngrupo,ramal\nA,1\n")
	if rows[0].Line != 3 {
		t.Errorf("linha = %d, queria 3 (conta comentario e cabecalho)", rows[0].Line)
	}
	bad := map[string]string{
		"vazio":              "# so comentario\n",
		"faixa invertida":    "A,20-10\n",
		"nao numerico":       "A,abc\n",
		"campo a mais":       "A,1,2\n",
		"faixa em Sem grupo": "sem GRUPO,1-5\n",
		"nome vazio":         ",1\n",
		"sobreposicao":       "A,1-10\nB,10-20\n",
		"sobreposicao igual": "A,5\nB,5\n",
		"negativo":           "A,-5\n",
	}
	for name, in := range bad {
		if _, err := parse(t, in); err == nil {
			t.Errorf("%s: deveria rejeitar", name)
		}
	}
	_, err := parse(t, "A,1-10\nB,5-20\nC,x\n")
	if err == nil || !strings.Contains(err.Error(), "linha 3") || !strings.Contains(err.Error(), "linhas 1 e 2") {
		t.Errorf("mensagem deve citar linhas: %v", err)
	}
}

func TestImportGroups(t *testing.T) {
	s := open(t)
	rows, _ := parse(t, "Suporte,2000-2099\nSuporte,2150\nVendas,3000-3999\n")
	if err := s.ImportGroups(ctx, "t", rows, false, now); err != nil {
		t.Fatal(err)
	}
	// Sobreposicao com o que existe rejeita tudo (inclusive a linha valida).
	more, _ := parse(t, "Novo,5000\nOutro,2050-2060\n")
	err := s.ImportGroups(ctx, "t", more, false, now)
	if !errors.Is(err, ErrOverlap) || !strings.Contains(err.Error(), "linha 2") {
		t.Fatalf("sobreposicao: %v", err)
	}
	gs, _ := s.ListGroups(ctx)
	if len(gs) != 2 || gs[0].Name != "Suporte" || len(gs[0].Ranges) != 1 || gs[1].Name != "Vendas" {
		t.Fatalf("nada deve ter mudado: %+v", gs)
	}
	// replace troca tudo; o grupo que sumiu do arquivo some do banco.
	rep, _ := parse(t, "Diretoria,4000-4000\nsuporte,2000-2000\n")
	if err := s.ImportGroups(ctx, "t", rep, true, now); err != nil {
		t.Fatal(err)
	}
	gs, _ = s.ListGroups(ctx)
	if len(gs) != 2 || gs[0].Name != "Diretoria" || gs[1].Name != "suporte" {
		t.Fatalf("replace: %+v", gs)
	}
	rr, _ := s.GroupRanges(ctx)
	if len(rr) != 2 || rr[0].Start != 2000 || rr[0].Name != "suporte" {
		t.Fatalf("ranges ordenadas por inicio: %+v", rr)
	}
	// Mesmo CSV de novo no replace: mesmo resultado (idempotente para o instalar.py).
	if err := s.ImportGroups(ctx, "t", rep, true, now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(ctx, "t", "DIRETORIA", now); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(ctx, "t", "Diretoria", now); !errors.Is(err, ErrNotFound) {
		t.Error("del de inexistente")
	}
}

func TestTriggerGuardsOverlapAndImmutability(t *testing.T) {
	s := open(t)
	s.db.Exec("INSERT INTO groups(name, created_at) VALUES('A', 1)")
	if _, err := s.db.Exec("INSERT INTO group_ranges(group_id, range_start, range_end) VALUES(1, 1, 10)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO group_ranges(group_id, range_start, range_end) VALUES(1, 10, 12)"); err == nil {
		t.Error("trigger deve barrar sobreposicao")
	}
	if _, err := s.db.Exec("UPDATE group_ranges SET range_end = 99"); err == nil {
		t.Error("faixa e imutavel")
	}
	if _, err := s.db.Exec("INSERT INTO groups(name, created_at) VALUES('sem GRUPO', 1)"); err == nil {
		t.Error("CHECK do nome reservado")
	}
}

func TestHistory(t *testing.T) {
	s := open(t)
	m := now.Truncate(time.Minute)
	rows := []HistoryRow{
		{Minute: m, Group: TotalGroup, Registered: 90, Unregistered: 8, Unreachable: 2, InCall: 5},
		{Minute: m.Add(time.Minute), Group: TotalGroup, Gap: true},
		{Minute: m, Group: "Suporte", Registered: 10},
	}
	if err := s.InsertHistory(ctx, rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.QueryHistory(ctx, TotalGroup, m, m.Add(2*time.Minute))
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Registered != 90 || got[0].Gap || !got[1].Gap || got[1].Registered != 0 {
		t.Errorf("%+v", got)
	}
	// Reescrever o mesmo minuto substitui.
	s.InsertHistory(ctx, []HistoryRow{{Minute: m, Group: TotalGroup, Registered: 91}})
	got, _ = s.QueryHistory(ctx, TotalGroup, m, m.Add(time.Minute))
	if len(got) != 1 || got[0].Registered != 91 {
		t.Errorf("%+v", got)
	}
	// CHECK: minuto precisa ser multiplo de 60.
	if err := s.InsertHistory(ctx, []HistoryRow{{Minute: m.Add(5 * time.Second), Group: "x"}}); err == nil {
		t.Error("minute % 60 = 0")
	}
	if n, _ := s.PurgeHistory(ctx, m.Add(time.Minute)); n != 2 {
		t.Errorf("purge = %d", n)
	}
}

func TestBackupOnMigration(t *testing.T) {
	// Simula banco v0 com dados: o backup so existe quando ha versao antiga (>0).
	d := t.TempDir()
	s, _ := Open(ctx, d)
	s.Close()
	if _, err := os.Stat(filepath.Join(d, "painel.db.bak-v0")); err == nil {
		t.Error("banco novo nao precisa de backup")
	}
}

// O rollback do instalador depende de painel.db.bak-v<versao antiga>: a migracao
// cria o backup (VACUUM INTO) ANTES de mexer no esquema, e o backup abre e tem os dados.
func TestMigrationCreatesBackupOfOldVersion(t *testing.T) {
	oldMig, oldVer := migrations, SchemaVersion
	defer func() { migrations, SchemaVersion = oldMig, oldVer }()
	cur := len(oldMig) // versao do banco antes da migracao de teste

	d := t.TempDir()
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, "fabio", "$2a$hash", now); err != nil {
		t.Fatal(err)
	}
	s.Close()
	bak := filepath.Join(d, fmt.Sprintf("painel.db.bak-v%d", cur))
	if _, err := os.Stat(bak); err == nil {
		t.Fatal("backup nao deve existir antes da migracao")
	}

	migrations = append(append([]string(nil), oldMig...), "ALTER TABLE users ADD COLUMN extra TEXT;")
	SchemaVersion = len(migrations)
	s, err = Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	s.db.QueryRow("PRAGMA user_version").Scan(&v)
	s.Close()
	if v != cur+1 {
		t.Fatalf("user_version = %d, queria %d", v, cur+1)
	}
	fi, err := os.Stat(bak)
	if err != nil {
		t.Fatalf("backup ausente: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("backup com modo %v, queria 0600 (tem hashes)", fi.Mode().Perm())
	}
	// O backup e o banco da versao antiga: com o usuario e sem a coluna nova.
	b, err := sql.Open("sqlite", "file:"+filepath.ToSlash(bak)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var bv, n, cols int
	b.QueryRow("PRAGMA user_version").Scan(&bv)
	b.QueryRow("SELECT count(*) FROM users").Scan(&n)
	b.QueryRow("SELECT count(*) FROM pragma_table_info('users') WHERE name = 'extra'").Scan(&cols)
	if bv != cur || n != 1 || cols != 0 {
		t.Errorf("backup: user_version=%d usuarios=%d coluna_nova=%d", bv, n, cols)
	}
}

func TestExtensionNamesCache(t *testing.T) {
	s := open(t)
	if err := s.UpsertNames(ctx, map[string]string{"2001": "Maria", "2002": "Joao", "": "x", "2003": ""}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNames(ctx, map[string]string{"2001": "Maria Souza"}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadNames(ctx)
	if err != nil || len(got) != 2 || got["2001"] != "Maria Souza" || got["2002"] != "Joao" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestDeleteAllSessions(t *testing.T) {
	s := open(t)
	s.CreateSession(ctx, Session{IDHash: [32]byte{1}, Kind: "token", Subject: "a", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	s.CreateSession(ctx, Session{IDHash: [32]byte{2}, Kind: "token", Subject: "b", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if n, err := s.DeleteAllSessions(ctx); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestDeleteTokenSessionsBySubject(t *testing.T) {
	s := open(t)
	uid := int64(1)
	if err := s.CreateUser(ctx, "issabel:user:ana", "$2a$04$x", now); err != nil {
		t.Fatal(err)
	}
	mk := func(b byte, kind, sub string) {
		se := Session{IDHash: [32]byte{b}, Kind: kind, Subject: sub, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if kind == "password" {
			se.UserID = &uid
		}
		if err := s.CreateSession(ctx, se); err != nil {
			t.Fatal(err)
		}
	}
	mk(1, "token", "issabel:admin:ana")
	mk(2, "token", "issabel:user:ana")
	mk(3, "token", "issabel:user:bia")
	mk(4, "token", "ana")
	mk(5, "password", "issabel:user:ana") // sessao de senha nunca casa
	if n, err := s.DeleteTokenSessionsBySubject(ctx); err != nil || n != 0 {
		t.Fatalf("sem subjects: %d %v", n, err)
	}
	if n, err := s.DeleteTokenSessionsBySubject(ctx, "issabel:admin:ana", "issabel:user:ana"); err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v (queria 2)", n, err)
	}
	for b, want := range map[byte]bool{1: false, 2: false, 3: true, 4: true, 5: true} {
		if _, err := s.GetSession(ctx, [32]byte{b}, now); (err == nil) != want {
			t.Errorf("sessao %d: err = %v, deveria existir = %v", b, err, want)
		}
	}
	// O subject entra como parametro, nunca como SQL.
	if n, err := s.DeleteTokenSessionsBySubject(ctx, "x') OR 1=1 --"); err != nil || n != 0 {
		t.Errorf("injecao: %d %v", n, err)
	}
}

// Migracao real v1 -> v2 (cache de nomes): backup painel.db.bak-v1 com os dados antigos,
// dados mantidos e tabela nova criada. E o que o rollback do instalador usa.
func TestRealMigrationV1ToV2(t *testing.T) {
	if len(migrations) < 2 {
		t.Skip("so ha uma versao")
	}
	full, fullVer := migrations, SchemaVersion
	defer func() { migrations, SchemaVersion = full, fullVer }()
	d := t.TempDir()
	migrations, SchemaVersion = full[:1], 1
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	s.CreateUser(ctx, "fabio", "$2a$hash", now)
	s.Close()

	migrations, SchemaVersion = full, fullVer
	s, err = Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.UserByName(ctx, "fabio"); err != nil {
		t.Errorf("usuario perdido na migracao: %v", err)
	}
	if err := s.UpsertNames(ctx, map[string]string{"2001": "Maria"}, now); err != nil {
		t.Errorf("tabela nova ausente: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d, "painel.db.bak-v1")); err != nil {
		t.Errorf("backup v1 ausente: %v", err)
	}
}

func TestPruneNames(t *testing.T) {
	s := open(t)
	s.UpsertNames(ctx, map[string]string{"2001": "Maria", "2002": "Joao", "2003": "Ana"}, now)
	if n, err := s.PruneNames(ctx, nil); err != nil || n != 0 {
		t.Fatalf("lista vazia nao apaga nada: %d %v", n, err)
	}
	if n, err := s.PruneNames(ctx, []string{"2001", "2003", "9999"}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	got, _ := s.LoadNames(ctx)
	if len(got) != 2 || got["2002"] != "" || got["2001"] != "Maria" {
		t.Errorf("%v", got)
	}
}
