package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jasonpiedrasantasdk/netview/capture"
	"github.com/jasonpiedrasantasdk/netview/tracker"
	"github.com/jasonpiedrasantasdk/netview/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	ifaceFlag := flag.String("i", "", "Interfaces to monitor (comma-separated, defaults to all active)")
	flag.Parse()

	log.SetPrefix("[netview] ")
	log.SetFlags(log.Ltime | log.Lshortfile)

	// Check root
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "NetView needs root for packet capture.")
		fmt.Fprintln(os.Stderr, "Run: sudo", os.Args[0])
		os.Exit(1)
	}

	// Get active interfaces
	var ifaces []string
	var err error
	if *ifaceFlag != "" {
		ifaces = strings.Split(*ifaceFlag, ",")
		for i := range ifaces {
			ifaces[i] = strings.TrimSpace(ifaces[i])
		}
	} else {
		ifaces, err = capture.GetActiveIfaces()
		if err != nil {
			log.Fatalf("Error getting interfaces: %v", err)
		}
	}
	if len(ifaces) == 0 {
		log.Fatalf("No active network interfaces found")
	}
	log.Printf("Active interfaces: %v", ifaces)

	// Create capture engine
	engine, err := capture.NewEngine(ifaces)
	if err != nil {
		log.Fatalf("Error creating capture engine: %v", err)
	}

	// Create tracker
	store := tracker.NewStore(5000)

	// Create process mapper
	procMap := tracker.NewProcMapper()

	// Start capture
	if err := engine.Start(); err != nil {
		log.Fatalf("Error starting capture: %v", err)
	}
	defer engine.Stop()

	// Start event processing goroutine
	go processEvents(engine, store, procMap)

	// Start stale connection cleanup
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			store.ExpireOld(5 * time.Minute)
		}
	}()

	// Start TUI with save support
	saveFn := func() (string, error) {
		packets := engine.CollectBuffer()
		return capture.SavePcap(packets)
	}
	bufCount := func() int {
		return engine.BufferCount()
	}

	m := tui.New(store, procMap, ifaces, saveFn, bufCount)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running TUI: %v", err)
	}
}

func processEvents(engine *capture.Engine, store *tracker.Store, procMap *tracker.ProcMapper) {
	// Initial process map
	procMap.Update()

	// Rate limit: process at most ~10000 events/sec
	throttle := time.NewTicker(100 * time.Microsecond)
	defer throttle.Stop()

	for ev := range engine.Events() {
		<-throttle.C

		flowEv := tracker.FlowEvent{
			SrcIP:     ev.SrcIP,
			DstIP:     ev.DstIP,
			SrcPort:   ev.SrcPort,
			DstPort:   ev.DstPort,
			Protocol:  ev.Protocol,
			Payload:   ev.Payload,
			Timestamp: ev.Timestamp,
			PacketLen: ev.PacketLen,
			Interface: ev.Interface,
			Direction: tracker.DirectionType(ev.Direction),
			IsDNS:     ev.IsDNS,
			IsIPv6:    ev.IsIPv6,
		}

		store.HandleEvent(flowEv)
	}
}
