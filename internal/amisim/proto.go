package amisim

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type item struct {
	b   []byte
	st  *Stamp
	fin bool // fecha a conexao depois de gravar
}

// maxQueue: acima disso o simulador derruba o cliente lento, como o Asterisk faz.
const maxQueue = 2000000

type conn struct {
	s      *Sim
	nc     net.Conn
	qmu    sync.Mutex
	qc     *sync.Cond
	q      []item
	closed bool

	auth, events bool
	read, write  map[string]bool
}

func newConn(s *Sim, nc net.Conn) *conn {
	c := &conn{s: s, nc: nc}
	c.qc = sync.NewCond(&c.qmu)
	return c
}

func (c *conn) push(its ...item) {
	c.qmu.Lock()
	if !c.closed {
		c.q = append(c.q, its...)
		if len(c.q) > maxQueue {
			c.closed = true
			c.nc.Close()
		}
	}
	c.qmu.Unlock()
	c.qc.Signal()
}

func (c *conn) close() {
	c.qmu.Lock()
	c.closed = true
	c.qmu.Unlock()
	c.nc.Close()
	c.qc.Signal()
}

// wants decide se o evento vai para esta conexao (eventfilter e privilegios de leitura).
func (c *conn) wants(name, priv string) bool {
	if !c.auth || !c.events {
		return false
	}
	if !c.s.cfg.Unfiltered && !filterSet[name] {
		return false
	}
	if c.read["all"] {
		return true
	}
	for _, p := range strings.Split(priv, ",") {
		if c.read[p] {
			return true
		}
	}
	return false
}

// filterSet e o eventfilter do bloco de manager_custom.conf (contrato 7.2): exatamente os 29 nomes do postinstall.
var filterSet = map[string]bool{
	"ContactStatus": true, "PeerStatus": true, "DeviceStateChange": true, "Newchannel": true, "Newstate": true,
	"NewCallerid": true, "NewConnectedLine": true, "DialBegin": true, "DialEnd": true, "BridgeEnter": true,
	"BridgeLeave": true, "Hangup": true, "Hold": true, "Unhold": true, "Reload": true, "ModuleLoadReport": true, "FullyBooted": true,
	"Shutdown": true,
	// rc.8 (ADR-008): eventos de fila e agente, classe de leitura "agent".
	"QueueCallerJoin": true, "QueueCallerLeave": true, "QueueCallerAbandon": true, "QueueMemberStatus": true,
	"QueueMemberPause": true, "QueueMemberAdded": true, "QueueMemberRemoved": true, "AgentCalled": true,
	"AgentConnect": true, "AgentComplete": true, "AgentRingNoAnswer": true,
}

func (c *conn) writeLoop() {
	bw := bufio.NewWriterSize(c.nc, 256<<10)
	for {
		c.qmu.Lock()
		for len(c.q) == 0 && !c.closed {
			c.qc.Wait()
		}
		if c.closed {
			c.qmu.Unlock()
			return
		}
		n := len(c.q)
		if n > 256 {
			n = 256
		}
		batch := make([]item, n)
		copy(batch, c.q[:n])
		c.q = c.q[n:]
		if len(c.q) == 0 {
			c.q = nil
		}
		c.qmu.Unlock()
		fin := false
		for _, it := range batch {
			bw.Write(it.b)
			fin = fin || it.fin
		}
		err := bw.Flush()
		now := time.Now().UnixNano()
		for _, it := range batch {
			if it.st != nil {
				atomic.CompareAndSwapInt64(&it.st.At, 0, now)
			}
		}
		if err != nil || fin {
			c.close()
			return
		}
	}
}

// ---- servidor

var (
	allowlist = map[string]string{}
	required  = map[string]string{ // classes de escrita exigidas (qualquer uma serve); "" = nenhuma
		"login": "", "logoff": "", "ping": "",
		"coresettings": "system,reporting", "modulecheck": "system",
		"pjsipshowendpoints": "system", "pjsipshowendpoint": "system", "pjsipshowcontacts": "system", "pjsipshowaors": "system",
		"sippeers": "system,reporting", "sipshowpeer": "system,reporting",
		"devicestatelist": "call,reporting", "coreshowchannels": "system,reporting",
	}
)

func init() {
	for _, a := range []string{"Login", "Logoff", "Ping", "CoreSettings", "ModuleCheck", "PJSIPShowEndpoints", "PJSIPShowEndpoint",
		"PJSIPShowContacts", "PJSIPShowAors", "SIPpeers", "SIPshowpeer", "DeviceStateList", "CoreShowChannels"} {
		allowlist[strings.ToLower(a)] = a
	}
}

// ActionRec e uma action fora da allowlist recebida (violacao).
type ActionRec struct {
	Time   string `json:"time"`
	Action string `json:"action"`
}

// Actions devolve a contagem por action recebida e as violacoes (fora da allowlist).
func (s *Sim) Actions() (counts map[string]int, violations []ActionRec) {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	counts = make(map[string]int, len(s.actCount))
	for k, v := range s.actCount {
		counts[k] = v
	}
	return counts, append([]ActionRec(nil), s.actViol...)
}

func (s *Sim) logAction(name string, allowed bool) {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	s.actCount[name]++
	if !allowed && len(s.actViol) < 1000 {
		s.actViol = append(s.actViol, ActionRec{Time: time.Now().UTC().Format(tsFmt), Action: name})
	}
}

func (s *Sim) amiVersion() string {
	switch {
	case strings.HasPrefix(s.cfg.Asterisk, "13."):
		return "2.10.6"
	case strings.HasPrefix(s.cfg.Asterisk, "16."):
		return "4.0.4"
	}
	return "5.0.1"
}

// Listen abre a porta AMI e serve em background. addr "127.0.0.1:0" escolhe porta.
func (s *Sim) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.lnMu.Lock()
	s.ln, s.addr, s.down = ln, ln.Addr().String(), false
	s.lnMu.Unlock()
	go s.acceptLoop(ln)
	return nil
}

// Addr e o endereco AMI efetivo.
func (s *Sim) Addr() string {
	s.lnMu.Lock()
	defer s.lnMu.Unlock()
	return s.addr
}

func (s *Sim) acceptLoop(ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serveConn(nc)
	}
}

// Close encerra o listener e todas as conexoes.
func (s *Sim) Close() {
	s.lnMu.Lock()
	s.down = true
	if s.ln != nil {
		s.ln.Close()
	}
	s.lnMu.Unlock()
	s.KickAll()
}

// KickAll derruba as conexoes AMI abertas (o listener continua).
func (s *Sim) KickAll() {
	s.mu.Lock()
	cs := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		c.close()
	}
}

// Down simula Asterisk fora do ar: fecha o listener (conexao recusada) e as
// conexoes; volta a escutar no mesmo endereco depois de d. Nao bloqueia.
func (s *Sim) Down(d time.Duration) error {
	s.lnMu.Lock()
	if s.ln == nil || s.down {
		s.lnMu.Unlock()
		return errors.New("simulador ja esta fora do ar")
	}
	s.down = true
	addr := s.addr
	s.ln.Close()
	s.lnMu.Unlock()
	s.KickAll()
	go func() {
		time.Sleep(d)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if err := s.Listen(addr); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return nil
}

func (s *Sim) serveConn(nc net.Conn) {
	c := newConn(s, nc)
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	go c.writeLoop()
	c.push(item{b: []byte("Asterisk Call Manager/" + s.amiVersion() + "\r\n")})
	br := bufio.NewReaderSize(nc, 64<<10)
	h := map[string]string{}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(h) > 0 {
				s.handle(c, h)
				h = map[string]string{}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			k = strings.ToLower(strings.TrimSpace(k))
			if _, dup := h[k]; !dup {
				h[k] = strings.TrimSpace(v)
			}
		}
	}
	c.close()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func resp(c *conn, id, status string, kv ...string) {
	head := "Response: " + status + "\r\n"
	if id != "" {
		head += "ActionID: " + id + "\r\n"
	}
	c.push(item{b: kvMsg(head, kv)})
}

func (s *Sim) handle(c *conn, h map[string]string) {
	raw := h["action"]
	id := h["actionid"]
	canon, ok := allowlist[strings.ToLower(raw)]
	if !ok {
		s.logAction(raw, false)
		low := strings.ToLower(raw)
		if low == "command" || low == "originate" || low == "redirect" || low == "hangup" || low == "atxfer" ||
			low == "setvar" || low == "reload" || low == "moduleload" || low == "updateconfig" || low == "getconfig" || low == "filter" {
			resp(c, id, "Error", "Message", "Permission denied")
			return
		}
		resp(c, id, "Error", "Message", "Invalid/unknown command: "+raw+". Use Action: ListCommands to show available commands.")
		return
	}
	s.logAction(canon, true)
	low := strings.ToLower(canon)
	if low == "login" {
		s.doLogin(c, id, h)
		return
	}
	if !c.auth {
		resp(c, id, "Error", "Message", "Permission denied")
		return
	}
	if need := required[low]; need != "" {
		okc := c.write["all"]
		for _, p := range strings.Split(need, ",") {
			okc = okc || c.write[p]
		}
		if !okc {
			resp(c, id, "Error", "Message", "Permission denied")
			return
		}
	}
	unknown := func() {
		resp(c, id, "Error", "Message", "Invalid/unknown command: "+canon+". Use Action: ListCommands to show available commands.")
	}
	switch canon {
	case "Logoff":
		c.push(item{b: kvMsg("Response: Goodbye\r\n"+idLine(id), []string{"Message", "Thanks for all the fish."}), fin: true})
	case "Ping":
		resp(c, id, "Success", "Ping", "Pong", "Timestamp", fmt.Sprintf("%d.%06d", time.Now().Unix(), time.Now().Nanosecond()/1000))
	case "CoreSettings":
		resp(c, id, "Success", "AMIversion", s.amiVersion(), "AsteriskVersion", s.cfg.Asterisk, "SystemName", "",
			"CoreMaxCalls", "0", "CoreMaxLoadAvg", "0.000000", "CoreRunUser", "asterisk", "CoreRunGroup", "asterisk",
			"CoreMaxFilehandles", "0", "CoreRealTimeEnabled", "No", "CoreCDRenabled", "Yes", "CoreHTTPenabled", "Yes")
	case "ModuleCheck":
		m := h["module"]
		if !strings.Contains(m, ".") {
			m += ".so"
		}
		if s.loaded(m) {
			resp(c, id, "Success", "Version", s.cfg.Asterisk)
		} else {
			resp(c, id, "Error", "Message", "Module not loaded")
		}
	case "PJSIPShowEndpoints":
		if !s.loaded("chan_pjsip.so") {
			unknown()
			return
		}
		s.listEndpoints(c, id)
	case "PJSIPShowEndpoint":
		if !s.loaded("chan_pjsip.so") {
			unknown()
			return
		}
		s.showEndpoint(c, id, h["endpoint"])
	case "PJSIPShowContacts":
		if !s.loaded("chan_pjsip.so") || s.cfg.NoShowContacts {
			unknown()
			return
		}
		s.listContacts(c, id)
	case "PJSIPShowAors":
		if !s.loaded("chan_pjsip.so") {
			unknown()
			return
		}
		s.listAors(c, id)
	case "SIPpeers":
		if !s.loaded("chan_sip.so") {
			unknown()
			return
		}
		s.listPeers(c, id)
	case "SIPshowpeer":
		if !s.loaded("chan_sip.so") {
			unknown()
			return
		}
		s.showPeer(c, id, h["peer"])
	case "DeviceStateList":
		s.listDevices(c, id)
	case "CoreShowChannels":
		s.listChannels(c, id)
	}
}

func idLine(id string) string {
	if id == "" {
		return ""
	}
	return "ActionID: " + id + "\r\n"
}

func (s *Sim) doLogin(c *conn, id string, h map[string]string) {
	if h["username"] != s.cfg.User || h["secret"] != s.cfg.Secret {
		resp(c, id, "Error", "Message", "Authentication failed")
		return
	}
	c.read, c.write = classSet(s.cfg.Read), classSet(s.cfg.Write)
	ev := strings.ToLower(h["events"])
	s.mu.Lock()
	c.auth = true
	c.events = ev != "off" && ev != "0" && ev != "no"
	resp(c, id, "Success", "Message", "Authentication accepted")
	if c.events {
		c.push(item{b: kvMsg("Event: FullyBooted\r\nPrivilege: system,all\r\n", []string{"Status", "Fully Booted"})})
	}
	s.mu.Unlock()
}

func classSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

// ---- listas (respondidas sob s.mu: foto consistente, eventos ao vivo depois dela)

type lister struct {
	c   *conn
	id  string
	n   int
	its []item
}

func (l *lister) add(name string, kv ...string) {
	head := "Event: " + name + "\r\n" + idLine(l.id)
	l.its = append(l.its, item{b: kvMsg(head, kv)})
	l.n++
}

func (s *Sim) startList(c *conn, id, msg string) *lister {
	l := &lister{c: c, id: id}
	l.its = append(l.its, item{b: kvMsg("Response: Success\r\n"+idLine(id), []string{"EventList", "start", "Message", msg})})
	return l
}

func (l *lister) finish(endEvent string) {
	head := "Event: " + endEvent + "\r\n" + idLine(l.id)
	l.its = append(l.its, item{b: kvMsg(head, []string{"EventList", "Complete", "ListItems", strconv.Itoa(l.n)})})
	l.c.push(l.its...)
}

var devText = map[string]string{
	"unknown": "Unknown", "not_inuse": "Not in use", "inuse": "In use", "busy": "Busy", "invalid": "Invalid",
	"unavailable": "Unavailable", "ringing": "Ringing", "ringinuse": "Ring, In use", "onhold": "On Hold",
}

func (s *Sim) chanNamesOf(e *ext) string {
	var ns []string
	for _, ch := range s.chans {
		if ch.e == e {
			ns = append(ns, ch.name)
		}
	}
	sort.Strings(ns)
	return strings.Join(ns, ",")
}

func (s *Sim) listEndpoints(c *conn, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.startList(c, id, "A listing of Endpoints follows, presented as EndpointList events")
	for _, e := range s.exts {
		contacts := ""
		if e.reg != "unregistered" {
			contacts = e.num + "/sip:" + e.num + "@" + e.ip + ":5060"
		}
		l.add("EndpointList", "ObjectType", "endpoint", "ObjectName", e.num, "Transport", "0.0.0.0-udp", "Aor", e.num,
			"Auths", e.num, "OutboundAuths", "", "Contacts", contacts, "DeviceState", devText[e.dev], "ActiveChannels", s.chanNamesOf(e))
	}
	for _, t := range s.trunks {
		l.add("EndpointList", "ObjectType", "endpoint", "ObjectName", t, "Transport", "0.0.0.0-udp", "Aor", t,
			"Auths", "", "OutboundAuths", t, "Contacts", t+"/sip:203.0.113.10:5060", "DeviceState", "Not in use", "ActiveChannels", "")
	}
	l.finish("EndpointListComplete")
}

// showEndpoint responde como o Asterisk: EndpointDetail (com Callerid), AorDetail e EndpointDetailComplete.
func (s *Sim) showEndpoint(c *conn, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.byNum[name]
	if e == nil {
		resp(c, id, "Error", "Message", "Unable to retrieve endpoint "+name)
		return
	}
	l := s.startList(c, id, "A listing of Endpoint follows, presented as EndpointDetail events")
	l.add("EndpointDetail", "ObjectType", "endpoint", "ObjectName", e.num, "Context", "from-internal", "Disallow", "all",
		"Allow", "ulaw,alaw", "DtmfMode", "rfc4733", "Callerid", "\""+e.name+"\" <"+e.num+">", "Aors", e.num, "Auth", e.num,
		"DeviceStateBusyAt", "0", "Transport", "0.0.0.0-udp", "Mailboxes", "", "DirectMedia", "false", "Accountcode", "")
	l.add("AorDetail", "ObjectType", "aor", "ObjectName", e.num, "MaxContacts", "1", "RemoveExisting", "true", "QualifyFrequency", "60")
	l.finish("EndpointDetailComplete")
}

func (s *Sim) listContacts(c *conn, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.startList(c, id, "A listing of Contacts follows, presented as ContactList events")
	for _, e := range s.exts {
		if e.reg == "unregistered" {
			continue
		}
		st, rtt := "Reachable", strconv.Itoa(e.lat*1000)
		if e.reg == "unreachable" {
			st, rtt = "Unreachable", "0"
		}
		l.add("ContactList", "ObjectType", "contact", "ObjectName", e.contact, "ViaAddr", e.ip, "QualifyTimeout", "3.000000",
			"UserAgent", e.ua, "RegExpire", strconv.FormatInt(time.Now().Unix()+3600, 10), "ViaPort", "5060", "AuthenticateQualify", "false",
			"OutboundProxy", "", "Path", "", "CallID", "c"+e.num+"@"+e.ip, "Endpoint", e.num, "Uri", "sip:"+e.num+"@"+e.ip+":5060",
			"QualifyFrequency", "60", "Status", st, "RoundtripUsec", rtt, "EndpointName", e.num, "ID", e.contact)
	}
	for _, t := range s.trunks {
		l.add("ContactList", "ObjectType", "contact", "ObjectName", t+";@static", "ViaAddr", "203.0.113.10", "UserAgent", "",
			"Endpoint", t, "Uri", "sip:203.0.113.10:5060", "Status", "Reachable", "RoundtripUsec", "21000", "EndpointName", t, "ID", t+";@static")
	}
	l.finish("ContactListComplete")
}

func (s *Sim) listAors(c *conn, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.startList(c, id, "A listing of AORs follows, presented as AorList events")
	for _, e := range s.exts {
		tot := "0"
		if e.reg != "unregistered" {
			tot = "1"
		}
		l.add("AorList", "ObjectType", "aor", "ObjectName", e.num, "MinimumExpiration", "60", "MaximumExpiration", "7200",
			"DefaultExpiration", "3600", "QualifyFrequency", "60", "AuthenticateQualify", "false", "MaxContacts", "1",
			"RemoveExisting", "true", "Mailboxes", "", "OutboundProxy", "", "SupportPath", "false", "TotalContacts", tot,
			"ContactsRegistered", tot, "EndpointName", e.num)
	}
	l.finish("AorListComplete")
}

func (s *Sim) peerStatus(e *ext) string {
	switch e.reg {
	case "registered":
		return fmt.Sprintf("OK (%d ms)", e.lat)
	case "unreachable":
		return "UNREACHABLE"
	}
	return "UNKNOWN"
}

func (s *Sim) listPeers(c *conn, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.startList(c, id, "Peer status list will follow")
	for _, e := range s.exts {
		ip, port := "-none-", "0"
		if e.reg != "unregistered" {
			ip, port = e.ip, "5060"
		}
		l.add("PeerEntry", "Channeltype", "SIP", "ObjectName", e.num, "ChanObjectType", "peer", "IPaddress", ip, "IPport", port,
			"Dynamic", "yes", "AutoForcerport", "no", "Forcerport", "no", "AutoComedia", "no", "Comedia", "no",
			"VideoSupport", "no", "TextSupport", "no", "ACL", "no", "Status", s.peerStatus(e), "RealtimeDevice", "no")
	}
	for _, t := range s.trunks {
		l.add("PeerEntry", "Channeltype", "SIP", "ObjectName", t, "ChanObjectType", "peer", "IPaddress", "203.0.113.10", "IPport", "5060",
			"Dynamic", "no", "AutoForcerport", "no", "Forcerport", "no", "AutoComedia", "no", "Comedia", "no",
			"VideoSupport", "no", "TextSupport", "no", "ACL", "no", "Status", "OK (21 ms)", "RealtimeDevice", "no")
	}
	l.finish("PeerlistComplete")
}

func (s *Sim) showPeer(c *conn, id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.byNum[name]
	if e == nil {
		resp(c, id, "Error", "Message", "Peer "+name+" not found.")
		return
	}
	ip, port := "-none-", "0"
	ua := ""
	if e.reg != "unregistered" {
		ip, port, ua = e.ip, "5060", e.ua
	}
	resp(c, id, "Success", "Channeltype", "SIP", "ObjectName", e.num, "ChanObjectType", "peer", "SecretExist", "Y",
		"Callerid", "\""+e.name+"\" <"+e.num+">", "Context", "from-internal", "Dynamic", "Y",
		"Address-IP", ip, "Address-Port", port, "SIP-Useragent", ua, "Status", s.peerStatus(e))
}

func (s *Sim) listDevices(c *conn, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.startList(c, id, "Device State Changes will follow")
	for _, e := range s.exts {
		l.add("DeviceStateChange", "Device", s.tech+"/"+e.num, "State", strings.ToUpper(e.dev))
	}
	for _, t := range s.trunks {
		l.add("DeviceStateChange", "Device", s.tech+"/"+t, "State", "NOT_INUSE")
	}
	// dispositivos de outras origens: o cliente tem de ignora-los
	l.add("DeviceStateChange", "Device", "Custom:DND"+s.exts[0].num, "State", "NOT_INUSE")
	l.add("DeviceStateChange", "Device", "Queue:5000_pause_"+s.exts[0].num, "State", "NOT_INUSE")
	l.finish("DeviceStateListComplete")
}

func durHMS(d time.Duration) string {
	sec := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

func (s *Sim) listChannels(c *conn, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.startList(c, id, "Channels will follow")
	cs := make([]*chanT, 0, len(s.chans))
	for _, ch := range s.chans {
		cs = append(cs, ch)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].uid < cs[j].uid })
	now := time.Now()
	for _, ch := range cs {
		app, appData := "AppDial", "(Outgoing Line)" // como o Asterisk mostra o destino de um Dial
		if !ch.dest {
			app, appData = "Dial", ch.dialData
		}
		started := ch.created
		l.add("CoreShowChannel", "Channel", ch.name, "ChannelState", strconv.Itoa(ch.state), "ChannelStateDesc", stateDesc[ch.state],
			"CallerIDNum", unk(ch.num), "CallerIDName", unk(ch.cname), "ConnectedLineNum", unk(ch.cnum), "ConnectedLineName", unk(ch.cname2),
			"Language", "en", "AccountCode", "", "Context", ch.ctx, "Exten", ch.exten, "Priority", "1", "Uniqueid", ch.uid, "Linkedid", ch.linked,
			"Application", app, "ApplicationData", appData, "Duration", durHMS(now.Sub(started)), "BridgeId", ch.bridge)
	}
	l.finish("CoreShowChannelsComplete")
}
