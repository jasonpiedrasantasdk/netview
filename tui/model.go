package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonpiedrasantasdk/netview/tracker"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time
type refreshMsg time.Time
type errMsg struct{ error }

type saveDoneMsg struct {
	path   string
	err    error
	noQuit bool
}

type viewMode int

const (
	viewInbound  viewMode = iota
	viewOutbound
	viewBoth
)

type saveState int

const (
	saveIdle saveState = iota
	saveConfirm
	saveSaving
	saveDone
)

type Model struct {
	store    *tracker.Store
	procMap  *tracker.ProcMapper

	connections []*tracker.Connection
	selected    int
	width       int
	height      int
	ready       bool

	showLAN   bool
	showDNS   bool
	filterLAN bool
	filterDNS bool

	ifaces       []string
	currentIface string
	paused       bool

	showDetail bool
	detailTab  int

	helpVisible   bool
	err           error
	searchMode    bool
	searchQuery   string
	expandedIndex int

	detailVP   viewport.Model

	viewMode   viewMode

	saveState  saveState
	savePath   string
	saveErr    error
	bufCollect func() int // callback to get buffer count
	saveFn     func() (string, error)
}

func New(store *tracker.Store, procMap *tracker.ProcMapper, ifaces []string, saveFn func() (string, error), bufCollect func() int) Model {
	return Model{
		store:         store,
		procMap:       procMap,
		ifaces:        ifaces,
		currentIface:  "all",
		filterLAN:     true,
		filterDNS:     true,
		showLAN:       false,
		showDNS:       false,
		selected:      0,
		expandedIndex: -1,
		detailTab:     0,
		saveFn:        saveFn,
		bufCollect:    bufCollect,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.tick(),
		m.refresh(),
	)
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) refresh() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return refreshMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.detailVP = viewport.New(msg.Width-4, 10)
		m.detailVP.Style = lipgloss.NewStyle().Padding(0, 1)
		m.ready = true

	case tea.KeyMsg:
		// Save dialog key routing
		if m.saveState == saveDone {
			m.saveState = saveIdle
			break
		}
		if m.saveState == saveConfirm {
			switch msg.String() {
			case "y", "Y":
				m.saveState = saveSaving
				return m, func() tea.Msg {
					path, err := m.saveFn()
					return saveDoneMsg{path: path, err: err}
				}
			case "n", "N":
				m.saveState = saveSaving
				return m, func() tea.Msg {
					path, err := m.saveFn()
					return saveDoneMsg{path: path, err: err, noQuit: true}
				}
			case "esc":
				m.saveState = saveIdle
			}
			break
		}

		// Search mode key handling
		if m.searchMode {
			switch msg.String() {
			case "esc":
				m.searchMode = false
				m.searchQuery = ""
			case "enter":
				m.searchMode = false
			case "backspace":
				if len(m.searchQuery) > 0 {
					m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				}
			default:
				if len(msg.Runes) > 0 {
					m.searchQuery += string(msg.Runes)
				}
			}
			break
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "p":
			m.paused = !m.paused

		case "l":
			m.filterLAN = !m.filterLAN
			if !m.filterLAN {
				m.showLAN = true
			}

		case "d":
			m.filterDNS = !m.filterDNS
			if !m.filterDNS {
				m.showDNS = true
			}

		case "i":
			m.cycleInterface()
			m.selected = 0

		case "up", "k":
			m.selected--
			if m.selected < 0 {
				m.selected = 0
			}
			m.showDetail = true

		case "down", "j":
			m.selected++
			if m.selected >= len(m.filteredConns()) {
				m.selected = len(m.filteredConns()) - 1
				if m.selected < 0 {
					m.selected = 0
				}
			}
			m.showDetail = true

		case "enter":
			m.showDetail = !m.showDetail

		case "tab":
			m.detailTab++
			if m.detailTab > 2 {
				m.detailTab = 0
			}

		case "right", "left":
			if m.showDetail {
				if msg.String() == "right" {
					m.detailTab++
				} else {
					m.detailTab--
				}
				if m.detailTab < 0 {
					m.detailTab = 2
				}
				if m.detailTab > 2 {
					m.detailTab = 0
				}
			}

		case "?":
			m.helpVisible = !m.helpVisible

		case "g":
			m.selected = 0

		case "G":
			m.selected = len(m.filteredConns()) - 1
			if m.selected < 0 {
				m.selected = 0
			}

		case "s":
			if m.saveState == saveIdle {
				m.saveState = saveConfirm
				m.saveErr = nil
				m.savePath = ""
			}

		case "w":
			m.viewMode = (m.viewMode + 1) % 3

		case "+":
			conns := m.filteredConns()
			if m.selected >= 0 && m.selected < len(conns) {
				if m.expandedIndex == m.selected {
					m.expandedIndex = -1
				} else {
					m.expandedIndex = m.selected
				}
			}

		case "r":
			m.updateProcessMap()

		case "/":
			m.searchMode = true
			m.searchQuery = ""
		}

	case saveDoneMsg:
		m.saveState = saveDone
		m.savePath = msg.path
		m.saveErr = msg.err
		if msg.err == nil && !msg.noQuit {
			return m, tea.Sequence(tea.Printf("Saved to %s", msg.path), tea.Quit)
		}

	case tickMsg:
		if !m.paused {
			m.refreshConnections()
			if m.procMap != nil {
				m.procMap.Update()
			}
			m.updateProcesses()
		}
		cmds = append(cmds, m.tick())

	case refreshMsg:
		// Periodic full refresh
		cmds = append(cmds, m.refresh())

	case errMsg:
		m.err = msg.error

	case tea.QuitMsg:
		return m, tea.Quit
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) refreshConnections() {
	m.connections = m.store.GetConnections()
}

func (m *Model) updateProcesses() {
	if m.procMap == nil {
		return
	}
	for _, conn := range m.connections {
		if conn.PID == 0 {
			entry, found := m.procMap.Lookup(conn.LocalPort, conn.Protocol)
			if found {
				m.store.SetProcess(conn.Key.String(), entry.PID, entry.Name, entry.CmdLine)
				conn.PID = entry.PID
				conn.ProcessName = entry.Name
				conn.ProcessCmd = entry.CmdLine
			}
		}
	}
}

func (m *Model) updateProcessMap() {
	if m.procMap != nil {
		m.procMap.Update()
		m.updateProcesses()
	}
}

func (m *Model) cycleInterface() {
	ifaces := make([]string, 0, len(m.ifaces)+1)
	ifaces = append(ifaces, "all")
	ifaces = append(ifaces, m.ifaces...)

	current := 0
	for i, iface := range ifaces {
		if iface == m.currentIface {
			current = i
			break
		}
	}
	next := (current + 1) % len(ifaces)
	m.currentIface = ifaces[next]
}

func (m Model) filteredConns() []*tracker.Connection {
	if m.connections == nil {
		return nil
	}

	var result []*tracker.Connection
	for _, conn := range m.connections {
		// Interface filter
		if m.currentIface != "all" && conn.Interface != m.currentIface {
			continue
		}
		// LAN filter
		if m.filterLAN && conn.IsLAN {
			continue
		}
		// DNS filter
		if m.filterDNS && conn.IsDNS {
			continue
		}
		// View mode filter
		switch m.viewMode {
		case viewInbound:
			if !conn.IsInbound {
				continue
			}
		case viewOutbound:
			if conn.IsInbound {
				continue
			}
		}
		result = append(result, conn)
	}

	// Search filter
	if m.searchQuery != "" {
		q := strings.ToLower(m.searchQuery)
		var searched []*tracker.Connection
		for _, conn := range result {
			if searchMatch(conn, q) {
				searched = append(searched, conn)
			}
		}
		result = searched
	}

	// Sort by last seen (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastSeen.After(result[j].LastSeen)
	})

	return result
}

func searchMatch(conn *tracker.Connection, q string) bool {
	if strings.Contains(strings.ToLower(conn.RemoteIP.String()), q) {
		return true
	}
	if strings.Contains(strings.ToLower(conn.LocalIP.String()), q) {
		return true
	}
	port := fmt.Sprintf("%d", conn.RemotePort)
	if port == q || strings.Contains(port, q) {
		return true
	}
	if strings.Contains(strings.ToLower(conn.ProtoInfo.SNI), q) {
		return true
	}
	if strings.Contains(strings.ToLower(conn.ProtoInfo.DNSQuery), q) {
		return true
	}
	if strings.Contains(strings.ToLower(conn.ProtoInfo.HTTPHost), q) {
		return true
	}
	if strings.Contains(strings.ToLower(conn.ProcessName), q) {
		return true
	}
	if strings.Contains(strings.ToLower(conn.ProcessCmd), q) {
		return true
	}
	return false
}

func (m Model) View() string {
	if !m.ready {
		return "Starting NetView..."
	}

	if m.helpVisible {
		return m.helpView()
	}

	if m.saveState != saveIdle {
		return m.saveOverlay()
	}

	var b strings.Builder

	// Header
	b.WriteString(m.headerView())
	b.WriteString("\n")

	// Connection list
	conns := m.filteredConns()

	// Sticky column headers
	colHeaders := m.columnHeaderView()
	if colHeaders != "" {
		b.WriteString(colHeaders)
		b.WriteString("\n")
	}

	listHeight := m.height - 8
	if m.showDetail && len(conns) > 0 {
		listHeight = m.height / 2
		if listHeight > 20 {
			listHeight = m.height*2/3 - 3
		}
	}
	if listHeight < 3 {
		listHeight = 3
	}

	tableContent := m.connTableView(conns, listHeight)

	table := connListStyle.
		Width(m.width - 4).
		Height(listHeight).
		Render(tableContent)
	b.WriteString(table)
	b.WriteString("\n")

	// Detail panel
	if m.showDetail && len(conns) > 0 && m.selected >= 0 && m.selected < len(conns) {
		detailContent := m.detailView(conns[m.selected])
		detailHeight := m.height - listHeight - 6
		if detailHeight < 6 {
			detailHeight = 6
		}
		dv := detailStyle.
			Width(m.width - 4).
			Height(detailHeight).
			Render(detailContent)
		b.WriteString(dv)
		b.WriteString("\n")
	}

	b.WriteString(m.statusView())

	return b.String()
}

func (m Model) headerView() string {
	var left, right string

	left = " NetView"
	left += "  [" + m.currentIface + "]"

	if m.filterLAN {
		left += "  LAN:✕"
	} else {
		left += "  LAN:✓"
	}
	if m.filterDNS {
		left += " DNS:✕"
	} else {
		left += " DNS:✓"
	}

	count := m.store.Count()
	filtered := len(m.filteredConns())
	totalPackets := 0
	for _, c := range m.connections {
		totalPackets += int(c.PacketsSent + c.PacketsRecv)
	}

	right = fmt.Sprintf("%d pkts  ", totalPackets)
	if filtered < count {
		right += fmt.Sprintf("%d/%d", filtered, count)
	} else {
		right += fmt.Sprintf("%d", count)
	}
	right += " conns"

	switch m.viewMode {
	case viewInbound:
		right += "  IN"
	case viewOutbound:
		right += "  OUT"
	case viewBoth:
		right += "  ALL"
	}

	if m.paused {
		right += "  PAUSED"
	}

	return headerBarStyle.
		Width(m.width - 4).
		Render(left + "  │  " + right)
}

func (m Model) columnHeaderView() string {
	availWidth := m.width - 8
	if availWidth < 50 {
		return ""
	}

	colProto := 6
	colTX := 8
	colRX := 8
	colProc := 22
	colSep := 3
	fixedWidth := colProto + colTX + colRX + colProc + colSep*3
	colRemote := availWidth - fixedWidth
	if colRemote < 15 {
		colRemote = 15
	}

	headerSep := mutedStyle.Render(strings.Repeat("─", availWidth))
	headProto := lipgloss.NewStyle().Foreground(colHeader).Bold(true).Width(colProto).Render("Proto")
	headRemote := lipgloss.NewStyle().Foreground(colHeader).Bold(true).Width(colRemote).Render("Remote")
	headTX := lipgloss.NewStyle().Foreground(colHeader).Bold(true).Width(colTX).Align(lipgloss.Right).Render("TX")
	headRX := lipgloss.NewStyle().Foreground(colHeader).Bold(true).Width(colRX).Align(lipgloss.Right).Render("RX")
	headProc := lipgloss.NewStyle().Foreground(colHeader).Bold(true).Width(colProc).Render("Process")
	sep := mutedStyle.Render("│")

	return lipgloss.JoinHorizontal(lipgloss.Left, "  ", headProto, sep, headRemote, sep, headTX, sep, headRX, sep, headProc) + "\n" + headerSep
}

func (m Model) connTableView(conns []*tracker.Connection, height int) string {
	if conns == nil || len(conns) == 0 {
		return mutedStyle.Render(" No connections yet. Waiting for traffic...")
	}

	var b strings.Builder

	availWidth := m.width - 8
	if availWidth < 50 {
		return " Terminal too narrow"
	}

	colProto := 6
	colTX := 8
	colRX := 8
	colProc := 22
	colSep := 3
	fixedWidth := colProto + colTX + colRX + colProc + colSep*3
	colRemote := availWidth - fixedWidth
	if colRemote < 15 {
		colRemote = 15
	}

	start, end := 0, len(conns)
	if end > height {
		end = height
	}
	scrollOffset := 0
	if m.selected >= end && len(conns) > 0 {
		scrollOffset = m.selected - height + 1
	}
	start += scrollOffset
	end += scrollOffset
	if end > len(conns) {
		end = len(conns)
	}
	if start < 0 {
		start = 0
	}

	for i := start; i < end; i++ {
		conn := conns[i]
		col := m.connCols(conn, colRemote, colTX, colRX, colProc)
		row := lipgloss.JoinHorizontal(lipgloss.Left, col...)
		if i == m.selected {
			b.WriteString("▸ ")
			b.WriteString(selectedStyle.Render(row))
		} else {
			b.WriteString("  ")
			b.WriteString(normalStyle.Render(row))
		}
		b.WriteString("\n")

		// Expanded row
		if m.expandedIndex == i {
			expandText := m.expandedRow(conn)
			if expandText != "" {
				b.WriteString(mutedStyle.Render("  " + expandText))
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func (m Model) connCols(conn *tracker.Connection, wRemote, wTX, wRX, wProc int) []string {
	proto := conn.ProtoInfo.Protocol
	if proto == "" || proto == "?" || proto == "TCP" || proto == "UDP" {
		switch conn.Protocol {
		case 6:
			proto = "TCP"
		case 17:
			proto = "UDP"
		default:
			proto = fmt.Sprintf("P%d", conn.Protocol)
		}
	}
	if conn.ProtoInfo.HTTPMethod != "" {
		proto = conn.ProtoInfo.HTTPMethod
	}
	protoStr := truncate(proto, 5)

	remoteStr := conn.RemoteIP.String()
	if conn.ProtoInfo.SNI != "" {
		remoteStr = conn.ProtoInfo.SNI
	} else if conn.ProtoInfo.DNSQuery != "" {
		remoteStr = conn.ProtoInfo.DNSQuery
	} else if conn.ProtoInfo.HTTPHost != "" {
		remoteStr = conn.ProtoInfo.HTTPHost
	}
	remoteStr = fmt.Sprintf("%s:%d", remoteStr, conn.RemotePort)
	remoteStr = truncate(remoteStr, wRemote)

	tx := formatBytes(conn.BytesSent)
	rx := formatBytes(conn.BytesRecv)

	proc := "?"
	if conn.ProcessName != "" {
		proc = truncate(conn.ProcessName, wProc-1)
	}

	sep := mutedStyle.Render("│")

	col1 := protoStyle(proto).Width(6).Render(protoStr)
	col2 := normalStyle.Width(wRemote).Render(remoteStr)
	col3 := normalStyle.Width(wTX).Align(lipgloss.Right).Render(tx)
	col4 := normalStyle.Width(wRX).Align(lipgloss.Right).Render(rx)
	col5 := normalStyle.Width(wProc).Render(proc)

	return []string{col1, sep, col2, sep, col3, sep, col4, sep, col5}
}

func ProtoName(protocol uint8, detected string) string {
	if detected != "" && detected != "?" && detected != "TCP" && detected != "UDP" {
		return detected
	}
	switch protocol {
	case 6:
		return "TCP"
	case 17:
		return "UDP"
	case 1:
		return "ICMP"
	case 58:
		return "ICMPv6"
	default:
		return fmt.Sprintf("%d", protocol)
	}
}

func (m Model) expandedRow(conn *tracker.Connection) string {
	avail := m.width - 14
	if avail < 20 {
		return ""
	}
	var parts []string
	if conn.ProcessName != "" {
		cmd := conn.ProcessName
		if conn.ProcessCmd != "" {
			cmd = truncate(conn.ProcessCmd, avail-4)
		}
		parts = append(parts, fmt.Sprintf("pid:%d %s", conn.PID, cmd))
	}
	if conn.ProtoInfo.SNI != "" {
		parts = append(parts, fmt.Sprintf("sni:%s", conn.ProtoInfo.SNI))
	}
	if conn.ProtoInfo.DNSQuery != "" {
		parts = append(parts, fmt.Sprintf("dns:%s", conn.ProtoInfo.DNSQuery))
	}
	remote := fmt.Sprintf("%s:%d", conn.RemoteIP, conn.RemotePort)
	parts = append(parts, remote)
	line := strings.Join(parts, "  │  ")
	return truncate(line, avail)
}

func (m Model) detailView(conn *tracker.Connection) string {
	var b strings.Builder

	tabs := []string{"Summary", "Packets", "Hex"}
	for i, tab := range tabs {
		if i == m.detailTab {
			b.WriteString(activeTabStyle.Render(tab))
		} else {
			b.WriteString(tabStyle.Render(tab))
		}
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", m.width-8)))
	b.WriteString("\n")

	switch m.detailTab {
	case 0:
		b.WriteString(m.summaryTab(conn))
	case 1:
		b.WriteString(m.packetsTab(conn))
	case 2:
		b.WriteString(m.hexTab(conn))
	}

	return b.String()
}

func (m Model) summaryTab(conn *tracker.Connection) string {
	var b strings.Builder

	proto := conn.ProtoInfo.Protocol
	if proto == "" || proto == "?" {
		proto = ProtoName(conn.Protocol, "")
	}

	b.WriteString(fmt.Sprintf("Protocol:  %s\n", protoStyle(proto).Render(proto)))

	if conn.ProtoInfo.SNI != "" {
		b.WriteString(fmt.Sprintf("SNI:       %s\n", conn.ProtoInfo.SNI))
	}
	if conn.ProtoInfo.TLSVersion != "" {
		b.WriteString(fmt.Sprintf("TLS:       %s\n", conn.ProtoInfo.TLSVersion))
	}
	if conn.ProtoInfo.ALPN != "" {
		b.WriteString(fmt.Sprintf("ALPN:      %s\n", conn.ProtoInfo.ALPN))
	}
	if conn.ProtoInfo.DNSQuery != "" {
		b.WriteString(fmt.Sprintf("DNS:       %s\n", conn.ProtoInfo.DNSQuery))
	}
	if conn.ProtoInfo.HTTPMethod != "" {
		b.WriteString(fmt.Sprintf("HTTP:      %s %s", conn.ProtoInfo.HTTPMethod, conn.ProtoInfo.HTTPPath))
		if conn.ProtoInfo.HTTPHost != "" {
			b.WriteString(fmt.Sprintf(" (Host: %s)", conn.ProtoInfo.HTTPHost))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("Remote:    %s:%d\n", conn.RemoteIP, conn.RemotePort))
	b.WriteString(fmt.Sprintf("Local:     %s:%d\n", conn.LocalIP, conn.LocalPort))
	b.WriteString(fmt.Sprintf("Interface: %s\n", conn.Interface))

	b.WriteString(fmt.Sprintf("TX:        %s (%d packets)\n", formatBytes(conn.BytesSent), conn.PacketsSent))
	b.WriteString(fmt.Sprintf("RX:        %s (%d packets)\n", formatBytes(conn.BytesRecv), conn.PacketsRecv))

	if conn.BytesPerSec > 0 {
		b.WriteString(fmt.Sprintf("Rate:      %s\n", formatBytesPerSec(conn.BytesPerSec)))
	}

	if conn.PID > 0 {
		b.WriteString(fmt.Sprintf("Process:   %s (PID: %d)\n", conn.ProcessName, conn.PID))
		if conn.ProcessCmd != "" {
			b.WriteString(fmt.Sprintf("Cmd:       %s\n", truncate(conn.ProcessCmd, m.width-20)))
		}
	}

	age := time.Since(conn.FirstSeen).Round(time.Second)
	b.WriteString(fmt.Sprintf("Age:       %s\n", age))

	// Alert flags
	var alerts []string
	totalBytes := conn.BytesSent + conn.BytesRecv
	if totalBytes > 10<<20 {
		alerts = append(alerts, fmt.Sprintf("⚠ High volume: %s", formatBytes(totalBytes)))
	}
	if !conn.IsLAN && conn.ProtoInfo.SNI != "" {
		b.WriteString(alertStyle.Render(fmt.Sprintf("⚠ SNI: %s\n", conn.ProtoInfo.SNI)))
	}

	for _, a := range alerts {
		b.WriteString(alertStyle.Render(a))
		b.WriteString("\n")
	}

	return b.String()
}

func (m Model) packetsTab(conn *tracker.Connection) string {
	var b strings.Builder

	if len(conn.PacketHistory) == 0 {
		return mutedStyle.Render(" No packets recorded.")
	}

	start := 0
	if len(conn.PacketHistory) > 30 {
		start = len(conn.PacketHistory) - 30
	}

	for i := start; i < len(conn.PacketHistory); i++ {
		p := conn.PacketHistory[i]
		dir := "→"
		if p.Direction == tracker.DirectionInbound {
			dir = "←"
		}
		ts := p.Timestamp.Format("15:04:05.000")
		size := formatBytes(uint64(p.Len))
		b.WriteString(fmt.Sprintf("%s %s %5s  %s\n", ts, dir, size, p.Summary))
	}

	return b.String()
}

func (m Model) hexTab(conn *tracker.Connection) string {
	var b strings.Builder

	if len(conn.LastPayload) == 0 {
		return mutedStyle.Render(" No application payload captured for this connection.")
	}

	payload := conn.LastPayload
	avail := m.width - 12
	hexPerLine := avail / 4
	if hexPerLine > 16 {
		hexPerLine = 16
	}
	if hexPerLine < 4 {
		hexPerLine = 4
	}

	for i := 0; i < len(payload) && i < 4096; i += hexPerLine {
		end := i + hexPerLine
		if end > len(payload) {
			end = len(payload)
		}
		line := payload[i:end]

		// Hex part
		var hexSide string
		for j, bVal := range line {
			if j > 0 && j%2 == 0 {
				hexSide += " "
			}
			hexSide += fmt.Sprintf("%02x", bVal)
		}
		hexPadded := fmt.Sprintf("%-*s", hexPerLine*3-1, hexSide)

		// ASCII part
		var asciiSide string
		for _, bVal := range line {
			if bVal >= 32 && bVal <= 126 {
				asciiSide += string(bVal)
			} else {
				asciiSide += "."
			}
		}

		b.WriteString(fmt.Sprintf("%04x  %s  %s\n", i, hexPadded, asciiSide))
	}

	if len(payload) > 4096 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf(" ... %d more bytes", len(payload)-4096)))
		b.WriteString("\n")
	}

	return b.String()
}

func hotkeyLabel(key, label string) string {
	k := lipgloss.NewStyle().Foreground(colYellow).Bold(true).Render(key)
	return fmt.Sprintf("(%s)%s", k, label)
}

func (m Model) saveOverlay() string {
	var b strings.Builder

	switch m.saveState {
	case saveConfirm:
		count := 0
		if m.bufCollect != nil {
			count = m.bufCollect()
		}
		s := dialogStyle.Render(
			dialogTitle.Render("Save Capture") + "\n\n" +
				fmt.Sprintf("Captured packets: %d\n\n", count) +
				hotkeyLabel("y", "es") + "  save & quit\n" +
				hotkeyLabel("n", "o") + "   save only, keep capturing\n" +
				hotkeyLabel("esc", "cape") + "  cancel",
		)
		b.WriteString(m.centerOverlay(s))

	case saveSaving:
		s := dialogStyle.Render(
			dialogTitle.Render("Saving...") + "\n\n" +
				"Writing pcap file, please wait...",
		)
		b.WriteString(m.centerOverlay(s))

	case saveDone:
		msg := ""
		if m.saveErr != nil {
			msg = errorStyle.Render("Error saving capture:") + "\n" +
				mutedStyle.Render(m.saveErr.Error())
		} else {
			msg = successStyle.Render("Capture saved!") + "\n\n" +
				normalStyle.Render(m.savePath) + "\n\n" +
				mutedStyle.Render("Press any key to continue")
		}
		s := dialogStyle.Render(
			dialogTitle.Render("Save Complete") + "\n\n" + msg,
		)
		b.WriteString(m.centerOverlay(s))
	}

	return b.String()
}

func (m Model) centerOverlay(content string) string {
	lines := strings.Split(content, "\n")
	maxW := 0
	for _, l := range lines {
		clean := lipgloss.Width(l)
		if clean > maxW {
			maxW = clean
		}
	}

	vPad := (m.height - len(lines)) / 2
	if vPad < 0 {
		vPad = 0
	}

	out := strings.Repeat("\n", vPad)
	for _, l := range lines {
		pad := (m.width - lipgloss.Width(l)) / 2
		if pad < 0 {
			pad = 0
		}
		out += strings.Repeat(" ", pad) + l + "\n"
	}
	return out
}

func (m Model) statusView() string {
	if m.searchMode {
		prompt := fmt.Sprintf("/%s█", m.searchQuery)
		return statusBarStyle.
			Width(m.width - 4).
			Render(prompt + "  │  " + hotkeyLabel("esc", "cancel") + " " + hotkeyLabel("enter", "done"))
	}

	conns := m.filteredConns()

	var left, right string

	count := m.store.Count()
	filtered := len(conns)
	if filtered < count {
		left = fmt.Sprintf("%d/%d conns", filtered, count)
	} else {
		left = fmt.Sprintf("%d conns", count)
	}

	if m.searchQuery != "" {
		left = fmt.Sprintf("\"/%s\" ", m.searchQuery) + left
	}

	paused := ""
	if m.paused {
		paused = pausedStyle.Render(" PAUSED ") + " "
	}

	right = hotkeyLabel("q", "uit") + " "
	right += hotkeyLabel("s", "ave") + " "
	right += hotkeyLabel("w", "witch") + " "
	right += hotkeyLabel("p", "ause") + " "
	right += hotkeyLabel("i", "nterface") + " "
	right += hotkeyLabel("l", "an") + " "
	right += hotkeyLabel("d", "ns") + " "
	right += hotkeyLabel("+", "expand") + " "
	if m.showDetail {
		right += hotkeyLabel("tab", "detail") + " "
	}
	right += hotkeyLabel("/", "search") + " "
	right += hotkeyLabel("?", "help")

	return statusBarStyle.
		Width(m.width - 4).
		Render(paused + left + "  │  " + right)
}

func (m Model) helpView() string {
	var b strings.Builder

	help := []struct{ key, label, desc string }{
		{"q", "uit", "Quit the application"},
		{"s", "ave", "Save captured packets to pcap file"},
		{"w", "witch", "Cycle view: inbound / outbound / both"},
		{"p", "ause", "Pause/resume capture"},
		{"l", "an", "Toggle LAN traffic visibility"},
		{"d", "ns", "Toggle DNS traffic visibility"},
		{"i", "nterface", "Cycle through interfaces"},
		{"↑/↓", "", "Navigate connection list"},
		{"j/k", "", "Navigate connection list"},
		{"Enter", "", "Toggle detail panel"},
		{"+", "", "Expand row: full process + remote info"},
		{"Tab", "", "Switch detail tabs"},
		{"←/→", "", "Switch detail tabs"},
		{"g", "", "Go to top of list"},
		{"G", "", "Go to bottom of list"},
		{"r", "efresh", "Force refresh process map"},
		{"/", "", "Search/filter connections"},
		{"?", "", "Toggle this help"},
	}

	title := lipgloss.NewStyle().Foreground(colTitle).Bold(true).Render(" NetView Help ")
	b.WriteString(title)
	b.WriteString("\n\n")

	for _, h := range help {
		k := lipgloss.NewStyle().Foreground(colYellow).Bold(true).Render(h.key)
		entry := "  " + k
		if h.label != "" {
			l := mutedStyle.Render(h.label)
			entry += "(" + l + ")"
		}
		entry += strings.Repeat(" ", 20-len(h.key)-len(h.label)-2)
		entry += normalStyle.Render(h.desc)
		b.WriteString(entry)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(" Press ? or q to close "))
	b.WriteString("\n")

	return b.String()
}

func (m Model) InterfaceNames() []string {
	names := make([]string, 0, len(m.ifaces)+1)
	names = append(names, "all")
	names = append(names, m.ifaces...)
	return names
}
