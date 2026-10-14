package amisim

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tx e uma mutacao atomica do modelo: tudo que ela emite vai junto, sob s.mu.
type Tx struct {
	s     *Sim
	out   []out
	dirty map[*ext]struct{}
	sc    *Script
}

type out struct {
	name, priv string
	b          []byte
}

func (s *Sim) tx(fn func(t *Tx)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &Tx{s: s, dirty: map[*ext]struct{}{}}
	fn(t)
	t.commit()
}

// kvMsg monta "k: v\r\n..." e o terminador.
func kvMsg(head string, kv []string) []byte {
	var b strings.Builder
	b.WriteString(head)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(kv[i])
		b.WriteString(": ")
		b.WriteString(kv[i+1])
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}

func (t *Tx) ev(name, priv string, kv ...string) {
	t.out = append(t.out, out{name: name, priv: priv, b: kvMsg("Event: "+name+"\r\nPrivilege: "+priv+"\r\n", kv)})
}

func (t *Tx) commit() {
	s := t.s
	dirty := make([]*ext, 0, len(t.dirty))
	for e := range t.dirty {
		dirty = append(dirty, e)
	}
	sort.Slice(dirty, func(i, j int) bool { return dirty[i].idx < dirty[j].idx })
	for _, e := range dirty {
		if nd := s.devState(e); nd != e.dev {
			e.dev = nd
			t.ev("DeviceStateChange", "call,all", "Device", s.tech+"/"+e.num, "State", strings.ToUpper(nd))
		}
	}
	if len(t.out) == 0 {
		return
	}
	type push struct {
		c     *conn
		items []item
	}
	var pushes []push
	for c := range s.conns {
		var items []item
		for _, o := range t.out {
			if c.wants(o.name, o.priv) {
				items = append(items, item{b: o.b})
			}
		}
		if len(items) > 0 {
			pushes = append(pushes, push{c, items})
		}
	}
	if len(pushes) == 0 {
		return
	}
	st := &Stamp{}
	for _, p := range pushes {
		p.items[len(p.items)-1].st = st
	}
	if s.OnCommit != nil {
		for _, e := range dirty {
			s.OnCommit(s.view(e), st)
		}
	}
	for _, p := range pushes {
		p.c.push(p.items...)
	}
}

// ---- registro

func (t *Tx) setReg(e *ext, to string) {
	s := t.s
	from := e.reg
	e.reg = to
	t.dirty[e] = struct{}{}
	if s.cfg.Mode == ModePJSIP {
		uri := "sip:" + e.num + "@" + e.ip + ":5060"
		cs := func(status, rtt string) {
			t.ev("ContactStatus", "system,all", "URI", uri, "ContactStatus", status, "AOR", e.num,
				"EndpointName", e.num, "RoundtripUsec", rtt, "UserAgent", e.ua, "ViaAddress", e.ip+":5060")
		}
		rtt := strconv.Itoa(e.lat * 1000)
		switch to {
		case "registered":
			if from == "unregistered" {
				cs("Created", "N/A")
			}
			cs("Reachable", rtt)
		case "unreachable":
			if from == "unregistered" {
				cs("Created", "N/A")
			}
			cs("Unreachable", "0")
		case "unregistered":
			cs("Removed", "0")
		}
		return
	}
	ps := func(status string, extra ...string) {
		kv := append([]string{"ChannelType", "SIP", "Peer", "SIP/" + e.num, "PeerStatus", status}, extra...)
		t.ev("PeerStatus", "system,all", kv...)
	}
	switch to {
	case "registered":
		if from == "unregistered" {
			ps("Registered", "Address", e.ip+":5060")
		}
		ps("Reachable", "Address", e.ip+":5060", "Time", strconv.Itoa(e.lat))
	case "unreachable":
		if from == "unregistered" {
			ps("Registered", "Address", e.ip+":5060")
		}
		ps("Unreachable", "Address", e.ip+":5060")
	case "unregistered":
		ps("Unregistered", "Cause", "Expired")
	}
}

// ---- canais

var stateDesc = map[int]string{0: "Down", 4: "Ring", 5: "Ringing", 6: "Up"}

func unk(s string) string {
	if s == "" {
		return "<unknown>"
	}
	return s
}

func (c *chanT) fields(p string) []string {
	return []string{
		p + "Channel", c.name,
		p + "ChannelState", strconv.Itoa(c.state),
		p + "ChannelStateDesc", stateDesc[c.state],
		p + "CallerIDNum", unk(c.num),
		p + "CallerIDName", unk(c.cname),
		p + "ConnectedLineNum", unk(c.cnum),
		p + "ConnectedLineName", unk(c.cname2),
		p + "Language", "en",
		p + "AccountCode", "",
		p + "Context", c.ctx,
		p + "Exten", c.exten,
		p + "Priority", "1",
		p + "Uniqueid", c.uid,
		p + "Linkedid", c.linked,
	}
}

func (t *Tx) seq() uint64 { t.s.seq++; return t.s.seq }

// pj devolve o nome de um canal de ramal ou tronco: PJSIP/2001-0000002a.
func (t *Tx) pj(who string) string { return fmt.Sprintf("%s/%s-%08x", t.s.tech, who, t.seq()) }

// localNames devolve o par Local/<dst>@<ctx>-0000001a;1 e ;2.
func (t *Tx) localNames(dst, ctx string) (string, string) {
	h := fmt.Sprintf("%08x", t.seq())
	return "Local/" + dst + "@" + ctx + "-" + h + ";1", "Local/" + dst + "@" + ctx + "-" + h + ";2"
}

func (t *Tx) newChan(name string, state int, num, cname, exten, ctx, linked string) *chanT {
	s := t.s
	n := t.seq()
	c := &chanT{name: name, uid: fmt.Sprintf("%d.%d", s.t0, n), created: time.Now(), state: state, num: num, cname: cname, exten: exten, ctx: ctx}
	c.linked = linked
	if linked == "" {
		c.linked = c.uid
	}
	s.chans[c.uid] = c
	if t.sc != nil {
		t.sc.chans = append(t.sc.chans, c)
	}
	t.ev("Newchannel", "call,all", c.fields("")...)
	if s.cfg.Unfiltered {
		t.ev("Newexten", "dialplan,all", "Channel", c.name, "Context", ctx, "Extension", exten, "Priority", "1", "Application", "NoOp", "AppData", "", "Uniqueid", c.uid)
		t.ev("VarSet", "dialplan,all", "Channel", c.name, "Variable", "DIALSTATUS", "Value", "", "Uniqueid", c.uid)
	}
	return c
}

func (t *Tx) state(c *chanT, st int) {
	if c.state == st {
		return
	}
	c.state = st
	t.ev("Newstate", "call,all", c.fields("")...)
}

func (t *Tx) connected(c *chanT, num, name string) {
	c.cnum, c.cname2 = num, name
	t.ev("NewConnectedLine", "call,all", c.fields("")...)
}

func (t *Tx) dial(src, dst *chanT, dialstr string) {
	dst.dest = true
	if strings.HasPrefix(dialstr, "Local/") {
		src.dialData = dialstr + ",30"
	} else {
		src.dialData = t.s.tech + "/" + dialstr + ",30,tr"
	}
	kv := append(src.fields(""), dst.fields("Dest")...)
	t.ev("DialBegin", "call,all", append(kv, "DialString", dialstr)...)
}

func (t *Tx) dialEnd(src, dst *chanT, status string) {
	kv := append(src.fields(""), dst.fields("Dest")...)
	t.ev("DialEnd", "call,all", append(kv, "DialStatus", status)...)
}

func (t *Tx) newBridge() string {
	r := t.s.rng
	id := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", r.Uint32(), r.Intn(1<<16), r.Intn(1<<16), r.Intn(1<<16), r.Uint64()&0xffffffffffff)
	t.s.bridge[id] = 0
	if t.s.cfg.Unfiltered {
		t.ev("BridgeCreate", "call,all", "BridgeUniqueid", id, "BridgeType", "basic")
	}
	return id
}

func (t *Tx) bridgeEv(name string, id string, c *chanT) {
	kv := []string{"BridgeUniqueid", id, "BridgeType", "basic", "BridgeTechnology", "simple_bridge",
		"BridgeCreator", "<unknown>", "BridgeName", "<unknown>", "BridgeNumChannels", strconv.Itoa(t.s.bridge[id]), "BridgeVideoSourceMode", "none"}
	t.ev(name, "call,all", append(kv, c.fields("")...)...)
}

func (t *Tx) enter(id string, c *chanT) {
	t.s.bridge[id]++
	c.bridge = id
	t.bridgeEv("BridgeEnter", id, c)
}

func (t *Tx) leave(c *chanT) {
	id := c.bridge
	if id == "" {
		return
	}
	t.s.bridge[id]--
	t.bridgeEv("BridgeLeave", id, c)
	c.bridge = ""
	if t.s.bridge[id] <= 0 {
		delete(t.s.bridge, id)
		if t.s.cfg.Unfiltered {
			t.ev("BridgeDestroy", "call,all", "BridgeUniqueid", id)
		}
	}
}

func (t *Tx) hangup(c *chanT) {
	if _, ok := t.s.chans[c.uid]; !ok {
		return
	}
	t.leave(c)
	t.ev("Hangup", "call,all", append(c.fields(""), "Cause", "16", "Cause-txt", "Normal Clearing")...)
	delete(t.s.chans, c.uid)
	t.untrack(c)
}

func (t *Tx) setHold(c *chanT, on bool) {
	c.hold = on
	if on {
		t.ev("Hold", "call,all", append(c.fields(""), "MusicClass", "default")...)
	} else {
		t.ev("Unhold", "call,all", c.fields("")...)
	}
	if c.tc != nil {
		if on {
			c.tc.state = "hold"
		} else {
			c.tc.state = "talking"
		}
		t.dirty[c.e] = struct{}{}
	}
}

// up leva o canal a Up e a verdade do ramal dono para "talking".
func (t *Tx) up(c *chanT) {
	t.state(c, 6)
	if c.tc != nil && c.tc.state == "ringing" {
		c.tc.state = "talking"
		c.tc.answered = time.Now()
		t.dirty[c.e] = struct{}{}
	}
}

// reRing marca a chamada do transferido como ainda nao atendida na nova ponta.
func (t *Tx) reRing(c *chanT) {
	if c.tc != nil {
		c.tc.state, c.tc.answered = "ringing", time.Time{}
		t.dirty[c.e] = struct{}{}
	}
}

// upTrack marca a chamada como atendida sem mexer no estado do canal (ja estava Up).
func (t *Tx) upTrack(c *chanT) {
	if c.tc != nil && c.tc.state == "ringing" {
		c.tc.state, c.tc.answered = "talking", time.Now()
		t.dirty[c.e] = struct{}{}
	}
}

// ---- verdade das chamadas

func (t *Tx) track(c *chanT, e *ext, dir, pk, pn, pname string, dest bool) *tcall {
	tc := &tcall{id: c.uid, dir: dir, state: "ringing", pk: pk, pn: pn, pname: pname, dest: dest, started: c.created}
	c.e, c.tc = e, tc
	e.calls = append(e.calls, tc)
	t.dirty[e] = struct{}{}
	return tc
}

func (t *Tx) untrack(c *chanT) {
	if c.tc == nil {
		return
	}
	e := c.e
	for i, x := range e.calls {
		if x == c.tc {
			e.calls = append(e.calls[:i], e.calls[i+1:]...)
			break
		}
	}
	c.tc, c.e = nil, nil
	t.dirty[e] = struct{}{}
}

func (t *Tx) retarget(c *chanT, dir, pk, pn, pname string) {
	if c.tc == nil {
		return
	}
	c.tc.dir, c.tc.pk, c.tc.pn, c.tc.pname, c.tc.soft = dir, pk, pn, pname, false
	t.dirty[c.e] = struct{}{}
}

// partyOf descreve o canal c do ponto de vista de quem fala com ele.
func partyOf(c *chanT) (dir, pk, pn, pname string) {
	if c.e != nil {
		return "internal", "extension", c.e.num, c.e.name
	}
	return c.trunkDir, "external", c.rnum, c.rname
}

// ---- pernas

func (t *Tx) extLeg(e *ext, exten, linked string) *chanT {
	c := t.newChan(t.pj(e.num), 0, e.num, e.name, exten, "from-internal", linked)
	c.e = e
	return c
}

func (t *Tx) trunkLeg(trunk, num, name, exten, dir, linked string) *chanT {
	c := t.newChan(t.pj(trunk), 0, num, name, exten, "from-trunk", linked)
	c.trunkDir = dir
	if dir == "inbound" {
		c.rnum, c.rname = num, name
	}
	return c
}

// dialExt: src disca para o ramal dst; devolve a perna de dst (destino de Dial).
func (t *Tx) dialExt(src *chanT, dst *ext, dir, pk, pn, pname string) *chanT {
	c := t.newChan(t.pj(dst.num), 0, dst.num, dst.name, "", "from-internal", src.linked)
	c.cnum, c.cname2 = pn, pname
	t.dial(src, c, dst.num)
	t.state(c, 5)
	if src.state < 5 { // transferido ja esta Up: o Asterisk nao o rebaixa
		t.state(src, 5)
	}
	t.track(c, dst, dir, pk, pn, pname, true)
	return c
}

// dialLocal: src disca para o ramal dst por um par Local (grupo de toque, fila).
func (t *Tx) dialLocal(src *chanT, dst *ext, dir, pk, pn, pname string) (l1, l2, c *chanT) {
	n1, n2 := t.localNames(dst.num, "from-internal-xfer")
	l1 = t.newChan(n1, 0, "", "", dst.num, "from-internal", src.linked)
	l2 = t.newChan(n2, 0, "", "", dst.num, "from-internal", src.linked)
	t.dial(src, l1, "Local/"+dst.num+"@from-internal-xfer")
	c = t.newChan(t.pj(dst.num), 0, dst.num, dst.name, "", "from-internal", src.linked)
	c.cnum, c.cname2 = pn, pname
	t.dial(l2, c, dst.num)
	t.state(c, 5)
	t.state(l2, 5)
	t.state(l1, 5)
	t.state(src, 5)
	t.track(c, dst, dir, pk, pn, pname, true)
	return
}

// answer: dst atende a chamada de src; os dois entram numa bridge nova.
func (t *Tx) answer(src, dst *chanT) string {
	t.up(dst)
	t.up(src)
	t.dialEnd(src, dst, "ANSWER")
	br := t.newBridge()
	t.enter(br, src)
	t.enter(br, dst)
	return br
}

func (t *Tx) answerLocal(src, l1, l2, c *chanT) {
	t.up(c)
	t.up(l2)
	t.dialEnd(l2, c, "ANSWER")
	b1 := t.newBridge()
	t.enter(b1, l2)
	t.enter(b1, c)
	t.up(l1)
	t.up(src)
	t.dialEnd(src, l1, "ANSWER")
	b2 := t.newBridge()
	t.enter(b2, src)
	t.enter(b2, l1)
	if src.tc != nil {
		src.tc.soft = false
	}
}

// optimizeLocal: o Asterisk descarta o par Local e junta src e c numa bridge so.
func (t *Tx) optimizeLocal(src, l1, l2, c *chanT) {
	t.hangup(l2)
	t.hangup(l1)
	b3 := t.newBridge()
	t.leave(c)
	t.leave(src)
	t.enter(b3, src)
	t.enter(b3, c)
}
