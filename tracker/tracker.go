package tracker

import (
	"fmt"
	"net"
	"sync"
	"time"
)

type FlowKey struct {
	SrcIP    string
	DstIP    string
	SrcPort  uint16
	DstPort  uint16
	Protocol uint8
}

func (k FlowKey) String() string {
	return fmt.Sprintf("%s:%d-%s:%d/%d", k.SrcIP, k.SrcPort, k.DstIP, k.DstPort, k.Protocol)
}

func (k FlowKey) Reverse() FlowKey {
	return FlowKey{
		SrcIP:    k.DstIP,
		DstIP:    k.SrcIP,
		SrcPort:  k.DstPort,
		DstPort:  k.SrcPort,
		Protocol: k.Protocol,
	}
}

type FlowEvent struct {
	SrcIP     net.IP
	DstIP     net.IP
	SrcPort   uint16
	DstPort   uint16
	Protocol  uint8
	Payload   []byte
	Timestamp time.Time
	PacketLen int
	Interface string
	Direction DirectionType
	IsDNS     bool
	IsIPv6    bool
}

type DirectionType int

const (
	DirectionOutbound DirectionType = iota
	DirectionInbound
)

type PacketRecord struct {
	Timestamp time.Time
	Direction DirectionType
	Len       int
	Summary   string
}

type Connection struct {
	Key     FlowKey

	LocalIP    net.IP
	RemoteIP   net.IP
	LocalPort  uint16
	RemotePort uint16
	Protocol   uint8

	ProtoInfo ProtocolInfo

	BytesSent   uint64
	BytesRecv   uint64
	PacketsSent uint64
	PacketsRecv uint64

	FirstSeen  time.Time
	LastSeen   time.Time
	LastPacket time.Time

	Interface   string
	PID         int
	ProcessName string
	ProcessCmd  string

	BytesPerSec float64
	IsInbound   bool
	IsLAN       bool
	IsDNS       bool

	PacketHistory []PacketRecord
	LastPayload   []byte
}

type Store struct {
	mu         sync.RWMutex
	conns      map[string]*Connection
	altKeys    map[string]string
	order      []string
	maxConns   int
	maxHistory int
}

func NewStore(maxConns int) *Store {
	return &Store{
		conns:      make(map[string]*Connection),
		altKeys:    make(map[string]string),
		order:      make([]string, 0),
		maxConns:   maxConns,
		maxHistory: 100,
	}
}

func canonicalKey(localIP, remoteIP net.IP, localPort, remotePort uint16, protocol uint8) FlowKey {
	return FlowKey{
		SrcIP:    localIP.String(),
		SrcPort:  localPort,
		DstIP:    remoteIP.String(),
		DstPort:  remotePort,
		Protocol: protocol,
	}
}

func (s *Store) HandleEvent(ev FlowEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := FlowKey{
		SrcIP:    ev.SrcIP.String(),
		DstIP:    ev.DstIP.String(),
		SrcPort:  ev.SrcPort,
		DstPort:  ev.DstPort,
		Protocol: ev.Protocol,
	}

	// Try to find existing connection via canonical key or alt keys
	conn, ok := s.conns[key.String()]
	if !ok {
		conn, ok = s.conns[key.Reverse().String()]
	}
	if !ok {
		if alt, found := s.altKeys[key.String()]; found {
			conn, ok = s.conns[alt]
		}
	}
	if !ok {
		if alt, found := s.altKeys[key.Reverse().String()]; found {
			conn, ok = s.conns[alt]
		}
	}

	if ok && conn != nil {
		s.updateConn(conn, ev, key)
		return
	}

	// Determine canonical direction
	isInbound := ev.Direction == DirectionInbound
	var localIP, remoteIP net.IP
	var localPort, remotePort uint16

	if !isInbound {
		localIP, remoteIP = ev.SrcIP, ev.DstIP
		localPort, remotePort = ev.SrcPort, ev.DstPort
	} else {
		localIP, remoteIP = ev.DstIP, ev.SrcIP
		localPort, remotePort = ev.DstPort, ev.SrcPort
	}

	isLAN := false
	if r := remoteIP.To4(); r != nil {
		switch {
		case r[0] == 10:
			isLAN = true
		case r[0] == 172 && r[1] >= 16 && r[1] <= 31:
			isLAN = true
		case r[0] == 192 && r[1] == 168:
			isLAN = true
		}
	}

	protoInfo := DetectProtocol(ev.Payload, ev.SrcPort, ev.DstPort, ev.Protocol)
	ck := canonicalKey(localIP, remoteIP, localPort, remotePort, ev.Protocol)

	conn = &Connection{
		Key:              ck,
		LocalIP:          localIP,
		RemoteIP:         remoteIP,
		LocalPort:        localPort,
		RemotePort:       remotePort,
		Protocol:         ev.Protocol,
		ProtoInfo:        protoInfo,
		FirstSeen:        ev.Timestamp,
		LastSeen:         ev.Timestamp,
		LastPacket:       ev.Timestamp,
		Interface:        ev.Interface,
		IsInbound:        isInbound,
		IsLAN:            isLAN,
		IsDNS:            ev.IsDNS || protoInfo.IsDNS,
		PacketHistory:    make([]PacketRecord, 0, s.maxHistory),
	}

	s.conns[ck.String()] = conn
	s.altKeys[key.String()] = ck.String()
	s.altKeys[key.Reverse().String()] = ck.String()
	s.order = append(s.order, ck.String())

	s.addPacketToConn(conn, ev)

	if len(s.conns) > s.maxConns {
		s.trimOld()
	}
}

func (s *Store) updateConn(conn *Connection, ev FlowEvent, key FlowKey) {
	if ev.Direction == DirectionOutbound {
		conn.BytesSent += uint64(ev.PacketLen)
		conn.PacketsSent++
	} else {
		conn.BytesRecv += uint64(ev.PacketLen)
		conn.PacketsRecv++
	}

	conn.LastSeen = ev.Timestamp
	conn.LastPacket = ev.Timestamp
	conn.Interface = ev.Interface

	if len(ev.Payload) > 0 && conn.ProtoInfo.SNI == "" {
		info := DetectProtocol(ev.Payload, ev.SrcPort, ev.DstPort, ev.Protocol)
		if info.SNI != "" || info.DNSQuery != "" {
			conn.ProtoInfo = info
		}
	}

	// Track alternate keys
	s.altKeys[key.String()] = conn.Key.String()
	s.altKeys[key.Reverse().String()] = conn.Key.String()

	s.addPacketToConn(conn, ev)
}

func (s *Store) addPacketToConn(conn *Connection, ev FlowEvent) {
	if ev.PacketLen <= 0 {
		return
	}
	dir := ev.Direction
	summary := packetSummary(ev, conn.ProtoInfo)
	pr := PacketRecord{
		Timestamp: ev.Timestamp,
		Direction: dir,
		Len:       ev.PacketLen,
		Summary:   summary,
	}
	if len(conn.PacketHistory) >= s.maxHistory {
		conn.PacketHistory = conn.PacketHistory[1:]
	}
	conn.PacketHistory = append(conn.PacketHistory, pr)

	// Store truncated application payload for hex view
	if len(ev.Payload) > 0 {
		maxPayload := 2048
		if len(ev.Payload) > maxPayload {
			conn.LastPayload = make([]byte, maxPayload)
			copy(conn.LastPayload, ev.Payload[:maxPayload])
		} else {
			conn.LastPayload = make([]byte, len(ev.Payload))
			copy(conn.LastPayload, ev.Payload)
		}
	}
}

func packetSummary(ev FlowEvent, pi ProtocolInfo) string {
	switch {
	case pi.SNI != "":
		return fmt.Sprintf("TLS → %s", pi.SNI)
	case pi.DNSQuery != "":
		return fmt.Sprintf("DNS: %s", pi.DNSQuery)
	case pi.HTTPMethod != "":
		host := pi.HTTPHost
		if host == "" {
			host = ev.DstIP.String()
		}
		return fmt.Sprintf("%s http://%s%s", pi.HTTPMethod, host, pi.HTTPPath)
	case pi.Protocol == ProtoQUIC:
		return fmt.Sprintf("QUIC → %s", portName(ev.DstPort))
	default:
		ip := ev.DstIP
		port := ev.DstPort
		if ev.Direction == DirectionInbound {
			ip = ev.SrcIP
			port = ev.SrcPort
		}
		return fmt.Sprintf("%s:%s", ip, portName(port))
	}
}

func portName(port uint16) string {
	switch port {
	case 80:
		return "http"
	case 443:
		return "https"
	case 22:
		return "ssh"
	case 53:
		return "dns"
	case 21:
		return "ftp"
	case 25:
		return "smtp"
	case 110:
		return "pop3"
	case 143:
		return "imap"
	case 993:
		return "imaps"
	case 995:
		return "pop3s"
	case 3306:
		return "mysql"
	case 5432:
		return "postgres"
	case 6379:
		return "redis"
	case 8080:
		return "http-alt"
	case 8443:
		return "https-alt"
	default:
		return fmt.Sprintf("%d", port)
	}
}

func (s *Store) GetConnections() []*Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Connection, 0, len(s.order))
	for _, key := range s.order {
		if conn, ok := s.conns[key]; ok {
			// Recalculate bytes/sec
			elapsed := time.Since(conn.FirstSeen).Seconds()
			if elapsed > 1 {
				conn.BytesPerSec = float64(conn.BytesSent+conn.BytesRecv) / elapsed
			}
			result = append(result, conn)
		}
	}
	return result
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.conns)
}

func (s *Store) SetProcess(keyStr string, pid int, name, cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn, ok := s.conns[keyStr]; ok {
		conn.PID = pid
		conn.ProcessName = name
		conn.ProcessCmd = cmd
	}
}

func (s *Store) SetProcessByPort(localPort uint16, protocol uint8, pid int, name, cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		if conn.LocalPort == localPort && conn.Protocol == protocol {
			conn.PID = pid
			conn.ProcessName = name
			conn.ProcessCmd = cmd
		}
	}
}

func (s *Store) trimOld() {
	for len(s.conns) > s.maxConns {
		var oldestKey string
		var oldestTime time.Time
		for _, key := range s.order {
			if conn, ok := s.conns[key]; ok {
				if oldestKey == "" || conn.LastSeen.Before(oldestTime) {
					oldestKey = key
					oldestTime = conn.LastSeen
				}
			}
		}
		if oldestKey == "" {
			break
		}
		delete(s.conns, oldestKey)
		for k, v := range s.altKeys {
			if v == oldestKey {
				delete(s.altKeys, k)
			}
		}
		s.removeOrder(oldestKey)
	}
}

func (s *Store) removeOrder(key string) {
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

func (s *Store) ExpireOld(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for _, conn := range s.conns {
		if conn.LastSeen.Before(cutoff) {
			delete(s.conns, conn.Key.String())
			for k, v := range s.altKeys {
				if v == conn.Key.String() {
					delete(s.altKeys, k)
				}
			}
			s.removeOrder(conn.Key.String())
		}
	}
}
