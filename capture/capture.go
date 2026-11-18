package capture

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/afpacket"
	"github.com/google/gopacket/layers"
)

type SavedPacket struct {
	Data      []byte
	Timestamp time.Time
	Iface     string
}

type PacketEvent struct {
	SrcIP     net.IP
	DstIP     net.IP
	SrcPort   uint16
	DstPort   uint16
	Protocol  uint8
	Payload   []byte
	Timestamp time.Time
	PacketLen int
	Interface string
	Direction Direction
	IsDNS     bool
	IsIPv6    bool
}

type Direction int

const (
	DirectionOutbound Direction = iota
	DirectionInbound
)

type Engine struct {
	mu           sync.RWMutex
	handles      map[string]*afpacket.TPacket
	localIPs     []net.IP
	events       chan PacketEvent
	ifaces       []string
	currentIface string
	paused       bool
	stopped      bool
	stopCh       chan struct{}
	pktBuf       []SavedPacket
	maxBuf       int
}

func NewEngine(ifaces []string) (*Engine, error) {
	ips, err := getLocalIPs()
	if err != nil {
		return nil, fmt.Errorf("get local IPs: %w", err)
	}

	return &Engine{
		handles:      make(map[string]*afpacket.TPacket),
		localIPs:     ips,
		events:       make(chan PacketEvent, 10000),
		ifaces:       ifaces,
		currentIface: "all",
		stopCh:       make(chan struct{}),
		pktBuf:       make([]SavedPacket, 0, 50000),
		maxBuf:       500000,
	}, nil
}

func (e *Engine) Events() <-chan PacketEvent { return e.events }

func (e *Engine) Start() error {
	for _, name := range e.ifaces {
		if err := e.openIface(name); err != nil {
			log.Printf("Warning: cannot open %s: %v", name, err)
			continue
		}
	}
	if len(e.handles) == 0 {
		return fmt.Errorf("no interfaces could be opened")
	}
	for name, handle := range e.handles {
		go e.captureLoop(name, handle)
	}
	go e.refreshLocalIPs()
	return nil
}

func (e *Engine) openIface(name string) error {
	handle, err := afpacket.NewTPacket(
		afpacket.OptInterface(name),
		afpacket.OptFrameSize(65536),
		afpacket.OptBlockSize(128*1024),
		afpacket.OptNumBlocks(128),
		afpacket.OptPollTimeout(time.Duration(200)), // 200ms (value is in ms)
	)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.handles[name] = handle
	e.mu.Unlock()
	log.Printf("Capturing on %s", name)
	return nil
}

func (e *Engine) captureLoop(iface string, handle *afpacket.TPacket) {
	for {
		select {
		case <-e.stopCh:
			return
		default:
		}

		e.mu.RLock()
		if e.paused {
			e.mu.RUnlock()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		ifaceFilter := e.currentIface
		e.mu.RUnlock()

		if ifaceFilter != "all" && ifaceFilter != iface {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		data, _, err := handle.ReadPacketData()
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		ev := e.parsePacket(data, iface)
		if ev == nil {
			continue
		}
		if shouldSkipByDefault(ev) {
			continue
		}
		e.bufferPacket(data, iface)
		select {
		case e.events <- *ev:
		default:
		}
	}
}

func (e *Engine) parsePacket(data []byte, iface string) *PacketEvent {
	packet := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.DecodeOptions{
		Lazy:   true,
		NoCopy: true,
	})

	netLayer := packet.NetworkLayer()
	if netLayer == nil {
		return nil
	}
	transLayer := packet.TransportLayer()
	if transLayer == nil {
		return nil
	}

	var srcIP, dstIP net.IP
	var protocol uint8
	isIPv6 := false

	switch l := netLayer.(type) {
	case *layers.IPv4:
		srcIP = l.SrcIP
		dstIP = l.DstIP
		protocol = uint8(l.Protocol)
	case *layers.IPv6:
		srcIP = l.SrcIP
		dstIP = l.DstIP
		protocol = uint8(l.NextHeader)
		isIPv6 = true
	default:
		return nil
	}

	var srcPort, dstPort uint16
	var payload []byte

	switch l := transLayer.(type) {
	case *layers.TCP:
		srcPort = uint16(l.SrcPort)
		dstPort = uint16(l.DstPort)
		if p := l.LayerPayload(); len(p) > 0 {
			payload = make([]byte, len(p))
			copy(payload, p)
		}
	case *layers.UDP:
		srcPort = uint16(l.SrcPort)
		dstPort = uint16(l.DstPort)
		if p := l.LayerPayload(); len(p) > 0 {
			payload = make([]byte, len(p))
			copy(payload, p)
		}
	default:
		return nil
	}

	isDNS := (protocol == 17 && (srcPort == 53 || dstPort == 53 || srcPort == 5353 || dstPort == 5353 || srcPort == 5355 || dstPort == 5355)) ||
		(protocol == 6 && (srcPort == 53 || dstPort == 53))

	dir := e.determineDirection(srcIP)

	return &PacketEvent{
		SrcIP:     srcIP,
		DstIP:     dstIP,
		SrcPort:   srcPort,
		DstPort:   dstPort,
		Protocol:  protocol,
		Payload:   payload,
		Timestamp: time.Now(),
		PacketLen: len(data),
		Interface: iface,
		Direction: dir,
		IsDNS:     isDNS,
		IsIPv6:    isIPv6,
	}
}

func (e *Engine) determineDirection(srcIP net.IP) Direction {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, local := range e.localIPs {
		if local.Equal(srcIP) {
			return DirectionOutbound
		}
	}
	return DirectionInbound
}

func (e *Engine) Pause()                { e.mu.Lock(); e.paused = true; e.mu.Unlock() }
func (e *Engine) Resume()               { e.mu.Lock(); e.paused = false; e.mu.Unlock() }
func (e *Engine) IsPaused() bool        { e.mu.RLock(); defer e.mu.RUnlock(); return e.paused }

func (e *Engine) TogglePause() bool {
	e.mu.Lock()
	e.paused = !e.paused
	p := e.paused
	e.mu.Unlock()
	return p
}

func (e *Engine) SetInterface(name string) {
	e.mu.Lock()
	e.currentIface = name
	e.mu.Unlock()
}

func (e *Engine) CurrentInterface() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentIface
}

func (e *Engine) Interfaces() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r := make([]string, len(e.ifaces))
	copy(r, e.ifaces)
	return r
}

func (e *Engine) Stop() {
	close(e.stopCh)
	e.mu.Lock()
	defer e.mu.Unlock()
	for name, h := range e.handles {
		h.Close()
		log.Printf("Stopped %s", name)
	}
}

func (e *Engine) bufferPacket(data []byte, iface string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pktBuf) >= e.maxBuf {
		e.pktBuf = e.pktBuf[1:]
	}
	pkt := make([]byte, len(data))
	copy(pkt, data)
	e.pktBuf = append(e.pktBuf, SavedPacket{
		Data:      pkt,
		Timestamp: time.Now(),
		Iface:     iface,
	})
}

func (e *Engine) CollectBuffer() []SavedPacket {
	e.mu.Lock()
	defer e.mu.Unlock()
	buf := e.pktBuf
	e.pktBuf = make([]SavedPacket, 0, 50000)
	return buf
}

func (e *Engine) BufferCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.pktBuf)
}

func (e *Engine) refreshLocalIPs() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ips, err := getLocalIPs()
		if err == nil {
			e.mu.Lock()
			e.localIPs = ips
			e.mu.Unlock()
		}
	}
}

func getLocalIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ips = append(ips, ipnet.IP)
	}
	return ips, nil
}

func GetActiveIfaces() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		names = append(names, iface.Name)
	}
	return names, nil
}

func shouldSkipByDefault(ev *PacketEvent) bool {
	if ev.SrcIP.IsLoopback() || ev.DstIP.IsLoopback() {
		return true
	}
	if ev.SrcIP.IsLinkLocalUnicast() || ev.DstIP.IsLinkLocalUnicast() {
		return true
	}
	if ev.SrcIP.IsLinkLocalMulticast() || ev.DstIP.IsLinkLocalMulticast() {
		return true
	}
	if ev.SrcIP.IsMulticast() || ev.DstIP.IsMulticast() {
		return true
	}
	if ev.SrcIP.Equal(net.IPv4zero) || ev.DstIP.Equal(net.IPv4zero) {
		return true
	}
	if ev.Protocol == 17 {
		if ev.SrcPort == 5353 || ev.DstPort == 5353 {
			return true
		}
		if ev.SrcPort == 5355 || ev.DstPort == 5355 {
			return true
		}
		if ev.SrcPort == 1900 || ev.DstPort == 1900 {
			return true
		}
	}
	return false
}

func IsRFC1918(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 10:
		return true
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return true
	case ip4[0] == 192 && ip4[1] == 168:
		return true
	}
	return false
}

func IsLAN(ip net.IP) bool {
	return IsRFC1918(ip) || ip.IsLinkLocalUnicast()
}
