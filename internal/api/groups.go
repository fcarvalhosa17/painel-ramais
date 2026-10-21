package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"painel-ramais/internal/store"
)

// maxGroupsCSVLines limita o CSV enviado pela tela (a CLI nao tem este teto): protege a memoria.
const maxGroupsCSVLines = 20000

// groupsImport: POST /api/groups/import (so administrador, C6). Mesma validacao e mesma
// transacao da CLI; depois de gravar, o daemon relê os grupos como no SIGHUP.
func (s *Server) groupsImport(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		CSV     *string `json:"csv"`
		Replace bool    `json:"replace"`
	}
	if !decodeBodyMax(w, r, &in, maxGroupsBodyBytes) {
		return
	}
	if in.CSV == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Campo csv obrigatorio")
		return
	}
	n := strings.Count(*in.CSV, "\n")
	if !strings.HasSuffix(*in.CSV, "\n") {
		n++ // ultima linha sem quebra; a quebra final nao abre uma linha vazia
	}
	if n > maxGroupsCSVLines {
		writeError(w, http.StatusBadRequest, "invalid_csv", fmt.Sprintf("CSV com %d linhas; o maximo pela tela e %d", n, maxGroupsCSVLines))
		return
	}
	rows, err := store.ParseGroupsCSV(strings.NewReader(*in.CSV))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_csv", err.Error())
		return
	}
	res, err := s.Store.ImportGroups(r.Context(), sess.Subject, rows, in.Replace, s.Now())
	if err != nil {
		if errors.Is(err, store.ErrOverlap) || errors.Is(err, store.ErrInvalid) { // faixa cruzada no mesmo grupo ou teto de faixas
			writeError(w, http.StatusBadRequest, "invalid_csv", err.Error())
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.log.Info("visões importadas", requestIDKey, requestID(r), "sub", sess.Subject, "rows", res.Rows, "ignored", res.Ignored, "replace", in.Replace)
	if !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"rows": res.Rows, "replace": in.Replace, "ignored": res.Ignored}, nil)
}

// groupsDelete: POST /api/groups/delete (so administrador, C6).
func (s *Server) groupsDelete(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeBodyMax(w, r, &in, maxGroupsBodyBytes) {
		return
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > store.MaxGroupNameRunes {
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("Nome da visão deve ter de 1 a %d caracteres", store.MaxGroupNameRunes))
		return
	}
	switch err := s.Store.DeleteGroup(r.Context(), sess.Subject, in.Name, s.Now()); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Visão inexistente")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.log.Info("visão removida", requestIDKey, requestID(r), "sub", sess.Subject, "name", in.Name)
	if !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"name": in.Name}, nil)
}

type rangeIn struct {
	Start *int64 `json:"start"`
	End   *int64 `json:"end"`
}

// toRanges exige start e end em toda faixa; false = faltou algum.
func toRanges(in []rangeIn) ([]store.Range, bool) {
	out := make([]store.Range, len(in))
	for i, r := range in {
		if r.Start == nil || r.End == nil {
			return nil, false
		}
		out[i] = store.Range{Start: *r.Start, End: *r.End}
	}
	return out, true
}

// groupsWriteError traduz o erro de uma edicao de grupo: 400 entrada, 404 grupo, 409 conflito.
func (s *Server) groupsWriteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Visão inexistente")
	case errors.Is(err, store.ErrExists), errors.Is(err, store.ErrOverlap):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		s.internalError(w, r, err)
	}
}

// groupsCreate: POST /api/groups/create {name, ranges?} (so administrador).
func (s *Server) groupsCreate(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		Name   string    `json:"name"`
		Ranges []rangeIn `json:"ranges"`
	}
	if !decodeBodyMax(w, r, &in, maxGroupsBodyBytes) {
		return
	}
	ranges, ok := toRanges(in.Ranges)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "Cada faixa precisa de start e end")
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := s.Store.CreateGroup(r.Context(), sess.Subject, name, ranges, s.Now()); err != nil {
		s.groupsWriteError(w, r, err)
		return
	}
	s.log.Info("visão criada", requestIDKey, requestID(r), "sub", sess.Subject, "name", name, "ranges", len(ranges))
	if !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"name": name}, nil)
}

// groupsUpdate: POST /api/groups/update {name, new_name?, ranges?} (so administrador). ranges
// substitui TODAS as faixas do grupo; pelo menos um dos dois campos opcionais e obrigatorio.
func (s *Server) groupsUpdate(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		Name    string    `json:"name"`
		NewName *string   `json:"new_name"`
		Ranges  []rangeIn `json:"ranges"`
	}
	if !decodeBodyMax(w, r, &in, maxGroupsBodyBytes) {
		return
	}
	var ed store.GroupEdit
	if in.NewName != nil {
		n := strings.TrimSpace(*in.NewName)
		ed.NewName = &n
	}
	if in.Ranges != nil {
		rs, ok := toRanges(in.Ranges)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "Cada faixa precisa de start e end")
			return
		}
		ed.Ranges = &rs
	}
	name := strings.TrimSpace(in.Name)
	final, err := s.Store.UpdateGroup(r.Context(), sess.Subject, name, ed, s.Now())
	if err != nil {
		s.groupsWriteError(w, r, err)
		return
	}
	s.log.Info("visão alterada", requestIDKey, requestID(r), "sub", sess.Subject, "name", name, "final", final,
		"renamed", ed.NewName != nil, "ranges", ed.Ranges != nil)
	if !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"name": final}, nil)
}

// groupsAssign: POST /api/groups/assign (so administrador), vinculo manual em massa, atomico (uma
// transacao). mode "add": {numbers, groups} acrescenta o vinculo em 1 a 20 grupos; mode "remove":
// {numbers, group} apaga so o vinculo manual com um grupo (ramal pela faixa nao muda: not_manual).
// O formato antigo, sem mode, responde 400.
func (s *Server) groupsAssign(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		Mode    string   `json:"mode"`
		Numbers []string `json:"numbers"`
		Groups  []string `json:"groups"`
		Group   *string  `json:"group"`
	}
	if !decodeBodyMax(w, r, &in, maxAssignBodyBytes) {
		return
	}
	switch in.Mode {
	case "add":
		s.assignAdd(w, r, sess, in.Numbers, in.Groups, in.Group != nil)
	case "remove":
		s.assignRemove(w, r, sess, in.Numbers, in.Group, in.Groups != nil)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "Campo mode obrigatorio: add ou remove")
	}
}

func (s *Server) assignAdd(w http.ResponseWriter, r *http.Request, sess store.Session, numbers, groups []string, hasGroup bool) {
	if hasGroup {
		writeError(w, http.StatusBadRequest, "bad_request", "mode add usa groups (lista), nao group")
		return
	}
	trimmed := make([]string, len(groups))
	for i, g := range groups {
		trimmed[i] = strings.TrimSpace(g)
	}
	changed, err := s.Store.AddToGroups(r.Context(), sess.Subject, numbers, trimmed, s.Now())
	if err != nil {
		s.groupsWriteError(w, r, err)
		return
	}
	s.log.Info("ramais atribuidos a visão", requestIDKey, requestID(r), "sub", sess.Subject, "groups", trimmed,
		"requested", len(numbers), "changed", changed)
	if changed > 0 && !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"changed": changed}, nil)
}

func (s *Server) assignRemove(w http.ResponseWriter, r *http.Request, sess store.Session, numbers []string, group *string, hasGroups bool) {
	if hasGroups || group == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "mode remove usa group (uma visão), nao groups")
		return
	}
	name := strings.TrimSpace(*group)
	changed, notManual, err := s.Store.RemoveFromGroup(r.Context(), sess.Subject, numbers, name, s.Now())
	if err != nil {
		s.groupsWriteError(w, r, err)
		return
	}
	s.log.Info("ramais retirados de visão", requestIDKey, requestID(r), "sub", sess.Subject, "group", name,
		"requested", len(numbers), "changed", changed, "not_manual", notManual)
	if changed > 0 && !s.reloadGroups(w, r) {
		return
	}
	writeOK(w, map[string]any{"changed": changed, "not_manual": notManual}, nil)
}

// reloadGroups devolve false (e ja respondeu) se a recarga falhou: os grupos estao no
// banco, mas as telas abertas nao foram avisadas; repetir a operacao e seguro.
func (s *Server) reloadGroups(w http.ResponseWriter, r *http.Request) bool {
	if s.ReloadGroups == nil {
		return true
	}
	if err := s.ReloadGroups(r.Context()); err != nil {
		s.internalError(w, r, err)
		return false
	}
	return true
}
